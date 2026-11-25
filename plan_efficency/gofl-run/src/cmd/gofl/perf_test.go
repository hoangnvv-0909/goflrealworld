package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/contract"
	"gofl/internal/diffprof"
)

func profiles(t *testing.T) (string, string) {
	return abs(t, "testdata", "profiles", "dedup_base.pprof"), abs(t, "testdata", "profiles", "dedup_scaled.pprof")
}

func readHotspots(t *testing.T, out string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	if err != nil {
		t.Fatal(err)
	}
	var h []map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return h
}

func readDiag(t *testing.T, out string) (map[string]any, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, contract.DiagnosticsFile))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d, data
}

func perfRun(t *testing.T, st stages, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runPerf(context.Background(), args, &stdout, &stderr, fixed(st))
	return code, stdout.String(), stderr.String()
}

func TestComputeCommand(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	if code, _, e := gofl(t, "profile", "-base", base, "-scaled", scaled, "-out", out); code != 0 {
		t.Fatal(e)
	}
	code, stdout, stderr := gofl(t, "compute", "-out", out, "-dir", t.TempDir())
	if code != 0 || !strings.Contains(stdout, "hotspots.json") {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	h := readHotspots(t, out)
	if len(h) == 0 || len(h) > 5 {
		t.Fatalf("%d hotspots", len(h))
	}
	if fn := h[0]["function"].(string); !strings.HasSuffix(fn, ".contains") && !strings.HasSuffix(fn, ".Dedup") {
		t.Errorf("top hotspot %s", fn)
	}
	if _, ok := h[0]["delta_per_op_seconds"]; ok {
		t.Error("per-op field in pprof mode")
	}
	// -topk and -attribute-runtime are accepted
	if code, _, e := gofl(t, "compute", "-out", out, "-topk", "2", "-attribute-runtime"); code != 0 {
		t.Fatal(e)
	}
	// the real profiles have a single function that grew, so only the bound
	// can be checked here; TopK itself is tested in package diffprof
	if got := len(readHotspots(t, out)); got < 1 || got > 2 {
		t.Errorf("-topk 2 gave %d", got)
	}
}

func TestComputeRefusesFailedRunAndUsageErrors(t *testing.T) {
	out := t.TempDir()
	junk := abs(t, "testdata", "profiles", "junk.pprof")
	gofl(t, "profile", "-base", junk, "-scaled", junk, "-out", out)
	os.WriteFile(filepath.Join(out, contract.HotspotsFile), []byte("[]"), 0o644)
	code, _, stderr := gofl(t, "compute", "-out", out)
	if code != 1 || !strings.Contains(stderr, "NO_PROFILE") {
		t.Errorf("failed run: exit %d %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, contract.HotspotsFile)); err == nil {
		t.Error("stale hotspots.json kept")
	}

	good := t.TempDir()
	base, scaled := profiles(t)
	gofl(t, "profile", "-base", base, "-scaled", scaled, "-out", good)
	for _, args := range [][]string{
		{"compute", "-out", good, "-rank-by", "perop"}, // pprof mode has no bench
		{"compute", "-out", good, "-rank-by", "bogus"},
		{"compute", "-out", good, "-topk", "-1"},
		{"compute", "-out", filepath.Join(good, "nothing-here")},
		{"compute", "-out", good, "extra"},
		{"compute", "-nosuchflag"},
	} {
		if code, _, _ := gofl(t, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}

func TestPerfPprofModeWithStagesNotAssembled(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	code, stdout, stderr := perfRun(t, stages{}, "-base", base, "-scaled", scaled, "-out", out)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if n := strings.Count(stderr, "stage skipped: not assembled yet"); n != 2 {
		t.Errorf("%d skipped lines, want 2\n%s", n, stderr)
	}
	h := readHotspots(t, out)
	d, raw := readDiag(t, out)
	if len(d) != 11 {
		t.Errorf("diagnostics has %d keys, want 11: %s", len(d), raw)
	}
	if int(d["hotspot_count"].(float64)) != len(h) || d["path_count"] != nil || d["status"] != "ok" || d["error_code"] != nil {
		t.Errorf("diagnostics: %s", raw)
	}
	for _, x := range h {
		name := x["function"].(string)
		if strings.Contains(string(raw), name) {
			t.Errorf("diagnostics.json names %s", name)
		}
	}
	for _, f := range []string{contract.RunFile, contract.DiagnosticsFile, contract.HotspotsFile} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, contract.PathsFile)); err == nil {
		t.Error("critical_paths.json written by nobody")
	}
}

func TestPerfJunkStopsAtProfile(t *testing.T) {
	out := t.TempDir()
	junk := abs(t, "testdata", "profiles", "junk.pprof")
	code, _, stderr := perfRun(t, stages{}, "-base", junk, "-scaled", junk, "-out", out)
	d, _ := readDiag(t, out)
	if code != 1 || d["error_code"] != "NO_PROFILE" || d["status"] != "error" {
		t.Errorf("exit %d diag %v", code, d)
	}
	if strings.Contains(stderr, "stage skipped") {
		t.Error("later stages must not run after a failure")
	}
	if _, err := os.Stat(filepath.Join(out, contract.HotspotsFile)); err == nil {
		t.Error("hotspots.json after a failed profile step")
	}
}

func TestPerfNoPositiveDelta(t *testing.T) {
	out := t.TempDir()
	base, _ := profiles(t)
	var called bool
	st := stages{Paths: func(context.Context, string) error { called = true; return nil }}
	code, _, _ := perfRun(t, st, "-base", base, "-scaled", base, "-out", out)
	d, _ := readDiag(t, out)
	if code != 1 || d["error_code"] != "NO_POSITIVE_DELTA" || d["hotspot_count"] != float64(0) {
		t.Errorf("exit %d diag %v", code, d)
	}
	if called {
		t.Error("Paths ran after NO_POSITIVE_DELTA")
	}
	data, _ := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	if strings.TrimSpace(string(data)) != "[]" {
		t.Errorf("hotspots.json %q", data)
	}
}

func TestPerfStageHooks(t *testing.T) {
	base, scaled := profiles(t)
	paths := abs(t, "testdata", "contract", contract.PathsFile)
	var order []string
	ok := stages{
		Paths: func(_ context.Context, out string) error {
			order = append(order, "paths")
			data, _ := os.ReadFile(paths)
			return os.WriteFile(filepath.Join(out, contract.PathsFile), data, 0o644)
		},
		Report: func(_ context.Context, out string) error {
			order = append(order, "report")
			if _, err := os.Stat(filepath.Join(out, contract.PathsFile)); err != nil {
				t.Error("report ran before paths wrote its file")
			}
			return nil
		},
	}
	out := t.TempDir()
	code, _, stderr := perfRun(t, ok, "-base", base, "-scaled", scaled, "-out", out)
	d, _ := readDiag(t, out)
	if code != 0 || strings.Join(order, ",") != "paths,report" || d["path_count"] != float64(3) || strings.Contains(stderr, "skipped") {
		t.Errorf("exit %d order %v diag %v\n%s", code, order, d, stderr)
	}

	// A coded error of Paths stops the chain and goes into diagnostics.
	order = nil
	failing := stages{
		Paths:  func(context.Context, string) error { return &diffprof.Error{Code: contract.EntryNotCovering, Msg: "x"} },
		Report: func(context.Context, string) error { order = append(order, "report"); return nil },
	}
	out = t.TempDir()
	code, _, _ = perfRun(t, failing, "-base", base, "-scaled", scaled, "-out", out)
	d, _ = readDiag(t, out)
	if code != 1 || d["error_code"] != "ENTRY_NOT_COVERING" || len(order) != 0 {
		t.Errorf("exit %d order %v diag %v", code, order, d)
	}

	// An error without a code still fails the run, without inventing a code.
	plain := stages{Report: func(context.Context, string) error { return errors.New("disk full") }}
	out = t.TempDir()
	code, _, _ = perfRun(t, plain, "-base", base, "-scaled", scaled, "-out", out)
	d, _ = readDiag(t, out)
	if code != 1 || d["error_code"] != nil {
		t.Errorf("exit %d diag %v", code, d)
	}
}

func TestPerfCancelledAssignsNoCode(t *testing.T) {
	base, scaled := profiles(t)
	ctx, cancel := context.WithCancel(context.Background())
	st := stages{Paths: func(context.Context, string) error { cancel(); return ctx.Err() }}
	out := t.TempDir()
	var so, se bytes.Buffer
	code := runPerf(ctx, []string{"-base", base, "-scaled", scaled, "-out", out}, &so, &se, fixed(st))
	d, _ := readDiag(t, out)
	if code != 1 || d["error_code"] != nil || d["status"] != "ok" {
		t.Errorf("exit %d diag %v", code, d)
	}
}

func TestPerfUsageErrors(t *testing.T) {
	base, scaled := profiles(t)
	out := t.TempDir()
	for _, args := range [][]string{
		{"-base", base, "-out", out},                                         // no -scaled
		{"-base", base, "-scaled", scaled, "-rank-by", "perop", "-out", out}, // pprof mode has no bench
		{"-base", base, "-scaled", scaled, "-rank-by", "x", "-out", out},
		{"-base", base, "-scaled", scaled, "-topk", "-3", "-out", out},
		{"-nosuchflag"},
		{"extra"},
		{"-config", filepath.Join(out, "missing.yaml")},
	} {
		if code, _, _ := perfRun(t, stages{}, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("usage errors wrote %d files", len(entries))
	}
}

func TestPerfBenchModeEndToEnd(t *testing.T) {
	needGo(t)
	out := t.TempDir()
	args := []string{"-dir", abs(t, "testdata", "sample"), "-pkg", "./slow", "-bench", "BenchmarkDedup",
		"-sizes", "100,2000", "-benchtime", "300ms", "-out", out}
	// No -rank-by: the benchmark mode ranks per operation. (Raw ranking of two
	// timed runs is not stable: Go picks b.N for about the same wall time at
	// both sizes, so the raw delta can be negative for every function.)
	code, stdout, stderr := perfRun(t, stages{}, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	h := readHotspots(t, out)
	if len(h) == 0 {
		t.Fatal("no hotspot")
	}
	if _, ok := h[0]["ndc_per_op"]; !ok {
		t.Errorf("benchmark mode must add the per-op fields: %v", h[0])
	}
	d, _ := readDiag(t, out)
	if d["status"] != "ok" || int(d["hotspot_count"].(float64)) != len(readHotspots(t, out)) {
		t.Errorf("diagnostics %v", d)
	}
}

func TestPerfAcceptsComputeFlags(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	code, _, stderr := perfRun(t, stages{}, "-base", base, "-scaled", scaled, "-out", out, "-topk", "1", "-rank-by", "raw", "-attribute-runtime")
	if code != 0 || len(readHotspots(t, out)) != 1 {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

// fixed makes a stageMaker that ignores the options, for tests that plug in
// their own stages.
func fixed(st stages) stageMaker {
	return func(stageParams) stages { return st }
}
