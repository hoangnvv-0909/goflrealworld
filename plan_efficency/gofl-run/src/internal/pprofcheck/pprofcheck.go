// Package pprofcheck checks that a file is a usable CPU profile and counts
// its samples. It reads with github.com/google/pprof/profile, the same
// reader as the pprofread package.
package pprofcheck

import (
	"fmt"
	"os"

	"github.com/google/pprof/profile"
)

// Info summarises a profile.
type Info struct {
	Samples  int64 // number of samples (the samples/count value, summed)
	PeriodNs int64 // sampling period in nanoseconds, 0 if unknown
	CPU      bool  // it is a CPU profile
}

// Error says why a profile cannot be used. It always maps to NO_PROFILE.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string { return e.Path + ": " + e.Reason }

// Check reads path. With needSamples, a profile without any sample is an
// error: there is nothing to compare.
func Check(path string, needSamples bool) (*Info, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &Error{path, "no such file"}
		}
		return nil, &Error{path, err.Error()}
	}
	defer f.Close()
	st, err := f.Stat()
	if err == nil && st.Size() == 0 {
		return nil, &Error{path, "empty file"}
	}
	p, err := profile.Parse(f)
	if err != nil {
		return nil, &Error{path, fmt.Sprintf("not a pprof profile (%v)", err)}
	}
	info := &Info{}
	idx := -1
	for i, st := range p.SampleType {
		switch st.Type {
		case "samples":
			idx = i
		case "cpu":
			info.CPU = true
		}
	}
	if p.PeriodType != nil && p.PeriodType.Type == "cpu" {
		info.CPU = true
		if p.PeriodType.Unit == "nanoseconds" {
			info.PeriodNs = p.Period
		}
	}
	for _, s := range p.Sample {
		if idx >= 0 && idx < len(s.Value) {
			info.Samples += s.Value[idx]
		} else {
			info.Samples++
		}
	}
	if !info.CPU {
		return info, &Error{path, "not a CPU profile (use -cpuprofile)"}
	}
	if needSamples && info.Samples == 0 {
		return info, &Error{path, "the profile has no samples: the run was too short; raise benchtime"}
	}
	return info, nil
}
