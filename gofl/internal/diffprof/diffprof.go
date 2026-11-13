// Package diffprof compares CPU profiles taken at two input sizes to find
// time-inefficiency hotspots and the paths that lead to them.
//
// Owners: TV4 (Compute), TV6 (pprof reading, call tree, Paths).
// Paper: E, sections 3.2-3.3 (claims E3-E8).
//
// Differs from the paper: the scaled workload comes from Go sub-benchmarks
// named by size (b.Run("n=1000")) instead of LLM-generated tests.
// Known limit (E15 and Go specifics): a CPU profile does not see I/O wait.
package diffprof

import (
	"errors"

	"gofl/internal/finding"
)

var ErrNotImplemented = errors.New("diffprof: not implemented yet")

// Pair is two pprof files of the same workload at base and scaled size.
type Pair struct {
	Base   string
	Scaled string
}

// Hotspot is a function whose self time grows with input size.
type Hotspot struct {
	Func      string
	File      string
	DeltaSelf float64 // differential self time (E5, formula 1)
	NDC       float64 // normalised differential cost (E5, formula 2)
}

// Path is a compressed entry-to-hotspot call path (E7, E8):
// a sequence of (file, function) without line numbers.
type Path struct {
	Frames   []finding.Location
	Hotspot  Hotspot
	CumDelta float64 // cumulative delta-self along the path
}

// Compute returns the top-k hotspots.
func Compute(p Pair, topK int) ([]Hotspot, error) {
	return nil, ErrNotImplemented
}

// Paths reconstructs and compresses paths from the entry function to each hotspot.
func Paths(p Pair, hs []Hotspot) ([]Path, error) {
	return nil, ErrNotImplemented
}
