package diffprof

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gofl/internal/contract"
	"gofl/internal/pprofread"
)

// DefaultTopK is the number of hotspots (HOTSPOT_K in the paper's constants.py,
// line 5).
const DefaultTopK = 5

// Values of Options.RankBy.
const (
	// RankRaw ranks by the delta of raw seconds, as contract v0 and the paper
	// define it. In benchmark mode it is unreliable: go test picks b.N so that
	// each size runs for about the same time, so both profiles have about the
	// same total and the delta is mostly noise (it often says
	// NO_POSITIVE_DELTA on a function that is plainly quadratic).
	RankRaw = "raw"
	// RankPerOp ranks by the delta of seconds per benchmark operation. It is
	// the default whenever run.json has the bench object.
	RankPerOp = "perop"
)

// ResolveRank returns the ranking to use: rankBy if given, else perop when
// run.json has usable bench data (benchmark mode) and raw when it has not
// (pprof mode, where there is no operation to divide by).
func ResolveRank(rankBy string, bench *contract.Bench) string {
	if rankBy != "" {
		return rankBy
	}
	if bench != nil && bench.BaseNsPerOp > 0 && bench.ScaledNsPerOp > 0 {
		return RankPerOp
	}
	return RankRaw
}

// Options configure Compute and Rank.
type Options struct {
	TopK   int    // 0 means DefaultTopK
	RankBy string // RankRaw, RankPerOp, or "" for ResolveRank's choice
	// AttributeRuntime keeps the cost of runtime.* frames (allocation, GC):
	// it is added to the nearest caller that is not runtime.*, and only
	// testing.* frames are dropped. By default both are dropped.
	AttributeRuntime bool
	Dir              string // project root; files inside it are written relative
	Bench            *contract.Bench
	Warn             io.Writer // warnings; they may name functions, so stderr only
}

func (o *Options) validate() error {
	switch o.RankBy {
	case "", RankRaw, RankPerOp:
	default:
		return &UsageError{fmt.Sprintf("-rank-by must be raw or perop, not %q", o.RankBy)}
	}
	if o.TopK < 0 {
		return &UsageError{"-topk must be positive"}
	}
	if o.RankBy == RankPerOp && o.Bench == nil {
		return &UsageError{"-rank-by perop needs the benchmark mode: run.json has no bench object"}
	}
	return nil
}

// PriorError means run.json says an earlier step failed, so there is nothing
// to compute.
type PriorError struct{ Code contract.Code }

func (e *PriorError) Error() string {
	return fmt.Sprintf("run.json reports status error (%s): the profile step failed, nothing to compute", e.Code)
}

// Compute reads out/run.json and the two profiles it names, ranks the
// hotspots and writes out/hotspots.json (an empty list, with an *Error of code
// NO_POSITIVE_DELTA, when nothing grew). A stale hotspots.json is removed
// first. Errors: *UsageError, *PriorError, *Error (a contract code), or an
// I/O error.
func Compute(out string, o Options) ([]Hotspot, error) {
	hotspotsPath := filepath.Join(out, contract.HotspotsFile)
	os.Remove(hotspotsPath)
	runPath := filepath.Join(out, contract.RunFile)
	run, err := contract.ReadRun(runPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &UsageError{fmt.Sprintf("%s not found: run gofl profile first", runPath)}
		}
		return nil, &UsageError{err.Error()}
	}
	if run.Status != contract.StatusOK {
		code := contract.Code("")
		if run.ErrorCode != nil {
			code = *run.ErrorCode
		}
		return nil, &PriorError{code}
	}
	if run.BasePprof == nil || run.ScaledPprof == nil {
		return nil, &Error{Code: contract.NoProfile, Msg: runPath + " has no base_pprof or scaled_pprof"}
	}
	o.Bench = run.Bench
	if err := o.validate(); err != nil {
		return nil, err
	}
	base, err := readProfile(contract.Resolve(runPath, *run.BasePprof))
	if err != nil {
		return nil, err
	}
	scaled, err := readProfile(contract.Resolve(runPath, *run.ScaledPprof))
	if err != nil {
		return nil, err
	}
	hotspots, err := Rank(base, scaled, o)
	var e *Error
	if err != nil && !(errors.As(err, &e) && e.Code == contract.NoPositiveDelta) {
		return nil, err
	}
	if werr := contract.WriteJSON(hotspotsPath, hotspots); werr != nil {
		return nil, werr
	}
	return hotspots, err
}

