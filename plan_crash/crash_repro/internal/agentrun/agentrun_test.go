package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const src = `package p

type Registry struct{ m map[string]int }

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Inc(k string) {
	r.m[k]++
}

func Other() int { return 1 }
`

func TestChangesNamesTheEditedFunction(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	before, after := t.TempDir(), t.TempDir()
	write(t, before, "p.go", src)
	write(t, after, "p.go", strings.Replace(src, "return &Registry{}", "return &Registry{m: map[string]int{}}", 1))
	_, funcs, tests := changes(before, after)
	if !reflect.DeepEqual(funcs, []string{"NewRegistry"}) || tests {
		t.Fatalf("funcs=%v tests=%v", funcs, tests)
	}
	if containsFunc(funcs, "Registry.Inc") {
		t.Error("Inc was not edited")
	}
	if !containsFunc(funcs, "NewRegistry") {
		t.Error("NewRegistry was edited")
	}
}

func TestChangesMethodAndTestFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	before, after := t.TempDir(), t.TempDir()
	write(t, before, "p.go", src)
	write(t, before, "p_test.go", "package p\n")
	write(t, after, "p.go", strings.Replace(src, "r.m[k]++", "if r.m == nil {\n\t\tr.m = map[string]int{}\n\t}\n\tr.m[k]++", 1))
	write(t, after, "p_test.go", "package p\n\n// edited\n")
	write(t, after, "gofl-localize.json", "{}") // never counts as a change
	_, funcs, tests := changes(before, after)
	if !reflect.DeepEqual(funcs, []string{"Registry.Inc"}) || !tests {
		t.Fatalf("funcs=%v tests=%v", funcs, tests)
	}
}

func TestHunkLines(t *testing.T) {
	d := "@@ -3,0 +4,2 @@\n+a\n+b\n@@ -9 +11 @@\n-x\n+y\n@@ -20,2 +22,0 @@\n-p\n-q\n"
	if got := hunkLines(d); !reflect.DeepEqual(got, []int{4, 5, 11, 22, 23}) {
		t.Fatalf("got %v", got)
	}
}

func TestReadLocalize(t *testing.T) {
	ws := t.TempDir()
	if got := readLocalize(ws); got != nil {
		t.Fatalf("missing file gave %v", got)
	}
	write(t, ws, "gofl-localize.json", `{"locations":[{"file":"crash\\hard.go","function":"NewSession","line":57},{"file":"a.go","func":"F"}],"explanation":"x"}`)
	got := readLocalize(ws)
	if len(got) != 2 || got[0].File != "crash/hard.go" || got[0].Func != "NewSession" || got[1].Func != "F" {
		t.Fatalf("got %+v", got)
	}
	write(t, ws, "gofl-localize.json", `not json`)
	if got := readLocalize(ws); got != nil {
		t.Fatalf("bad json gave %v", got)
	}
}

func TestParseClaudeJSON(t *testing.T) {
	var r Result
	parseClaudeJSON([]byte(`{"is_error":false,"result":"done","total_cost_usd":0.42,"num_turns":7,
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20},
		"modelUsage":{"claude-x":{}}}`), &r)
	if r.CostUSD != 0.42 || r.Turns != 7 || r.InputTokens != 130 || r.OutputTokens != 5 || r.Model != "claude-x" {
		t.Fatalf("got %+v", r)
	}
	var bad Result
	parseClaudeJSON([]byte("garbage"), &bad)
	if bad.CostUSD != 0 {
		t.Fatal("garbage must leave zero values")
	}
}
