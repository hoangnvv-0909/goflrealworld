// Package benchrun is the benchmark mode of gofl perf: it runs one Go
// benchmark at a small and a large size, with
//
//	go test -run '^$' -bench <benchmark at that size> -cpuprofile <file>
//
// and writes the two CPU profiles, run.json and diagnostics.json. It also
// handles the pprof mode, where the two profiles are given, and provides the
// steps gofl preflight checks one by one.
//
// The test binary is compiled once (go test -c) and run directly, once per
// size. This is what go test does, but gofl can then kill a run that exceeds
// the time limit: the -timeout of go test does not cover benchmarks.
package benchrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gofl/internal/contract"
	"gofl/internal/goenv"
	"gofl/internal/pprofcheck"
	"gofl/internal/proc"
)

// SizeEnv is the environment variable that carries the size to a benchmark
// without sub-benchmarks.
const SizeEnv = "GOFL_SIZE"

// Defaults.
const (
	DefaultTimeout   = 10 * time.Minute
	DefaultBenchTime = "1s"
	DefaultOut       = "out"
	minBuildTimeout  = 10 * time.Minute
)

// Options configure the benchmark mode. They come from gofl.yaml and flags.
type Options struct {
	Dir         string        // project root (or any directory inside the module); default "."
	Package     string        // package with the benchmark; default: search ./...
	Bench       string        // benchmark function; the Benchmark prefix may be left out
	Sizes       [2]string     // small size, large size
	Timeout     time.Duration // limit for each setup command and each benchmark run
	BenchTime   string        // -benchtime for each size; default 1s
	Count       int           // -count; default 1
	CPU         string        // -cpu; default: GOMAXPROCS
	Setup       []string      // shell commands run first, in order
	Env         []string      // KEY=VALUE added to the environment of every command
	Out         string        // output directory; default "out"
	PathsMethod string        // paper (default) or stack; copied into run.json
	Go          string        // go binary; default: found by goenv.Find
	Log         io.Writer     // progress and warnings; nil discards them
}

// UsageError is a mistake in the options. No contract error code applies:
// gofl exits with status 2 and writes nothing.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Failure is a run that stopped with a contract error code. Msg may name
// packages, functions or files: it is printed locally and never copied to
// diagnostics.json.
type Failure struct {
	Code contract.Code
	Msg  string
	Log  string // file with the full output, if any
}

func (f *Failure) Error() string {
	s := string(f.Code) + ": " + f.Msg
	if f.Log != "" {
		s += " (full output: " + f.Log + ")"
	}
	return s
}

// CodeOf returns the contract code of err: the code of a *Failure or a
// *pprofcheck.Error, and "" for anything else.
func CodeOf(err error) contract.Code {
	var f *Failure
	if errors.As(err, &f) {
		return f.Code
	}
	var pe *pprofcheck.Error
	if errors.As(err, &pe) {
		return contract.NoProfile
	}
	return ""
}

var benchTimeRE = regexp.MustCompile(`^(\d+x|\d+(\.\d+)?(ns|us|µs|ms|s|m|h))$`)

// normalize fills defaults and checks the options of the benchmark mode.
func (o *Options) normalize(needBench bool) error {
	if o.Dir == "" {
		o.Dir = "."
	}
	abs, err := filepath.Abs(o.Dir)
	if err != nil {
		return &UsageError{err.Error()}
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return &UsageError{fmt.Sprintf("project directory %s does not exist", o.Dir)}
	}
	o.Dir = abs
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.BenchTime == "" {
		o.BenchTime = DefaultBenchTime
	}
	if !benchTimeRE.MatchString(o.BenchTime) {
		return &UsageError{fmt.Sprintf("benchtime %q: use a duration such as 1s or a count such as 100x", o.BenchTime)}
	}
	if o.Count <= 0 {
		o.Count = 1
	}
	if o.CPU != "" && !digits(o.CPU) {
		return &UsageError{fmt.Sprintf("cpu %q: use one number, such as 1", o.CPU)}
	}
	if o.Out == "" {
		o.Out = DefaultOut
	}
	if err := checkPathsMethod(&o.PathsMethod); err != nil {
		return err
	}
	for _, kv := range o.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return &UsageError{fmt.Sprintf("env %q: use NAME=value", kv)}
		}
	}
	if !needBench {
		return nil
	}
	o.Bench = strings.TrimSpace(o.Bench)
	if o.Bench == "" {
		return &UsageError{"no benchmark: pass -bench or set bench in gofl.yaml"}
	}
	if !strings.HasPrefix(o.Bench, "Benchmark") {
		o.Bench = "Benchmark" + o.Bench
	}
	if strings.ContainsAny(o.Bench, "/ \t") {
		return &UsageError{fmt.Sprintf("benchmark %q: give the function name only; sizes come from -sizes", o.Bench)}
	}
	return checkSizes(o.Sizes)
}

