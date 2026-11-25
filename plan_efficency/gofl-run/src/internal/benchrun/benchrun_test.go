package benchrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gofl/internal/contract"
	"gofl/internal/goenv"
)

// These tests run the real go command on the modules in testdata.

func needGo(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test; skipped with -short")
	}
	if _, err := goenv.Find(""); err != nil {
		t.Skip("go not found; set GOFL_GO")
	}
}

func sample(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// opts are options for a quick run on the sample module.
func opts(t *testing.T, bench string, sizes ...string) Options {
	if len(sizes) == 0 {
		sizes = []string{"100", "2000"}
	}
	return Options{
		Dir:       sample(t),
		Package:   "./slow",
		Bench:     bench,
		Sizes:     [2]string{sizes[0], sizes[1]},
		BenchTime: "300ms",
		Timeout:   2 * time.Minute,
		Out:       t.TempDir(),
	}
}

// check runs Run and verifies that run.json and diagnostics.json agree with
// the result and with the expected code ("" for ok).
func check(t *testing.T, o Options, want contract.Code) *contract.Run {
	t.Helper()
	r, err := Run(context.Background(), o)
	if r == nil {
		t.Fatalf("no run: %v", err)
	}
	got := CodeOf(err)
	if got != want {
		t.Fatalf("code %q, want %q (err %v)", got, want, err)
	}
	if err != nil && got == "" {
		t.Fatalf("error without code: %v", err)
	}
	onDisk, rerr := contract.ReadRun(filepath.Join(o.Out, contract.RunFile))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if onDisk.Status != r.Status || (want != "" && (onDisk.ErrorCode == nil || *onDisk.ErrorCode != want)) {
		t.Errorf("run.json: status %s code %v", onDisk.Status, onDisk.ErrorCode)
	}
	if want == "" && (onDisk.Status != contract.StatusOK || onDisk.ErrorCode != nil) {
		t.Errorf("run.json: status %s code %v, want ok", onDisk.Status, onDisk.ErrorCode)
	}
	if _, err := os.Stat(filepath.Join(o.Out, contract.DiagnosticsFile)); err != nil {
		t.Errorf("diagnostics.json missing: %v", err)
	}
	return r
}

func TestSubBenchmarks(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkDedup")
	r := check(t, o, "")
	if r.Mode != contract.ModeBench || r.GoVersion == nil || !strings.HasPrefix(*r.GoVersion, "go") || r.PathsMethod != "paper" {
		t.Errorf("run: %+v", r)
	}
	for _, p := range []*string{r.BasePprof, r.ScaledPprof} {
		if p == nil {
			t.Fatal("profile path missing")
		}
		if _, err := os.Stat(contract.Resolve(filepath.Join(o.Out, contract.RunFile), *p)); err != nil {
			t.Errorf("profile %s: %v", *p, err)
		}
	}
	b := r.Bench
	if b == nil || b.BaseIterations == 0 || b.ScaledIterations == 0 {
		t.Fatalf("bench: %+v", b)
	}
	// Dedup is quadratic: 20x the size is far more than 20x the time.
	if b.ScaledNsPerOp < 20*b.BaseNsPerOp {
		t.Errorf("ns/op %.0f -> %.0f: expected quadratic growth", b.BaseNsPerOp, b.ScaledNsPerOp)
	}
	for _, f := range []string{"build.log", "bench_base.log", "bench_scaled.log"} {
		if _, err := os.Stat(filepath.Join(o.Out, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestSizeFromEnv(t *testing.T) {
	needGo(t)
	r := check(t, opts(t, "Sized"), "")
	if r.Bench.ScaledNsPerOp < 20*r.Bench.BaseNsPerOp {
		t.Errorf("GOFL_SIZE did not reach the benchmark: %+v", r.Bench)
	}
}

func TestLogsInOutput(t *testing.T) {
	needGo(t)
	check(t, opts(t, "BenchmarkLogs"), "")
}

func TestFindsPackage(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkOnlyHere")
	o.Package = ""
	check(t, o, "")
}

func TestAmbiguous(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkDedup")
	o.Package = ""
	r, err := Run(context.Background(), o)
	if CodeOf(err) != contract.NoBench || !strings.Contains(err.Error(), "2 packages") {
		t.Fatalf("got %v", err)
	}
	if r.BasePprof != nil {
		t.Error("base_pprof should be null")
	}
}

func TestMissingBenchmark(t *testing.T) {
	needGo(t)
	check(t, opts(t, "BenchmarkNope"), contract.NoBench)
}

func TestMissingSize(t *testing.T) {
	needGo(t)
	r := check(t, opts(t, "BenchmarkDedup", "100", "5000"), contract.NoBench)
	if r.BasePprof == nil || r.ScaledPprof != nil {
		t.Errorf("base should be kept and scaled null: %v %v", r.BasePprof, r.ScaledPprof)
	}
}

func TestNeedsEnv(t *testing.T) {
	needGo(t)
	check(t, opts(t, "BenchmarkNeedsEnv"), contract.TestNeedsEnv)
	o := opts(t, "BenchmarkNeedsEnv")
	o.Env = []string{"SAMPLE_DB=memory"}
	check(t, o, "")
}

func TestSkipped(t *testing.T) {
	needGo(t)
	_, err := Run(context.Background(), opts(t, "BenchmarkSkips"))
	if CodeOf(err) != contract.TestNeedsEnv || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("got %v", err)
	}
}

func TestSetup(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkDedup")
	o.Setup = []string{"exit 3"}
	_, err := Run(context.Background(), o)
	if CodeOf(err) != contract.TestNeedsEnv || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("got %v", err)
	}

	// The configured variables reach the setup commands, which run in the
	// project directory.
	o = opts(t, "BenchmarkDedup")
	o.Env = []string{"GOFL_TEST_MARK=marked"}
	if runtime.GOOS == "windows" {
		o.Setup = []string{`if exist go.mod (echo %GOFL_TEST_MARK%) else (exit 5)`}
	} else {
		o.Setup = []string{`test -f go.mod && echo "$GOFL_TEST_MARK"`}
	}
	check(t, o, "")
	log, _ := os.ReadFile(filepath.Join(o.Out, "setup.log"))
	if !strings.Contains(string(log), "marked") {
		t.Errorf("setup.log: %s", log)
	}
}

func TestTimeout(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkHang")
	o.Timeout = 3 * time.Second
	start := time.Now()
	_, err := Run(context.Background(), o)
	if CodeOf(err) != contract.Timeout {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > time.Minute {
		t.Errorf("took %s", d)
	}
}

func TestBuildFail(t *testing.T) {
	needGo(t)
	o := opts(t, "BenchmarkBroken")
	o.Dir, o.Package = filepath.Join(sample(t), "..", "broken"), ""
	_, err := Run(context.Background(), o)
	if CodeOf(err) != contract.BuildFail || !strings.Contains(err.Error(), "broken_test.go") {
		t.Fatalf("got %v", err)
	}
}

func TestNoModule(t *testing.T) {
	needGo(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0o644)
	o := opts(t, "BenchmarkX")
	o.Dir, o.Package = dir, ""
	o.Env = []string{"GO111MODULE=on", "GOWORK=off"}
	check(t, o, contract.NoModule)
}

func TestNoGo(t *testing.T) {
	o := opts(t, "BenchmarkDedup")
	o.Go = filepath.Join(t.TempDir(), "no-go-here")
	r := check(t, o, contract.NoGo)
	if r.GoVersion != nil {
		t.Errorf("go_version %v, want null", *r.GoVersion)
	}
}

func TestUsageWritesNothing(t *testing.T) {
	o := opts(t, "")
	_, err := Run(context.Background(), o)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v", err)
	}
	if entries, _ := os.ReadDir(o.Out); len(entries) != 0 {
		t.Errorf("wrote %d files", len(entries))
	}
}

func TestCancel(t *testing.T) {
	needGo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	o := opts(t, "BenchmarkHang")
	_, err := Run(ctx, o)
	if err == nil || CodeOf(err) != "" {
		t.Fatalf("a cancelled run should end without a code, got %v", err)
	}
}

func profiles(t *testing.T) (string, string) {
	dir := filepath.Join("..", "..", "testdata", "profiles")
	return filepath.Join(dir, "dedup_base.pprof"), filepath.Join(dir, "dedup_scaled.pprof")
}

func TestPprofMode(t *testing.T) {
	base, scaled := profiles(t)
	out := t.TempDir()
	// Files of an earlier run must not leak into this one.
	os.WriteFile(filepath.Join(out, contract.HotspotsFile), []byte("[]"), 0o644)
	r, err := FromPprof(context.Background(), PprofOptions{Base: base, Scaled: scaled, Out: out, PathsMethod: "stack"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != contract.ModePprof || r.PathsMethod != "stack" || r.Bench != nil || r.BasePprof == nil {
		t.Fatalf("run: %+v", r)
	}
	// The profiles lie outside the working directory, so the paths are absolute.
	if !filepath.IsAbs(filepath.FromSlash(*r.BasePprof)) || !strings.HasSuffix(*r.BasePprof, "testdata/profiles/dedup_base.pprof") {
		t.Errorf("base_pprof %q", *r.BasePprof)
	}
	if _, err := os.Stat(filepath.Join(out, contract.HotspotsFile)); !os.IsNotExist(err) {
		t.Error("stale hotspots.json kept")
	}
	d, err := contract.ReadRun(filepath.Join(out, contract.RunFile))
	if err != nil || d.Status != contract.StatusOK {
		t.Fatalf("run.json: %+v %v", d, err)
	}
}

func TestPprofModeBadProfile(t *testing.T) {
	base, _ := profiles(t)
	junk := filepath.Join("..", "..", "testdata", "profiles", "junk.pprof")
	for _, scaled := range []string{junk, filepath.Join(t.TempDir(), "missing.pprof")} {
		out := t.TempDir()
		r, err := FromPprof(context.Background(), PprofOptions{Base: base, Scaled: scaled, Out: out})
		if CodeOf(err) != contract.NoProfile {
			t.Fatalf("%s: got %v", scaled, err)
		}
		if r.BasePprof == nil || r.ScaledPprof != nil || r.Status != contract.StatusError {
			t.Errorf("%s: %+v", scaled, r)
		}
	}
	if _, err := FromPprof(context.Background(), PprofOptions{Base: base}); err == nil {
		t.Error("pprof mode without -scaled passed")
	}
}
