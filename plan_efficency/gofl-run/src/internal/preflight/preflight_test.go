package preflight

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gofl/internal/benchrun"
	"gofl/internal/contract"
	"gofl/internal/goenv"
)

func needGo(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test; skipped with -short")
	}
	if _, err := goenv.Find(""); err != nil {
		t.Skip("go not found; set GOFL_GO")
	}
}

func input(t *testing.T, bench string) Input {
	dir, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "sample"))
	return Input{Options: benchrun.Options{
		Dir:     dir,
		Package: "./slow",
		Bench:   bench,
		Sizes:   [2]string{"100", "2000"},
		Timeout: 2 * time.Minute,
		Out:     t.TempDir(),
	}}
}

// names that must never appear in a report.
var names = []string{"slow", "Dedup", "NeedsEnv", "SAMPLE_DB", "sample", "testdata", ".go", "BenchmarkDedup"}

func noNames(t *testing.T, r *Report) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if strings.Contains(string(data), n) {
			t.Errorf("report contains %q:\n%s", n, data)
		}
	}
}

func statuses(r *Report) string {
	var parts []string
	for _, c := range r.Checks {
		parts = append(parts, c.Name+"="+c.Status)
	}
	return strings.Join(parts, " ")
}

func TestAllOK(t *testing.T) {
	needGo(t)
	r, err := Run(context.Background(), input(t, "BenchmarkDedup"))
	if err != nil {
		t.Fatal(err)
	}
	want := "go=ok module=ok config=skip output=ok setup=skip packages=ok benchmark=ok build=ok dry-run=ok"
	if got := statuses(r); got != want || r.Status != contract.StatusOK || r.ErrorCode != nil || r.GoVersion == nil {
		t.Errorf("got %s (%s)\nwant %s", got, r.Status, want)
	}
	noNames(t, r)
	if !strings.Contains(r.Checks[len(r.Checks)-1].Detail, "ratio") {
		t.Errorf("dry run detail: %s", r.Checks[len(r.Checks)-1].Detail)
	}
}

func TestFailureNamesNothing(t *testing.T) {
	needGo(t)
	in := input(t, "BenchmarkNeedsEnv")
	in.ConfigFile = "gofl.yaml"
	in.Options.Env = []string{"OTHER="}
	r, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != contract.StatusError || r.ErrorCode == nil || *r.ErrorCode != contract.TestNeedsEnv {
		t.Fatalf("got %s %v", r.Status, r.ErrorCode)
	}
	if got := statuses(r); !strings.HasPrefix(got, "go=ok module=ok config=warn") || !strings.HasSuffix(got, "dry-run=fail") {
		t.Errorf("checks: %s", got)
	}
	noNames(t, r)
	// The full message is still available locally.
	if local := r.Local(); len(local) != 1 || !strings.Contains(local[0], "SAMPLE_DB") {
		t.Errorf("local: %q", local)
	}
}

func TestWithoutBenchmark(t *testing.T) {
	needGo(t)
	in := input(t, "")
	in.Options.Sizes = [2]string{}
	r, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(r); !strings.HasSuffix(got, "packages=ok benchmark=skip") || r.Status != contract.StatusOK {
		t.Errorf("checks: %s", got)
	}
	if !strings.Contains(r.Checks[len(r.Checks)-2].Detail, "2 packages, 2 with benchmarks") {
		t.Errorf("survey: %s", r.Checks[len(r.Checks)-2].Detail)
	}
}

func TestStopsAtFirstFailure(t *testing.T) {
	in := input(t, "BenchmarkDedup")
	in.Options.Go = filepath.Join(t.TempDir(), "missing-go")
	r, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if statuses(r) != "go=fail" || *r.ErrorCode != contract.NoGo || r.GoVersion != nil {
		t.Errorf("got %s", statuses(r))
	}
	noNames(t, r)
}

func TestSetupAndSkips(t *testing.T) {
	needGo(t)
	in := input(t, "BenchmarkDedup")
	in.Options.Setup = []string{"exit 4"}
	r, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(statuses(r), "setup=fail") || *r.ErrorCode != contract.TestNeedsEnv {
		t.Errorf("got %s", statuses(r))
	}
	in.NoSetup, in.NoDryRun = true, true
	r, err = Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(r); !strings.Contains(got, "setup=skip") || !strings.HasSuffix(got, "dry-run=skip") || r.Status != contract.StatusOK {
		t.Errorf("got %s", got)
	}
}
