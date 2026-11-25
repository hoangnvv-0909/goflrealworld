package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"gofl/internal/benchrun"
	"gofl/internal/contract"
	"gofl/internal/diag"
	"gofl/internal/diffprof"
)

// stages are the steps of gofl perf after Compute (see assemble in
// stages.go). A nil step is skipped with a line on stderr, not an error;
// tests use that. Each function receives the output directory, reads what the earlier steps
// wrote there and writes its own file (critical_paths.json for Paths, the
// report for Report). On failure it returns an error whose code gofl records
// in diagnostics.json: a *diffprof.Error, or any error with an
// ErrorCode() contract.Code method. An error with no code fails the run
// without a code.
type stages struct {
	Paths  func(ctx context.Context, out string) error
	Report func(ctx context.Context, out string) error
}

// computeFlags are the flags of Compute, shared by gofl compute and gofl perf.
type computeFlags struct {
	topk   int
	rankBy string
	attr   bool
}

func addComputeFlags(fs *flag.FlagSet) *computeFlags {
	c := &computeFlags{}
	fs.IntVar(&c.topk, "topk", diffprof.DefaultTopK, "number of hotspots")
	fs.StringVar(&c.rankBy, "rank-by", "", "perop (seconds per benchmark operation) or raw (seconds, as contract v0 and the paper). Default: perop in the benchmark mode, raw with -base/-scaled. raw is unreliable for benchmarks: go test runs each size for about the same time, so the totals match and the delta is mostly noise")
	fs.BoolVar(&c.attr, "attribute-runtime", false, "charge runtime.* self time (allocation, GC) to its nearest caller instead of dropping it")
	return c
}

func (c *computeFlags) check() error {
	if c.topk < 1 {
		return fmt.Errorf("-topk must be at least 1")
	}
	if c.rankBy != "" && c.rankBy != diffprof.RankRaw && c.rankBy != diffprof.RankPerOp {
		return fmt.Errorf("-rank-by must be raw or perop, not %q", c.rankBy)
	}
	return nil
}

func (c *computeFlags) options(dir string, stderr io.Writer) diffprof.Options {
	return diffprof.Options{TopK: c.topk, RankBy: c.rankBy, AttributeRuntime: c.attr, Dir: dir, Warn: stderr}
}

// codeOf returns the contract code carried by err, or "".
func codeOf(err error) contract.Code {
	var de *diffprof.Error
	if errors.As(err, &de) {
		return de.Code
	}
	if c := benchrun.CodeOf(err); c != "" {
		return c
	}
	var coded interface{ ErrorCode() contract.Code }
	if errors.As(err, &coded) && coded.ErrorCode().Valid() {
		return coded.ErrorCode()
	}
	return ""
}

func cmdCompute(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gofl compute", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", benchrun.DefaultOut, "output directory holding run.json; hotspots.json is written there")
	dir := fs.String("dir", ".", "project root: files inside it are written relative to it")
	cf := addComputeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gofl compute: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := cf.check(); err != nil {
		fmt.Fprintf(stderr, "gofl compute: %v\n", err)
		return 2
	}
	hs, err := diffprof.Compute(*out, cf.options(*dir, stderr))
	var ue *diffprof.UsageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "gofl compute: %v\n", err)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "gofl compute: %v\n", err)
		var pe *diffprof.PriorError
		if code := codeOf(err); code != "" {
			fmt.Fprintf(stdout, "status: error %s\n", code)
		} else if errors.As(err, &pe) {
			fmt.Fprintf(stdout, "status: error (earlier step: %s)\n", pe.Code)
		}
		return 1
	}
	fmt.Fprintf(stdout, "status: ok\nwrote %s (%d hotspots)\n", filepath.Join(*out, contract.HotspotsFile), len(hs))
	return 0
}

func cmdPerf(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runPerf(ctx, args, stdout, stderr, assemble)
}

