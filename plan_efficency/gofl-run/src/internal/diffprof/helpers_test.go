package diffprof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/pprof/profile"

	"gofl/internal/pprofread"
)

// st is one stack of a hand-built profile: frames from root to leaf, written
// "name" or "name@file", and the number of samples that end at the leaf.
type st struct {
	frames []string
	n      int64
}

func stk(n int64, frames ...string) st { return st{frames: frames, n: n} }

const defaultFile = "/proj/a.go"

// buildProfile makes a CPU profile with a samples/count and a cpu/nanoseconds
// dimension, like the one go test writes, and the given period.
func buildProfile(periodNs int64, stacks ...st) *profile.Profile {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     periodNs,
	}
	funcs := map[string]*profile.Function{}
	fn := func(spec string) *profile.Function {
		if f, ok := funcs[spec]; ok {
			return f
		}
		name, file, ok := strings.Cut(spec, "@")
		if !ok {
			file = defaultFile
		}
		f := &profile.Function{ID: uint64(len(funcs) + 1), Name: name, Filename: file}
		funcs[spec] = f
		p.Function = append(p.Function, f)
		return f
	}
	loc := func(specs ...string) *profile.Location {
		l := &profile.Location{ID: uint64(len(p.Location) + 1)}
		for _, s := range specs {
			l.Line = append(l.Line, profile.Line{Function: fn(s)})
		}
		p.Location = append(p.Location, l)
		return l
	}
	for _, s := range stacks {
		var locs []*profile.Location
		for i := len(s.frames) - 1; i >= 0; i-- { // leaf first
			// "a+b" is one location with inlining: a is the innermost.
			locs = append(locs, loc(strings.Split(s.frames[i], "+")...))
		}
		p.Sample = append(p.Sample, &profile.Sample{Location: locs, Value: []int64{s.n, s.n * periodNs}})
	}
	return p
}

// writeProfile writes p to a file in a temp directory and returns its path.
func writeProfile(t testing.TB, name string, p *profile.Profile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}
	return path
}

const ms10 = 10_000_000 // the default go sampling period, in ns

// prof builds a profile the way pprofread reads it.
func prof(periodNs int64, stacks ...st) *pprofread.Profile {
	p, err := pprofread.FromProfile(buildProfile(periodNs, stacks...), pprofread.Options{})
	if err != nil {
		panic(err)
	}
	return p
}