// readProfile reads a profile with pprofread, files as the profile has them
// (Rank makes them relative to the project root when it writes the hotspots).
func readProfile(path string) (*pprofread.Profile, error) {
	p, err := pprofread.Load(path, pprofread.Options{})
	if err != nil {
		return nil, &Error{Code: contract.NoProfile, Msg: err.Error()}
	}
	return p, nil
}

// periodNs is the sampling period of p in nanoseconds.
func periodNs(p *pprofread.Profile) int64 { return int64(math.Round(p.SecondsPerSample * 1e9)) }

// isNoise reports whether a function is left out of the ranking. The paper
// only drops its own harness frames (VizTracer and the run script,
// stage2.py:58-75); dropping runtime.* and testing.* is the design of this
// port (section 4.1). With attributeRuntime, runtime.* is not noise because
// its self time has been moved to the caller.
func isNoise(name string, attributeRuntime bool) bool {
	if strings.HasPrefix(name, "testing.") {
		return true
	}
	return !attributeRuntime && isRuntime(name)
}

// isRuntime: the runtime and, since Go 1.21, its internal packages.
func isRuntime(name string) bool {
	return strings.HasPrefix(name, "runtime.") || strings.HasPrefix(name, "internal/runtime/")
}

// Scale returns the factor that turns self seconds of p into seconds per
// operation: nsPerOp / T, where T is the time (ns) of the samples whose stack
// holds a benchmark function frame (name ending in Benchmark..., closures of
// sub-benchmarks included). found is false when there is no such frame; T is
// then the time of all samples. Paths uses the same factor if the per-op
// scores are chosen. T includes any work before b.ResetTimer and the
// calibration rounds of b.N: that is the limit of this method.
func Scale(p *pprofread.Profile, nsPerOp float64) (factor float64, found bool) {
	var samples int64
	for _, s := range p.Stacks {
		for _, f := range s.Frames {
			if isBenchmarkFunc(f.Name) {
				samples += s.Samples
				found = true
				break
			}
		}
	}
	if !found {
		samples = p.TotalSamples
	}
	t := samples * periodNs(p)
	if t <= 0 {
		return 0, found
	}
	return nsPerOp / float64(t), found
}

// isBenchmarkFunc matches pkg.BenchmarkX and its closures pkg.BenchmarkX.func1.
func isBenchmarkFunc(name string) bool {
	for {
		i := strings.LastIndexByte(name, '.')
		if i < 0 {
			return false
		}
		last := name[i+1:]
		if strings.HasPrefix(last, "Benchmark") {
			return true
		}
		if isClosureSuffix(last) {
			name = name[:i]
			continue
		}
		return false
	}
}

