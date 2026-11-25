package diffprof_test

import (
	"errors"
	"math"
	"testing"

	"gofl/internal/diffprof"
	"gofl/internal/pprofread"
	"gofl/internal/pproftest"
)

// Kịch bản viết tay (chu kỳ 10ms). Kết quả mong đợi tính bằng tay:
//
//	base:   run→Build→slow 2 mẫu; run→fast 3 mẫu
//	scaled: run→Build→slow 40; run→Other→slow 10; run→fast 5; runtime GC 7
//
//	Δslow = 0.50−0.02 = 0.48 (rank 1); Δfast = 0.05−0.03 = 0.02 (rank 2)
//	Cách 1, entry run: run→Build→slow = (0−0)+(0−0)+(0.40−0.02) = 0.38
//	                   run→Other→slow = 0+0+(0.10−0.02) = 0.08
//	                   run→fast       = 0+(0.05−0.03) = 0.02
//	Cách 2:            run→Build→slow = 0.40−0.02 = 0.38
//	                   run→Other→slow = 0.10−0    = 0.10
//	                   run→fast       = 0.05−0.03 = 0.02
const (
	M  = "main.main@/src/app/main.go:10"
	RN = "example.com/app.run@/src/app/run.go:20"
	BU = "example.com/app.Build@/src/app/build.go:30"
	OT = "example.com/app.Other@/src/app/other.go:40"
	SL = "example.com/app.slow@/src/app/slow.go:50"
	FA = "example.com/app.fast@/src/app/fast.go:60"
)

var baseS = []pproftest.Sample{
	{Stack: []string{M, RN, BU, SL}, Count: 2},
	{Stack: []string{M, RN, FA}, Count: 3},
}
var scaledS = []pproftest.Sample{
	{Stack: []string{M, RN, BU, SL}, Count: 40},
	{Stack: []string{M, RN, OT, SL}, Count: 10},
	{Stack: []string{M, RN, FA}, Count: 5},
	{Stack: []string{"runtime.goexit@/go/rt.s:1", "runtime.gcBgMarkWorker@/go/mgc.go:2"}, Count: 7},
}

