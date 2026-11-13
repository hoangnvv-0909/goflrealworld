// Package runner is the execution harness: it runs "go test" safely and
// collects per-test results and CPU profiles.
//
// Owner: TV5. Not in any paper: paper S only names the problem (claim S13:
// execution infrastructure is the bottleneck on real repositories).
//
// Rules: never modify source files in place; always respect Timeout; clean
// up temporary directories; when tests cannot run, return Unrunnable with a
// concrete reason instead of failing silently.
package runner

import (
	"context"
	"errors"
	"time"
)

var ErrNotImplemented = errors.New("runner: not implemented yet")

// Spec describes one run.
type Spec struct {
	Dir      string   // project root (module or workspace)
	Packages []string // e.g. ["./..."]
	Env      []string // extra KEY=VALUE entries
	Setup    []string // optional command run before tests, e.g. start a DB
	Timeout  time.Duration

	Run   string // -run regexp; empty = all tests
	Bench string // -bench regexp; empty = no benchmarks

	// Overlay maps original path -> replacement path (go build -overlay).
	// Only needed by the optional SemLoc branch.
	Overlay map[string]string

	CPUProfile string // output path for -cpuprofile; empty = no profile
}

type TestResult struct {
	Package string
	Test    string
	Passed  bool
	Output  string
	Elapsed time.Duration
}

// Reason explains why tests could not run.
type Reason struct {
	Kind   string // "build", "timeout", "env", "setup", ...
	Detail string
}

type Result struct {
	Tests      []TestResult
	Profile    string // path of the CPU profile written, if any
	Unrunnable *Reason
}

// Run executes the spec.
func Run(ctx context.Context, s Spec) (Result, error) {
	return Result{}, ErrNotImplemented
}
