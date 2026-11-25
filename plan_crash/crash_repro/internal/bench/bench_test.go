package bench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gofl/internal/llm"
)

const (
	sampleDir  = "../../testdata/goflsample"
	stacksPath = "../llm/testdata/stacks"
)

func TestParseTrigger(t *testing.T) {
	cases := []struct {
		in   string
		want Trigger
	}{
		{"go test ./crash -run TestNth", Trigger{Packages: []string{"./crash"}, Run: "TestNth"}},
		{"go test ./crash -run 'TestEmailOfNilProfile|TestProcessChain'", Trigger{Packages: []string{"./crash"}, Run: "TestEmailOfNilProfile|TestProcessChain"}},
		{"GOFL_RUN_FATAL=1 go test ./crash -run TestConcurrentMap", Trigger{Env: []string{"GOFL_RUN_FATAL=1"}, Packages: []string{"./crash"}, Run: "TestConcurrentMap"}},
		{"go test ./internal/app -run '^TestW1_' -count=1", Trigger{Packages: []string{"./internal/app"}, Run: "^TestW1_"}},
		{"go test -run=X", Trigger{Packages: []string{"./..."}, Run: "X"}},
	}
	for _, c := range cases {
		got, err := ParseTrigger(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "make test", "go test ./x -run 'unterminated"} {
		if _, err := ParseTrigger(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestLoadBugsAndEveryTriggerParses(t *testing.T) {
	for _, dir := range []string{sampleDir, "../../testdata/goflrealworld"} {
		bugs, err := LoadBugs(dir, "crash")
		if err != nil || len(bugs) < 7 {
			t.Fatalf("%s: %d bugs, %v", dir, len(bugs), err)
		}
		for _, b := range bugs {
			for _, trig := range append([]string{b.Trigger}, b.RelatedTriggers...) {
				if _, err := ParseTrigger(trig); err != nil {
					t.Errorf("%s: %v", b.ID, err)
				}
			}
		}
	}
}

// storedStacks serves the saved real stacks of C1..C13 instead of running tests.
func storedStacks(_ context.Context, b Bug) (string, error) {
	data, err := os.ReadFile(filepath.Join(stacksPath, b.ID+".txt"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestRunOnSavedStacks(t *testing.T) {
	bugs, err := LoadBugs(sampleDir, "crash")
	if err != nil {
		t.Fatal(err)
	}
	var have []Bug
	for _, b := range bugs {
		if _, err := os.Stat(filepath.Join(stacksPath, b.ID+".txt")); err == nil {
			have = append(have, b)
		}
	}
	if len(have) != 13 {
		t.Fatalf("%d saved stacks, want 13", len(have))
	}
	s, err := Run(context.Background(), Options{Dir: sampleDir, Bugs: have, Source: storedStacks})
	if err != nil {
		t.Fatal(err)
	}
	if s.Ran != 13 || s.PipelineRate() != 100 {
		t.Errorf("pipeline rate %.0f%% (%d/%d)", s.PipelineRate(), s.Ran, s.Total)
	}
	// The fix file of every layer-1 bug is on its stack, so file Acc@5
	// must be total. Function level is harder: the faulty function of C9 and
	// C10 (the constructors) is not on the stack at all.
	if m := s.Ranking.File[5]; m.Hit != m.Total {
		for _, c := range s.Cases {
			t.Logf("%s top=%v", c.ID, c.Top)
		}
		t.Errorf("file Acc@5 = %d/%d", m.Hit, m.Total)
	}
	if m := s.Ranking.Func[5]; m.Hit == m.Total || m.Hit < 8 {
		t.Errorf("func Acc@5 = %d/%d looks wrong", m.Hit, m.Total)
	}
	if s.Ranking.File[3].Hit < s.Baseline.File[3].Hit {
		t.Errorf("ranking (%d) worse than baseline (%d) at file Acc@3", s.Ranking.File[3].Hit, s.Baseline.File[3].Hit)
	}
	var out bytes.Buffer
	s.WriteMarkdown(&out, "test")
	for _, want := range []string{"file@3", "baseline: first user frame", "| C13 |"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q", want)
		}
	}
}

func TestFailedCaseCountsAsMiss(t *testing.T) {
	bugs := []Bug{{ID: "X1", File: "a.go", Func: "F", Lines: []int{1}, Trigger: "go test"}}
	src := func(context.Context, Bug) (string, error) { return "", errors.New("did not build") }
	s, err := Run(context.Background(), Options{Dir: sampleDir, Bugs: bugs, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	if s.Ran != 0 || s.Ranking.File[3].Total != 1 || s.Ranking.File[3].Hit != 0 || !strings.Contains(s.Cases[0].Why, "did not build") {
		t.Errorf("%+v", s)
	}
}

func TestRunWithLLMQuorum(t *testing.T) {
	bugs, _ := LoadBugs(sampleDir, "crash")
	var c9 []Bug
	for _, b := range bugs {
		if b.ID == "C9" {
			c9 = append(c9, b)
		}
	}
	good := `{"locations":[{"file":"crash/hard.go","function":"crash.NewSession","line":57}],"explanation":"x"}`
	bad := `{"locations":[{"file":"crash/crash.go","function":"crash.Nth","line":21}],"explanation":"x"}`
	cases := []struct {
		name      string
		responses []string
		wantQuorm bool // 3 of 5 answers right
	}{
		{"all right", []string{good}, true},
		{"2 of 5 right", []string{good, bad, bad, good, bad}, false},
		{"3 of 5 right", []string{good, good, bad, bad, good}, true},
	}
	for _, c := range cases {
		fake := &llm.FakeProvider{Responses: c.responses}
		s, err := Run(context.Background(), Options{
			Dir: sampleDir, Bugs: c9, Source: storedStacks, LLMMode: "local",
			Provider: func(string) (llm.Provider, error) { return fake, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.LLM[0].Quorum.Func[1].Hit == 1; got != c.wantQuorm {
			t.Errorf("%s: quorum func@1 = %v, want %v", c.name, got, c.wantQuorm)
		}
		if fake.Calls() != llm.DefaultRuns {
			t.Errorf("%s: %d calls", c.name, fake.Calls())
		}
	}
}

// The prompt sent during a benchmark never contains the "// BUG:" answer
// tags of the sample project (they would hand the answer to the model), and
// every prompt configuration is measured with its own 5 answers.
func TestLLMPromptHasNoAnswerTagsAndAllConfigsRun(t *testing.T) {
	bugs, _ := LoadBugs(sampleDir, "crash")
	var some []Bug
	for _, b := range bugs {
		if b.ID == "C1" || b.ID == "C9" {
			some = append(some, b)
		}
	}
	src, err := os.ReadFile(filepath.Join(sampleDir, "crash", "hard.go"))
	if err != nil || !strings.Contains(string(src), "BUG:") {
		t.Fatalf("sample source should carry BUG tags: %v", err)
	}
	fake := &llm.FakeProvider{Responses: []string{`{"locations":[{"file":"crash/hard.go","function":"crash.NewSession","line":57}]}`}}
	var shown bytes.Buffer
	s, err := Run(context.Background(), Options{
		Dir: sampleDir, Bugs: some, Source: storedStacks, LLMMode: "local", Show: &shown,
		Configs:  llm.AllConfigs(),
		Provider: func(string) (llm.Provider, error) { return fake, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range fake.Prompts() {
		if strings.Contains(p, "BUG:") {
			t.Fatalf("answer tag leaked into the prompt:\n%s", p)
		}
	}
	if strings.Contains(shown.String(), "BUG:") {
		t.Error("answer tag in the shown prompt")
	}
	if want := len(some) * 4 * llm.DefaultRuns; fake.Calls() != want {
		t.Errorf("%d calls, want %d", fake.Calls(), want)
	}
	if len(s.LLM) != 4 || s.LLM[0].Config != "ST" || s.LLM[3].Config != "SM+RoSFM" {
		t.Fatalf("configs %+v", s.LLM)
	}
	for _, c := range s.Cases {
		if len(c.LLMRaw["SM"]) != llm.DefaultRuns {
			t.Errorf("%s: raw answers not kept: %v", c.ID, c.LLMRaw)
		}
	}
	var out bytes.Buffer
	s.WriteMarkdown(&out, "test")
	for _, want := range []string{"SM+RoSFM: LLM alone", "answer tags were removed", "| ST+RoSFM |"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestStripAnswerTagsKeepsLines(t *testing.T) {
	src := "package x\n\n// Hard cases. Every line tagged \"BUG:<id>\" is the answer.\nfunc F() int {\n\treturn 1 / zero // BUG:C4 divide\n}\n"
	got := StripAnswerTags(src)
	if strings.Contains(got, "BUG") || strings.Count(got, "\n") != strings.Count(src, "\n") || !strings.Contains(got, "return 1 / zero\n") {
		t.Errorf("%q", got)
	}
}

// A provider that cannot be built (no key) counts as a miss in every LLM
// metric and is reported, without stopping the run.
func TestLLMProviderErrorCountsAsMiss(t *testing.T) {
	bugs, _ := LoadBugs(sampleDir, "crash")
	s, err := Run(context.Background(), Options{
		Dir: sampleDir, Bugs: bugs[:2], Source: storedStacks, LLMMode: "api",
		Provider: func(string) (llm.Provider, error) { return nil, errors.New("no key") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.LLM[0].Merged.File[1].Total != 2 || s.LLM[0].Merged.File[1].Hit != 0 || !strings.Contains(s.Cases[0].Why, "no key") {
		t.Errorf("%+v", s.LLM[0])
	}
	if s.Ranking.File[1].Hit != 2 {
		t.Errorf("ranking must still be measured: %+v", s.Ranking.File[1])
	}
}
