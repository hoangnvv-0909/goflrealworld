// Command gofl holds the commands of the run package (gói chạy) that this
// part provides:
//
//	gofl preflight     check the machine; the report names no code
//	gofl profile       benchmark mode or pprof mode: two profiles, run.json, diagnostics.json
//	gofl diagnostics   rebuild diagnostics.json after the later stages
//	gofl compute       diffprof.Compute alone: run.json to hotspots.json
//	gofl perf          profile, Compute, Paths, report, diagnostics in one go
//
// gofl perf runs profile, then diffprof.Compute, diffprof.Paths and the
// report, then diagnostics (the stages are assembled in stages.go).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"gofl/internal/benchrun"
	"gofl/internal/config"
	"gofl/internal/contract"
	"gofl/internal/diag"
	"gofl/internal/preflight"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `gofl %s: the run package of gofl perf (benchmark mode, preflight, diagnostics)

Usage:
  gofl preflight   [flags]   check that the benchmark mode can run here
  gofl profile     [flags]   run the benchmark at two sizes, or check two given profiles
  gofl diagnostics [flags]   rebuild diagnostics.json from the output directory
  gofl compute     [flags]   rank the hotspots: out/run.json to out/hotspots.json
  gofl perf        [flags]   the whole chain: profile, compute, paths, report, diagnostics
  gofl version

Benchmark mode:
  gofl profile -bench BenchmarkBuildRows -sizes 100,10000
pprof mode (no benchmark, no code needed):
  gofl profile -base a.pprof -scaled b.pprof

Settings come from gofl.yaml in the current directory (or -config FILE);
flags override it. Run "gofl <command> -h" for the flags.

Exit status: 0 ok, 1 the run failed (run.json and diagnostics.json hold the
error code), 2 wrong flags or gofl.yaml.
`

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, contract.ToolVersion)
		return 2
	}
	switch args[0] {
	case "preflight":
		return cmdPreflight(ctx, args[1:], stdout, stderr)
	case "profile":
		return cmdProfile(ctx, args[1:], stdout, stderr)
	case "diagnostics":
		return cmdDiagnostics(args[1:], stdout, stderr)
	case "compute":
		return cmdCompute(args[1:], stdout, stderr)
	case "perf":
		return cmdPerf(ctx, args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintf(stdout, "gofl %s\n", contract.ToolVersion)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, usage, contract.ToolVersion)
		return 0
	}
	fmt.Fprintf(stderr, "gofl: unknown command %q\n\n", args[0])
	fmt.Fprintf(stderr, usage, contract.ToolVersion)
	return 2
}

// list is a repeatable flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

// common are the flags shared by preflight and profile.
type common struct {
	fs                                                      *flag.FlagSet
	config, dir, pkg, bench, sizes, timeout, benchtime, cpu string
	out, pathsMethod, goBin                                 string
	count                                                   int
	setup, env                                              list
}

func newCommon(name string, stderr io.Writer) *common {
	c := &common{fs: flag.NewFlagSet("gofl "+name, flag.ContinueOnError)}
	fs := c.fs
	fs.SetOutput(stderr)
	fs.StringVar(&c.config, "config", "", "settings file (default: gofl.yaml in the current directory, if any)")
	fs.StringVar(&c.dir, "dir", "", "project root, the directory with go.mod (default .)")
	fs.StringVar(&c.pkg, "pkg", "", "package with the benchmark, e.g. ./internal/report (default: search ./...)")
	fs.StringVar(&c.bench, "bench", "", "benchmark function, e.g. BenchmarkBuildRows")
	fs.StringVar(&c.sizes, "sizes", "", "small and large size, e.g. 100,10000")
	fs.StringVar(&c.timeout, "timeout", "", "limit for each setup command and each benchmark run (default 10m)")
	fs.StringVar(&c.benchtime, "benchtime", "", "go test -benchtime for each size (default 1s)")
	fs.IntVar(&c.count, "count", 0, "go test -count (default 1)")
	fs.StringVar(&c.cpu, "cpu", "", "go test -cpu, e.g. 1 (default GOMAXPROCS)")
	fs.Var(&c.setup, "setup", "setup command, run before the benchmark (repeatable; replaces gofl.yaml's)")
	fs.Var(&c.env, "env", "NAME=value for setup, build and benchmark (repeatable; added to gofl.yaml's)")
	fs.StringVar(&c.out, "out", "", "output directory (default out)")
	fs.StringVar(&c.pathsMethod, "paths-method", "", "paper (strict) or stack; unset: paper, then stack if the entry point cannot cover a hotspot. run.json records the one used")
	fs.StringVar(&c.goBin, "go", "", "go binary (default: GOFL_GO, then PATH)")
	return c
}

