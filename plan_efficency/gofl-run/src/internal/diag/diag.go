// Package diag builds diagnostics.json (section 3.4 of data contract v0):
// the one file the team leader can always send, because it holds counts and
// codes only, never a function or file name.
//
// It is built from the files in the output directory: run.json, the two
// profiles it points to, and hotspots.json and critical_paths.json when the
// later stages wrote them. gofl profile writes it right away, so it exists
// even when the run fails; gofl diagnostics rebuilds it after the later
// stages to add their counts.
package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gofl/internal/contract"
	"gofl/internal/pprofcheck"
)

// Options override what the files say.
type Options struct {
	// ErrorCode is the code of a later stage that failed, for example
	// NO_POSITIVE_DELTA or ENTRY_NOT_COVERING. "" keeps run.json's.
	ErrorCode contract.Code
	// DurationMs is the time of the whole pipeline. 0 keeps run.json's.
	DurationMs int64
}

// Build reads out/run.json and the files next to it.
func Build(out string, o Options) (*contract.Diagnostics, []string, error) {
	runPath := filepath.Join(out, contract.RunFile)
	run, err := contract.ReadRun(runPath)
	if err != nil {
		return nil, nil, err
	}
	d, warnings := FromRun(run, runPath, o)
	return d, warnings, nil
}

// Write builds diagnostics.json and writes it into out.
func Write(out string, o Options) (*contract.Diagnostics, []string, error) {
	d, warnings, err := Build(out, o)
	if err != nil {
		return nil, nil, err
	}
	return d, warnings, contract.WriteJSON(filepath.Join(out, contract.DiagnosticsFile), d)
}

// FromRun builds the diagnostics of run, whose run.json is (or will be) at
// runPath. Warnings say which files could not be read; they may name files
// and are meant for the local terminal only.
func FromRun(run *contract.Run, runPath string, o Options) (*contract.Diagnostics, []string) {
	d := &contract.Diagnostics{
		ToolVersion: contract.ToolVersion,
		Status:      run.Status,
		ErrorCode:   run.ErrorCode,
		Mode:        run.Mode,
		GoVersion:   run.GoVersion,
		OS:          run.OS,
		DurationMs:  run.DurationMs,
	}
	var warnings []string
	d.BaselineSamplesTotal = samples(runPath, run.BasePprof, &warnings)
	d.ScaledSamplesTotal = samples(runPath, run.ScaledPprof, &warnings)
	dir := filepath.Dir(runPath)
	d.HotspotCount = count(filepath.Join(dir, contract.HotspotsFile), &warnings)
	d.PathCount = count(filepath.Join(dir, contract.PathsFile), &warnings)

	if o.DurationMs > 0 {
		d.DurationMs = o.DurationMs
	}
	switch {
	case o.ErrorCode != "":
		c := o.ErrorCode
		d.Status, d.ErrorCode = contract.StatusError, &c
	case d.Status == contract.StatusOK && d.HotspotCount != nil && *d.HotspotCount == 0:
		// Compute writes an empty list when no function grew.
		c := contract.NoPositiveDelta
		d.Status, d.ErrorCode = contract.StatusError, &c
	}
	return d, warnings
}

func samples(runPath string, p *string, warnings *[]string) *int64 {
	if p == nil || *p == "" {
		return nil
	}
	info, err := pprofcheck.Check(contract.Resolve(runPath, *p), false)
	if info == nil {
		*warnings = append(*warnings, err.Error())
		return nil
	}
	return contract.Ptr(info.Samples)
}

// count returns the number of entries of a JSON array file, nil if the file
// does not exist or is not an array.
func count(path string, warnings *[]string) *int {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		*warnings = append(*warnings, err.Error())
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: not a JSON array (%v)", path, err))
		return nil
	}
	return contract.Ptr(len(list))
}
