// Package sampler polls pg_stat_* views on the primary while a run is active.
package sampler

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

// Sampler collects server-side samples at a fixed interval over a dedicated connection.
type Sampler struct {
	conn       *pgx.Conn
	connCfg    *pgx.ConnConfig
	interval   time.Duration
	infoLoaded bool

	mu      sync.Mutex
	samples []metrics.ServerSample
	errs    map[string]string
	info    metrics.ServerInfo
	skip    map[string]bool
	prev    *raw
}

// New creates a sampler that polls using conn (and reconnects with connCfg if closed).
func New(conn *pgx.Conn, connCfg *pgx.ConnConfig, interval time.Duration) *Sampler {
	return &Sampler{
		conn:     conn,
		connCfg:  connCfg,
		interval: interval,
		errs:     map[string]string{},
		skip:     map[string]bool{},
	}
}

// Run polls until ctx is cancelled. The first poll seeds counters only.
func (s *Sampler) Run(ctx context.Context, start time.Time) {
	defer func() {
		if s.conn != nil && !s.conn.IsClosed() {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.conn.Close(cctx)
			cancel()
		}
	}()
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

func (s *Sampler) ensureConn(ctx context.Context) bool {
	if s.conn != nil && !s.conn.IsClosed() {
		return true
	}
	if s.connCfg == nil || ctx.Err() != nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 2*s.interval+10*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, s.connCfg.Copy())
	if err != nil {
		s.fail(ctx, "connect", err)
		return false
	}
	s.conn = conn
	return true
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

func (s *Sampler) fail(ctx context.Context, view string, err error) {
	if ctx.Err() != nil {
		return // cancelled mid-poll on shutdown: not a real failure
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.errs[view]; !seen {
		s.errs[view] = err.Error()
	}
	// Only give up permanently on causes that cannot resolve themselves.
	// Connection exhaustion and poll timeouts are exactly the conditions we
	// most need to keep observing.
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "42501", "42P01", "42883": // insufficient_privilege, undefined_table, undefined_function
			s.skip[view] = true
		}
	}
}

func (s *Sampler) collectInfo(ctx context.Context) {
	if !s.ensureConn(ctx) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*s.interval+10*time.Second)
	defer cancel()
	var info metrics.ServerInfo
	if err := s.conn.QueryRow(cctx, "SHOW server_version").Scan(&info.Version); err != nil {
		s.fail(ctx, "server_version", err)
		return
	}
	var mc string
	if err := s.conn.QueryRow(cctx, "SHOW max_connections").Scan(&mc); err == nil {
		info.MaxConnections, _ = strconv.Atoi(mc)
	}
	_ = s.conn.QueryRow(cctx, "SELECT current_database()").Scan(&info.Database)
	var monitor bool
	if err := s.conn.QueryRow(cctx, "SELECT pg_has_role(current_user, 'pg_monitor', 'member')").Scan(&monitor); err == nil && !monitor {
		s.mu.Lock()
		s.errs["privileges"] = "current user is not a member of pg_monitor; activity and replication views are partial. Fix: GRANT pg_monitor TO <user>;"
		s.mu.Unlock()
	}
	s.mu.Lock()
	if s.info.Instances > info.Instances {
		info.Instances = s.info.Instances
	}
	s.info = info
	s.mu.Unlock()
	s.infoLoaded = true
}