// isClosureSuffix matches func1, func2, 1, 2 (what the compiler appends).
func isClosureSuffix(s string) bool {
	s = strings.TrimPrefix(s, "func")
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type agg struct{ ns, samples int64 }

// selfTimes sums the self time of every function of a profile, and counts the
// nodes of each function in the call-context tree.
func selfTimes(p *pprofread.Profile, attributeRuntime bool) (map[pprofread.Key]*agg, map[pprofread.Key]int) {
	tree := p.Tree()
	period := periodNs(p)
	sums := map[pprofread.Key]*agg{}
	occ := map[pprofread.Key]int{}
	for k, nodes := range tree.ByFunc {
		occ[k] = len(nodes)
		for _, n := range nodes {
			if n.SelfSamples <= 0 {
				continue
			}
			target := n
			if attributeRuntime {
				target = nil
				for c := n; c != nil && !c.IsRoot(); c = c.Parent {
					if !isRuntime(c.Name) {
						target = c
						break
					}
				}
				if target == nil {
					continue // only runtime frames: no caller to charge
				}
			}
			if isNoise(target.Name, attributeRuntime) {
				continue
			}
			a := sums[target.Key()]
			if a == nil {
				a = &agg{}
				sums[target.Key()] = a
			}
			a.samples += n.SelfSamples
			a.ns += n.SelfSamples * period
		}
	}
	return sums, occ
}

type entry struct {
	f                  pprofread.Key
	bNs, sNs, bSm, sSm int64
	bOcc, sOcc         int
	rawDelta           int64   // ns
	bPO, sPO, poDelta  float64 // seconds per operation
}

// Rank computes the hotspots of two profiles. See Options for the choices.
// With no function that grew it returns an empty, non-nil list and an *Error
// with code NO_POSITIVE_DELTA.
func Rank(baseP, scaledP *pprofread.Profile, o Options) ([]Hotspot, error) {
	warn := o.Warn
	if warn == nil {
		warn = io.Discard
	}
	warnf := func(format string, a ...any) { fmt.Fprintf(warn, "gofl: warning: "+format+"\n", a...) }
	if o.Bench != nil && (o.Bench.BaseNsPerOp <= 0 || o.Bench.ScaledNsPerOp <= 0) {
		warnf("run.json has no usable ns_per_op: per-operation values are not computed")
		o.Bench = nil
	}
	o.RankBy = ResolveRank(o.RankBy, o.Bench)
	if err := o.validate(); err != nil {
		return nil, err
	}
	k := o.TopK
	if k == 0 {
		k = DefaultTopK
	}

	baseSum, baseOcc := selfTimes(baseP, o.AttributeRuntime)
	scaledSum, scaledOcc := selfTimes(scaledP, o.AttributeRuntime)

	var fb, fs float64
	if o.Bench != nil {
		var foundB, foundS bool
		fb, foundB = Scale(baseP, o.Bench.BaseNsPerOp)
		fs, foundS = Scale(scaledP, o.Bench.ScaledNsPerOp)
		if !foundB || !foundS {
			warnf("no Benchmark... frame in the %s profile: T is the time of all samples, so per-operation values include everything",
				map[bool]string{true: "baseline", false: "scaled"}[!foundB])
		}
	} else if t0, t1 := float64(baseP.TotalSamples), float64(scaledP.TotalSamples); t0 > 0 && t1 > 0 && max(t0, t1)/min(t0, t1) < 1.5 {
		warnf("the two profiles are about equally long (under 1.5x): the delta is mostly a shift in share, not growth")
	}

	// Only functions of the scaled profile can have a positive delta.
	entries := make([]*entry, 0, len(scaledSum))
	for f, s := range scaledSum {
		e := &entry{f: f, sNs: s.ns, sSm: s.samples, sOcc: scaledOcc[f], bOcc: baseOcc[f]}
		if b := baseSum[f]; b != nil {
			e.bNs, e.bSm = b.ns, b.samples
		}
		e.rawDelta = e.sNs - e.bNs
		if o.Bench != nil {
			e.bPO, e.sPO = perOp(e.bNs, fb), perOp(e.sNs, fs)
			e.poDelta = e.sPO - e.bPO
		}
		entries = append(entries, e)
	}

	less := func(delta func(*entry) float64) func(a, b *entry) bool {
		return func(a, b *entry) bool {
			if da, db := delta(a), delta(b); da != db {
				return da > db
			}
			if a.f.Name != b.f.Name {
				return a.f.Name < b.f.Name
			}
			return a.f.File < b.f.File
		}
	}
	rawDelta := func(e *entry) float64 { return float64(e.rawDelta) }
	poDelta := func(e *entry) float64 { return e.poDelta }
	positive := func(delta func(*entry) float64) (list []*entry, sum float64) {
		for _, e := range entries {
			if d := delta(e); d > 0 {
				list = append(list, e)
				sum += d
			}
		}
		l := less(delta)
		sort.Slice(list, func(i, j int) bool { return l(list[i], list[j]) })
		return
	}
	rawList, rawSum := positive(rawDelta)
	var poList []*entry
	var poSum float64
	if o.Bench != nil {
		poList, poSum = positive(poDelta)
	}
	chosen := rawList
	if o.RankBy == RankPerOp {
		chosen = poList
	}
	if len(chosen) == 0 {
		if o.Bench != nil && o.RankBy != RankPerOp && len(poList) > 0 {
			warnf("no function grew in raw seconds, but %d did per operation: try -rank-by perop", len(poList))
		}
		return []Hotspot{}, &Error{Code: contract.NoPositiveDelta, Msg: "no function has a positive delta of self time"}
	}
	top := func(l []*entry) []*entry { return l[:min(k, len(l))] }
	if o.Bench != nil {
		reportDifference(warn, top(rawList), top(poList), k)
	}

	var out []Hotspot
	for i, e := range top(chosen) {
		h := Hotspot{
			Rank:                i + 1,
			Function:            e.f.Name,
			File:                pprofread.RelFile(e.f.File, rootOf(o.Dir)),
			BaselineSelfSeconds: round(float64(e.bNs)/1e9, 6),
			ScaledSelfSeconds:   round(float64(e.sNs)/1e9, 6),
			DeltaSelfSeconds:    round(float64(e.rawDelta)/1e9, 6),
			BaselineSamples:     e.bSm,
			ScaledSamples:       e.sSm,
			BaselineOccurrences: e.bOcc,
			ScaledOccurrences:   e.sOcc,
		}
		if e.rawDelta > 0 && rawSum > 0 {
			h.NDC = round(float64(e.rawDelta)/rawSum, 6)
		}
		if o.Bench != nil {
			b, s, d := round(e.bPO, 9), round(e.sPO, 9), round(e.poDelta, 9)
			n := 0.0
			if e.poDelta > 0 && poSum > 0 {
				n = round(e.poDelta/poSum, 6)
			}
			h.BaselinePerOpSeconds, h.ScaledPerOpSeconds, h.DeltaPerOpSeconds, h.NDCPerOp = &b, &s, &d, &n
		}
		out = append(out, h)
	}
	return out, nil
}

// perOp turns self nanoseconds into seconds per operation: (ns / T) * ns_per_op
// is nanoseconds per operation, and factor is ns_per_op / T.
func perOp(selfNs int64, factor float64) float64 { return float64(selfNs) * factor / 1e9 }

func rootOf(dir string) string {
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// reportDifference writes one line when the raw and per-operation rankings
// pick different functions. Names go to the terminal only.
func reportDifference(w io.Writer, raw, po []*entry, k int) {
	in := func(l []*entry) map[pprofread.Key]bool {
		m := map[pprofread.Key]bool{}
		for _, e := range l {
			m[e.f] = true
		}
		return m
	}
	inRaw, inPO := in(raw), in(po)
	var onlyRaw, onlyPO []string
	for _, e := range raw {
		if !inPO[e.f] {
			onlyRaw = append(onlyRaw, e.f.Name)
		}
	}
	for _, e := range po {
		if !inRaw[e.f] {
			onlyPO = append(onlyPO, e.f.Name)
		}
	}
	if len(onlyRaw) == 0 && len(onlyPO) == 0 {
		return
	}
	msg := fmt.Sprintf("the raw and per-operation top-%d differ:", k)
	if len(onlyRaw) > 0 {
		msg += " only in raw: " + strings.Join(onlyRaw, ", ") + ";"
	}
	if len(onlyPO) > 0 {
		msg += " only in perop: " + strings.Join(onlyPO, ", ") + ";"
	}
	fmt.Fprintf(w, "gofl: warning: %s\n", strings.TrimSuffix(msg, ";"))
}
