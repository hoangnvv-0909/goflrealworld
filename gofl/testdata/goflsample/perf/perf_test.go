package perf

import (
	"fmt"
	"testing"
)

func TestDedup(t *testing.T) {
	got := Dedup([]int{1, 2, 1, 3, 2})
	if len(got) != 3 {
		t.Fatal(got)
	}
}

func TestLoadOrders(t *testing.T) {
	if got := LoadOrders(NewFakeDB(3)); len(got) != 3 || len(got[0].Items) != 2 {
		t.Fatal(got)
	}
}

func TestFingerprints(t *testing.T) {
	if got := Fingerprints(Config{Name: "x"}, []int{1, 2}); len(got) != 2 {
		t.Fatal(got)
	}
}

// Sub-benchmarks named n=<size> are the base/scaled load pair used by `gofl perf`.
var sizes = []int{100, 1000, 5000}

func BenchmarkLoadOrders(b *testing.B) {
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				LoadOrders(NewFakeDB(n))
			}
		})
	}
}

func BenchmarkDedup(b *testing.B) {
	for _, n := range sizes {
		xs := make([]int, n)
		for i := range xs {
			xs[i] = i // all distinct: worst case for the nested scan
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				Dedup(xs)
			}
		})
	}
}

func BenchmarkFingerprints(b *testing.B) {
	cfg := Config{Name: "svc", Tags: []string{"a", "b", "c"}, Opts: map[string]string{"k": "v", "x": "y"}}
	for _, n := range sizes {
		ids := make([]int, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				Fingerprints(cfg, ids)
			}
		})
	}
}