func checkPathsMethod(m *string) error {
	switch *m {
	case "":
		*m = contract.PathsPaper
	case contract.PathsPaper, contract.PathsStack:
	default:
		return &UsageError{fmt.Sprintf("paths_method %q: use paper or stack", *m)}
	}
	return nil
}

func checkSizes(sz [2]string) error {
	for _, s := range sz {
		if s == "" {
			return &UsageError{"give two sizes, small then large: -sizes 100,10000"}
		}
		if strings.ContainsAny(s, "/, \t") {
			return &UsageError{fmt.Sprintf("size %q may not contain '/', ',' or spaces", s)}
		}
	}
	if sz[0] == sz[1] {
		return &UsageError{"the two sizes are equal"}
	}
	a, errA := strconv.ParseFloat(sz[0], 64)
	b, errB := strconv.ParseFloat(sz[1], 64)
	if errA == nil && errB == nil && a >= b {
		return &UsageError{fmt.Sprintf("sizes %s,%s: put the small size first", sz[0], sz[1])}
	}
	return nil
}

// ParseSizes splits "100,10000" or a two-element list.
func ParseSizes(list []string) ([2]string, error) {
	var parts []string
	for _, x := range list {
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
	}
	if len(parts) != 2 {
		return [2]string{}, &UsageError{"give exactly two sizes, small then large: -sizes 100,10000"}
	}
	return [2]string{parts[0], parts[1]}, nil
}

// Session runs the steps of the benchmark mode one at a time. Run strings
// them together; preflight reports each one.
type Session struct {
	o       Options
	Go      *goenv.Info
	target  *Target
	tmp     string
	testBin string
}

// NewSession checks the options. bench says whether a benchmark and sizes
// are required.
func NewSession(o Options, bench bool) (*Session, error) {
	if err := o.normalize(bench); err != nil {
		return nil, err
	}
	return &Session{o: o}, nil
}

// Options returns the options after defaults were filled in.
func (s *Session) Options() Options { return s.o }

// Close removes the compiled test binary.
func (s *Session) Close() {
	if s.tmp != "" {
		os.RemoveAll(s.tmp)
		s.tmp = ""
	}
}

func (s *Session) logf(format string, args ...any) {
	if s.o.Log != nil {
		fmt.Fprintf(s.o.Log, format+"\n", args...)
	}
}

// env is the environment of every command: gofl's own, the configured
// variables, then extra.
func (s *Session) env(extra ...string) []string {
	env := append(os.Environ(), s.o.Env...)
	return append(env, extra...)
}

func (s *Session) buildTimeout() time.Duration {
	if s.o.Timeout > minBuildTimeout {
		return s.o.Timeout
	}
	return minBuildTimeout
}

// FindGo finds the go binary and asks it for its version. NO_GO if Go is
// missing or does not run.
func (s *Session) FindGo(ctx context.Context) (*goenv.Info, error) {
	bin, err := goenv.Find(s.o.Go)
	if err != nil {
		return nil, &Failure{Code: contract.NoGo, Msg: err.Error()}
	}
	info, err := goenv.Probe(ctx, bin, s.o.Dir, s.env())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Failure{Code: classifyGoEnv(err.Error()), Msg: err.Error()}
	}
	s.Go = info
	return info, nil
}

// classifyGoEnv: go env fails for a broken go.mod as well as for a broken Go.
func classifyGoEnv(msg string) contract.Code {
	if c := classifyBuild(msg); c != contract.BuildFail {
		return c
	}
	return contract.NoGo
}