// runPerf is gofl perf with the later stages given, so tests can plug in
// their own. Order: profile (benchmark or two pprof files), Compute, Paths,
// report, then diagnostics for the whole run.
func runPerf(ctx context.Context, args []string, stdout, stderr io.Writer, mk stageMaker) int {
	start := time.Now()
	c := newCommon("perf", stderr)
	base := c.fs.String("base", "", "pprof mode: CPU profile at the small size")
	scaled := c.fs.String("scaled", "", "pprof mode: CPU profile at the large size")
	hash := c.fs.Bool("hash-names", false, "replace function and file names in report.md and report.json by hashes (diagnostics.json never has names)")
	entry := c.fs.String("entry", "", "function the paths start from (default: the -bench function; with -base/-scaled, the root of the call tree)")
	cf := addComputeFlags(c.fs)
	if err := c.fs.Parse(args); err != nil {
		return 2
	}
	if c.fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gofl perf: unexpected argument %q\n", c.fs.Arg(0))
		return 2
	}
	if err := cf.check(); err != nil {
		fmt.Fprintf(stderr, "gofl perf: %v\n", err)
		return 2
	}
	pprofMode := *base != "" || *scaled != ""
	if pprofMode && cf.rankBy == diffprof.RankPerOp {
		fmt.Fprintln(stderr, "gofl perf: -rank-by perop needs the benchmark mode (no bench data with -base/-scaled)")
		return 2
	}
	o, _, err := c.options(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gofl perf: %v\n", err)
		return 2
	}
	out := o.Out
	if out == "" {
		out = benchrun.DefaultOut
	}
	st := mk(stageParams{Opts: o, Hash: *hash, Entry: *entry, RankBy: cf.rankBy, Stderr: stderr})

	// Step 1: the two profiles. It writes run.json and diagnostics.json.
	var r *contract.Run
	if pprofMode {
		r, err = benchrun.FromPprof(ctx, benchrun.PprofOptions{Base: *base, Scaled: *scaled, Out: o.Out, PathsMethod: o.PathsMethod, Go: o.Go})
	} else {
		r, err = benchrun.Run(ctx, o)
	}
	var bue *benchrun.UsageError
	if errors.As(err, &bue) {
		fmt.Fprintf(stderr, "gofl perf: %v\n", err)
		return 2
	}
	if r == nil || (err != nil && r.Status != contract.StatusError) {
		fmt.Fprintf(stderr, "gofl perf: %v\n", err) // interrupted, or the output cannot be written
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "gofl perf: %v\n", err)
		fmt.Fprintf(stdout, "status: error %s\nwrote %s and %s\n", *r.ErrorCode,
			filepath.Join(out, contract.RunFile), filepath.Join(out, contract.DiagnosticsFile))
		return 1
	}

	// The remaining steps. The first failure stops the chain; its code, if it
	// has one, goes into diagnostics.json.
	var code contract.Code
	failed := func(step string, err error) {
		fmt.Fprintf(stderr, "gofl perf: %s: %v\n", step, err)
		code = codeOf(err)
	}
	hotspots := -1
	steps := []struct {
		name string
		run  func() error
	}{
		{"compute", func() error {
			hs, err := diffprof.Compute(out, cf.options(o.Dir, stderr))
			if hs != nil {
				hotspots = len(hs)
			}
			return err
		}},
		{"paths", stageFunc(st.Paths, ctx, out, stderr, "paths")},
		{"report", stageFunc(st.Report, ctx, out, stderr, "report")},
	}
	ok := true
	for _, s := range steps {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "gofl perf: interrupted\n")
			ok = false
			break
		}
		if err := s.run(); err != nil {
			var ue *diffprof.UsageError
			if errors.As(err, &ue) {
				fmt.Fprintf(stderr, "gofl perf: %v\n", err)
				return 2
			}
			failed(s.name, err)
			ok = false
			break
		}
	}

	d, warnings, derr := diag.Write(out, diag.Options{ErrorCode: code, DurationMs: time.Since(start).Milliseconds()})
	for _, w := range warnings {
		fmt.Fprintf(stderr, "gofl perf: warning: %s\n", w)
	}
	if derr != nil {
		fmt.Fprintf(stderr, "gofl perf: %v\n", derr)
		return 1
	}
	if !ok {
		if d.ErrorCode != nil {
			fmt.Fprintf(stdout, "status: error %s\n", *d.ErrorCode)
		}
		fmt.Fprintf(stdout, "wrote %s\n", filepath.Join(out, contract.DiagnosticsFile))
		return 1
	}
	fmt.Fprintf(stdout, "status: ok (%s mode, %.1fs)\nwrote %d hotspots to %s and %s\n", r.Mode,
		time.Since(start).Seconds(), hotspots, filepath.Join(out, contract.HotspotsFile), filepath.Join(out, contract.DiagnosticsFile))
	return 0
}

// stageFunc wraps a later stage; nil means it is not assembled yet.
func stageFunc(f func(context.Context, string) error, ctx context.Context, out string, stderr io.Writer, name string) func() error {
	return func() error {
		if f == nil {
			fmt.Fprintf(stderr, "gofl: stage skipped: not assembled yet (%s)\n", name)
			return nil
		}
		return f(ctx, out)
	}
}
