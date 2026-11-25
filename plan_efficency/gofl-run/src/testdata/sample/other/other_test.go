package other

import (
	"fmt"
	"testing"
)

func work(n int) int {
	s := 0
	for i := 0; i < n; i++ {
		s += i % 7
	}
	return s
}

// BenchmarkDedup has the same name as a benchmark in package slow.
func BenchmarkDedup(b *testing.B) {
	for _, n := range []int{100, 2000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				work(n)
			}
		})
	}
}

// BenchmarkOnlyHere exists in this package only.
func BenchmarkOnlyHere(b *testing.B) {
	for _, n := range []int{100, 2000} {
		b.Run(fmt.Sprintf("size_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				work(n * n)
			}
		})
	}
}