// options merges gofl.yaml and the flags that were set.
func (c *common) options(stderr io.Writer) (benchrun.Options, string, error) {
	var o benchrun.Options
	file := c.config
	if file == "" {
		if _, err := os.Stat("gofl.yaml"); err == nil {
			file = "gofl.yaml"
		}
	}
	cfg := &config.Config{}
	if file != "" {
		var err error
		if cfg, err = config.Load(file); err != nil {
			return o, "", err
		}
	}
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	pick := func(name, flagVal, cfgVal string) string {
		if set[name] {
			return flagVal
		}
		return cfgVal
	}
	// One absolute project root for every stage (profile, compute, paths):
	// a stage that got "" used to read file names differently from the others.
	o.Dir = pick("dir", c.dir, cfg.Dir)
	if o.Dir == "" {
		o.Dir = "."
	}
	if abs, err := filepath.Abs(o.Dir); err == nil {
		o.Dir = abs
	}
	o.Package = pick("pkg", c.pkg, cfg.Package)
	o.Bench = pick("bench", c.bench, cfg.Bench)
	o.BenchTime = pick("benchtime", c.benchtime, cfg.BenchTime)
	o.CPU = pick("cpu", c.cpu, cfg.CPU)
	o.Out = pick("out", c.out, cfg.Out)
	o.PathsMethod = pick("paths-method", c.pathsMethod, cfg.PathsMethod)
	o.Go = pick("go", c.goBin, cfg.Go)
	o.Count = cfg.Count
	if set["count"] {
		o.Count = c.count
	}
	o.Timeout = cfg.Timeout
	if set["timeout"] {
		d, err := config.ParseDuration(c.timeout)
		if err != nil {
			return o, "", fmt.Errorf("-timeout: %v", err)
		}
		o.Timeout = d
	}
	o.Setup = cfg.Setup
	if set["setup"] {
		o.Setup = c.setup
	}
	o.Env = append(append([]string{}, cfg.Env...), c.env...)
	sizes := cfg.Sizes
	if set["sizes"] {
		sizes = []string{c.sizes}
	}
	if len(sizes) > 0 {
		sz, err := benchrun.ParseSizes(sizes)
		if err != nil {
			return o, "", err
		}
		o.Sizes = sz
	}
	o.Log = prefixed{stderr}
	return o, file, nil
}

// prefixed writes progress lines to stderr.
type prefixed struct{ w io.Writer }

func (p prefixed) Write(b []byte) (int, error) {
	fmt.Fprintf(p.w, "gofl: %s", b)
	return len(b), nil
}

