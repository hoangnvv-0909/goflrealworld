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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestResult is the outcome of one test. A record with Test == "" carries the
// package-level output (where a panic that kills the test binary is printed).
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

// Output joins the output of every record, in order. It is the text a crash
// reader should scan.
func (r Result) Output() string {
	var b strings.Builder
	for _, t := range r.Tests {
		b.WriteString(t.Output)
		if !strings.HasSuffix(t.Output, "\n") && t.Output != "" {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// DefaultTimeout is used when Spec.Timeout is zero.
const DefaultTimeout = 5 * time.Minute

// Run executes the spec. It returns an error only for misuse (an empty Dir,
// an unreadable overlay); everything the environment can do wrong is reported
// as Result.Unrunnable.
func Run(ctx context.Context, s Spec) (Result, error) {
	if s.Dir == "" {
		return Result{}, errors.New("runner: Spec.Dir is empty")
	}
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return Result{}, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Result{Unrunnable: &Reason{Kind: "env", Detail: fmt.Sprintf("project directory %q does not exist", s.Dir)}}, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return Result{Unrunnable: &Reason{Kind: "env", Detail: "the go tool is not on PATH"}}, nil
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmp, err := os.MkdirTemp("", "gofl-run-")
	if err != nil {
		return Result{Unrunnable: &Reason{Kind: "env", Detail: "cannot create a temporary directory: " + err.Error()}}, nil
	}
	defer os.RemoveAll(tmp)

	env := append(os.Environ(), s.Env...)

	if len(s.Setup) > 0 {
		cmd := exec.CommandContext(ctx, s.Setup[0], s.Setup[1:]...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				return Result{Unrunnable: timeoutReason(timeout, "setup command")}, nil
			}
			return Result{Unrunnable: &Reason{Kind: "setup", Detail: fmt.Sprintf("%s: %v: %s", s.Setup[0], err, tail(string(out), 500))}}, nil
		}
	}

	args := []string{"test", "-json", "-count=1"}
	if s.Run != "" {
		args = append(args, "-run", s.Run)
	}
	if s.Bench != "" {
		args = append(args, "-bench", s.Bench)
	}
	var profile string
	if s.CPUProfile != "" {
		profile, err = filepath.Abs(s.CPUProfile)
		if err != nil {
			return Result{}, err
		}
		// -cpuprofile leaves the test binary in the current directory unless
		// -o says otherwise: keep it in the temporary directory.
		args = append(args, "-cpuprofile", profile, "-o", filepath.Join(tmp, "pkg.test"))
	}
	if len(s.Overlay) > 0 {
		f, err := writeOverlay(tmp, s.Overlay)
		if err != nil {
			return Result{}, err
		}
		args = append(args, "-overlay", f)
	}
	pkgs := s.Packages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	args = append(args, pkgs...)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	prepare(cmd)
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()

	res := parseJSON(stdout.Bytes())
	if profile != "" {
		if _, err := os.Stat(profile); err == nil {
			res.Profile = profile
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.Unrunnable = timeoutReason(timeout, "go test")
		return res.Result, nil
	}
	if ctx.Err() != nil {
		res.Unrunnable = &Reason{Kind: "env", Detail: "cancelled"}
		return res.Result, nil
	}
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			res.Unrunnable = &Reason{Kind: "env", Detail: runErr.Error()}
			return res.Result, nil
		}
		// Exit status 1 is the normal "some tests failed". It is a build
		// problem when nothing ran, or when go reported build failures.
		if res.buildFailed || (len(res.Tests) == 0 && strings.TrimSpace(stderr.String()) != "") {
			detail := res.buildOutput
			if detail == "" {
				detail = stderr.String()
			}
			res.Unrunnable = &Reason{Kind: "build", Detail: tail(strings.TrimSpace(detail), 2000)}
		} else if len(res.Tests) == 0 {
			res.Unrunnable = &Reason{Kind: "env", Detail: "go test produced no test results: " + runErr.Error()}
		}
	}
	return res.Result, nil
}

func timeoutReason(d time.Duration, what string) *Reason {
	return &Reason{Kind: "timeout", Detail: fmt.Sprintf("%s did not finish within %s", what, d)}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

func writeOverlay(dir string, m map[string]string) (string, error) {
	type overlay struct {
		Replace map[string]string
	}
	abs := map[string]string{}
	for k, v := range m {
		ak, err := filepath.Abs(k)
		if err != nil {
			return "", err
		}
		av, err := filepath.Abs(v)
		if err != nil {
			return "", err
		}
		abs[ak] = av
	}
	data, err := json.Marshal(overlay{Replace: abs})
	if err != nil {
		return "", err
	}
	f := filepath.Join(dir, "overlay.json")
	return f, os.WriteFile(f, data, 0o600)
}

// ParseTestJSON reads the output of "go test -json" that was captured
// elsewhere (a file, a pipe) into the same records Run returns.
func ParseTestJSON(data []byte) Result { return parseJSON(data).Result }

// parsed is Result plus what only the parser knows.
type parsed struct {
	Result
	buildFailed bool
	buildOutput string
}

// testEvent is one line of "go test -json" (test2json).
type testEvent struct {
	Action     string
	Package    string
	ImportPath string
	Test       string
	Output     string
	Elapsed    float64
}

type key struct{ pkg, test string }

// parseJSON turns the output of "go test -json" into per-test records, in the
// order the tests (or packages) first appeared. Lines that are not JSON are
// kept as package-level output of the package "" so nothing is lost.
func parseJSON(data []byte) parsed {
	var p parsed
	idx := map[key]int{}
	rec := func(pkg, test string) *TestResult {
		k := key{pkg, test}
		i, ok := idx[k]
		if !ok {
			i = len(p.Tests)
			idx[k] = i
			p.Tests = append(p.Tests, TestResult{Package: pkg, Test: test})
		}
		return &p.Tests[i]
	}
	var build strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e testEvent
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &e) != nil {
			if len(bytes.TrimSpace(line)) > 0 {
				r := rec("", "")
				r.Output += string(line) + "\n"
			}
			continue
		}
		switch e.Action {
		case "build-output":
			build.WriteString(e.Output)
			continue
		case "build-fail":
			p.buildFailed = true
			continue
		}
		if e.Package == "" {
			continue
		}
		r := rec(e.Package, e.Test)
		switch e.Action {
		case "output":
			r.Output += e.Output
		case "pass":
			r.Passed = true
			r.Elapsed = time.Duration(e.Elapsed * float64(time.Second))
		case "fail":
			r.Passed = false
			r.Elapsed = time.Duration(e.Elapsed * float64(time.Second))
		}
	}
	p.buildOutput = build.String()
	// A package that failed with "[build failed]" has no tests.
	for _, t := range p.Tests {
		if strings.Contains(t.Output, "[build failed]") {
			p.buildFailed = true
		}
	}
	return p
}