func load(t *testing.T, s []pproftest.Sample) *pprofread.Profile {
	t.Helper()
	p, err := pprofread.Load(pproftest.Write(t, s), pprofread.Options{Root: "/src/app"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fixture(t *testing.T) (*pprofread.Profile, *pprofread.Profile, []diffprof.Hotspot) {
	b, s := load(t, baseS), load(t, scaledS)
	hs, err := ReferenceHotspots(b, s, 5)
	if err != nil {
		t.Fatal(err)
	}
	return b, s, hs
}

type want struct {
	rank  int
	sig   string
	score float64
	trunc bool
}

func check(t *testing.T, got []diffprof.CriticalPath, w []want) {
	t.Helper()
	if len(got) != len(w) {
		t.Fatalf("got %d paths, want %d: %+v", len(got), len(w), got)
	}
	for i := range w {
		g := got[i]
		if g.TargetRank != w[i].rank || g.Signature != w[i].sig || math.Abs(g.Score-w[i].score) > 1e-9 || g.Truncated != w[i].trunc {
			t.Errorf("path %d = {%d %q %v %v}, want %+v", i, g.TargetRank, g.Signature, g.Score, g.Truncated, w[i])
		}
	}
}

const (
	sigBuild = "run.go(app.run) -> build.go(app.Build) -> slow.go(app.slow)"
	sigOther = "run.go(app.run) -> other.go(app.Other) -> slow.go(app.slow)"
	sigFast  = "run.go(app.run) -> fast.go(app.fast)"
)

func TestReferenceHotspots(t *testing.T) {
	_, _, hs := fixture(t)
	if len(hs) != 2 || hs[0].Function != "example.com/app.slow" || math.Abs(hs[0].DeltaSelfSeconds-0.48) > 1e-9 ||
		math.Abs(hs[0].NDC-0.96) > 1e-9 || hs[0].ScaledOccurrences != 2 || hs[0].BaselineOccurrences != 1 {
		t.Fatalf("%+v", hs)
	}
}

func TestPaperMethod(t *testing.T) {
	b, s, hs := fixture(t)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "paper", Entry: "app.run"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "paper" || r.Stats.EntryUsed != "example.com/app.run" {
		t.Fatalf("%+v", r.Stats)
	}
	check(t, r.Paths, []want{{1, sigBuild, 0.38, false}, {1, sigOther, 0.08, false}, {2, sigFast, 0.02, false}})
	f := r.Paths[0].Frames
	if f[0].Name != "app.run" || f[0].Line != 20 || f[2].File != "slow.go" || f[2].Line != 50 {
		t.Fatalf("frames %+v", f)
	}
}

func TestPaperRootEntryTrimsNoise(t *testing.T) {
	// Không có điểm vào: bắt đầu từ gốc; hai stack có tiền tố runtime khác
	// nhau nhưng giống nhau sau khi bỏ khung nhiễu → gộp, giữ score cao hơn.
	base := []pproftest.Sample{{Stack: []string{"runtime.goexit@/rt:1", RN, SL}, Count: 1}}
	scaled := []pproftest.Sample{
		{Stack: []string{"runtime.goexit@/rt:1", "testing.tRunner@/t:1", RN, SL}, Count: 30},
		{Stack: []string{"runtime.goexit@/rt:1", RN, SL}, Count: 20},
	}
	b, s := load(t, base), load(t, scaled)
	hs, _ := ReferenceHotspots(b, s, 5)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// mean_base(slow)=0.01; 0.30−0.01=0.29 thắng 0.20−0.01=0.19
	check(t, r.Paths, []want{{1, "run.go(app.run) -> slow.go(app.slow)", 0.29, false}})
	if r.Stats.Candidates["example.com/app.slow"] != 1 {
		t.Fatalf("%+v", r.Stats)
	}
}

func TestEntryNotCovering(t *testing.T) {
	b, s, hs := fixture(t)
	_, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "paper", Entry: "app.Build"})
	var pe *diffprof.Error
	if !errors.As(err, &pe) || pe.Code != diffprof.CodeEntryNotCovering {
		t.Fatalf("err=%v", err)
	}
	_, err = diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "paper", Entry: "NoSuchFunc"})
	if !errors.As(err, &pe) || pe.Code != diffprof.CodeEntryNotCovering {
		t.Fatalf("err=%v", err)
	}
}

func TestAutoFallsBackToStack(t *testing.T) {
	b, s, hs := fixture(t)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "auto", Entry: "app.Build"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "stack" || r.Stats.FallbackReason == "" {
		t.Fatalf("%+v", r)
	}
	// Stack qua Build cắt tại Build; stack qua Other và fast không chứa Build → bắt đầu từ main.
	check(t, r.Paths, []want{
		{1, "build.go(app.Build) -> slow.go(app.slow)", 0.38, false},
		{1, "main.go(main.main) -> " + sigOther, 0.10, false},
		{2, "main.go(main.main) -> " + sigFast, 0.02, false},
	})
	if r.Stats.OutsideEntry != 2 {
		t.Fatalf("outside=%d", r.Stats.OutsideEntry)
	}
}

func TestStackMethod(t *testing.T) {
	b, s, hs := fixture(t)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "stack", Entry: "app.run"})
	if err != nil {
		t.Fatal(err)
	}
	check(t, r.Paths, []want{{1, sigBuild, 0.38, false}, {1, sigOther, 0.10, false}, {2, sigFast, 0.02, false}})
}

func TestTruncation(t *testing.T) {
	b, s, hs := fixture(t)
	for _, m := range []string{"paper", "stack"} {
		r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: m, Entry: "app.run", MaxPaths: 1})
		if err != nil {
			t.Fatal(err)
		}
		check(t, r.Paths, []want{{1, sigBuild, 0.38, true}, {2, sigFast, 0.02, false}})
		if r.Stats.TruncatedTarget != 1 {
			t.Fatalf("%s: %+v", m, r.Stats)
		}
	}
}