// CheckModule fails with NO_MODULE when the project has no go.mod.
func (s *Session) CheckModule() error {
	if s.Go.GoMod == "" {
		return &Failure{Code: contract.NoModule, Msg: fmt.Sprintf("no go.mod in %s or its parents (or GO111MODULE=off)", s.o.Dir)}
	}
	return nil
}

// Setup runs the setup commands in the project directory, in order. A
// command that fails gives TEST_NEEDS_ENV; one that runs too long, TIMEOUT.
func (s *Session) Setup(ctx context.Context, logPath string) error {
	if len(s.o.Setup) == 0 {
		return nil
	}
	log, closeLog, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	for i, line := range s.o.Setup {
		fmt.Fprintf(log, "$ %s\n", line)
		s.logf("setup %d/%d", i+1, len(s.o.Setup))
		res := proc.Run(ctx, proc.Spec{Shell: line, Dir: s.o.Dir, Env: s.env(), Timeout: s.o.Timeout, Out: log})
		switch {
		case res.Err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &Failure{Code: contract.TestNeedsEnv, Msg: fmt.Sprintf("setup command %d could not start: %v", i+1, res.Err), Log: logPath}
		case res.TimedOut:
			return &Failure{Code: contract.Timeout, Msg: fmt.Sprintf("setup command %d did not finish within %s", i+1, s.o.Timeout), Log: logPath}
		case res.ExitCode != 0:
			return &Failure{Code: contract.TestNeedsEnv, Msg: fmt.Sprintf("setup command %d exited with status %d", i+1, res.ExitCode), Log: logPath}
		}
	}
	return nil
}

// Build compiles the test binary of the target package once.
func (s *Session) Build(ctx context.Context, logPath string) error {
	if s.target == nil {
		return errors.New("benchrun: Build before Find")
	}
	if s.tmp == "" {
		dir, err := os.MkdirTemp("", "gofl-")
		if err != nil {
			return err
		}
		s.tmp = dir
	}
	exe := "bench.test"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	s.testBin = filepath.Join(s.tmp, exe)
	log, closeLog, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	var sc scanner
	s.logf("compiling the test binary")
	res := proc.Run(ctx, proc.Spec{
		Name:    s.Go.Path,
		Args:    []string{"test", "-c", "-o", s.testBin, s.target.ImportPath},
		Dir:     s.o.Dir,
		Env:     s.env(),
		Timeout: s.buildTimeout(),
		Out:     io.MultiWriter(log, &sc),
	})
	sc.flush()
	text := strings.Join(sc.tail, "\n")
	switch {
	case res.Err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Failure{Code: contract.NoGo, Msg: "go test -c could not start: " + res.Err.Error()}
	case res.TimedOut:
		return &Failure{Code: contract.Timeout, Msg: fmt.Sprintf("compiling did not finish within %s", s.buildTimeout()), Log: logPath}
	case res.ExitCode != 0:
		return &Failure{Code: classifyBuild(text), Msg: "go test -c: " + firstError(text), Log: logPath}
	}
	if _, err := os.Stat(s.testBin); err != nil {
		return &Failure{Code: contract.NoBench, Msg: "go test -c produced no test binary (no test files?)", Log: logPath}
	}
	return nil
}

// Measure is what one benchmark run measured.
type Measure struct {
	Size       string
	Results    int     // result lines that matched
	Iterations int64   // sum of b.N
	NsPerOp    float64 // weighted mean
	Samples    int64   // samples in the profile
	Elapsed    time.Duration
}

// RunParams describe one run of the test binary.
type RunParams struct {
	BenchTime   string // default: the session's
	Profile     string // CPU profile to write
	Log         string // full output; "" keeps none
	NeedSamples bool   // fail with NO_PROFILE if the profile has no sample
}

// Bench runs the benchmark at one size.
func (s *Session) Bench(ctx context.Context, size string, p RunParams) (*Measure, error) {
	m, hasSub, err := s.benchOnce(ctx, size, p)
	if err == nil && hasSub && !s.target.HasSub {
		// b.Run is called from a helper: the benchmark has sub-benchmarks
		// after all, so select the size by name.
		s.logf("the benchmark has sub-benchmarks; selecting size %s by name", size)
		s.target.HasSub = true
		m, _, err = s.benchOnce(ctx, size, p)
	}
	return m, err
}

