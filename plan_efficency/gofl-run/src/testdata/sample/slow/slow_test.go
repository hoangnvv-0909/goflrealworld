package slow

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// BenchmarkDedup has one sub-benchmark per size.
func BenchmarkDedup(b *testing.B) {
	for _, n := range []int{100, 2000} {
		xs := Input(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				Dedup(xs)
				Sum(xs)
			}
		})
	}
}

// size reads the size gofl passes in GOFL_SIZE.
func size(b *testing.B) int {
	s := os.Getenv("GOFL_SIZE")
	if s == "" {
		return 100
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		b.Fatalf("GOFL_SIZE=%q: %v", s, err)
	}
	return n
}

// BenchmarkSized has no sub-benchmarks; it reads its size from GOFL_SIZE.
func BenchmarkSized(b *testing.B) {
	xs := Input(size(b))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Dedup(xs)
	}
}

// BenchmarkNeedsEnv fails unless SAMPLE_DB is set, like a benchmark that
// needs a database.
func BenchmarkNeedsEnv(b *testing.B) {
	if os.Getenv("SAMPLE_DB") == "" {
		b.Fatal("SAMPLE_DB is not set")
	}
	xs := Input(size(b))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Dedup(xs)
	}
}

// BenchmarkSkips skips unless SAMPLE_DB is set.
func BenchmarkSkips(b *testing.B) {
	if os.Getenv("SAMPLE_DB") == "" {
		b.Skip("SAMPLE_DB is not set")
	}
	xs := Input(size(b))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Dedup(xs)
	}
}

// BenchmarkLogs prints while it runs.
func BenchmarkLogs(b *testing.B) {
	for _, n := range []int{100, 2000} {
		xs := Input(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			fmt.Println("starting", n)
			for i := 0; i < b.N; i++ {
				Dedup(xs)
			}
		})
	}
}

// BenchmarkHang never finishes an iteration.
func BenchmarkHang(b *testing.B) {
	for i := 0; i < b.N; i++ {
		time.Sleep(time.Hour)
	}
}
