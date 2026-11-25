package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The example of section 3.1 must read into Run and write back with the same keys.
const runExample = `{
  "tool_version": "0.1.0",
  "status": "ok",
  "error_code": null,
  "mode": "pprof",
  "go_version": "go1.26",
  "os": "linux/amd64",
  "duration_ms": 41230,
  "base_pprof": "out/base.pprof",
  "scaled_pprof": "out/scaled.pprof",
  "paths_method": "paper"
}`

func keys(t *testing.T, data []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func TestRunMatchesContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), RunFile)
	os.WriteFile(path, []byte(runExample), 0o644)
	r, err := ReadRun(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusOK || r.ErrorCode != nil || *r.GoVersion != "go1.26" || *r.BasePprof != "out/base.pprof" || r.DurationMs != 41230 {
		t.Errorf("read %+v", r)
	}
	out := filepath.Join(t.TempDir(), RunFile)
	if err := WriteJSON(out, r); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if got, want := keys(t, data), keys(t, []byte(runExample)); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v\nwant %v", got, want)
	}
}

func TestFailAndCodes(t *testing.T) {
	r := &Run{Status: StatusOK}
	r.Fail(Timeout)
	data, _ := json.Marshal(r)
	var back Run
	json.Unmarshal(data, &back)
	if back.Status != StatusError || back.ErrorCode == nil || *back.ErrorCode != Timeout {
		t.Errorf("got %s", data)
	}
	if len(Codes) != 9 || !NoBench.Valid() || Code("NOPE").Valid() {
		t.Error("codes")
	}
	path := filepath.Join(t.TempDir(), RunFile)
	os.WriteFile(path, []byte(`{"status":"error","error_code":"NOPE"}`), 0o644)
	if _, err := ReadRun(path); err == nil {
		t.Error("unknown code accepted")
	}
}

func TestPaths(t *testing.T) {
	wd, _ := os.Getwd()
	if got := PathFor(filepath.Join(wd, "out", "base.pprof")); got != "out/base.pprof" {
		t.Errorf("inside: %q", got)
	}
	outside := filepath.Join(filepath.Dir(wd), "elsewhere.pprof")
	if got := PathFor(outside); got != filepath.ToSlash(outside) {
		t.Errorf("outside: %q", got)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	os.WriteFile(filepath.Join(dir, "out", "base.pprof"), []byte("x"), 0o644)
	runPath := filepath.Join(dir, "out", RunFile)
	if got := Resolve(runPath, "out/base.pprof"); got != filepath.Join(dir, "out", "base.pprof") {
		t.Errorf("resolve via parent: %q", got)
	}
	if got := Resolve(runPath, "base.pprof"); got != filepath.Join(dir, "out", "base.pprof") {
		t.Errorf("resolve via run.json dir: %q", got)
	}
}
