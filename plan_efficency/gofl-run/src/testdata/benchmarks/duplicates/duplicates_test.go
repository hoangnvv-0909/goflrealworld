package duplicates

import (
	"strconv"
	"testing"
)

var uniqueValues []int

func BenchmarkDuplicates(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			values := make([]int, size)
			for i := range values {
				values[i] = i / 2
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				uniqueValues = Unique(values)
			}
			b.StopTimer()
			if len(uniqueValues) != size/2 {
				b.Fatalf("got %d unique values, want %d", len(uniqueValues), size/2)
			}
		})
	}
}
