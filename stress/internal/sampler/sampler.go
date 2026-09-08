// Package sampler polls pg_stat_* views on the primary while a run is active.
package sampler

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
)

type raw struct {
	at                                   time.Time
	commit, rollback, blksRead, blksHit  int64
	tupReturned, tupFetched, tupModified int64
	tempBytes, deadlocks                 int64
	walBytes                             float64
	active, idle, idleInTx, waiting      int
	replicas                             []metrics.ReplicaSample
}

// Sampler collects server-side samples at a fixed interval.
type Sampler struct {
	pool     *pgxpool.Pool
	interval time.Duration

	mu      sync.Mutex
	samples []metrics.ServerSample
	errs    map[string]string
	info    metrics.ServerInfo
	skip    map[string]bool
	prev    *raw
}

// New creates a sampler that polls pool.
func New(pool *pgxpool.Pool, interval time.Duration) *Sampler {
	return &Sampler{pool: pool, interval: interval, errs: map[string]string{}, skip: map[string]bool{}}
}

// Run polls until ctx is cancelled. The first poll seeds counters only.
func (s *Sampler) Run(ctx context.Context, start time.Time) {
	s.collectInfo(ctx)
	s.poll(ctx, start)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.poll(ctx, start)
		}
	}
}

// Results returns everything collected so far.
func (s *Sampler) Results() ([]metrics.ServerSample, map[string]string, metrics.ServerInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]metrics.ServerSample, len(s.samples))
	copy(out, s.samples)
	errs := make(map[string]string, len(s.errs))
	for k, v := range s.errs {
		errs[k] = v
	}
	return out, errs, s.info
}

func (s *Sampler) fail(view string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.errs[view]; !seen {
		s.errs[view] = err.Error()
	}
	s.skip[view] = true
}

func (s *Sampler) collectInfo(ctx context.Context) {
	var info metrics.ServerInfo
	if err := s.pool.QueryRow(ctx, "SHOW server_version").Scan(&info.Version); err != nil {
		s.fail("server_version", err)
	}
	var mc string
	if err := s.pool.QueryRow(ctx, "SHOW max_connections").Scan(&mc); err == nil {
		info.MaxConnections, _ = strconv.Atoi(mc)
	}
	_ = s.pool.QueryRow(ctx, "SELECT current_database()").Scan(&info.Database)
	var monitor bool
	if err := s.pool.QueryRow(ctx, "SELECT pg_has_role(current_user, 'pg_monitor', 'member')").Scan(&monitor); err == nil && !monitor {
		s.mu.Lock()
		s.errs["privileges"] = "current user is not a member of pg_monitor; activity and replication views are partial. Fix: GRANT pg_monitor TO <user>;"
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
}

func (s *Sampler) poll(ctx context.Context, start time.Time) {
	cctx, cancel := context.WithTimeout(ctx, s.interval)
	defer cancel()
	r := &raw{at: time.Now()}

	if !s.skip["pg_stat_database"] {
		err := s.pool.QueryRow(cctx, `SELECT xact_commit, xact_rollback, blks_read, blks_hit,
			tup_returned, tup_fetched, tup_inserted + tup_updated + tup_deleted, temp_bytes, deadlocks
			FROM pg_stat_database WHERE datname = current_database()`).Scan(
			&r.commit, &r.rollback, &r.blksRead, &r.blksHit,
			&r.tupReturned, &r.tupFetched, &r.tupModified, &r.tempBytes, &r.deadlocks)
		if err != nil {
			s.fail("pg_stat_database", err)
		}
	}
	if !s.skip["pg_stat_activity"] {
		err := s.pool.QueryRow(cctx, `SELECT
			count(*) FILTER (WHERE state = 'active'),
			count(*) FILTER (WHERE state = 'idle'),
			count(*) FILTER (WHERE state LIKE 'idle in transaction%'),
			count(*) FILTER (WHERE state = 'active' AND wait_event_type IS NOT NULL)
			FROM pg_stat_activity WHERE backend_type = 'client backend'`).Scan(
			&r.active, &r.idle, &r.idleInTx, &r.waiting)
		if err != nil {
			s.fail("pg_stat_activity", err)
		}
	}
	if !s.skip["wal"] {
		if err := s.pool.QueryRow(cctx, "SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0')::float8").Scan(&r.walBytes); err != nil {
			s.fail("wal", err)
		}
	}
	if !s.skip["pg_stat_replication"] {
		rows, err := s.pool.Query(cctx, `SELECT application_name,
			COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::float8,
			COALESCE(EXTRACT(EPOCH FROM replay_lag) * 1000, 0)::float8
			FROM pg_stat_replication ORDER BY application_name`)
		if err != nil {
			s.fail("pg_stat_replication", err)
		} else {
			for rows.Next() {
				var rep metrics.ReplicaSample
				if err := rows.Scan(&rep.Name, &rep.LagBytes, &rep.LagMs); err != nil {
					s.fail("pg_stat_replication", err)
					break
				}
				r.replicas = append(r.replicas, rep)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				s.fail("pg_stat_replication", err)
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.Instances == 0 || len(r.replicas)+1 > s.info.Instances {
		s.info.Instances = len(r.replicas) + 1
	}
	if s.prev == nil {
		s.prev = r
		return
	}
	p := s.prev
	dt := r.at.Sub(p.at).Seconds()
	if dt <= 0 {
		return
	}
	rate := func(a, b int64) float64 { return float64(a-b) / dt }
	smp := metrics.ServerSample{
		T:                 int(r.at.Sub(start).Seconds()),
		CommitsPerSec:     rate(r.commit, p.commit),
		RollbacksPerSec:   rate(r.rollback, p.rollback),
		TupReturnedPerSec: rate(r.tupReturned, p.tupReturned),
		TupModifiedPerSec: rate(r.tupModified, p.tupModified),
		TempBytesPerSec:   rate(r.tempBytes, p.tempBytes),
		WALBytesPerSec:    (r.walBytes - p.walBytes) / dt,
		Deadlocks:         r.deadlocks,
		Active:            r.active, Idle: r.idle, IdleInTx: r.idleInTx, Waiting: r.waiting,
		Replicas: r.replicas,
	}
	if reads := (r.blksHit - p.blksHit) + (r.blksRead - p.blksRead); reads > 0 {
		ratio := float64(r.blksHit-p.blksHit) / float64(reads)
		smp.CacheHitRatio = &ratio
	}
	s.samples = append(s.samples, smp)
	s.prev = r
}
