package metrics

import (
	"math"
	"testing"
	"time"
)

func TestAggregatorSummariesAndStages(t *testing.T) {
	in := make(chan Sample, 1024)
	a := NewAggregator([]string{"read", "write"}, in, time.Hour) // no ticker flushes during test
	start := time.Now()
	go a.Run(start)

	a.SetStage(0, 5)
	for i := 1; i <= 100; i++ { // read: 1..100 ms
		in <- Sample{Stmt: 0, Latency: time.Duration(i) * time.Millisecond}
	}
	in <- Sample{Stmt: 1, ErrCode: "40001"}
	in <- Sample{Stmt: 1, ErrCode: "40001"}
	in <- Sample{Stmt: 1, Latency: 10 * time.Millisecond}
	// give the aggregator time to attribute these to stage 0 before switching
	time.Sleep(50 * time.Millisecond)
	a.SetStage(1, 10)
	in <- Sample{Stmt: 0, Latency: 500 * time.Millisecond}
	close(in)
	a.Wait()

	series, stmts, stages, overall := a.Results(10 * time.Second)

	if overall.Count != 102 || overall.Errors != 2 {
		t.Fatalf("overall count/errors = %d/%d", overall.Count, overall.Errors)
	}
	if math.Abs(overall.TPS-10.2) > 1e-9 {
		t.Fatalf("overall TPS = %v, want 10.2", overall.TPS)
	}
	read := stmts[0]
	if read.Count != 101 || math.Abs(read.P50-50) > 2 || math.Abs(read.P99-100) > 2 || math.Abs(read.Max-500) > 1 {
		t.Fatalf("read summary wrong: %+v", read)
	}
	write := stmts[1]
	if write.Errors != 2 || write.ErrorsByCode["40001"] != 2 || write.Count != 1 {
		t.Fatalf("write summary wrong: %+v", write)
	}
	if len(stages) != 2 || stages[0].Workers != 5 || stages[0].Count != 101 || stages[1].Count != 1 {
		t.Fatalf("stage attribution wrong: %+v", stages)
	}
	if stages[0].DurationSec <= 0 || stages[1].DurationSec <= 0 {
		t.Fatalf("stage durations must be positive: %+v", stages)
	}
	// one final flush on close
	if len(series) != 1 || series[0].Total.Count != 102 || series[0].Stage != 1 || series[0].Workers != 10 {
		t.Fatalf("series wrong: %+v", series)
	}
}

func TestAggregatorIgnoresOutOfRangeStmt(t *testing.T) {
	in := make(chan Sample, 4)
	a := NewAggregator([]string{"x"}, in, time.Hour)
	go a.Run(time.Now())
	in <- Sample{Stmt: 7, Latency: time.Millisecond}
	close(in)
	a.Wait()
	_, _, _, overall := a.Results(time.Second)
	if overall.Count != 0 {
		t.Fatalf("out-of-range sample was counted")
	}
}

func TestPointClampsLatency(t *testing.T) {
	c := newAcc()
	c.record(Sample{Latency: 0})
	c.record(Sample{Latency: 10 * time.Minute})
	p := c.point()
	if p.Count != 2 || p.Max < 119_000 {
		t.Fatalf("clamping wrong: %+v", p)
	}
}

func TestAggregatorEmptyStage(t *testing.T) {
	in := make(chan Sample, 16)
	a := NewAggregator([]string{"x"}, in, time.Hour)
	go a.Run(time.Now())

	a.SetStage(0, 5)
	in <- Sample{Stmt: 0, Latency: 10 * time.Millisecond}
	time.Sleep(20 * time.Millisecond)

	a.SetStage(1, 10) // stage 1 receives zero samples
	time.Sleep(250 * time.Millisecond)

	a.SetStage(2, 20)
	in <- Sample{Stmt: 0, Latency: 10 * time.Millisecond}
	close(in)
	a.Wait()

	_, _, stages, _ := a.Results(time.Second)
	if len(stages) != 3 {
		t.Fatalf("want 3 stages, got %d: %+v", len(stages), stages)
	}
	if stages[1].Index != 1 || stages[1].Workers != 10 || stages[1].Count != 0 {
		t.Fatalf("empty stage 1 wrong: %+v", stages[1])
	}
	// Stage 0 (~20ms) must not absorb stage 1's 250ms duration.
	if stages[0].DurationSec > 0.18 {
		t.Fatalf("stage 0 absorbed empty stage 1 duration: %v s", stages[0].DurationSec)
	}
}
