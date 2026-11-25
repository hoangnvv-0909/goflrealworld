package linearlookup

import (
	"strconv"
	"testing"
)

var matchCount int

func BenchmarkLinearLookup(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			values := make([]int, size)
			queries := make([]int, size)
			for i := range values {
				values[i] = i
				queries[i] = size - 1 - i
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matchCount = CountMatches(values, queries)
			}
			b.StopTimer()
			if matchCount != size {
				b.Fatalf("got %d matches, want %d", matchCount, size)
			}
		})
	}
}
