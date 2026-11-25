// Package slow is a tiny program with a known performance bug. gofl's tests
// use it to exercise the benchmark mode.
package slow

// Dedup removes duplicates and keeps the first occurrence of each value.
// The bug: seen is a slice, so every lookup scans it and Dedup is quadratic.
func Dedup(xs []int) []int {
	var seen []int
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if contains(seen, x) {
			continue
		}
		seen = append(seen, x)
		out = append(out, x)
	}
	return out
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Sum is linear and only adds noise.
func Sum(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

// Input returns n values with some repeats.
func Input(n int) []int {
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i * 7919 % (n/2 + 1)
	}
	return xs
}
