// Package preflight is gofl preflight: it checks, step by step, that the
// benchmark mode can run on this machine, and reports the result without
// any function, file or package name, so the team leader can send it as is.
//
// The checks are the steps of the benchmark mode, in order: Go, go.mod,
// gofl.yaml, the output directory, the setup commands, the packages, the
// benchmark, compiling, and a dry run of one iteration at each size.
package preflight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gofl/internal/benchrun"
	"gofl/internal/contract"
	"gofl/internal/goenv"
)

// Check statuses.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

// Check is one line of the report. Detail never names code.
type Check struct {
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	ErrorCode *contract.Code `json:"error_code,omitempty"`
	Detail    string         `json:"detail"`
	// local is the full message of a failure. It may name code, so it is
	// shown only with -v and never marshalled.
	local string
}

// Report is the result of preflight.
type Report struct {
	ToolVersion string         `json:"tool_version"`
	Status      string         `json:"status"`
	ErrorCode   *contract.Code `json:"error_code"`
	GoVersion   *string        `json:"go_version"`
	OS          string         `json:"os"`
	DurationMs  int64          `json:"duration_ms"`
	Checks      []Check        `json:"checks"`
}

// Input configures preflight.
type Input struct {
	Options    benchrun.Options
	ConfigFile string // the gofl.yaml that was read, "" if none
	NoSetup    bool   // do not run the setup commands
	NoDryRun   bool   // do not run the benchmark
}

