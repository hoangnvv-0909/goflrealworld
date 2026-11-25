package diffprof

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gofl/internal/contract"
	"gofl/internal/pprofread"
)

// outDir writes run.json for two profiles and returns the output directory.
func outDir(t *testing.T, base, scaled *pprofread.Profile, mut func(*contract.Run)) string {
	t.Helper()
	dir := t.TempDir()
	bp, sp := filepath.Join(dir, "base.pprof"), filepath.Join(dir, "scaled.pprof")
	copyProfile(t, bp, base)
	copyProfile(t, sp, scaled)
	run := &contract.Run{ToolVersion: contract.ToolVersion, Status: contract.StatusOK, Mode: contract.ModePprof, OS: "linux/amd64",
		BasePprof: &bp, ScaledPprof: &sp, PathsMethod: contract.PathsPaper}
	if mut != nil {
		mut(run)
	}
	if err := contract.WriteJSON(filepath.Join(dir, contract.RunFile), run); err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyProfile writes a *pprofread.Profile back as a pprof file, through the helper that
// builds raw profiles from stacks.
func copyProfile(t *testing.T, path string, p *pprofread.Profile) {
	t.Helper()
	var stacks []st
	for _, s := range p.Stacks {
		var fr []string
		for _, f := range s.Frames {
			fr = append(fr, f.Name+"@"+f.File)
		}
		stacks = append(stacks, st{frames: fr, n: s.Samples})
	}
	src := writeProfile(t, "tmp.pprof", buildProfile(periodNs(p), stacks...))
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComputeWritesHotspotsAndIsDeterministic(t *testing.T) {
	base, scaled := coreProfiles()
	out := outDir(t, base, scaled, nil)
	os.WriteFile(filepath.Join(out, contract.HotspotsFile), []byte("stale"), 0o644)
	h, err := Compute(out, Options{Dir: "/proj"})
	if err != nil || len(h) != 3 {
		t.Fatalf("%v %v", h, err)
	}
	first, err := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compute(out, Options{Dir: "/proj"}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	if string(first) != string(second) {
		t.Error("two runs wrote different bytes")
	}
	var raw []map[string]any
	if err := json.Unmarshal(first, &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"rank", "function", "file", "baseline_self_seconds", "scaled_self_seconds", "delta_self_seconds", "ndc",
		"baseline_samples", "scaled_samples", "baseline_occurrences", "scaled_occurrences"}
	if len(raw[0]) != len(want) {
		t.Errorf("keys %v", raw[0])
	}
	for _, k := range want {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
}

func TestComputeWithBenchAddsFourFields(t *testing.T) {
	base, scaled, bench := perOpProfiles()
	out := outDir(t, base, scaled, func(r *contract.Run) { r.Mode = contract.ModeBench; r.Bench = bench })
	if _, err := Compute(out, Options{Dir: "/proj"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	var raw []map[string]any
	json.Unmarshal(data, &raw)
	for _, k := range []string{"baseline_per_op_seconds", "scaled_per_op_seconds", "delta_per_op_seconds", "ndc_per_op"} {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if len(raw[0]) != 15 {
		t.Errorf("%d keys, want 11 + 4", len(raw[0]))
	}
}

func TestComputeRefusesFailedRun(t *testing.T) {
	base, scaled := coreProfiles()
	out := outDir(t, base, scaled, func(r *contract.Run) { r.Fail(contract.BuildFail) })
	os.WriteFile(filepath.Join(out, contract.HotspotsFile), []byte("stale"), 0o644)
	_, err := Compute(out, Options{})
	var pe *PriorError
	if !errors.As(err, &pe) || pe.Code != contract.BuildFail || !strings.Contains(err.Error(), "BUILD_FAIL") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, contract.HotspotsFile)); err == nil {
		t.Error("stale hotspots.json left behind")
	}
}

func TestComputeNoPositiveDeltaWritesEmptyList(t *testing.T) {
	p := prof(ms10, stk(5, "main", "f"))
	out := outDir(t, p, p, nil)
	_, err := Compute(out, Options{})
	var e *Error
	if !errors.As(err, &e) || e.Code != contract.NoPositiveDelta {
		t.Fatalf("%v", err)
	}
	data, _ := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
	if strings.TrimSpace(string(data)) != "[]" {
		t.Errorf("hotspots.json = %q", data)
	}
}

func TestComputePerOpWithoutBenchIsUsageError(t *testing.T) {
	base, scaled := coreProfiles()
	out := outDir(t, base, scaled, nil)
	_, err := Compute(out, Options{RankBy: RankPerOp})
	var u *UsageError
	if !errors.As(err, &u) {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, contract.HotspotsFile)); err == nil {
		t.Error("hotspots.json written")
	}
}

func TestComputeBadProfileIsNoProfile(t *testing.T) {
	base, scaled := coreProfiles()
	out := outDir(t, base, scaled, nil)
	os.WriteFile(filepath.Join(out, "scaled.pprof"), []byte("junk"), 0o644)
	_, err := Compute(out, Options{})
	var e *Error
	if !errors.As(err, &e) || e.Code != contract.NoProfile {
		t.Errorf("%v", err)
	}
}

// --- real data of the run package -----------------------------------------

func realProfiles(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "profiles")
	return filepath.Join(dir, "dedup_base.pprof"), filepath.Join(dir, "dedup_scaled.pprof")
}

func readReal(t *testing.T) (*pprofread.Profile, *pprofread.Profile) {
	t.Helper()
	b, s := realProfiles(t)
	base, err := pprofread.Load(b, pprofread.Options{})
	if err != nil {
		t.Fatal(err)
	}
	scaled, err := pprofread.Load(s, pprofread.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return base, scaled
}

// The profiles of testdata/sample/slow, whose Dedup is quadratic: the growth
// must show up in Dedup or in what it calls (contains, inlined into it).
func TestRealDedupProfiles(t *testing.T) {
	base, scaled := readReal(t)
	h, err := Rank(base, scaled, Options{Dir: "/nonexistent-project-root"})
	if err != nil {
		t.Fatal(err)
	}
	top := h[0]
	if !strings.HasPrefix(top.Function, "sample/slow.") || !(strings.HasSuffix(top.Function, ".Dedup") || strings.HasSuffix(top.Function, ".contains")) {
		t.Errorf("top hotspot is %s, want Dedup or a function it calls", top.Function)
	}
	if strings.Contains(top.File, `\`) || !strings.HasSuffix(top.File, "slow.go") {
		t.Errorf("file %q", top.File)
	}
	for _, x := range h {
		if strings.HasPrefix(x.Function, "runtime.") || strings.HasPrefix(x.Function, "testing.") {
			t.Errorf("noise in the ranking: %s", x.Function)
		}
		if x.DeltaSelfSeconds <= 0 {
			t.Errorf("%s has delta %v", x.Function, x.DeltaSelfSeconds)
		}
	}
	t.Logf("top: %s %v -> %v s", top.Function, top.BaselineSelfSeconds, top.ScaledSelfSeconds)
}

// Cross-check with the standard tool: for the same two profiles, go tool pprof
// -top -base gives the flat difference per function. Apart from runtime.* and
// testing.* and the functions that did not grow, the values must agree.
func TestAgainstGoToolPprof(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not found")
	}
	b, s := realProfiles(t)
	cmd := exec.Command(goBin, "tool", "pprof", "-top", "-nodecount=40", "-base", b, s)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go tool pprof failed: %v", err)
	}
	type row struct {
		name string
		flat time.Duration
	}
	var rows []row
	started := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && f[0] == "flat" && f[1] == "flat%" {
			started = true
			continue
		}
		if !started || len(f) < 6 {
			continue
		}
		d, err := time.ParseDuration(f[0])
		if err != nil {
			continue
		}
		name := strings.Join(f[5:], " ")
		name = strings.TrimSuffix(name, " (inline)")
		rows = append(rows, row{name, d})
	}
	if len(rows) == 0 {
		t.Fatalf("could not parse pprof output:\n%s", out)
	}
	base, scaled := readReal(t)
	h, err := Rank(base, scaled, Options{TopK: 1000, Dir: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	ours := map[string]float64{}
	for _, x := range h {
		ours[x.Function] = x.DeltaSelfSeconds
	}
	var firstPprof string
	compared := 0
	for _, r := range rows {
		if r.flat <= 0 || strings.HasPrefix(r.name, "runtime.") || strings.HasPrefix(r.name, "testing.") {
			continue
		}
		if firstPprof == "" {
			firstPprof = r.name
		}
		got, ok := ours[r.name]
		want := r.flat.Seconds()
		if !ok || got < want-1e-6 || got > want+1e-6 {
			t.Errorf("%s: pprof says +%v s, we say %v (found %v)", r.name, want, got, ok)
		}
		compared++
	}
	if compared == 0 {
		t.Fatal("no row to compare")
	}
	if h[0].Function != firstPprof {
		t.Errorf("top function: ours %s, pprof %s", h[0].Function, firstPprof)
	}
}
