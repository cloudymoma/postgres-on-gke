package metrics

import (
	"sync/atomic"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

const (
	minLatencyUS = 1
	maxLatencyUS = 120_000_000 // 120 s
	sigFigs      = 3
)

func newHist() *hdr.Histogram { return hdr.New(minLatencyUS, maxLatencyUS, sigFigs) }

// acc accumulates one statement's samples over some window.
type acc struct {
	h      *hdr.Histogram
	errors int64
	byCode map[string]int64
}

func newAcc() *acc { return &acc{h: newHist(), byCode: map[string]int64{}} }

func (a *acc) record(s Sample) {
	if s.ErrCode != "" {
		a.errors++
		a.byCode[s.ErrCode]++
		return
	}
	us := s.Latency.Microseconds()
	if us < minLatencyUS {
		us = minLatencyUS
	}
	if us > maxLatencyUS {
		us = maxLatencyUS
	}
	_ = a.h.RecordValue(us)
}

func (a *acc) reset() {
	a.h.Reset()
	a.errors = 0
	a.byCode = map[string]int64{}
}

func ms(us int64) float64 { return float64(us) / 1000 }

func (a *acc) point() Point {
	p := Point{Count: a.h.TotalCount(), Errors: a.errors}
	if p.Count > 0 {
		p.P50 = ms(a.h.ValueAtQuantile(50))
		p.P95 = ms(a.h.ValueAtQuantile(95))
		p.P99 = ms(a.h.ValueAtQuantile(99))
		p.Max = ms(a.h.Max())
		p.Mean = a.h.Mean() / 1000
	}
	return p
}

func (a *acc) summary(name string, seconds float64) Summary {
	s := Summary{Name: name, Count: a.h.TotalCount(), Errors: a.errors}
	if len(a.byCode) > 0 {
		s.ErrorsByCode = map[string]int64{}
		for k, v := range a.byCode {
			s.ErrorsByCode[k] = v
		}
	}
	if s.Count > 0 {
		s.P50 = ms(a.h.ValueAtQuantile(50))
		s.P95 = ms(a.h.ValueAtQuantile(95))
		s.P99 = ms(a.h.ValueAtQuantile(99))
		s.P999 = ms(a.h.ValueAtQuantile(99.9))
		s.Max = ms(a.h.Max())
		s.Mean = a.h.Mean() / 1000
	}
	if seconds > 0 {
		s.TPS = float64(s.Count) / seconds
	}
	return s
}

// stageInfo is published by the engine when a stage begins.
type stageInfo struct {
	index   int
	workers int
	start   time.Time
}

type stageAcc struct {
	info  stageInfo
	end   time.Time
	stmts []*acc
	total *acc
}

// Aggregator is the single consumer of worker samples. It owns all
// histograms, so the hot path needs no locks.
type Aggregator struct {
	names   []string
	in      <-chan Sample
	tick    time.Duration
	stage   atomic.Pointer[stageInfo]
	stageCh chan stageInfo

	start   time.Time
	cur     []*acc // current bucket, per statement
	curTot  *acc
	overall []*acc
	overTot *acc
	stages  []*stageAcc
	series  []Bucket
	done    chan struct{}
}

// NewAggregator creates an aggregator for the named statements reading
// from in. tick is the bucket width (1s in production, shorter in tests).
func NewAggregator(names []string, in <-chan Sample, tick time.Duration) *Aggregator {
	a := &Aggregator{
		names:   names,
		in:      in,
		tick:    tick,
		stageCh: make(chan stageInfo, 256),
		done:    make(chan struct{}),
	}
	a.cur = make([]*acc, len(names))
	a.overall = make([]*acc, len(names))
	for i := range names {
		a.cur[i] = newAcc()
		a.overall[i] = newAcc()
	}
	a.curTot = newAcc()
	a.overTot = newAcc()
	return a
}

// SetStage tells the aggregator a new stage has begun. Safe to call from
// another goroutine.
func (a *Aggregator) SetStage(index, workers int) {
	info := stageInfo{index: index, workers: workers, start: time.Now()}
	a.stage.Store(&info)
	select {
	case a.stageCh <- info:
	case <-a.done:
	}
}

// Run consumes samples until in is closed, then finalizes. Call in a goroutine.
func (a *Aggregator) Run(start time.Time) {
	defer close(a.done)
	a.start = start
	ticker := time.NewTicker(a.tick)
	defer ticker.Stop()
	for {
		select {
		case info := <-a.stageCh:
			a.applyStage(info)
		case s, ok := <-a.in:
			if !ok {
				a.drainStages()
				if a.curTot.h.TotalCount() > 0 || a.curTot.errors > 0 {
					a.flush(time.Now())
				}
				a.closeStage(time.Now())
				return
			}
			a.drainStages()
			a.record(s)
		case now := <-ticker.C:
			a.drainStages()
			a.flush(now)
		}
	}
}

// Wait blocks until Run has finished.
func (a *Aggregator) Wait() { <-a.done }

func (a *Aggregator) drainStages() {
	for {
		select {
		case info := <-a.stageCh:
			a.applyStage(info)
		default:
			return
		}
	}
}

func (a *Aggregator) applyStage(info stageInfo) *stageAcc {
	if n := len(a.stages); n > 0 && a.stages[n-1].info.index == info.index {
		return a.stages[n-1]
	}
	a.closeStage(info.start)
	st := &stageAcc{info: info, total: newAcc(), stmts: make([]*acc, len(a.names))}
	for i := range a.names {
		st.stmts[i] = newAcc()
	}
	a.stages = append(a.stages, st)
	return st
}

func (a *Aggregator) currentStage() *stageAcc {
	if n := len(a.stages); n > 0 {
		return a.stages[n-1]
	}
	info := a.stage.Load()
	if info == nil {
		return nil
	}
	return a.applyStage(*info)
}

func (a *Aggregator) closeStage(at time.Time) {
	if n := len(a.stages); n > 0 && a.stages[n-1].end.IsZero() {
		a.stages[n-1].end = at
	}
}

func (a *Aggregator) record(s Sample) {
	if s.Stmt < 0 || s.Stmt >= len(a.names) {
		return
	}
	a.cur[s.Stmt].record(s)
	a.curTot.record(s)
	a.overall[s.Stmt].record(s)
	a.overTot.record(s)
	if st := a.currentStage(); st != nil {
		st.stmts[s.Stmt].record(s)
		st.total.record(s)
	}
}

func (a *Aggregator) flush(now time.Time) {
	b := Bucket{T: int(now.Sub(a.start) / a.tick), Total: a.curTot.point(), Stmts: make([]Point, len(a.names))}
	if n := len(a.stages); n > 0 {
		last := a.stages[n-1].info
		b.Stage, b.Workers = last.index, last.workers
	} else if info := a.stage.Load(); info != nil {
		b.Stage, b.Workers = info.index, info.workers
	}
	for i, c := range a.cur {
		b.Stmts[i] = c.point()
		c.reset()
	}
	a.curTot.reset()
	a.series = append(a.series, b)
}

// Results returns the aggregated series and summaries. Call after Wait.
func (a *Aggregator) Results(elapsed time.Duration) (series []Bucket, stmts []Summary, stages []StageSummary, overall Summary) {
	sec := elapsed.Seconds()
	overall = a.overTot.summary("all", sec)
	for i, o := range a.overall {
		stmts = append(stmts, o.summary(a.names[i], sec))
	}
	for _, st := range a.stages {
		d := st.end.Sub(st.info.start).Seconds()
		stages = append(stages, StageSummary{
			Index: st.info.index, Workers: st.info.workers, DurationSec: d,
			Summary: st.total.summary("stage", d),
		})
	}
	return a.series, stmts, stages, overall
}
