// Package sem holds seeded semantic (logic) bugs: no panic, no slowness, a test fails
// (benchmark layer 1, owner: P6). Failing and passing tests execute the same lines.
package sem

import (
	"math"
	"time"
)

// Pages returns how many pages are needed for total items.
func Pages(total, pageSize int) int {
	return total / pageSize // BUG:S1 boundary: drops the partial last page, want (total+pageSize-1)/pageSize
}

// SameDay reports whether a and b fall on the same calendar day in loc.
func SameDay(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.UTC().Date() // BUG:S2 timezone: ignores loc, should be a.In(loc).Date()
	by, bm, bd := b.UTC().Date() // BUG:S2 (same defect, second operand)
	return ay == by && am == bm && ad == bd
}

// MaxOf returns the largest element of xs (xs must be non-empty).
func MaxOf(xs []int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m { // BUG:S3 wrong comparison operator, should be x > m
			m = x
		}
	}
	return m
}

// Softmax returns the normalised exponentials of xs, shifted by the max for stability.
func Softmax(xs []float64) []float64 {
	m := math.Inf(-1)
	for _, x := range xs {
		m = math.Max(m, x)
	}
	out := make([]float64, len(xs))
	sum := 0.0
	for i, x := range xs {
		out[i] = math.Exp(x + m) // BUG:S4 sign error, should be x - m; overflows for large inputs
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}
