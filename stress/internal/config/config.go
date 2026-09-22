// Package config loads and validates pgstress scenario files.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Target selects which service a statement is sent to.
type Target string

const (
	TargetRW Target = "rw" // primary
	TargetRO Target = "ro" // streaming replicas
)

const (
	PrepareNone = "none"
	PrepareTPCB = "tpcb"
)

// Scenario is the top-level document of a scenario YAML file.
type Scenario struct {
	Name             string        `yaml:"name"`
	Prepare          string        `yaml:"prepare"`
	Scale            int           `yaml:"scale"`
	SampleInterval   time.Duration `yaml:"sample_interval"`
	StatementTimeout time.Duration `yaml:"statement_timeout"`
	Stages           []Stage       `yaml:"stages"`
	Statements       []Statement   `yaml:"statements"`
}

// Stage is one step of the load ramp.
type Stage struct {
	Workers  int           `yaml:"workers"`
	Duration time.Duration `yaml:"duration"`
}

// Statement is a weighted unit of work: one query, or several in a transaction.
type Statement struct {
	Name    string  `yaml:"name"`
	Weight  int     `yaml:"weight"`
	Target  Target  `yaml:"target"`
	SQL     string  `yaml:"sql"`
	Args    []Arg   `yaml:"args"`
	Queries []Query `yaml:"queries"`
}

// Query is a single SQL text with the names of the args bound to $1..$n.
type Query struct {
	SQL    string   `yaml:"sql"`
	Params []string `yaml:"params"`
}

// Arg describes a randomly generated statement argument.
type Arg struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // int | choice
	Min         int64    `yaml:"min"`
	Max         int64    `yaml:"max"`
	MaxPerScale int64    `yaml:"max_per_scale"`
	Choices     []string `yaml:"choices"`
}

// Load reads, normalizes and validates a scenario file.
func Load(path string) (*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Normalize()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid scenario %s: %w", path, err)
	}
	return &s, nil
}

// Normalize fills defaults and expands the single-SQL shorthand.
func (s *Scenario) Normalize() {
	if s.Prepare == "" {
		s.Prepare = PrepareNone
	}
	if s.Scale == 0 {
		s.Scale = 1
	}
	if s.SampleInterval == 0 {
		s.SampleInterval = 5 * time.Second
	}
	if s.StatementTimeout == 0 {
		s.StatementTimeout = 30 * time.Second
	}
	for i := range s.Statements {
		st := &s.Statements[i]
		if st.Weight == 0 {
			st.Weight = 1
		}
		if st.Target == "" {
			st.Target = TargetRW
		}
		if len(st.Queries) == 0 && st.SQL != "" {
			params := make([]string, len(st.Args))
			for j, a := range st.Args {
				params[j] = a.Name
			}
			st.Queries = []Query{{SQL: st.SQL, Params: params}}
		}
		for j := range st.Args {
			a := &st.Args[j]
			if a.Type == "" {
				a.Type = "int"
			}
			if a.MaxPerScale > 0 {
				a.Max = a.MaxPerScale * int64(s.Scale)
			}
		}
	}
}

// Validate reports the first problem found, or nil.
func (s *Scenario) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if s.Prepare != PrepareNone && s.Prepare != PrepareTPCB {
		return fmt.Errorf("prepare must be %q or %q, got %q", PrepareNone, PrepareTPCB, s.Prepare)
	}
	if s.Scale < 1 {
		return fmt.Errorf("scale must be >= 1, got %d", s.Scale)
	}
	if len(s.Stages) == 0 {
		return errors.New("at least one stage is required")
	}
	for i, st := range s.Stages {
		if st.Workers < 1 {
			return fmt.Errorf("stages[%d].workers must be >= 1", i)
		}
		if st.Workers > 100_000 {
			return fmt.Errorf("stages[%d].workers must be <= 100000", i)
		}
		if st.Duration <= 0 {
			return fmt.Errorf("stages[%d].duration must be > 0", i)
		}
	}
	if len(s.Statements) == 0 {
		if s.Prepare == PrepareTPCB {
			return nil // built-in TPC-B statements are injected later
		}
		return errors.New("statements are required unless prepare is tpcb")
	}
	names := map[string]bool{}
	for i, st := range s.Statements {
		if st.Name == "" {
			return fmt.Errorf("statements[%d].name is required", i)
		}
		if names[st.Name] {
			return fmt.Errorf("duplicate statement name %q", st.Name)
		}
		names[st.Name] = true
		if st.Weight < 1 {
			return fmt.Errorf("statement %q: weight must be >= 1", st.Name)
		}
		if st.Target != TargetRW && st.Target != TargetRO {
			return fmt.Errorf("statement %q: target must be rw or ro", st.Name)
		}
		if len(st.Queries) == 0 {
			return fmt.Errorf("statement %q: sql or queries is required", st.Name)
		}
		argNames := map[string]bool{}
		for j, a := range st.Args {
			if a.Name == "" {
				return fmt.Errorf("statement %q: args[%d].name is required", st.Name, j)
			}
			if argNames[a.Name] {
				return fmt.Errorf("statement %q: duplicate arg %q", st.Name, a.Name)
			}
			argNames[a.Name] = true
			switch a.Type {
			case "int":
				if a.Max < a.Min {
					return fmt.Errorf("statement %q: arg %q max (%d) < min (%d)", st.Name, a.Name, a.Max, a.Min)
				}
			case "choice":
				if len(a.Choices) == 0 {
					return fmt.Errorf("statement %q: arg %q needs choices", st.Name, a.Name)
				}
			default:
				return fmt.Errorf("statement %q: arg %q type must be int or choice", st.Name, a.Name)
			}
		}
		for j, q := range st.Queries {
			if strings.TrimSpace(q.SQL) == "" {
				return fmt.Errorf("statement %q: queries[%d].sql is empty", st.Name, j)
			}
			for _, p := range q.Params {
				if !argNames[p] {
					return fmt.Errorf("statement %q: queries[%d] references unknown arg %q", st.Name, j, p)
				}
			}
		}
	}
	return nil
}

// MaxWorkers is the largest worker count across stages.
func (s *Scenario) MaxWorkers() int {
	m := 0
	for _, st := range s.Stages {
		if st.Workers > m {
			m = st.Workers
		}
	}
	return m
}

// TotalDuration is the sum of stage durations.
func (s *Scenario) TotalDuration() time.Duration {
	var d time.Duration
	for _, st := range s.Stages {
		d += st.Duration
	}
	return d
}
