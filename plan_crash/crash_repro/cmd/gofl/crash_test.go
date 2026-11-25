package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/finding"
	"gofl/internal/llm"
)

const sampleDir = "../../testdata/goflsample"

func stack(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../internal/llm/testdata/stacks", id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runCLI(t *testing.T, stdin string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(append([]string{"crash"}, args...), strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func TestCrashStdinJSON(t *testing.T) {
	code, out, errOut := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var rep finding.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rep.Command != "crash" || rep.Version != finding.SchemaVersion || len(rep.Findings) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	top := rep.Findings[0].Location
	if top.File != "crash/crash.go" || top.Line != 21 || !strings.HasSuffix(top.Func, ".Nth") {
		t.Errorf("top = %+v", top)
	}
}

func TestCrashFormatsAndOutputFile(t *testing.T) {
	for _, f := range []string{"md", "json", "sarif"} {
		code, out, errOut := runCLI(t, stack(t, "C3"), "-dir", sampleDir, "-format", f)
		if code != 0 || out == "" {
			t.Errorf("%s: exit %d out=%q err=%s", f, code, out, errOut)
		}
	}
	p := filepath.Join(t.TempDir(), "r.sarif")
	code, out, _ := runCLI(t, stack(t, "C3"), "-dir", sampleDir, "-format", "sarif", "-o", p)
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q (must be empty with -o)", code, out)
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), `"version": "2.1.0"`) {
		t.Errorf("sarif file: %v %.80s", err, data)
	}
}

func TestCrashLogFileAndNoCrash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	log := "2026/01/02 03:04:05 starting\n2026/01/02 03:04:06 " + strings.ReplaceAll(stack(t, "C2"), "panic:", "panic:") + "\n"
	if err := os.WriteFile(p, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "", "-dir", sampleDir, "-log", p, "-format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "crash/crash.go") {
		t.Errorf("log not read:\n%s", out)
	}
	code, out, _ = runCLI(t, "nothing to see\n", "-format", "json")
	if code != 0 || !strings.Contains(out, "no panic or fatal error found") {
		t.Errorf("no-crash input: exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "", "-log", filepath.Join(t.TempDir(), "missing.log")); code != 1 {
		t.Errorf("missing log file: exit %d, want 1", code)
	}
}

func TestCrashTestJSONInput(t *testing.T) {
	lines := []string{`{"Action":"run","Package":"goflsample/crash","Test":"TestNth"}`}
	for _, l := range strings.SplitAfter(stack(t, "C2"), "\n") {
		if l == "" {
			continue
		}
		b, _ := json.Marshal(map[string]string{"Action": "output", "Package": "goflsample/crash", "Test": "TestNth", "Output": l})
		lines = append(lines, string(b))
	}
	code, out, errOut := runCLI(t, strings.Join(lines, "\n")+"\n", "-dir", sampleDir, "-format", "json")
	if code != 0 || !strings.Contains(out, "crash/crash.go") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
}

func TestCrashRunsTests(t *testing.T) {
	code, out, errOut := runCLI(t, "", "-dir", sampleDir, "-test", "./crash", "-run", `^TestNth$`, "-format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `"file": "crash/crash.go"`) {
		t.Errorf("no finding from the live run:\n%s", out)
	}
	// Unrunnable: exit 1 and the reason is in the report.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package x\nimport \"testing\"\nfunc TestA(t *testing.T){ nope() }\n"), 0o644)
	code, out, _ = runCLI(t, "", "-dir", dir, "-test", "./...", "-format", "json")
	if code != 1 || !strings.Contains(out, "tests could not run (build)") {
		t.Errorf("unrunnable: exit %d\n%s", code, out)
	}
}

func TestCrashUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-llm", "gpt"},
		{"-profile", "nope"},
		{"-test", "./...", "-log", "x.log"},
	} {
		if code, _, _ := runCLI(t, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestCrashLLM(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []string{
		`{"locations":[{"file":"crash/crash.go","function":"crash.Nth","line":21}],"explanation":"index not checked"}`}}
	old := newProvider
	newProvider = func(string) (llm.Provider, error) { return fake, nil }
	defer func() { newProvider = old }()

	code, out, errOut := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-llm", "local", "-format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if fake.Calls() != llm.DefaultRuns {
		t.Errorf("%d requests", fake.Calls())
	}
	if !strings.Contains(errOut, "would send this text") {
		t.Error("the prompt was not shown before sending")
	}
	if !strings.Contains(out, `"kind": "llm"`) {
		t.Errorf("no llm evidence:\n%s", out)
	}

	// dry run: shown, not sent
	fake2 := &llm.FakeProvider{Responses: []string{"{}"}}
	newProvider = func(string) (llm.Provider, error) { return fake2, nil }
	code, out, errOut = runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-llm", "api", "-llm-dry-run", "-format", "json")
	if code != 0 || fake2.Calls() != 0 || !strings.Contains(errOut, "Suspicious-Code-Part") {
		t.Errorf("dry run: exit %d calls %d", code, fake2.Calls())
	}

	// provider cannot be built (no key): ranking stays, note explains
	newProvider = func(string) (llm.Provider, error) { return nil, errors.New("llm: mode api needs GOFL_LLM_KEY") }
	code, out, _ = runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-llm", "api", "-format", "json")
	if code != 0 || !strings.Contains(out, "LLM triage was not applied") || !strings.Contains(out, "crash/crash.go") {
		t.Errorf("provider error: exit %d\n%s", code, out)
	}
}

func TestCrashLLMOffNeverTouchesProvider(t *testing.T) {
	old := newProvider
	newProvider = func(string) (llm.Provider, error) { t.Fatal("provider built with -llm off"); return nil, nil }
	defer func() { newProvider = old }()
	if code, _, e := runCLI(t, stack(t, "C2"), "-dir", sampleDir); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
}

func TestCrashAgentPrompt(t *testing.T) {
	code, e1, errOut := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-agent-prompt", "-repro", "go test ./crash -run TestNth")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"go test ./crash -run TestNth", "Where gofl ranks the suspicion", "crash/crash.go"} {
		if !strings.Contains(e1, want) {
			t.Errorf("E1 prompt lacks %q:\n%s", want, e1)
		}
	}
	code, e0, _ := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-agent-prompt", "-agent-mode", "e0")
	if code != 0 || strings.Contains(e0, "Where gofl ranks the suspicion") || !strings.Contains(e0, "no command is known") {
		t.Errorf("E0 prompt wrong (exit %d):\n%s", code, e0)
	}
	if code, _, _ := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-agent-prompt", "-agent-mode", "e9"); code != 2 {
		t.Errorf("bad -agent-mode: exit %d, want 2", code)
	}
	if code, _, _ := runCLI(t, "no crash here\n", "-dir", sampleDir, "-agent-prompt"); code != 2 {
		t.Errorf("no crash: exit %d, want 2", code)
	}
}

func TestCrashAgentPromptJSON(t *testing.T) {
	code, out, errOut := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-agent-prompt", "-format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var doc struct {
		Version int    `json:"version"`
		Mode    string `json:"mode"`
		Prompt  string `json:"prompt"`
		Files   []struct {
			File  string `json:"file"`
			Funcs []struct {
				Func string `json:"func"`
				Line int    `json:"line"`
			} `json:"funcs"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Version != finding.SchemaVersion || doc.Mode != "E1" || !strings.Contains(doc.Prompt, "crash/crash.go") {
		t.Errorf("doc = %+v", doc)
	}
	if len(doc.Files) == 0 || doc.Files[0].File != "crash/crash.go" || len(doc.Files[0].Funcs) == 0 || doc.Files[0].Funcs[0].Line != 21 {
		t.Errorf("files = %+v", doc.Files)
	}
	_, e0, _ := runCLI(t, stack(t, "C2"), "-dir", sampleDir, "-agent-prompt", "-format", "json", "-agent-mode", "e0")
	if !strings.Contains(e0, `"files"`) || !strings.Contains(e0, `"mode": "E0"`) {
		t.Errorf("E0 JSON must still list the suspect files:\n%s", e0)
	}
}
