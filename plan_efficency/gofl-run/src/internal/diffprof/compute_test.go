package diffprof

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/contract"
	"gofl/internal/pprofread"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func rank(t *testing.T, base, scaled *pprofread.Profile, o Options) ([]Hotspot, string, error) {
	t.Helper()
	var warn bytes.Buffer
	o.Warn = &warn
	if o.Dir == "" {
		o.Dir = "/proj"
	}
	h, err := Rank(base, scaled, o)
	return h, warn.String(), err
}

func fnames(h []Hotspot) []string {
	var out []string
	for _, x := range h {
		out = append(out, x.Function)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

// Period 10 ms. Samples by function (leaf self):
//
//	      base  scaled  delta(samples)
//	Y       30     420   +390   (1 node in base, 2 in scaled)
//	X       10      15     +5
//	N        0       7     +7   (only in scaled)
//	Z        5       2     -3   (dropped)
//	runtime.gcBgMarkWorker 50 -> 500, testing.(*B).launch 0 -> 100  (noise)
//
// Sum of positive deltas = 390 + 5 + 7 = 402 samples = 4.02 s, so
// NDC(Y) = 390/402 = 0.970149, NDC(N) = 7/402 = 0.017413, NDC(X) = 5/402 = 0.012438.
// Order by delta: Y, N, X.
func coreProfiles() (*pprofread.Profile, *pprofread.Profile) {
	base := prof(ms10, stk(10, "main", "X"), stk(30, "main", "Y"), stk(5, "main", "Z"), stk(50, "main", "runtime.gcBgMarkWorker"))
	scaled := prof(ms10, stk(15, "main", "X"), stk(400, "main", "Y"), stk(20, "main", "W", "Y"), stk(7, "main", "N"),
		stk(2, "main", "Z"), stk(500, "main", "runtime.gcBgMarkWorker"), stk(100, "main", "testing.(*B).launch"))
	return base, scaled
}

func TestRankHandComputed(t *testing.T) {
	base, scaled := coreProfiles()
	h, _, err := rank(t, base, scaled, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(fnames(h), []string{"Y", "N", "X"}) {
		t.Fatalf("order %v", fnames(h))
	}
	y := h[0]
	if y.Rank != 1 || y.BaselineSamples != 30 || y.ScaledSamples != 420 || y.BaselineOccurrences != 1 || y.ScaledOccurrences != 2 {
		t.Errorf("Y: %+v", y)
	}
	if y.BaselineSelfSeconds != 0.3 || y.ScaledSelfSeconds != 4.2 || y.DeltaSelfSeconds != 3.9 || y.NDC != 0.970149 {
		t.Errorf("Y numbers: %+v", y)
	}
	if h[1].NDC != 0.017413 || h[2].NDC != 0.012438 || h[1].BaselineSamples != 0 || h[1].BaselineOccurrences != 0 {
		t.Errorf("N, X: %+v %+v", h[1], h[2])
	}
	if y.File != "a.go" {
		t.Errorf("file %q", y.File)
	}
	if y.BaselinePerOpSeconds != nil {
		t.Error("per-op fields without bench")
	}
}

func TestRankTopKKeepsDenominatorOfAllPositives(t *testing.T) {
	base, scaled := coreProfiles()
	h, _, _ := rank(t, base, scaled, Options{TopK: 2})
	if len(h) != 2 || h[0].NDC != 0.970149 {
		t.Errorf("top-2: %+v", h)
	}
}

func TestRankTieBreaksByName(t *testing.T) {
	base := prof(ms10, stk(1, "main", "c"))
	scaled := prof(ms10, stk(10, "main", "c"), stk(5, "main", "b"), stk(5, "main", "a"))
	h, _, _ := rank(t, base, scaled, Options{})
	if !eq(fnames(h), []string{"c", "a", "b"}) { // c +9, then a and b +5 by name
		t.Errorf("order %v", fnames(h))
	}
}

func TestRankSameNameDifferentFile(t *testing.T) {
	base := prof(ms10, stk(10, "main", "f@/proj/a.go"))
	scaled := prof(ms10, stk(20, "main", "f@/proj/a.go"), stk(30, "main", "f@/proj/b.go"))
	h, _, _ := rank(t, base, scaled, Options{})
	if len(h) != 2 || h[0].File != "b.go" || h[0].DeltaSelfSeconds != 0.3 || h[1].File != "a.go" || h[1].DeltaSelfSeconds != 0.1 {
		t.Errorf("%+v", h)
	}
}

func TestRankInlinedLeafIsInnermost(t *testing.T) {
	// "b+a" is one location: a was inlined into b's caller position, b is
	// the innermost function, so the samples are b's self time.
	base := prof(ms10, stk(1, "main"))
	scaled := prof(ms10, stk(1, "main"), stk(5, "main", "b+a"))
	h, _, _ := rank(t, base, scaled, Options{})
	if !eq(fnames(h), []string{"b"}) {
		t.Errorf("%v", fnames(h))
	}
}

func TestRankNoPositiveDelta(t *testing.T) {
	p := prof(ms10, stk(5, "main", "f"))
	h, _, err := rank(t, p, p, Options{})
	var e *Error
	if !errors.As(err, &e) || e.Code != contract.NoPositiveDelta {
		t.Fatalf("err %v", err)
	}
	if h == nil || len(h) != 0 {
		t.Errorf("hotspots %#v, want empty non-nil", h)
	}
}

func TestRankFewerThanTopK(t *testing.T) {
	base, scaled := coreProfiles()
	h, _, _ := rank(t, base, scaled, Options{TopK: 5})
	if len(h) != 3 {
		t.Errorf("len %d", len(h))
	}
}

func TestRankPeriodNot10ms(t *testing.T) {
	// 4 ms period: 10 -> 60 samples is 0.04 s -> 0.24 s.
	base := prof(4_000_000, stk(10, "main", "f"))
	scaled := prof(4_000_000, stk(60, "main", "f"))
	h, _, _ := rank(t, base, scaled, Options{})
	if h[0].BaselineSelfSeconds != 0.04 || h[0].ScaledSelfSeconds != 0.24 || h[0].DeltaSelfSeconds != 0.2 || h[0].NDC != 1 {
		t.Errorf("%+v", h[0])
	}
}

func TestRankDifferentPeriodsPerProfile(t *testing.T) {
	// The two profiles may differ in period: seconds are what compare.
	base := prof(10_000_000, stk(10, "main", "f"))   // 0.1 s
	scaled := prof(1_000_000, stk(500, "main", "f")) // 0.5 s
	h, _, _ := rank(t, base, scaled, Options{})
	if h[0].DeltaSelfSeconds != 0.4 {
		t.Errorf("%+v", h[0])
	}
}

// The two hotspots of testdata/contract/hotspots.json.
func TestRankMatchesContractExample(t *testing.T) {
	const br = "example.com/app/internal/report.BuildRows@/proj/internal/report/build.go"
	const fm = "example.com/app/internal/report.format@/proj/internal/report/format.go"
	base := prof(ms10, stk(20, "main", br), stk(10, "main", "wrap", br), stk(10, "main", fm), stk(100, "main", "idle"))
	scaled := prof(ms10, stk(300, "main", br), stk(100, "main", "wrap", br), stk(20, "main", "wrap2", br),
		stk(170, "main", fm), stk(100, "main", "idle"))
	h, _, err := rank(t, base, scaled, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contract", "hotspots.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want []Hotspot
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(h) != len(want) {
		t.Fatalf("got %d hotspots, want %d", len(h), len(want))
	}
	for i, w := range want {
		g := h[i]
		if g.Rank != w.Rank || g.Function != w.Function || g.File != w.File ||
			g.BaselineSelfSeconds != w.BaselineSelfSeconds || g.ScaledSelfSeconds != w.ScaledSelfSeconds ||
			!near(g.DeltaSelfSeconds, w.DeltaSelfSeconds, 1e-9) ||
			g.BaselineSamples != w.BaselineSamples || g.ScaledSamples != w.ScaledSamples ||
			g.BaselineOccurrences != w.BaselineOccurrences || g.ScaledOccurrences != w.ScaledOccurrences {
			t.Errorf("hotspot %d:\n got %+v\nwant %+v", i+1, g, w)
		}
		if !near(g.NDC, w.NDC, 0.005) { // the example rounds NDC to two places
			t.Errorf("hotspot %d ndc %v, want about %v", i+1, g.NDC, w.NDC)
		}
	}
}

// --- -attribute-runtime ---------------------------------------------------

// Alloc's time sits in runtime.mallocgc (below Alloc), Slow grows a little.
// Off: runtime.* is noise, so Alloc has self 0 and only Slow is a hotspot.
// On: mallocgc's 500 samples (base 10) go to Alloc: delta 490; Slow +10;
// NDC(Alloc) = 490/500 = 0.98.
func allocProfiles() (*pprofread.Profile, *pprofread.Profile) {
	base := prof(ms10, stk(10, "main", "app.Alloc", "runtime.mallocgc"), stk(10, "main", "app.Slow"), stk(100, "main", "app.Other"))
	scaled := prof(ms10, stk(500, "main", "app.Alloc", "runtime.mallocgc"), stk(20, "main", "app.Slow"), stk(100, "main", "app.Other"))
	return base, scaled
}

func TestRankRuntimeDroppedByDefault(t *testing.T) {
	base, scaled := allocProfiles()
	h, _, err := rank(t, base, scaled, Options{})
	if err != nil || !eq(fnames(h), []string{"app.Slow"}) {
		t.Fatalf("%v %v", fnames(h), err)
	}
}

func TestRankAttributeRuntime(t *testing.T) {
	base, scaled := allocProfiles()
	h, _, err := rank(t, base, scaled, Options{AttributeRuntime: true})
	if err != nil || !eq(fnames(h), []string{"app.Alloc", "app.Slow"}) {
		t.Fatalf("%v %v", fnames(h), err)
	}
	if h[0].DeltaSelfSeconds != 4.9 || h[0].NDC != 0.98 || h[0].ScaledSamples != 500 || h[0].BaselineSamples != 10 {
		t.Errorf("%+v", h[0])
	}
}

func TestAttributeRuntimeNestedAndEdges(t *testing.T) {
	// runtime.mallocgc > runtime.memclr below Alloc: both go to Alloc.
	// A stack made only of runtime frames has no caller: dropped.
	// testing.* is still dropped; runtime under testing goes to testing, so
	// it is dropped with it.
	base := prof(ms10, stk(1, "main"))
	scaled := prof(ms10, stk(1, "main"),
		stk(3, "main", "app.Alloc", "runtime.mallocgc", "runtime.memclr"),
		stk(50, "runtime.mcall", "runtime.gcBgMarkWorker"),
		stk(40, "main", "testing.(*B).runN", "runtime.mallocgc"),
		stk(2, "main", "testing.(*B).runN"))
	h, _, err := rank(t, base, scaled, Options{AttributeRuntime: true})
	if err != nil || !eq(fnames(h), []string{"app.Alloc"}) || h[0].ScaledSamples != 3 || h[0].NDC != 1 {
		t.Fatalf("%+v %v", h, err)
	}
}

// --- per-operation delta --------------------------------------------------

// Base (T = 100 samples = 1.0 s, all under BenchmarkX): L 50, Q 50, 1 ms/op.
// Scaled (T = 1.0 s): L 40, Q 60, 10 ms/op.
// Raw: L -10 (dropped), Q +10 -> top = [Q].
// Per op: factor = ns_per_op / T_ns = 1e6/1e9 = 1e-3 (base), 1e7/1e9 = 1e-2 (scaled).
//
//	L: 0.5*1e-3 = 0.0005 -> 0.4*1e-2 = 0.004, delta 0.0035
//	Q: 0.5*1e-3 = 0.0005 -> 0.6*1e-2 = 0.006, delta 0.0055
//
// NDC per op: Q 0.0055/0.009 = 0.611111, L 0.0035/0.009 = 0.388889.
func perOpProfiles() (*pprofread.Profile, *pprofread.Profile, *contract.Bench) {
	base := prof(ms10, stk(50, "main", "pkg.BenchmarkX", "L"), stk(50, "main", "pkg.BenchmarkX", "Q"))
	scaled := prof(ms10, stk(40, "main", "pkg.BenchmarkX", "L"), stk(60, "main", "pkg.BenchmarkX", "Q"))
	return base, scaled, &contract.Bench{BaseNsPerOp: 1e6, ScaledNsPerOp: 1e7}
}

func TestPerOpFieldsAndRankByRaw(t *testing.T) {
	base, scaled, bench := perOpProfiles()
	h, warn, err := rank(t, base, scaled, Options{Bench: bench, RankBy: RankRaw})
	if err != nil || !eq(fnames(h), []string{"Q"}) {
		t.Fatalf("%v %v", fnames(h), err)
	}
	q := h[0]
	if q.BaselinePerOpSeconds == nil || *q.BaselinePerOpSeconds != 0.0005 || *q.ScaledPerOpSeconds != 0.006 ||
		*q.DeltaPerOpSeconds != 0.0055 || *q.NDCPerOp != 0.611111 {
		t.Errorf("%+v", q)
	}
	if q.DeltaSelfSeconds != 0.1 || q.NDC != 1 {
		t.Errorf("raw fields: %+v", q)
	}
	// the two rankings differ: the warning names L, the function raw missed
	if !strings.Contains(warn, "L") || !strings.Contains(warn, "differ") {
		t.Errorf("warning %q", warn)
	}
}

func TestRankByPerOp(t *testing.T) {
	base, scaled, bench := perOpProfiles()
	h, _, err := rank(t, base, scaled, Options{Bench: bench, RankBy: RankPerOp})
	if err != nil || !eq(fnames(h), []string{"Q", "L"}) {
		t.Fatalf("%v %v", fnames(h), err)
	}
	l := h[1]
	if *l.DeltaPerOpSeconds != 0.0035 || *l.NDCPerOp != 0.388889 {
		t.Errorf("L per-op: %+v", l)
	}
	// raw numbers are written for L as well: delta -10 samples
	if l.DeltaSelfSeconds != -0.1 {
		t.Errorf("L raw delta %v", l.DeltaSelfSeconds)
	}
	if l.NDC != 0 {
		t.Errorf("L raw ndc %v, want 0 (it did not grow)", l.NDC)
	}
}

func TestRankByPerOpWithoutBenchIsUsageError(t *testing.T) {
	base, scaled := coreProfiles()
	_, _, err := rank(t, base, scaled, Options{RankBy: RankPerOp})
	var u *UsageError
	if !errors.As(err, &u) {
		t.Errorf("err %v", err)
	}
	_, _, err = rank(t, base, scaled, Options{RankBy: "bogus"})
	if !errors.As(err, &u) {
		t.Errorf("bogus rank-by: %v", err)
	}
}

func TestScaleBenchmarkFrame(t *testing.T) {
	// 80 of 100 samples are under a Benchmark frame (also a sub-benchmark
	// closure), so T = 0.8 s and factor = 5e6 / 8e8 = 0.00625.
	p := prof(ms10, stk(50, "main", "pkg.BenchmarkX", "f"), stk(30, "main", "pkg.BenchmarkX.func1", "g"), stk(20, "main", "setup"))
	f, found := Scale(p, 5e6)
	if !found || !near(f, 0.00625, 1e-12) {
		t.Errorf("factor %v found %v", f, found)
	}
	// no Benchmark frame: T is everything, 1.0 s, factor 5e6/1e9
	q := prof(ms10, stk(100, "main", "f"))
	f, found = Scale(q, 5e6)
	if found || !near(f, 0.005, 1e-12) {
		t.Errorf("fallback factor %v found %v", f, found)
	}
	// testing.(*B).Run is not a benchmark function
	r := prof(ms10, stk(100, "testing.(*B).Run", "f"))
	if _, found := Scale(r, 5e6); found {
		t.Error("testing.(*B).Run taken for a benchmark")
	}
}

func TestPerOpWarnsWithoutBenchmarkFrame(t *testing.T) {
	base := prof(ms10, stk(10, "main", "f"))
	scaled := prof(ms10, stk(20, "main", "f"))
	_, warn, err := rank(t, base, scaled, Options{Bench: &contract.Bench{BaseNsPerOp: 1e6, ScaledNsPerOp: 1e6}, RankBy: RankRaw})
	if err != nil || !strings.Contains(warn, "Benchmark") {
		t.Errorf("%v %q", err, warn)
	}
}

// --- pprof mode warning ---------------------------------------------------

func TestWarnWhenProfilesAreAboutAsLong(t *testing.T) {
	base := prof(ms10, stk(100, "main", "f"), stk(100, "main", "g"))
	scaled := prof(ms10, stk(50, "main", "f"), stk(150, "main", "g")) // 200 vs 200 samples
	_, warn, _ := rank(t, base, scaled, Options{})
	if !strings.Contains(warn, "share") {
		t.Errorf("no warning: %q", warn)
	}
	// 1.5x or more: quiet
	scaled = prof(ms10, stk(50, "main", "f"), stk(250, "main", "g"))
	_, warn, _ = rank(t, base, scaled, Options{})
	if warn != "" {
		t.Errorf("warning at 1.5x: %q", warn)
	}
	// with bench the warning does not apply (per-op handles it)
	_, warn, _ = rank(t, base, prof(ms10, stk(50, "main", "f"), stk(150, "main", "g")), Options{Bench: &contract.Bench{BaseNsPerOp: 1, ScaledNsPerOp: 1}})
	if strings.Contains(warn, "share") {
		t.Errorf("share warning in bench mode: %q", warn)
	}
}

// Nothing grew in raw seconds (the scaled profile is shorter), but per
// operation both functions did: base T = 1.0 s, 1 ms/op; scaled T = 0.8 s,
// 10 ms/op, so L: 0.5 s * 1e-3 = 0.0005 -> 0.4 s * 0.0125 = 0.005.
func TestRawFailureHintsAtPerOp(t *testing.T) {
	base := prof(ms10, stk(50, "main", "pkg.BenchmarkX", "L"), stk(50, "main", "pkg.BenchmarkX", "Q"))
	scaled := prof(ms10, stk(40, "main", "pkg.BenchmarkX", "L"), stk(40, "main", "pkg.BenchmarkX", "Q"))
	bench := &contract.Bench{BaseNsPerOp: 1e6, ScaledNsPerOp: 1e7}
	_, warn, err := rank(t, base, scaled, Options{Bench: bench, RankBy: RankRaw})
	var e *Error
	if !errors.As(err, &e) || e.Code != contract.NoPositiveDelta {
		t.Fatalf("raw: %v", err)
	}
	if !strings.Contains(warn, "-rank-by perop") {
		t.Errorf("no hint: %q", warn)
	}
	h, _, err := rank(t, base, scaled, Options{Bench: bench, RankBy: RankPerOp})
	if err != nil || len(h) != 2 {
		t.Errorf("perop: %v %v", h, err)
	}
}

// Go 1.21+ moved runtime internals to internal/runtime/...: they are runtime
// too (Duc Anh found this on the cache sample: one leaked into the top-5).
func TestInternalRuntimeIsRuntime(t *testing.T) {
	base := prof(ms10, stk(10, "main", "app.Slow"))
	scaled := prof(ms10, stk(20, "main", "app.Slow"), stk(300, "internal/runtime/maps.(*Map).Get"))
	h, _, err := rank(t, base, scaled, Options{})
	if err != nil || !eq(fnames(h), []string{"app.Slow"}) {
		t.Fatalf("off: %v %v", fnames(h), err)
	}
	scaled = prof(ms10, stk(20, "main", "app.Slow"), stk(300, "main", "app.Look", "internal/runtime/maps.(*Map).Get"))
	h, _, err = rank(t, base, scaled, Options{AttributeRuntime: true})
	if err != nil || !eq(fnames(h), []string{"app.Look", "app.Slow"}) {
		t.Fatalf("on: %v %v", fnames(h), err)
	}
}

// Default ranking: per operation when run.json has usable bench data, raw
// otherwise (pprof mode has no operation to divide by). An explicit choice
// always wins.
func TestResolveRankDefaults(t *testing.T) {
	good := &contract.Bench{BaseNsPerOp: 1, ScaledNsPerOp: 2}
	cases := []struct {
		rankBy string
		bench  *contract.Bench
		want   string
	}{
		{"", nil, RankRaw},
		{"", good, RankPerOp},
		{"", &contract.Bench{BaseNsPerOp: 0, ScaledNsPerOp: 2}, RankRaw},
		{RankRaw, good, RankRaw},
		{RankPerOp, good, RankPerOp},
	}
	for _, c := range cases {
		if got := ResolveRank(c.rankBy, c.bench); got != c.want {
			t.Errorf("ResolveRank(%q, %v) = %q, want %q", c.rankBy, c.bench, got, c.want)
		}
	}
}

// With bench data and no -rank-by, Rank ranks per operation: the profiles of
// perOpProfiles have L and Q growing per operation but the same hotspot
// either way; use the raw-failure profiles, where raw finds nothing.
func TestRankDefaultsToPerOpWithBench(t *testing.T) {
	base := prof(ms10, stk(50, "main", "pkg.BenchmarkX", "L"), stk(50, "main", "pkg.BenchmarkX", "Q"))
	scaled := prof(ms10, stk(40, "main", "pkg.BenchmarkX", "L"), stk(40, "main", "pkg.BenchmarkX", "Q"))
	h, _, err := rank(t, base, scaled, Options{Bench: &contract.Bench{BaseNsPerOp: 1e6, ScaledNsPerOp: 1e7}})
	if err != nil || len(h) != 2 || h[0].NDCPerOp == nil {
		t.Fatalf("%v %v", h, err)
	}
}
