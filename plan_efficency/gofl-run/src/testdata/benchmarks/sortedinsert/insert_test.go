package sortedinsert

import (
	"sort"
	"strconv"
	"testing"
)

var sortedValues []int

func BenchmarkSortedInsert(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			values := make([]int, size)
			for i := range values {
				values[i] = size - i
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sortedValues = SortByInsertion(values)
			}
			b.StopTimer()
			if len(sortedValues) != size || !sort.IntsAreSorted(sortedValues) {
				b.Fatal("expected a sorted result with all input values")
			}
		})
	}
}
