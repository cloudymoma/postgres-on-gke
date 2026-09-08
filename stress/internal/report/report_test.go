package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
)

func sample(name string) *metrics.Results {
	return &metrics.Results{
		Scenario: name, StartedAt: time.Now(), FinishedAt: time.Now().Add(time.Minute), DurationSec: 60,
		StagePlan: []metrics.StagePlan{{Workers: 10, DurationSec: 60}},
		Server:    metrics.ServerInfo{Version: "18.0", Instances: 3},
		Overall:   metrics.Summary{Name: "all", Count: 1000, TPS: 16.6, P50: 1.5, P99: 9.9},
		Stages:    []metrics.StageSummary{{Index: 0, Workers: 10, DurationSec: 60, Summary: metrics.Summary{Count: 1000, TPS: 16.6}}},
		Statements: []metrics.Summary{{Name: "tpcb", Count: 1000, P99: 9.9,
			ErrorsByCode: map[string]int64{"40001": 2}}},
		Series:  []metrics.Bucket{{T: 0, Stage: 0, Workers: 10, Total: metrics.Point{Count: 17}, Stmts: []metrics.Point{{Count: 17}}}},
		Samples: []metrics.ServerSample{{T: 5, CommitsPerSec: 16, Replicas: []metrics.ReplicaSample{{Name: "pg-main-2", LagMs: 3}}}},
	}
}

func TestRenderIsSelfContainedAndEmbedsData(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, []*metrics.Results{sample("tpcb-<script>")}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"<!doctype html>", "Chart.js v4", `id="pgstress-data"`, `"scenario":"tpcb-\u003cscript\u003e"`, "stageBands", "pg-main-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if strings.Contains(out, "<script>alert") || strings.Contains(out, `src="http`) {
		t.Error("report must not contain unescaped input or external references")
	}
	if !strings.Contains(out, "tpcb-&lt;script&gt;") {
		t.Error("title not HTML-escaped")
	}
}

func TestRenderRejectsEmpty(t *testing.T) {
	if err := Render(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("expected error for no runs")
	}
}

func TestLoadResultsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		b, _ := json.Marshal(sample(n))
		os.WriteFile(filepath.Join(dir, n+".json"), b, 0o600)
	}
	runs, err := LoadResults([]string{filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")})
	if err != nil || len(runs) != 2 || runs[1].Scenario != "b" {
		t.Fatalf("round trip failed: %v %+v", err, runs)
	}
	var buf bytes.Buffer
	if err := Render(&buf, runs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "2 runs compared") {
		t.Error("compare title missing")
	}
}
