// Package metrics aggregates per-statement latency samples into time series
// and summaries, and defines the results document written as JSON.
package metrics

import "time"

// Sample is one completed statement execution reported by a worker.
// ErrCode is empty on success.
type Sample struct {
	Stmt    int
	Latency time.Duration
	ErrCode string
}

// Point is the latency distribution of one statement over one interval.
// Latencies are milliseconds.
type Point struct {
	Count  int64   `json:"count"`
	Errors int64   `json:"errors"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
}

// Bucket is one second of the run.
type Bucket struct {
	T       int     `json:"t"`
	Stage   int     `json:"stage"`
	Workers int     `json:"workers"`
	Total   Point   `json:"total"`
	Stmts   []Point `json:"stmts"`
}

// Summary is the latency distribution over a whole run, stage or statement.
type Summary struct {
	Name         string           `json:"name"`
	Count        int64            `json:"count"`
	TPS          float64          `json:"tps"`
	P50          float64          `json:"p50"`
	P95          float64          `json:"p95"`
	P99          float64          `json:"p99"`
	P999         float64          `json:"p999"`
	Max          float64          `json:"max"`
	Mean         float64          `json:"mean"`
	Errors       int64            `json:"errors"`
	ErrorsByCode map[string]int64 `json:"errorsByCode,omitempty"`
}

// StageSummary is a Summary plus the stage's shape.
type StageSummary struct {
	Index       int     `json:"index"`
	Workers     int     `json:"workers"`
	DurationSec float64 `json:"durationSec"`
	Summary
}

// ServerInfo is static information about the target.
type ServerInfo struct {
	Version        string `json:"version"`
	Database       string `json:"database"`
	MaxConnections int    `json:"maxConnections"`
	Instances      int    `json:"instances"`
}

// ReplicaSample is replication lag for one standby.
type ReplicaSample struct {
	Name     string  `json:"name"`
	LagBytes float64 `json:"lagBytes"`
	LagMs    float64 `json:"lagMs"`
}

// ServerSample is one poll of pg_stat_* views. Rates are per second.
type ServerSample struct {
	T                 int             `json:"t"`
	CommitsPerSec     float64         `json:"commitsPerSec"`
	RollbacksPerSec   float64         `json:"rollbacksPerSec"`
	CacheHitRatio     *float64        `json:"cacheHitRatio"` // nil when no blocks were read in the interval
	TupReturnedPerSec float64         `json:"tupReturnedPerSec"`
	TupModifiedPerSec float64         `json:"tupModifiedPerSec"`
	TempBytesPerSec   float64         `json:"tempBytesPerSec"`
	WALBytesPerSec    float64         `json:"walBytesPerSec"`
	Deadlocks         int64           `json:"deadlocks"`
	Active            int             `json:"active"`
	Idle              int             `json:"idle"`
	IdleInTx          int             `json:"idleInTx"`
	Waiting           int             `json:"waiting"`
	Replicas          []ReplicaSample `json:"replicas,omitempty"`
}

// ClientInfo records the load generator's own resource use.
type ClientInfo struct {
	NumCPU         int     `json:"numCpu"`
	GOMAXPROCS     int     `json:"gomaxprocs"`
	CPUSeconds     float64 `json:"cpuSeconds"`
	CPUUtilization float64 `json:"cpuUtilization"`
}

// StagePlan is the configured shape of a stage.
type StagePlan struct {
	Workers     int     `json:"workers"`
	DurationSec float64 `json:"durationSec"`
}

// Results is the complete output of one run.
type Results struct {
	Scenario      string            `json:"scenario"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    time.Time         `json:"finishedAt"`
	DurationSec   float64           `json:"durationSec"`
	StagePlan     []StagePlan       `json:"stagePlan"`
	Server        ServerInfo        `json:"server"`
	Client        ClientInfo        `json:"client"`
	Overall       Summary           `json:"overall"`
	Stages        []StageSummary    `json:"stages"`
	Statements    []Summary         `json:"statements"`
	Series        []Bucket          `json:"series"`
	Samples       []ServerSample    `json:"samples"`
	SamplerErrors map[string]string `json:"samplerErrors,omitempty"`
}
