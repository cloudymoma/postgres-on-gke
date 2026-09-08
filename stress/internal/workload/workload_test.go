package workload

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
)

func TestPickerRespectsWeights(t *testing.T) {
	p := NewPicker([]int{80, 20})
	r := rand.New(rand.NewPCG(1, 2))
	const n = 100_000
	counts := [2]int{}
	for i := 0; i < n; i++ {
		counts[p.Pick(r)]++
	}
	got := float64(counts[0]) / n
	if math.Abs(got-0.8) > 0.01 {
		t.Fatalf("weight 80/20 produced %.3f share for index 0", got)
	}
}

func TestPickerSingle(t *testing.T) {
	p := NewPicker([]int{7})
	if p.Pick(rand.New(rand.NewPCG(0, 0))) != 0 {
		t.Fatal("single-entry picker must return 0")
	}
}

func TestArgGenStaysInRange(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	g := ArgGen{Kind: KindInt, Min: -5, Max: 5}
	for i := 0; i < 10_000; i++ {
		v := g.Gen(r).(int64)
		if v < -5 || v > 5 {
			t.Fatalf("out of range: %d", v)
		}
	}
	c := ArgGen{Kind: KindChoice, Choices: []string{"a", "b"}}
	if v := c.Gen(r).(string); v != "a" && v != "b" {
		t.Fatalf("bad choice %q", v)
	}
}

func TestCompileResolvesParamsAndInjectsTPCB(t *testing.T) {
	sc := &config.Scenario{Name: "x", Prepare: config.PrepareTPCB, Scale: 2,
		Stages: []config.Stage{{Workers: 1, Duration: time.Second}}}
	sc.Normalize()
	stmts, err := Compile(sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 1 || stmts[0].Name != "tpcb" || len(stmts[0].Queries) != 5 {
		t.Fatalf("tpcb not injected: %+v", stmts)
	}
	if stmts[0].Args[0].Max != 200_000 {
		t.Fatalf("scale not applied to aid range: %d", stmts[0].Args[0].Max)
	}
	// UPDATE accounts uses (delta, aid) => arg indices (3, 0)
	if p := stmts[0].Queries[0].Params; p[0] != 3 || p[1] != 0 {
		t.Fatalf("param resolution wrong: %v", p)
	}
}

func TestCompileRejectsUnknownParam(t *testing.T) {
	sc := &config.Scenario{Name: "x", Statements: []config.Statement{{
		Name: "q", Weight: 1, Target: config.TargetRW,
		Queries: []config.Query{{SQL: "SELECT $1", Params: []string{"missing"}}},
	}}}
	if _, err := Compile(sc); err == nil {
		t.Fatal("expected error for unknown param")
	}
}
