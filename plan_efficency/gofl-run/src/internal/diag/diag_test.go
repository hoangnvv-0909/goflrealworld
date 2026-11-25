package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gofl/internal/contract"
)

var testdata = filepath.Join("..", "..", "testdata")

// setup writes run.json into a fresh output directory, pointing at the
// sample profiles, and copies the given contract files next to it.
func setup(t *testing.T, run *contract.Run, files ...string) string {
	t.Helper()
	out := t.TempDir()
	if run == nil {
		base, _ := filepath.Abs(filepath.Join(testdata, "profiles", "dedup_base.pprof"))
		scaled, _ := filepath.Abs(filepath.Join(testdata, "profiles", "dedup_scaled.pprof"))
		run = &contract.Run{
			ToolVersion: contract.ToolVersion, Status: contract.StatusOK, Mode: contract.ModeBench,
			GoVersion: contract.Ptr("go1.26.1"), OS: "linux/amd64", DurationMs: 41230,
			BasePprof: &base, ScaledPprof: &scaled, PathsMethod: contract.PathsPaper,
		}
	}
	if err := contract.WriteJSON(filepath.Join(out, contract.RunFile), run); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(testdata, "contract", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(out, f), data, 0o644)
	}
	return out
}

func TestFullPipeline(t *testing.T) {
	out := setup(t, nil, contract.HotspotsFile, contract.PathsFile)
	d, warnings, err := Write(out, Options{DurationMs: 50000})
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	if d.Status != contract.StatusOK || d.ErrorCode != nil || d.Mode != "bench" || *d.GoVersion != "go1.26.1" || d.OS != "linux/amd64" || d.DurationMs != 50000 {
		t.Errorf("header: %+v", d)
	}
	if d.BaselineSamplesTotal == nil || *d.BaselineSamplesTotal == 0 || d.ScaledSamplesTotal == nil || *d.ScaledSamplesTotal == 0 {
		t.Errorf("samples: %v %v", d.BaselineSamplesTotal, d.ScaledSamplesTotal)
	}
	if d.HotspotCount == nil || *d.HotspotCount != 2 || d.PathCount == nil || *d.PathCount != 3 {
		t.Errorf("counts: %v %v", d.HotspotCount, d.PathCount)
	}
	if _, err := os.Stat(filepath.Join(out, contract.DiagnosticsFile)); err != nil {
		t.Error(err)
	}
}

// TestNoCode checks the promise of section 3.4: exactly the contract keys,
// and no function, file or package name anywhere.
func TestNoCode(t *testing.T) {
	out := setup(t, nil, contract.HotspotsFile, contract.PathsFile)
	if _, _, err := Write(out, Options{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, contract.DiagnosticsFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"baseline_samples_total", "duration_ms", "error_code", "go_version", "hotspot_count", "mode", "os", "path_count", "scaled_samples_total", "status", "tool_version"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys %v\nwant %v", keys, want)
	}
	for _, bad := range []string{"report", "BuildRows", "internal", "pprof", "dedup", ".go", "example.com", "testdata"} {
		if strings.Contains(string(data), bad) {
			t.Errorf("diagnostics.json contains %q:\n%s", bad, data)
		}
	}
}

func TestEmptyHotspots(t *testing.T) {
	out := setup(t, nil)
	os.WriteFile(filepath.Join(out, contract.HotspotsFile), []byte("[]\n"), 0o644)
	d, _, err := Build(out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != contract.StatusError || d.ErrorCode == nil || *d.ErrorCode != contract.NoPositiveDelta || *d.HotspotCount != 0 || d.PathCount != nil {
		t.Errorf("got %+v", d)
	}
}

func TestOverrideCode(t *testing.T) {
	out := setup(t, nil, contract.HotspotsFile)
	d, _, err := Build(out, Options{ErrorCode: contract.EntryNotCovering})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != contract.StatusError || *d.ErrorCode != contract.EntryNotCovering || *d.HotspotCount != 2 || d.PathCount != nil {
		t.Errorf("got %+v", d)
	}
}

func TestFailedRun(t *testing.T) {
	run := &contract.Run{ToolVersion: contract.ToolVersion, Mode: contract.ModeBench, OS: "windows/amd64", PathsMethod: "paper"}
	run.Fail(contract.NoGo)
	out := setup(t, run)
	d, warnings, err := Build(out, Options{})
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	if d.Status != contract.StatusError || *d.ErrorCode != contract.NoGo || d.GoVersion != nil ||
		d.BaselineSamplesTotal != nil || d.ScaledSamplesTotal != nil || d.HotspotCount != nil || d.PathCount != nil {
		t.Errorf("got %+v", d)
	}
}

func TestUnreadableFiles(t *testing.T) {
	junk, _ := filepath.Abs(filepath.Join(testdata, "profiles", "junk.pprof"))
	run := &contract.Run{ToolVersion: contract.ToolVersion, Status: contract.StatusOK, Mode: contract.ModePprof, OS: "linux/amd64", BasePprof: &junk, ScaledPprof: &junk, PathsMethod: "paper"}
	out := setup(t, run)
	os.WriteFile(filepath.Join(out, contract.PathsFile), []byte(`{"not": "a list"}`), 0o644)
	d, warnings, err := Build(out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.BaselineSamplesTotal != nil || d.PathCount != nil || len(warnings) != 3 {
		t.Errorf("got %+v, warnings %q", d, warnings)
	}
}

func TestMissingRun(t *testing.T) {
	if _, _, err := Build(t.TempDir(), Options{}); err == nil {
		t.Error("no run.json, no error")
	}
}

func TestRelativeProfilePaths(t *testing.T) {
	// run.json stores paths relative to the directory gofl ran in. A reader
	// in another directory finds them next to run.json's directory.
	root := t.TempDir()
	out := filepath.Join(root, "out")
	os.MkdirAll(out, 0o755)
	data, _ := os.ReadFile(filepath.Join(testdata, "profiles", "dedup_base.pprof"))
	os.WriteFile(filepath.Join(out, "base.pprof"), data, 0o644)
	rel := "out/base.pprof"
	run := &contract.Run{ToolVersion: contract.ToolVersion, Status: contract.StatusOK, Mode: contract.ModeBench, OS: "linux/amd64", BasePprof: &rel, PathsMethod: "paper"}
	contract.WriteJSON(filepath.Join(out, contract.RunFile), run)
	d, warnings, err := Build(out, Options{})
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	if d.BaselineSamplesTotal == nil || *d.BaselineSamplesTotal == 0 {
		t.Errorf("relative path not resolved: %+v", d)
	}
}