func (s *Sampler) poll(ctx context.Context, start time.Time) {
	if !s.ensureConn(ctx) {
		return
	}
	if !s.infoLoaded {
		s.collectInfo(ctx)
		if ctx.Err() != nil {
			return
		}
	}
	// Safety net for network stalls; the per-statement limit is enforced
	// server-side via RuntimeParams["statement_timeout"] = SampleInterval.
	cctx, cancel := context.WithTimeout(ctx, 2*s.interval+10*time.Second)
	defer cancel()
	r := &raw{at: time.Now()}
	ok := true

	if !s.skip["pg_stat_database"] {
		err := s.conn.QueryRow(cctx, `SELECT xact_commit, xact_rollback, blks_read, blks_hit,
			tup_returned, tup_fetched, tup_inserted + tup_updated + tup_deleted, temp_bytes, deadlocks
			FROM pg_stat_database WHERE datname = current_database()`).Scan(
			&r.commit, &r.rollback, &r.blksRead, &r.blksHit,
			&r.tupReturned, &r.tupFetched, &r.tupModified, &r.tempBytes, &r.deadlocks)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.fail(ctx, "pg_stat_database", err)
			ok = false
		}
	}
	if !s.skip["pg_stat_activity"] {
		err := s.conn.QueryRow(cctx, `SELECT
			count(*) FILTER (WHERE state = 'active'),
			count(*) FILTER (WHERE state = 'idle'),
			count(*) FILTER (WHERE state LIKE 'idle in transaction%'),
			count(*) FILTER (WHERE state = 'active' AND wait_event_type IS NOT NULL)
			FROM pg_stat_activity WHERE backend_type = 'client backend'`).Scan(
			&r.active, &r.idle, &r.idleInTx, &r.waiting)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.fail(ctx, "pg_stat_activity", err)
			ok = false
		}
	}
	if !s.skip["wal"] {
		if err := s.conn.QueryRow(cctx, "SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0')::float8").Scan(&r.walBytes); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.fail(ctx, "wal", err)
			ok = false
		}
	}
	if !s.skip["pg_stat_replication"] {
		rows, err := s.conn.Query(cctx, `SELECT application_name,
			COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::float8,
			COALESCE(EXTRACT(EPOCH FROM replay_lag) * 1000, 0)::float8
			FROM pg_stat_replication ORDER BY application_name`)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.fail(ctx, "pg_stat_replication", err)
			ok = false
		} else {
			for rows.Next() {
				var rep metrics.ReplicaSample
				if err := rows.Scan(&rep.Name, &rep.LagBytes, &rep.LagMs); err != nil {
					if ctx.Err() != nil {
						rows.Close()
						return
					}
					s.fail(ctx, "pg_stat_replication", err)
					ok = false
					break
				}
				r.replicas = append(r.replicas, rep)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				if ctx.Err() != nil {
					return
				}
				s.fail(ctx, "pg_stat_replication", err)
				ok = false
			}
		}
	}

	if !ok {
		return
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
	// Counters reset on pg_stat_reset() and on failover; never report negative rates.
	rate := func(a, b int64) float64 {
		if a < b {
			return 0
		}
		return float64(a-b) / dt
	}
	walRate := 0.0
	if r.walBytes > p.walBytes {
		walRate = (r.walBytes - p.walBytes) / dt
	}
	smp := metrics.ServerSample{
		T:                 int(r.at.Sub(start).Seconds()),
		CommitsPerSec:     rate(r.commit, p.commit),
		RollbacksPerSec:   rate(r.rollback, p.rollback),
		TupReturnedPerSec: rate(r.tupReturned, p.tupReturned),
		TupModifiedPerSec: rate(r.tupModified, p.tupModified),
		TempBytesPerSec:   rate(r.tempBytes, p.tempBytes),
		WALBytesPerSec:    walRate,
		Deadlocks:         rate(r.deadlocks, p.deadlocks),
		Active:            r.active, Idle: r.idle, IdleInTx: r.idleInTx, Waiting: r.waiting,
		Replicas: r.replicas,
	}
	dHit := r.blksHit - p.blksHit
	dRead := r.blksRead - p.blksRead
	if dHit >= 0 && dRead >= 0 && dHit+dRead > 0 {
		ratio := float64(dHit) / float64(dHit+dRead)
		smp.CacheHitRatio = &ratio
	}
	s.samples = append(s.samples, smp)
	s.prev = r
}
