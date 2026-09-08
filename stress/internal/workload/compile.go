// Package workload turns scenario statements into executable units and
// provides the built-in TPC-B schema and transaction.
package workload

import (
	"fmt"
	"math/rand/v2"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
)

// ArgKind is the generator type of an argument.
type ArgKind int

const (
	KindInt ArgKind = iota
	KindChoice
)

// ArgGen produces a random value for one argument.
type ArgGen struct {
	Name    string
	Kind    ArgKind
	Min     int64
	Max     int64
	Choices []string
}

// Gen returns a fresh random value.
func (a ArgGen) Gen(r *rand.Rand) any {
	switch a.Kind {
	case KindChoice:
		return a.Choices[r.IntN(len(a.Choices))]
	default:
		return a.Min + r.Int64N(a.Max-a.Min+1)
	}
}

// Query is SQL plus the indices (into Statement.Args) bound to $1..$n.
type Query struct {
	SQL    string
	Params []int
}

// Statement is a compiled, weighted unit of work.
type Statement struct {
	Index   int
	Name    string
	Weight  int
	Target  config.Target
	Args    []ArgGen
	Queries []Query
}

// Compile resolves parameter names to argument indices. The scenario must
// already be normalized and validated; if it has no statements and
// prepare is tpcb, the built-in TPC-B transaction is used.
func Compile(sc *config.Scenario) ([]Statement, error) {
	src := sc.Statements
	if len(src) == 0 && sc.Prepare == config.PrepareTPCB {
		src = TPCBStatements(sc.Scale)
	}
	out := make([]Statement, 0, len(src))
	for i, cs := range src {
		st := Statement{Index: i, Name: cs.Name, Weight: cs.Weight, Target: cs.Target}
		idx := map[string]int{}
		for j, a := range cs.Args {
			g := ArgGen{Name: a.Name, Min: a.Min, Max: a.Max, Choices: a.Choices}
			if a.Type == "choice" {
				g.Kind = KindChoice
			}
			st.Args = append(st.Args, g)
			idx[a.Name] = j
		}
		for _, q := range cs.Queries {
			cq := Query{SQL: q.SQL}
			for _, p := range q.Params {
				j, ok := idx[p]
				if !ok {
					return nil, fmt.Errorf("statement %q: unknown param %q", cs.Name, p)
				}
				cq.Params = append(cq.Params, j)
			}
			st.Queries = append(st.Queries, cq)
		}
		out = append(out, st)
	}
	return out, nil
}

// Names returns statement names in index order.
func Names(stmts []Statement) []string {
	n := make([]string, len(stmts))
	for i, s := range stmts {
		n[i] = s.Name
	}
	return n
}

// Weights returns statement weights in index order.
func Weights(stmts []Statement) []int {
	w := make([]int, len(stmts))
	for i, s := range stmts {
		w[i] = s.Weight
	}
	return w
}
