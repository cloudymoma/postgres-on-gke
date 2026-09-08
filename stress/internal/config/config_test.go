package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func base() Scenario {
	return Scenario{
		Name:   "t",
		Stages: []Stage{{Workers: 1, Duration: time.Second}},
		Statements: []Statement{{
			Name: "q", SQL: "SELECT $1",
			Args: []Arg{{Name: "a", Type: "int", Min: 1, Max: 10}},
		}},
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Scenario)
		wantErr string
	}{
		{"valid", func(s *Scenario) {}, ""},
		{"missing name", func(s *Scenario) { s.Name = " " }, "name is required"},
		{"bad prepare", func(s *Scenario) { s.Prepare = "pgbench" }, "prepare must be"},
		{"no stages", func(s *Scenario) { s.Stages = nil }, "at least one stage"},
		{"zero workers", func(s *Scenario) { s.Stages[0].Workers = 0 }, "workers must be >= 1"},
		{"zero duration", func(s *Scenario) { s.Stages[0].Duration = 0 }, "duration must be > 0"},
		{"no statements without tpcb", func(s *Scenario) { s.Statements = nil }, "statements are required"},
		{"tpcb allows no statements", func(s *Scenario) { s.Prepare = PrepareTPCB; s.Statements = nil }, ""},
		{"duplicate statement", func(s *Scenario) { s.Statements = append(s.Statements, s.Statements[0]) }, "duplicate statement"},
		{"bad target", func(s *Scenario) { s.Statements[0].Target = "primary" }, "target must be rw or ro"},
		{"max < min", func(s *Scenario) { s.Statements[0].Args[0].Max = 0 }, "max (0) < min (1)"},
		{"unknown arg type", func(s *Scenario) { s.Statements[0].Args[0].Type = "uuid" }, "type must be int or choice"},
		{"choice needs choices", func(s *Scenario) { s.Statements[0].Args[0] = Arg{Name: "a", Type: "choice"} }, "needs choices"},
		{"unknown param", func(s *Scenario) {
			s.Statements[0].Queries = []Query{{SQL: "SELECT $1", Params: []string{"nope"}}}
		}, "unknown arg \"nope\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(&s)
			s.Normalize()
			err := s.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNormalizeExpandsShorthandAndScale(t *testing.T) {
	s := base()
	s.Scale = 3
	s.Statements[0].Args = append(s.Statements[0].Args, Arg{Name: "b", MaxPerScale: 100})
	s.Normalize()

	st := s.Statements[0]
	if st.Weight != 1 || st.Target != TargetRW {
		t.Fatalf("defaults not applied: %+v", st)
	}
	if len(st.Queries) != 1 || len(st.Queries[0].Params) != 2 || st.Queries[0].Params[1] != "b" {
		t.Fatalf("shorthand not expanded: %+v", st.Queries)
	}
	if st.Args[1].Type != "int" || st.Args[1].Max != 300 {
		t.Fatalf("max_per_scale not applied: %+v", st.Args[1])
	}
	if s.SampleInterval != 5*time.Second || s.StatementTimeout != 30*time.Second {
		t.Fatalf("interval defaults wrong: %v %v", s.SampleInterval, s.StatementTimeout)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.yaml")
	os.WriteFile(p, []byte("name: x\nstages: [{workers: 1, duration: 1s}]\nbogus: 1\n"), 0o600)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadParsesDurationsAndHelpers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.yaml")
	os.WriteFile(p, []byte(`
name: ramp
prepare: tpcb
stages:
  - {workers: 5, duration: 30s}
  - {workers: 20, duration: 1m}
`), 0o600)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxWorkers() != 20 || s.TotalDuration() != 90*time.Second {
		t.Fatalf("helpers wrong: %d %v", s.MaxWorkers(), s.TotalDuration())
	}
}
