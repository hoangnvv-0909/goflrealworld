package crashflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/llm"
	"gofl/internal/logprofile"
)

const (
	sampleDir = "../../testdata/goflsample"
	realDir   = "../../testdata/goflrealworld"
)

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func analyzeFile(t *testing.T, path, root string) Analysis {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, _ := logprofile.Get("default")
	a, err := Analyze(p, f, Options{Root: abs(t, root), ModulePath: ModulePath(abs(t, root)), Source: filepath.Base(path)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestModulePath(t *testing.T) {
	if got := ModulePath(abs(t, sampleDir)); got != "goflsample" {
		t.Errorf("module = %q", got)
	}
	if got := ModulePath(t.TempDir()); got != "" {
		t.Errorf("no go.mod: %q", got)
	}
}

// Crash point of each real-world stack file (from bugs.json "crash_point", or
// the fix location when the two coincide). The top finding must be there.
func TestAnalyzeRealWorldStacks(t *testing.T) {
	cases := map[string]string{
		"W1_CurrentUserDeleted":           "internal/handler/user.go",
		"W5_DeleteOwnComment":             "internal/service/comment.go",
		"W7_DeleteCommentBadID":           "internal/utils/must.go",
		"W2_CreateArticlePunctuatedTitle": "internal/utils/slug.go",
		"W6_CommentWithLoneAt":            "internal/service/comment.go",
	}
	for name, want := range cases {
		a := analyzeFile(t, filepath.Join(realDir, "stacks", name+".txt"), realDir)
		if len(a.Report.Findings) == 0 {
			t.Errorf("%s: no findings (events=%d bad=%d)", name, len(a.Events), a.Bad)
			continue
		}
		if got := a.Report.Findings[0].Location.File; got != want {
			t.Errorf("%s: top file %q, want %q", name, got, want)
		}
		for _, f := range a.Report.Findings {
			if filepath.IsAbs(f.Location.File) || strings.Contains(f.Location.File, "\\") || strings.HasPrefix(f.Location.File, "<") {
				t.Errorf("%s: file not project-relative: %q", name, f.Location.File)
			}
		}
	}
}

func TestAnalyzeServerLogGroupsMany(t *testing.T) {
	a := analyzeFile(t, filepath.Join(realDir, "stacks", "server_crash.log"), realDir)
	if len(a.Events) < 100 {
		t.Fatalf("only %d events read from the server log", len(a.Events))
	}
	if len(a.Groups) < 3 || len(a.Groups) >= len(a.Events)/4 {
		t.Errorf("%d groups for %d events: grouping looks off", len(a.Groups), len(a.Events))
	}
	total := 0
	for _, g := range a.Groups {
		total += len(g.Events)
	}
	if total != len(a.Events) {
		t.Errorf("groups hold %d events, want %d", total, len(a.Events))
	}
	for i, f := range a.Report.Findings {
		if f.Rank != i+1 {
			t.Fatalf("ranks not consecutive at %d", i)
		}
		if i > 0 && f.Score > a.Report.Findings[i-1].Score+1e-12 {
			t.Fatalf("scores not descending at %d", i)
		}
	}
}

func TestSniffTestJSON(t *testing.T) {
	in := `{"Action":"output","Package":"p","Test":"T","Output":"panic: boom\n"}` + "\n" +
		`{"Action":"output","Package":"p","Test":"T","Output":"goroutine 1 [running]:\n"}` + "\n"
	r, err := SniffTestJSON(strings.NewReader("\n  " + in))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.ReadFrom(r)
	if !strings.Contains(b.String(), "panic: boom") || strings.Contains(b.String(), "Action") {
		t.Errorf("json not unwrapped: %q", b.String())
	}
	r, _ = SniffTestJSON(strings.NewReader("panic: plain\n"))
	b.Reset()
	b.ReadFrom(r)
	if b.String() != "panic: plain\n" {
		t.Errorf("plain text changed: %q", b.String())
	}
	r, _ = SniffTestJSON(strings.NewReader(""))
	b.Reset()
	b.ReadFrom(r)
	if b.Len() != 0 {
		t.Error("empty input")
	}
}

const answerHard = `{"locations":[{"file":"crash/hard.go","function":"crash.NewSession","line":57}],"explanation":"constructor leaves a nil field"}`

func TestApplyLLMDryRunSendsNothing(t *testing.T) {
	a := analyzeFile(t, filepath.Join("../llm/testdata/stacks", "C9.txt"), sampleDir)
	fake := &llm.FakeProvider{Responses: []string{answerHard}}
	var show bytes.Buffer
	notes, out, err := ApplyLLM(context.Background(), &a.Report, a.Groups[0], abs(t, sampleDir), "api", true,
		func(string) (llm.Provider, error) { return fake, nil }, &show)
	if err != nil || out != nil || fake.Calls() != 0 {
		t.Fatalf("dry run sent something: err=%v out=%v calls=%d", err, out, fake.Calls())
	}
	if !strings.Contains(show.String(), "Suspicious-Code-Part") || !strings.Contains(show.String(), "crash/hard.go") {
		t.Errorf("prompt not shown:\n%s", show.String())
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "dry run") {
		t.Errorf("notes = %v", notes)
	}
}

func TestApplyLLMVotesFirst(t *testing.T) {
	a := analyzeFile(t, filepath.Join("../llm/testdata/stacks", "C9.txt"), sampleDir)
	before := a.Report.Findings[0].Location
	fake := &llm.FakeProvider{Responses: []string{answerHard}}
	notes, out, err := ApplyLLM(context.Background(), &a.Report, a.Groups[0], abs(t, sampleDir), "local", false,
		func(string) (llm.Provider, error) { return fake, nil }, nil)
	if err != nil || out == nil {
		t.Fatalf("err=%v out=%v notes=%v", err, out, notes)
	}
	if fake.Calls() != llm.DefaultRuns {
		t.Errorf("%d requests, want %d", fake.Calls(), llm.DefaultRuns)
	}
	top := a.Report.Findings[0]
	if top.Location.File != "crash/hard.go" || llm.NormFunc(top.Location.Func) != "NewSession" {
		t.Fatalf("model's location not first: %+v (was %+v)", top.Location, before)
	}
	hasLLM := false
	for _, e := range top.Evidence {
		hasLLM = hasLLM || e.Kind == "llm"
	}
	if !hasLLM {
		t.Error("voted finding has no llm evidence")
	}
	for i, f := range a.Report.Findings {
		if f.Rank != i+1 || (i > 0 && f.Score > a.Report.Findings[i-1].Score+1e-12) {
			t.Fatalf("ranking inconsistent at %d: %+v", i, f)
		}
	}
	// the deterministic top is still there, below
	found := false
	for _, f := range a.Report.Findings[1:] {
		found = found || f.Location == before
	}
	if !found {
		t.Error("deterministic ranking lost")
	}
}

func TestApplyLLMErrorKeepsRanking(t *testing.T) {
	a := analyzeFile(t, filepath.Join("../llm/testdata/stacks", "C9.txt"), sampleDir)
	n := len(a.Report.Findings)
	fake := &llm.FakeProvider{Err: context.DeadlineExceeded}
	_, _, err := ApplyLLM(context.Background(), &a.Report, a.Groups[0], abs(t, sampleDir), "local", false,
		func(string) (llm.Provider, error) { return fake, nil }, nil)
	if err == nil {
		t.Fatal("want an error when every request fails")
	}
	if len(a.Report.Findings) != n {
		t.Error("ranking changed although the LLM failed")
	}
}

func TestLLMInputStaysInsideRoot(t *testing.T) {
	a := analyzeFile(t, filepath.Join("../llm/testdata/stacks", "C2.txt"), sampleDir)
	in, err := LLMInput(a.Groups[0], abs(t, sampleDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := in.Files["crash/crash.go"]; !ok {
		t.Errorf("source of the top file missing: %v", keys(in.Files))
	}
	if _, ok := readSource(abs(t, sampleDir), "../../../go.mod"); ok {
		t.Error("readSource left the project root")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
