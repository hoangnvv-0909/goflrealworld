package diffprof_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"gofl/internal/diffprof"
	"gofl/internal/pproftest"
)

// Profile tổng hợp lớn: nStacks stack, sâu 20–40, 200 hàm, 5 hàm nóng
// xuất hiện ở nhiều ngữ cảnh khác nhau → nhiều lần xuất hiện, nhiều đường.
func synth(seed int64, nStacks int, scale int64) []pproftest.Sample {
	r := rand.New(rand.NewSource(seed))
	out := make([]pproftest.Sample, 0, nStacks)
	for i := 0; i < nStacks; i++ {
		depth := 20 + r.Intn(20)
		st := []string{"testing.(*B).launch@/go/t.go:1", "example.com/big.BenchmarkBig.func1@/src/big/b_test.go:1"}
		for d := 0; d < depth; d++ {
			f := r.Intn(200)
			st = append(st, fmt.Sprintf("example.com/big/p%d.F%d@/src/big/p%d/f.go:%d", f%10, f, f%10, 1+r.Intn(50)))
		}
		c := int64(1 + r.Intn(3))
		if r.Intn(4) == 0 { // ¼ stack kết thúc ở hàm nóng, nặng hơn ở tải lớn
			h := r.Intn(5)
			st = append(st, fmt.Sprintf("example.com/big/hot.H%d@/src/big/hot/h.go:%d", h, 10+h))
			c *= scale
		}
		out = append(out, pproftest.Sample{Stack: st, Count: c})
	}
	return out
}

func TestLargeProfileSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	t0 := time.Now()
	b, s := load(t, synth(1, 20000, 1)), load(t, synth(2, 20000, 8))
	t.Logf("đọc 2 pprof: %v", time.Since(t0).Round(time.Millisecond))
	t0 = time.Now()
	b.Tree()
	s.Tree()
	t.Logf("dựng 2 cây ngữ cảnh gọi: %v", time.Since(t0).Round(time.Millisecond))
	hs, err := ReferenceHotspots(b, s, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"paper", "stack"} {
		t0 := time.Now()
		r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: m, Entry: "BenchmarkBig", MaxPaths: 10})
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, n := range r.Stats.Candidates {
			total += n
		}
		t.Logf("%s: %d stack scaled, %d đường trước cắt, giữ %d, %d hotspot bị cắt, %v",
			m, len(s.Stacks), total, len(r.Paths), r.Stats.TruncatedTarget, time.Since(t0).Round(time.Millisecond))
	}
}
