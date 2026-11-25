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
	"gofl/internal/goenv"
)

func gofl(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func needGo(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test; skipped with -short")
	}
	if _, err := goenv.Find(""); err != nil {
		t.Skip("go not found; set GOFL_GO")
	}
}

func abs(t *testing.T, parts ...string) string {
	p, err := filepath.Abs(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommands(t *testing.T) {
	if code, out, _ := gofl(t, "version"); code != 0 || !strings.Contains(out, contract.ToolVersion) {
		t.Errorf("version: %d %q", code, out)
	}
	if code, _, _ := gofl(t); code != 2 {
		t.Errorf("no command: %d", code)
	}
	if code, _, errOut := gofl(t, "frobnicate"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown command: %d %q", code, errOut)
	}
	if code, out, _ := gofl(t, "help"); code != 0 || !strings.Contains(out, "gofl profile -base") {
		t.Errorf("help: %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	out := t.TempDir()
	cases := [][]string{
		{"profile", "-out", out},                                                  // no benchmark
		{"profile", "-bench", "X", "-sizes", "100", "-out", out},                  // one size
		{"profile", "-bench", "X", "-sizes", "1,2", "-timeout", "x", "-out", out}, // bad timeout
		{"profile", "-base", "a.pprof", "-out", out},                              // no -scaled
		{"profile", "-nosuchflag"},
		{"profile", "extra"},
		{"profile", "-config", filepath.Join(out, "missing.yaml")},
		{"diagnostics", "-out", out, "-error-code", "OOPS"},
		{"diagnostics", "-out", out}, // no run.json
	}
	for _, args := range cases {
		if code, _, _ := gofl(t, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("usage errors wrote %d files", len(entries))
	}
}

func TestPprofModeThenDiagnostics(t *testing.T) {
	out := t.TempDir()
	base, scaled := abs(t, "testdata", "profiles", "dedup_base.pprof"), abs(t, "testdata", "profiles", "dedup_scaled.pprof")
	code, stdout, stderr := gofl(t, "profile", "-base", base, "-scaled", scaled, "-out", out)
	if code != 0 || !strings.Contains(stdout, "status: ok (pprof mode") {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	// The later stages write their files; diagnostics adds their counts.
	for _, f := range []string{contract.HotspotsFile, contract.PathsFile} {
		data, _ := os.ReadFile(abs(t, "testdata", "contract", f))
		os.WriteFile(filepath.Join(out, f), data, 0o644)
	}
	code, stdout, _ = gofl(t, "diagnostics", "-out", out, "-duration-ms", "1234")
	if code != 0 || !strings.Contains(stdout, "status: ok") {
		t.Fatalf("diagnostics: %d %s", code, stdout)
	}
	var d contract.Diagnostics
	data, _ := os.ReadFile(filepath.Join(out, contract.DiagnosticsFile))
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if *d.HotspotCount != 2 || *d.PathCount != 3 || d.DurationMs != 1234 || *d.BaselineSamplesTotal == 0 {
		t.Errorf("diagnostics: %s", data)
	}
	code, stdout, _ = gofl(t, "diagnostics", "-out", out, "-error-code", "ENTRY_NOT_COVERING")
	if code != 0 || !strings.Contains(stdout, "status: error ENTRY_NOT_COVERING") {
		t.Errorf("override: %d %s", code, stdout)
	}
}

func TestPprofModeBadFile(t *testing.T) {
	out := t.TempDir()
	junk := abs(t, "testdata", "profiles", "junk.pprof")
	code, stdout, stderr := gofl(t, "profile", "-base", junk, "-scaled", junk, "-out", out)
	if code != 1 || !strings.Contains(stdout, "status: error NO_PROFILE") || !strings.Contains(stderr, "not a pprof profile") {
		t.Errorf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, contract.DiagnosticsFile)); err != nil {
		t.Error("diagnostics.json must be written on failure too")
	}
}

// TestConfigFile runs the benchmark mode from a gofl.yaml, then overrides
// one value with a flag.
func TestConfigFile(t *testing.T) {
	needGo(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gofl.yaml")
	yaml := "dir: " + abs(t, "testdata", "sample") + "\npackage: ./slow\nbench: BenchmarkNeedsEnv\nsizes: [100, 2000]\nbenchtime: 300ms\ntimeout: 2m\nenv:\n  SAMPLE_DB: memory\nout: results\n"
	if err := os.WriteFile(cfg, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := gofl(t, "profile", "-config", cfg)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	r, err := contract.ReadRun(filepath.Join(dir, "results", contract.RunFile))
	if err != nil || r.Status != contract.StatusOK || r.Bench.Sizes != [2]string{"100", "2000"} {
		t.Fatalf("run.json: %+v %v", r, err)
	}
	if !strings.Contains(stderr, "gofl: running size 2000") {
		t.Errorf("progress: %s", stderr)
	}

	// BenchmarkDedup has sub-benchmarks n=100 and n=2000 only.
	code, stdout, _ = gofl(t, "profile", "-config", cfg, "-bench", "BenchmarkDedup", "-sizes", "100,5000")
	if code != 1 || !strings.Contains(stdout, "NO_BENCH") {
		t.Errorf("override: exit %d %s", code, stdout)
	}

	code, stdout, _ = gofl(t, "preflight", "-config", cfg, "-json")
	var rep struct {
		Status string
		Checks []struct{ Name, Status string }
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil || code != 0 || rep.Status != "ok" || len(rep.Checks) != 9 {
		t.Errorf("preflight -json: exit %d %v\n%s", code, err, stdout)
	}
}

func TestPreflightText(t *testing.T) {
	needGo(t)
	code, stdout, stderr := gofl(t, "preflight", "-dir", abs(t, "testdata", "sample"), "-pkg", "./slow", "-bench", "BenchmarkSkips", "-sizes", "100,2000", "-out", t.TempDir(), "-v")
	if code != 1 || !strings.Contains(stdout, "status: error TEST_NEEDS_ENV") || !strings.Contains(stdout, "safe to send") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
	if strings.Contains(stdout, "Skips") || !strings.Contains(stderr, "do not send") || !strings.Contains(stderr, "skipped") {
		t.Errorf("stdout must name nothing, stderr (-v) must explain:\n%s\n%s", stdout, stderr)
	}
}
