package pprofread_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gofl/internal/pprofread"
	"gofl/internal/pproftest"
)

const R = "/src/app" // gốc dự án giả

var sample = []pproftest.Sample{
	{Stack: []string{"main.main@/src/app/main.go:10", "example.com/app.run@/src/app/run.go:20", "example.com/app.slow@/src/app/slow.go:5"}, Count: 40},
	{Stack: []string{"main.main@/src/app/main.go:10", "example.com/app.run@/src/app/run.go:21", "example.com/app.slow@/src/app/slow.go:6"}, Count: 10},
	{Stack: []string{"main.main@/src/app/main.go:10", "example.com/app.run@/src/app/run.go:22", "example.com/app.fast@/src/app/fast.go:3"}, Count: 5},
	{Stack: []string{"runtime.goexit@/go/src/runtime/asm.s:1", "runtime.gcBgMarkWorker@/go/src/runtime/mgc.go:9"}, Count: 7},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLoadAndSelfTable(t *testing.T) {
	p, err := pprofread.Load(pproftest.Write(t, sample), pprofread.Options{Root: R})
	if err != nil {
		t.Fatal(err)
	}
	if !near(p.SecondsPerSample, 0.01) || p.TotalSamples != 62 {
		t.Fatalf("period=%v total=%d", p.SecondsPerSample, p.TotalSamples)
	}
	rows := p.SelfTable()
	want := []struct {
		fn, file string
		n        int64
		noise    bool
	}{
		{"example.com/app.slow", "slow.go", 50, false},
		{"runtime.gcBgMarkWorker", "/go/src/runtime/mgc.go", 7, true},
		{"example.com/app.fast", "fast.go", 5, false},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	for i, w := range want {
		r := rows[i]
		if r.Function != w.fn || r.File != w.file || r.Samples != w.n || r.Noise != w.noise || !near(r.Seconds, float64(w.n)*0.01) {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
}

func TestTreeOccurrencesAndLines(t *testing.T) {
	p, _ := pprofread.Load(pproftest.Write(t, sample), pprofread.Options{Root: R})
	tr := p.Tree()
	run := pprofread.Key{Name: "example.com/app.run", File: "run.go"}
	slow := pprofread.Key{Name: "example.com/app.slow", File: "slow.go"}
	// run gọi slow ở hai dòng khác nhau nhưng vẫn là MỘT nút (định danh theo hàm+tệp)
	if tr.Occurrences(run) != 1 || tr.Occurrences(slow) != 1 {
		t.Fatalf("occ run=%d slow=%d", tr.Occurrences(run), tr.Occurrences(slow))
	}
	n := tr.ByFunc[slow][0]
	if n.SelfSamples != 50 || !near(n.Self, 0.5) || n.Line() != 5 {
		t.Fatalf("slow node self=%d line=%d", n.SelfSamples, n.Line())
	}
	r := tr.ByFunc[run][0]
	if r.TotalSamples != 55 || r.SelfSamples != 0 || r.Line() != 20 {
		t.Fatalf("run node total=%d self=%d line=%d", r.TotalSamples, r.SelfSamples, r.Line())
	}
	if tr.Root.TotalSamples != 62 {
		t.Fatalf("root total=%d", tr.Root.TotalSamples)
	}
	path := pprofread.PathFrom(tr.Root, n)
	if len(path) != 3 || path[0].Name != "main.main" {
		t.Fatalf("path=%v", path)
	}
	if !near(tr.MeanSelf(slow), 0.5) || tr.MeanSelf(pprofread.Key{Name: "x"}) != 0 {
		t.Fatal("MeanSelf")
	}
}

func TestInlineFramesExpanded(t *testing.T) {
	// main.main gọi app.outer; app.helper được inline vào app.outer.
	s := []pproftest.Sample{{Stack: []string{"main.main@/src/app/main.go:1",
		"example.com/app.outer@/src/app/o.go:7|example.com/app.helper@/src/app/h.go:3"}, Count: 4}}
	p, err := pprofread.Load(pproftest.Write(t, s), pprofread.Options{Root: R})
	if err != nil {
		t.Fatal(err)
	}
	fr := p.Stacks[0].Frames
	if len(fr) != 3 || fr[1].Name != "example.com/app.outer" || fr[2].Name != "example.com/app.helper" || fr[2].Line != 3 {
		t.Fatalf("frames=%+v", fr)
	}
}

func TestNoProfile(t *testing.T) {
	_, err := pprofread.Load(filepath.Join(t.TempDir(), "missing.pprof"), pprofread.Options{})
	if !errors.Is(err, pprofread.ErrNoProfile) {
		t.Fatalf("missing: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.pprof")
	os.WriteFile(bad, []byte("not a profile"), 0o644)
	if _, err := pprofread.Load(bad, pprofread.Options{}); !errors.Is(err, pprofread.ErrNoProfile) {
		t.Fatalf("bad: %v", err)
	}
	// Profile hợp lệ nhưng 0 mẫu (tải nhỏ chạy quá nhanh) không phải lỗi.
	empty := pproftest.Write(t, nil)
	p, err := pprofread.Load(empty, pprofread.Options{})
	if err != nil || p.TotalSamples != 0 || len(p.SelfTable()) != 0 || p.Tree().Root.TotalSamples != 0 {
		t.Fatalf("empty: %v", err)
	}
}

func TestShortNameAndMatch(t *testing.T) {
	cases := map[string]string{
		"example.com/app/internal/report.BuildRows":      "report.BuildRows",
		"example.com/app/internal/report.(*Builder).Add": "report.(*Builder).Add",
		"main.run": "main.run",
		"example.com/x.F[go.shape.*example.com/y.T]": "x.F[go.shape.*example.com/y.T]",
		"example.com/app.BenchmarkBuildRows.func1":   "app.BenchmarkBuildRows.func1",
	}
	for in, want := range cases {
		if got := pprofread.ShortName(in); got != want {
			t.Errorf("ShortName(%q)=%q want %q", in, got, want)
		}
	}
	yes := [][2]string{
		{"example.com/app.BenchmarkBuildRows", "BenchmarkBuildRows"},
		{"example.com/app.BenchmarkBuildRows.func1", "BenchmarkBuildRows"},
		{"example.com/app.run", "app.run"},
		{"example.com/app.run", "example.com/app.run"},
	}
	no := [][2]string{
		{"example.com/app.BenchmarkBuildRowsFast", "BenchmarkBuildRows"},
		{"example.com/app.run", ""},
		{"example.com/app.rerun", "run"},
	}
	for _, c := range yes {
		if !pprofread.MatchEntry(c[0], c[1]) {
			t.Errorf("MatchEntry(%q,%q) = false", c[0], c[1])
		}
	}
	for _, c := range no {
		if pprofread.MatchEntry(c[0], c[1]) {
			t.Errorf("MatchEntry(%q,%q) = true", c[0], c[1])
		}
	}
}

func TestRelFile(t *testing.T) {
	cases := []struct{ file, root, want string }{
		{"/proj/internal/a.go", "/proj", "internal/a.go"},
		{"/proj/internal/a.go", "/proj/", "internal/a.go"},
		{"/usr/local/go/src/sort/sort.go", "/proj", "/usr/local/go/src/sort/sort.go"},
		{"/projects/a.go", "/proj", "/projects/a.go"},
		{"/proj/../etc/x.go", "/proj", "/etc/x.go"},
		{"internal/a.go", "/proj", "internal/a.go"},
		{`C:\proj\internal\a.go`, `C:\proj`, "internal/a.go"},
		{`c:\Proj\a.go`, `C:\proj`, "a.go"},
		{`C:\Go\src\x.go`, `C:\proj`, "C:/Go/src/x.go"},
		{`C:\Users\u\go\pkg\mod\x.go`, `C:\proj`, "C:/Users/u/go/pkg/mod/x.go"},
		{"", "/proj", ""},
	}
	for _, c := range cases {
		if got := pprofread.RelFile(c.file, c.root); got != c.want {
			t.Errorf("RelFile(%q, %q) = %q, want %q", c.file, c.root, got, c.want)
		}
	}
}

// A period other than 10 ms, and a profile with only the cpu/nanoseconds
// column (both were tested on the first reader of Compute, now removed).
func TestPeriodAndCPUOnly(t *testing.T) {
	raw := pproftest.Build([]pproftest.Sample{{Stack: []string{"main.f@/x.go:1"}, Count: 5}})
	raw.Period = 4_000_000
	raw.Sample[0].Value = []int64{5, 5 * 4_000_000}
	p, err := pprofread.FromProfile(raw, pprofread.Options{})
	if err != nil || p.TotalSamples != 5 || math.Abs(p.SecondsPerSample-0.004) > 1e-12 || math.Abs(p.Stacks[0].Seconds-0.02) > 1e-12 {
		t.Fatalf("period: %+v %v", p, err)
	}
	raw = pproftest.Build([]pproftest.Sample{{Stack: []string{"main.f@/x.go:1"}, Count: 2}})
	raw.SampleType = raw.SampleType[1:] // only cpu/nanoseconds
	raw.Sample[0].Value = []int64{2 * pproftest.PeriodNS}
	p, err = pprofread.FromProfile(raw, pprofread.Options{})
	if err != nil || p.TotalSamples != 2 || p.Stacks[0].Samples != 2 {
		t.Fatalf("cpu only: %+v %v", p, err)
	}
}