func TestBadMethod(t *testing.T) {
	b, s, hs := fixture(t)
	if _, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "x"}); err == nil {
		t.Fatal("want error")
	}
}

func TestEntryUnionForGoBenchmarks(t *testing.T) {
	// Closure benchmark xuất hiện ở 2 ngữ cảnh: (*B).run1 (dò, b.N=1) và (*B).launch.
	const (
		L  = "testing.(*B).launch@/go/testing/benchmark.go:1"
		R1 = "testing.(*B).run1.func1@/go/testing/benchmark.go:2"
		RN = "testing.(*B).runN@/go/testing/benchmark.go:3"
		BF = "example.com/app.BenchmarkX.func1@/src/app/x_test.go:9"
	)
	base := []pproftest.Sample{{Stack: []string{L, RN, BF, SL}, Count: 1}}
	scaled := []pproftest.Sample{
		{Stack: []string{L, RN, BF, SL}, Count: 50},
		{Stack: []string{R1, RN, BF, SL}, Count: 2},
	}
	b, s := load(t, base), load(t, scaled)
	hs, _ := ReferenceHotspots(b, s, 5)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "paper", Entry: "BenchmarkX"})
	if err != nil {
		t.Fatal(err)
	}
	// Hai lần xuất hiện cùng chữ ký → gộp, giữ score cao nhất.
	// mean_base(slow)=0.01, mean_base(BF)=0 → nút launch: 0 + (0.50−0.01) = 0.49; nút run1: 0.02−0.01 = 0.01
	check(t, r.Paths, []want{{1, "x_test.go(app.BenchmarkX.func1) -> slow.go(app.slow)", 0.49, false}})
	if r.Stats.EntryNodes != 2 || r.Stats.Candidates["example.com/app.slow"] != 1 {
		t.Fatalf("%+v", r.Stats)
	}
}

// Per-operation scores: every self time is multiplied by the factor of its
// profile before the difference is taken (base 2, scaled 0.5 here), so that
// the score has the unit of the hotspots when they were ranked per operation.
//
//	paper: Build path  0.40*0.5 - 0.02*2 = 0.16;  Other 0.10*0.5 - 0.02*2 = 0.01
//	       fast        0.05*0.5 - 0.03*2 = -0.035
//	stack: Build 0.16;  Other 0.10*0.5 - 0 = 0.05;  fast -0.035
func TestPathsScoresPerOperation(t *testing.T) {
	b, s, hs := fixture(t)
	opt := diffprof.PathsOptions{Method: "paper", Entry: "app.run", BaseFactor: 2, ScaledFactor: 0.5}
	r, err := diffprof.Paths(b, s, hs, opt)
	if err != nil {
		t.Fatal(err)
	}
	check(t, r.Paths, []want{{1, sigBuild, 0.16, false}, {1, sigOther, 0.01, false}, {2, sigFast, -0.035, false}})
	opt.Method = "stack"
	r, err = diffprof.Paths(b, s, hs, opt)
	if err != nil {
		t.Fatal(err)
	}
	check(t, r.Paths, []want{{1, sigBuild, 0.16, false}, {1, sigOther, 0.05, false}, {2, sigFast, -0.035, false}})
}

// Small per-operation scores keep their digits (9 places, not 6).
func TestPathsPerOperationKeepsSmallScores(t *testing.T) {
	b, s, hs := fixture(t)
	r, err := diffprof.Paths(b, s, hs, diffprof.PathsOptions{Method: "stack", Entry: "app.run", BaseFactor: 1e-5, ScaledFactor: 1e-5})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Paths[0].Score; math.Abs(got-0.38e-5) > 1e-12 {
		t.Errorf("score %v, want 3.8e-06", got)
	}
}
