package pprofcheck

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

var dir = filepath.Join("..", "..", "testdata", "profiles")

func TestSampleProfiles(t *testing.T) {
	for _, f := range []string{"dedup_base.pprof", "dedup_scaled.pprof"} {
		info, err := Check(filepath.Join(dir, f), true)
		if err != nil {
			t.Fatal(err)
		}
		if !info.CPU || info.Samples == 0 || info.PeriodNs == 0 {
			t.Errorf("%s: %+v", f, info)
		}
	}
}

func TestUnusable(t *testing.T) {
	tmp := t.TempDir()
	empty := filepath.Join(tmp, "empty.pprof")
	os.WriteFile(empty, nil, 0o644)

	// A CPU profile with no sample, and a heap profile.
	write := func(name string, p *profile.Profile) string {
		var buf bytes.Buffer
		if err := p.Write(&buf); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(tmp, name)
		os.WriteFile(path, buf.Bytes(), 0o644)
		return path
	}
	cpu := write("nosamples.pprof", &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     10000000,
	})
	heap := write("heap.pprof", &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		PeriodType: &profile.ValueType{Type: "space", Unit: "bytes"},
		Sample:     []*profile.Sample{{Value: []int64{100}}},
	})

	for _, path := range []string{empty, filepath.Join(tmp, "missing.pprof"), filepath.Join(dir, "junk.pprof"), cpu, heap} {
		_, err := Check(path, true)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%s: got %v", filepath.Base(path), err)
		}
	}
	// Without needSamples an empty CPU profile is fine (the preflight dry run).
	if info, err := Check(cpu, false); err != nil || info.Samples != 0 {
		t.Errorf("no samples, not needed: %+v %v", info, err)
	}
}