// Run performs the checks. It stops at the first failure, since every step
// needs the ones before it.
func Run(ctx context.Context, in Input) (*Report, error) {
	start := time.Now()
	o := in.Options
	bench := o.Bench != ""
	s, err := benchrun.NewSession(o, bench)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	o = s.Options()

	r := &Report{ToolVersion: contract.ToolVersion, Status: contract.StatusOK, OS: goenv.HostOS()}
	defer func() { r.DurationMs = time.Since(start).Milliseconds() }()
	add := func(c Check) { r.Checks = append(r.Checks, c) }
	fail := func(name string, err error) (*Report, error) {
		code := benchrun.CodeOf(err)
		if code == "" {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		add(Check{Name: name, Status: Fail, ErrorCode: &code, Detail: hint(code), local: err.Error()})
		r.Status, r.ErrorCode = contract.StatusError, &code
		return r, nil
	}

	// 1. Go.
	info, err := s.FindGo(ctx)
	if err != nil {
		return fail("go", err)
	}
	r.GoVersion, r.OS = contract.Ptr(info.Version), info.OS
	add(Check{Name: "go", Status: OK, Detail: info.Version + " " + info.OS})

	// 2. go.mod.
	if err := s.CheckModule(); err != nil {
		return fail("module", err)
	}
	add(Check{Name: "module", Status: OK, Detail: "go.mod found"})

	// 3. gofl.yaml.
	if in.ConfigFile == "" {
		add(Check{Name: "config", Status: Skip, Detail: "no gofl.yaml; using flags only"})
	} else {
		detail := fmt.Sprintf("read; %d environment variables, %d setup commands", len(o.Env), len(o.Setup))
		status := OK
		if n := emptyEnv(o.Env); n > 0 {
			status = Warn
			detail += fmt.Sprintf("; %d variables have an empty value", n)
		}
		add(Check{Name: "config", Status: status, Detail: detail})
	}

	// 4. Output directory.
	if err := writable(o.Out); err != nil {
		code := contract.NoProfile
		add(Check{Name: "output", Status: Fail, ErrorCode: &code, Detail: "the output directory is not writable", local: err.Error()})
		r.Status, r.ErrorCode = contract.StatusError, &code
		return r, nil
	}
	add(Check{Name: "output", Status: OK, Detail: "the output directory is writable"})

	// 5. Setup commands.
	switch {
	case len(o.Setup) == 0:
		add(Check{Name: "setup", Status: Skip, Detail: "no setup commands"})
	case in.NoSetup:
		add(Check{Name: "setup", Status: Skip, Detail: "not run (-no-setup)"})
	default:
		t := time.Now()
		if err := s.Setup(ctx, ""); err != nil {
			return fail("setup", err)
		}
		add(Check{Name: "setup", Status: OK, Detail: fmt.Sprintf("%d commands ran in %s", len(o.Setup), since(t))})
	}

	// 6. Packages.
	sv, err := s.Survey(ctx)
	if err != nil {
		return fail("packages", err)
	}
	detail := fmt.Sprintf("%d packages, %d with benchmarks (%d benchmark functions)", sv.Packages, sv.PackagesWithBench, sv.Benchmarks)
	if sv.PackagesWithError > 0 {
		detail += fmt.Sprintf("; %d packages have errors (%s)", sv.PackagesWithError, sv.ErrorCode)
		add(Check{Name: "packages", Status: Warn, Detail: detail})
	} else {
		add(Check{Name: "packages", Status: OK, Detail: detail})
	}

	// 7. The benchmark.
	if !bench {
		add(Check{Name: "benchmark", Status: Skip, Detail: "no benchmark given; pass -bench and -sizes, or set bench and sizes in gofl.yaml"})
		return r, nil
	}
	t, err := s.Find(ctx)
	if err != nil {
		return fail("benchmark", err)
	}
	switch {
	case t.HasSub:
		add(Check{Name: "benchmark", Status: OK, Detail: "found in one package; sizes are chosen by sub-benchmark name"})
	case t.ReadsSize:
		add(Check{Name: "benchmark", Status: OK, Detail: "found in one package; the size is passed in " + benchrun.SizeEnv})
	default:
		add(Check{Name: "benchmark", Status: Warn, Detail: "found, but it has no sub-benchmarks and never reads " + benchrun.SizeEnv + ": both sizes may run the same work"})
	}

	// 8. Compile.
	ct := time.Now()
	if err := s.Build(ctx, ""); err != nil {
		return fail("build", err)
	}
	add(Check{Name: "build", Status: OK, Detail: "the test binary compiles (" + since(ct) + ")"})

	// 9. Dry run: one iteration per size, with a profile.
	if in.NoDryRun {
		add(Check{Name: "dry-run", Status: Skip, Detail: "not run (-no-dry-run)"})
		return r, nil
	}
	tmp, err := os.MkdirTemp("", "gofl-preflight-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var ns [2]float64
	for i, size := range o.Sizes {
		m, err := s.Bench(ctx, size, benchrun.RunParams{BenchTime: "1x", Profile: filepath.Join(tmp, fmt.Sprintf("p%d.pprof", i))})
		if err != nil {
			return fail("dry-run", err)
		}
		ns[i] = m.NsPerOp
	}
	detail = fmt.Sprintf("both sizes ran with a CPU profile; one operation: %s (small), %s (large)", benchrun.FormatNs(ns[0]), benchrun.FormatNs(ns[1]))
	status := OK
	if ns[0] > 0 {
		ratio := ns[1] / ns[0]
		detail += fmt.Sprintf(", ratio %.1fx", ratio)
		if ratio < 1.5 {
			status = Warn
			detail += "; the sizes may be too close to show growth"
		}
	}
	add(Check{Name: "dry-run", Status: status, Detail: detail})
	return r, nil
}

// hint is the code-free explanation of an error code.
func hint(c contract.Code) string {
	switch c {
	case contract.NoGo:
		return "Go is missing, does not run, or is older than go.mod requires; install Go or set GOFL_GO"
	case contract.NoModule:
		return "no go.mod, or the dependencies cannot be downloaded; run go mod download, or set GOFLAGS/GOPROXY in env"
	case contract.NoBench:
		return "the benchmark, or a sub-benchmark for one of the sizes, was not found (or the name is in several packages: set package)"
	case contract.BuildFail:
		return "the test binary does not compile"
	case contract.TestNeedsEnv:
		return "a setup command failed, or the benchmark failed or skipped itself; it may need a variable or a service (see env and setup)"
	case contract.Timeout:
		return "a setup command or the benchmark took longer than the timeout"
	case contract.NoProfile:
		return "the CPU profile could not be written or read"
	}
	return string(c)
}

func emptyEnv(env []string) int {
	n := 0
	for _, kv := range env {
		if _, v, _ := strings.Cut(kv, "="); v == "" {
			n++
		}
	}
	return n
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".gofl-write-test-")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func since(t time.Time) string {
	return time.Since(t).Round(100 * time.Millisecond).String()
}

// Local returns the full messages of the failed checks. They may name code:
// print them only on the local terminal (-v).
func (r *Report) Local() []string {
	var out []string
	for _, c := range r.Checks {
		if c.local != "" {
			out = append(out, c.Name+": "+c.local)
		}
	}
	return out
}
