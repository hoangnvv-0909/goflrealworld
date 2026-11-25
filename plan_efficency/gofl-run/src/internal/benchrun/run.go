package benchrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gofl/internal/contract"
	"gofl/internal/diag"
	"gofl/internal/goenv"
	"gofl/internal/pprofcheck"
)

// Run is the benchmark mode. It always writes out/run.json and
// out/diagnostics.json, also when a step fails; the returned error is then a
// *Failure (or a *pprofcheck.Error, code NO_PROFILE) and run.json holds its
// code. A *UsageError means the options were wrong and nothing was written.
//
// Files in out: base.pprof, scaled.pprof, run.json, diagnostics.json, and the
// logs setup.log, build.log, bench_base.log, bench_scaled.log. The logs hold
// the raw output of go and of the benchmark, so they may name code.
func Run(ctx context.Context, o Options) (*contract.Run, error) {
	start := time.Now()
	s, err := NewSession(o, true)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	o = s.o
	run := &contract.Run{
		ToolVersion: contract.ToolVersion,
		Status:      contract.StatusOK,
		Mode:        contract.ModeBench,
		OS:          goenv.HostOS(),
		PathsMethod: o.PathsMethod,
		Bench:       &contract.Bench{Sizes: o.Sizes, BenchTime: o.BenchTime},
	}
	if err := startOut(o.Out); err != nil {
		return nil, err
	}
	finish := func(err error) (*contract.Run, error) {
		return run, s.finish(run, start, err)
	}

	info, err := s.FindGo(ctx)
	if err != nil {
		return finish(err)
	}
	run.GoVersion, run.OS = contract.Ptr(info.Version), info.OS
	s.logf("go %s, %s", info.Version, info.OS)
	if err := s.CheckModule(); err != nil {
		return finish(err)
	}
	if err := s.Setup(ctx, filepath.Join(o.Out, "setup.log")); err != nil {
		return finish(err)
	}
	t, err := s.Find(ctx)
	if err != nil {
		return finish(err)
	}
	if t.HasSub {
		s.logf("found %s in %s; each size is a sub-benchmark", o.Bench, t.ImportPath)
	} else {
		s.logf("found %s in %s; the size is passed in %s", o.Bench, t.ImportPath, SizeEnv)
		if !t.ReadsSize {
			s.logf("warning: %s has no sub-benchmarks and its package never mentions %s: both runs may measure the same size", o.Bench, SizeEnv)
		}
	}
	if err := s.Build(ctx, filepath.Join(o.Out, "build.log")); err != nil {
		return finish(err)
	}
	var m [2]*Measure
	for i, side := range []string{"base", "scaled"} {
		size := o.Sizes[i]
		profile := filepath.Join(o.Out, contract.BaseProfile)
		if i == 1 {
			profile = filepath.Join(o.Out, contract.ScaledProfile)
		}
		s.logf("running size %s (%s, benchtime %s)", size, side, o.BenchTime)
		m[i], err = s.Bench(ctx, size, RunParams{
			Profile:     profile,
			Log:         filepath.Join(o.Out, "bench_"+side+".log"),
			NeedSamples: true,
		})
		if err != nil {
			return finish(err)
		}
		p := contract.PathFor(profile)
		if i == 0 {
			run.BasePprof = &p
			run.Bench.BaseIterations, run.Bench.BaseNsPerOp = m[i].Iterations, m[i].NsPerOp
		} else {
			run.ScaledPprof = &p
			run.Bench.ScaledIterations, run.Bench.ScaledNsPerOp = m[i].Iterations, m[i].NsPerOp
		}
		s.logf("  %d iterations, %s per op, %d samples", m[i].Iterations, FormatNs(m[i].NsPerOp), m[i].Samples)
	}
	if m[0].NsPerOp > 0 && m[1].NsPerOp < 1.5*m[0].NsPerOp {
		s.logf("warning: one operation at size %s takes %.1fx the time at size %s; the sizes may be too close to show growth",
			o.Sizes[1], m[1].NsPerOp/m[0].NsPerOp, o.Sizes[0])
	}
	return finish(nil)
}

// PprofOptions configure the pprof mode.
type PprofOptions struct {
	Base, Scaled string // the two CPU profiles
	Out          string // output directory; default "out"
	PathsMethod  string
	Go           string // go binary, only for go_version; Go is not required
}

// FromPprof is the pprof mode: it checks the two given profiles and writes
// out/run.json and out/diagnostics.json. Go is not needed; go_version is
// filled in when it is found.
func FromPprof(ctx context.Context, o PprofOptions) (*contract.Run, error) {
	start := time.Now()
	if o.Base == "" || o.Scaled == "" {
		return nil, &UsageError{"pprof mode needs both -base and -scaled"}
	}
	if err := checkPathsMethod(&o.PathsMethod); err != nil {
		return nil, err
	}
	if o.Out == "" {
		o.Out = DefaultOut
	}
	run := &contract.Run{
		ToolVersion: contract.ToolVersion,
		Status:      contract.StatusOK,
		Mode:        contract.ModePprof,
		OS:          goenv.HostOS(),
		PathsMethod: o.PathsMethod,
	}
	if err := startOut(o.Out); err != nil {
		return nil, err
	}
	if bin, err := goenv.Find(o.Go); err == nil {
		if info, err := goenv.Probe(ctx, bin, ".", nil); err == nil {
			run.GoVersion, run.OS = contract.Ptr(info.Version), info.OS
		}
	}
	s := &Session{o: Options{Out: o.Out}}
	var failure error
	for i, p := range []string{o.Base, o.Scaled} {
		if _, err := pprofcheck.Check(p, true); err != nil {
			failure = err
			break
		}
		rel := contract.PathFor(p)
		if i == 0 {
			run.BasePprof = &rel
		} else {
			run.ScaledPprof = &rel
		}
	}
	return run, s.finish(run, start, failure)
}

// startOut creates the output directory and removes the files of an earlier
// run that would otherwise be read as part of this one.
func startOut(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("output directory: %v", err)
	}
	for _, f := range []string{contract.RunFile, contract.DiagnosticsFile, contract.HotspotsFile, contract.PathsFile} {
		os.Remove(filepath.Join(out, f))
	}
	return nil
}

// finish records err in run, writes run.json and diagnostics.json, and
// returns err (or the write error).
func (s *Session) finish(run *contract.Run, start time.Time, err error) error {
	run.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		code := CodeOf(err)
		if code == "" {
			// A cancelled context or an I/O error: no contract code
			// fits, so nothing is written.
			return err
		}
		run.Fail(code)
	}
	runPath := filepath.Join(s.o.Out, contract.RunFile)
	if werr := contract.WriteJSON(runPath, run); werr != nil {
		return werr
	}
	d, _ := diag.FromRun(run, runPath, diag.Options{})
	if werr := contract.WriteJSON(filepath.Join(s.o.Out, contract.DiagnosticsFile), d); werr != nil {
		return werr
	}
	return err
}

// FormatNs prints a time per operation.
func FormatNs(ns float64) string {
	d := time.Duration(ns)
	switch {
	case d < time.Microsecond:
		return d.String()
	case d < time.Millisecond:
		return d.Round(10 * time.Nanosecond).String()
	case d < time.Second:
		return d.Round(10 * time.Microsecond).String()
	}
	return d.Round(10 * time.Millisecond).String()
}
