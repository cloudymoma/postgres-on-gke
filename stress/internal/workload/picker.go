package workload

import (
	"math/rand/v2"
	"sort"
)

// Picker selects an index with probability proportional to its weight.
type Picker struct {
	cum   []int
	total int
}

// NewPicker builds a picker from positive weights.
func NewPicker(weights []int) *Picker {
	p := &Picker{cum: make([]int, len(weights))}
	for i, w := range weights {
		p.total += w
		p.cum[i] = p.total
	}
	return p
}

// Pick returns a weighted random index.
func (p *Picker) Pick(r *rand.Rand) int {
	if len(p.cum) == 1 {
		return 0
	}
	n := r.IntN(p.total)
	return sort.SearchInts(p.cum, n+1)
}