func cmdProfile(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCommon("profile", stderr)
	base := c.fs.String("base", "", "pprof mode: CPU profile at the small size")
	scaled := c.fs.String("scaled", "", "pprof mode: CPU profile at the large size")
	if err := c.fs.Parse(args); err != nil {
		return 2
	}
	if c.fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gofl profile: unexpected argument %q\n", c.fs.Arg(0))
		return 2
	}
	o, _, err := c.options(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gofl profile: %v\n", err)
		return 2
	}
	var r *contract.Run
	if *base != "" || *scaled != "" {
		r, err = benchrun.FromPprof(ctx, benchrun.PprofOptions{Base: *base, Scaled: *scaled, Out: o.Out, PathsMethod: o.PathsMethod, Go: o.Go})
	} else {
		r, err = benchrun.Run(ctx, o)
	}
	var ue *benchrun.UsageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "gofl profile: %v\n", err)
		return 2
	}
	if r == nil || (err != nil && r.Status != contract.StatusError) {
		// Interrupted, or the output directory could not be written.
		fmt.Fprintf(stderr, "gofl profile: %v\n", err)
		return 1
	}
	out := o.Out
	if out == "" {
		out = benchrun.DefaultOut
	}
	if err != nil {
		fmt.Fprintf(stderr, "gofl profile: %v\n", err)
		fmt.Fprintf(stdout, "status: error %s\nwrote %s and %s\n", *r.ErrorCode,
			filepath.Join(out, contract.RunFile), filepath.Join(out, contract.DiagnosticsFile))
		return 1
	}
	fmt.Fprintf(stdout, "status: ok (%s mode, %.1fs)\nwrote %s, %s, %s and %s\n", r.Mode, float64(r.DurationMs)/1000,
		*r.BasePprof, *r.ScaledPprof, filepath.Join(out, contract.RunFile), filepath.Join(out, contract.DiagnosticsFile))
	return 0
}

func cmdPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCommon("preflight", stderr)
	asJSON := c.fs.Bool("json", false, "print the report as JSON")
	noSetup := c.fs.Bool("no-setup", false, "do not run the setup commands")
	noDry := c.fs.Bool("no-dry-run", false, "do not run the benchmark (one iteration per size)")
	verbose := c.fs.Bool("v", false, "also print the full error messages (they may name code: do not send them)")
	if err := c.fs.Parse(args); err != nil {
		return 2
	}
	o, file, err := c.options(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gofl preflight: %v\n", err)
		return 2
	}
	o.Log = nil // preflight prints its own report
	r, err := preflight.Run(ctx, preflight.Input{Options: o, ConfigFile: file, NoSetup: *noSetup, NoDryRun: *noDry})
	if err != nil {
		fmt.Fprintf(stderr, "gofl preflight: %v\n", err)
		var ue *benchrun.UsageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	if *asJSON {
		data, _ := json.MarshalIndent(r, "", "  ")
		fmt.Fprintf(stdout, "%s\n", data)
	} else {
		fmt.Fprintf(stdout, "gofl preflight %s (no function, file or package names: safe to send)\n", contract.ToolVersion)
		for _, ch := range r.Checks {
			fmt.Fprintf(stdout, "  %-5s %-10s %s\n", ch.Status, ch.Name, ch.Detail)
		}
		status := r.Status
		if r.ErrorCode != nil {
			status += " " + string(*r.ErrorCode)
		}
		fmt.Fprintf(stdout, "status: %s (%.1fs)\n", status, float64(r.DurationMs)/1000)
	}
	if *verbose {
		if local := r.Local(); len(local) > 0 {
			fmt.Fprintln(stderr, "details (may name code; do not send):")
			for _, l := range local {
				fmt.Fprintln(stderr, "  "+l)
			}
		}
	}
	if r.Status != contract.StatusOK {
		return 1
	}
	return 0
}

func cmdDiagnostics(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gofl diagnostics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", benchrun.DefaultOut, "output directory holding run.json")
	code := fs.String("error-code", "", "error code of a later stage that failed, e.g. ENTRY_NOT_COVERING")
	dur := fs.Int64("duration-ms", 0, "duration of the whole pipeline (default: run.json's)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o := diag.Options{ErrorCode: contract.Code(*code), DurationMs: *dur}
	if o.ErrorCode != "" && !o.ErrorCode.Valid() {
		fmt.Fprintf(stderr, "gofl diagnostics: unknown error code %q\n", *code)
		return 2
	}
	d, warnings, err := diag.Write(*out, o)
	if err != nil {
		fmt.Fprintf(stderr, "gofl diagnostics: %v\n", err)
		return 2
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "gofl diagnostics: warning: %s\n", w)
	}
	status := d.Status
	if d.ErrorCode != nil {
		status += " " + string(*d.ErrorCode)
	}
	fmt.Fprintf(stdout, "status: %s\nwrote %s\n", status, filepath.Join(*out, contract.DiagnosticsFile))
	return 0
}
