package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/contract"
)

// realPerf runs gofl perf with the assembled stages.
func realPerf(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var so, se bytes.Buffer
	code := runPerf(context.Background(), args, &so, &se, assemble)
	return code, so.String(), se.String()
}

func readPaths(t *testing.T, out string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, contract.PathsFile))
	if err != nil {
		t.Fatal(err)
	}
	var p []map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return p
}

func TestPerfAssembledPprofMode(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	args := []string{"-base", base, "-scaled", scaled, "-out", out}
	for i := 0; i < 2; i++ { // the second run replaces the first one's report
		code, stdout, stderr := realPerf(t, args...)
		if code != 0 {
			t.Fatalf("run %d: exit %d\n%s\n%s", i, code, stdout, stderr)
		}
	}
	if strings.Contains(realStderr(t, args), "not assembled") {
		t.Error("a stage was skipped")
	}
	paths := readPaths(t, out)
	if len(paths) == 0 || paths[0]["target"] == "" || len(paths[0]["frames"].([]any)) == 0 {
		t.Fatalf("paths: %v", paths)
	}
	for _, f := range []string{"report.md", "report.json"} {
		if _, err := os.Stat(filepath.Join(out, "report", f)); err != nil {
			t.Error(err)
		}
	}
	d, _ := readDiag(t, out)
	if d["status"] != "ok" || int(d["path_count"].(float64)) != len(paths) {
		t.Errorf("diagnostics %v", d)
	}
	run, err := contract.ReadRun(filepath.Join(out, contract.RunFile))
	if err != nil || (run.PathsMethod != "paper" && run.PathsMethod != "stack") {
		t.Errorf("paths_method %q %v", run.PathsMethod, err)
	}
}

func realStderr(t *testing.T, args []string) string {
	_, _, e := realPerf(t, args...)
	return e
}

func TestPerfStackMethodAndHashNames(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	code, _, stderr := realPerf(t, "-base", base, "-scaled", scaled, "-out", out, "-paths-method", "stack", "-hash-names")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	run, _ := contract.ReadRun(filepath.Join(out, contract.RunFile))
	if run.PathsMethod != "stack" {
		t.Errorf("paths_method %q, want stack", run.PathsMethod)
	}
	data, err := os.ReadFile(filepath.Join(out, "report", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "contains") || !strings.Contains(string(data), `"names_hashed": true`) {
		t.Errorf("names not hashed:\n%s", data)
	}
	// without the flag the names stay
	out2 := t.TempDir()
	realPerf(t, "-base", base, "-scaled", scaled, "-out", out2)
	data, _ = os.ReadFile(filepath.Join(out2, "report", "report.json"))
	if !strings.Contains(string(data), "contains") {
		t.Error("plain report lost the names")
	}
}

func TestPerfEntryNotCoveringIsCoded(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	// strict paper method (the contract default when asked for) has no fallback.
	var so, se bytes.Buffer
	st := assemble
	code := runPerf(context.Background(), []string{"-base", base, "-scaled", scaled, "-out", out, "-entry", "NoSuchFunc", "-paths-method", "paper"}, &so, &se, st)
	if code != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, so.String(), se.String())
	}
	d, _ := readDiag(t, out)
	if d["error_code"] != "ENTRY_NOT_COVERING" {
		t.Errorf("diagnostics %v", d)
	}
}

func TestPerfEntryNotCoveringFallsBackToStack(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	code, _, stderr := realPerf(t, "-base", base, "-scaled", scaled, "-out", out, "-entry", "NoSuchFunc")
	if code != 0 || !strings.Contains(stderr, "used stack") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	run, _ := contract.ReadRun(filepath.Join(out, contract.RunFile))
	if run.PathsMethod != "stack" || len(readPaths(t, out)) == 0 {
		t.Errorf("paths_method %q", run.PathsMethod)
	}
}

// The benchmark mode ranks per operation by default, and then the scores of
// the paths are per operation too (about 1e-4 s here, not the 1e-1 s of raw
// self seconds).
func TestPerfBenchModeScoresPerOperation(t *testing.T) {
	needGo(t)
	out := t.TempDir()
	code, stdout, stderr := realPerf(t, "-dir", abs(t, "testdata", "sample"), "-pkg", "./slow", "-bench", "BenchmarkDedup",
		"-sizes", "100,2000", "-benchtime", "300ms", "-out", out)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if _, ok := readHotspots(t, out)[0]["ndc_per_op"]; !ok {
		t.Fatal("default ranking in the benchmark mode is not per operation")
	}
	paths := readPaths(t, out)
	if len(paths) == 0 {
		t.Fatal("no path")
	}
	for _, p := range paths {
		if s := p["score"].(float64); s > 0.01 || s < -0.01 {
			t.Errorf("score %v looks like raw seconds", s)
		}
	}
}

// With -rank-by raw the scores stay in raw seconds (contract v0).
func TestPerfRawKeepsRawScores(t *testing.T) {
	out := t.TempDir()
	base, scaled := profiles(t)
	if code, _, e := realPerf(t, "-base", base, "-scaled", scaled, "-out", out, "-rank-by", "raw"); code != 0 {
		t.Fatal(e)
	}
	if s := readPaths(t, out)[0]["score"].(float64); s < 0.05 {
		t.Errorf("raw score %v", s)
	}
}

// Without -dir the project root is the current directory. The paths stage
// used to get "" there, kept absolute file names and matched no hotspot, so
// the benchmark mode wrote no path at all.
func TestPerfBenchModeWithoutDirFindsPaths(t *testing.T) {
	needGo(t)
	out := t.TempDir()
	t.Chdir(abs(t, "testdata", "sample"))
	code, stdout, stderr := realPerf(t, "-pkg", "./slow", "-bench", "BenchmarkDedup",
		"-sizes", "100,2000", "-benchtime", "300ms", "-out", out)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if len(readPaths(t, out)) == 0 {
		t.Fatal("no path when -dir is not given")
	}
}