func (s *Session) benchOnce(ctx context.Context, size string, p RunParams) (*Measure, bool, error) {
	if s.testBin == "" {
		return nil, false, errors.New("benchrun: Bench before Build")
	}
	bt := p.BenchTime
	if bt == "" {
		bt = s.o.BenchTime
	}
	profile, err := filepath.Abs(p.Profile)
	if err != nil {
		return nil, false, err
	}
	os.Remove(profile)
	pattern := "^" + regexp.QuoteMeta(s.o.Bench) + "$"
	if s.target.HasSub {
		pattern = sizePattern(s.o.Bench, size)
	}
	args := []string{
		"-test.run=^$",
		"-test.bench=" + pattern,
		"-test.benchtime=" + bt,
		"-test.count=" + strconv.Itoa(s.o.Count),
		"-test.cpuprofile=" + profile,
		"-test.v",
	}
	if s.o.CPU != "" {
		args = append(args, "-test.cpu="+s.o.CPU)
	}
	log, closeLog, err := openLog(p.Log)
	if err != nil {
		return nil, false, err
	}
	defer closeLog()
	fmt.Fprintf(log, "$ %s=%s %s %s\n", SizeEnv, size, filepath.Base(s.testBin), strings.Join(args, " "))
	var sc scanner
	res := proc.Run(ctx, proc.Spec{
		Name:    s.testBin,
		Args:    args,
		Dir:     s.target.Dir, // go test runs test binaries in the package directory
		Env:     s.env(SizeEnv + "=" + size),
		Timeout: s.o.Timeout,
		Out:     io.MultiWriter(log, &sc),
	})
	sc.flush()
	m := &Measure{Size: size, Elapsed: res.Elapsed}
	switch {
	case res.Err != nil:
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, &Failure{Code: contract.BuildFail, Msg: "the test binary could not start: " + res.Err.Error(), Log: p.Log}
	case res.TimedOut:
		return nil, false, &Failure{Code: contract.Timeout, Msg: fmt.Sprintf("the benchmark at size %s did not finish within %s", size, s.o.Timeout), Log: p.Log}
	case res.ExitCode != 0:
		why := fmt.Sprintf("exit status %d", res.ExitCode)
		if len(sc.failed) > 0 {
			why = strings.Join(sc.failed, "; ")
		}
		return nil, false, &Failure{Code: contract.TestNeedsEnv, Msg: fmt.Sprintf("the benchmark failed at size %s: %s", size, why), Log: p.Log}
	}

	hasSub := false
	var weighted float64
	for _, r := range sc.results {
		switch {
		case r.Name == s.o.Bench && !s.target.HasSub:
		case strings.HasPrefix(r.Name, s.o.Bench+"/"):
			hasSub = true
			if !s.target.HasSub {
				continue
			}
		default:
			continue
		}
		m.Results++
		m.Iterations += r.N
		weighted += float64(r.N) * r.NsPerOp
	}
	if m.Iterations > 0 {
		m.NsPerOp = weighted / float64(m.Iterations)
	}
	if hasSub && !s.target.HasSub {
		return m, true, nil
	}
	if m.Results == 0 {
		if sc.skipped {
			return nil, false, &Failure{Code: contract.TestNeedsEnv, Msg: fmt.Sprintf("the benchmark skipped itself at size %s (b.Skip; often a missing variable or service)", size), Log: p.Log}
		}
		if s.target.HasSub {
			return nil, false, &Failure{Code: contract.NoBench, Msg: fmt.Sprintf("no sub-benchmark of %s has the size %s in its name", s.o.Bench, size), Log: p.Log}
		}
		return nil, false, &Failure{Code: contract.NoBench, Msg: fmt.Sprintf("%s printed no result at size %s", s.o.Bench, size), Log: p.Log}
	}
	info, err := pprofcheck.Check(profile, p.NeedSamples)
	if err != nil {
		return nil, false, err
	}
	m.Samples = info.Samples
	return m, false, nil
}

// openLog creates a log file, or discards the output if path is "".
func openLog(path string) (io.Writer, func(), error) {
	if path == "" {
		return io.Discard, func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}
