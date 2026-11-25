// Package agentrun runs one repair Agent (Claude Code in headless mode) on one
// crash case and scores the outcome (plan step 11).
//
// One run:
//
//  1. copy the project into a temporary directory OUTSIDE the repository,
//     without bugs.json and without the "// BUG:" tags (bench.AgentWorkspace);
//  2. start "claude -p" there with the prompt of agentprompt (E0 or E1);
//  3. score what it left behind:
//     - localisation: gofl-localize.json against the truth (file and function
//     level, Acc@1 and Acc@3);
//     - fixed: every reproduce command stops crashing (a), every test that
//     passed before still passes (b), no test file was edited, and a function
//     of the truth was changed, which is the root cause and not the symptom (c).
//
// The answers (bugs.json) are read only here, after the Agent has finished.
package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gofl/internal/agentprompt"
	"gofl/internal/bench"
	"gofl/internal/finding"
	"gofl/internal/llm"
	"gofl/internal/runner"
)

// Spec is one run of one case.
type Spec struct {
	Project  string // project root that holds bugs.json
	Bug      bench.Bug
	Mode     agentprompt.Mode
	Prompt   string
	Baseline []string // tests that passed before any fix: "pkg/path TestName"
	Run      int      // 1-based number of the run for this case and mode

	Claude    string        // executable; default "claude"
	Model     string        // "" = the default of the Claude Code installation
	MaxBudget float64       // USD cap for the Agent; 0 = no flag
	Timeout   time.Duration // for the whole Agent run; default 20 minutes
	TestTime  time.Duration // for each verification "go test"; default 3 minutes
}

// Result is the outcome of one run. Everything the Agent said and did is kept
// so a run can be re-scored.
type Result struct {
	Case string
	Mode string
	Run  int

	Model        string
	Seconds      float64
	CostUSD      float64
	Turns        int
	InputTokens  int
	OutputTokens int
	AgentError   string // the Agent failed to run or ended with an error

	Localized  []finding.Location // what gofl-localize.json named
	LocFile1   bool
	LocFile3   bool
	LocFunc1   bool
	LocFunc3   bool
	Reproduced bool // (a) no reproduce command crashes any more
	OthersPass bool // (b) every baseline test still passes
	TestsEdit  bool // a *_test.go file was changed (counts as a failure)
	RootCause  bool // (c) a function of the truth was changed
	Fixed      bool // a && b && c && !TestsEdit

	ChangedFuncs []string
	Regressed    []string // baseline tests that no longer pass
	Diff         string
	AgentOutput  string // raw JSON printed by claude
	Notes        []string
}

// allowed is what the Agent may do: read and edit inside its workspace, and
// run the Go tool. Anything else is denied (nobody answers a prompt).
var allowed = []string{"Read", "Edit", "Write", "Glob", "Grep", "Bash(go:*)", "Bash(gofmt:*)", "Bash(ls:*)"}

// Run performs one run.
func Run(ctx context.Context, s Spec) (Result, error) {
	r := Result{Case: s.Bug.ID, Mode: s.Mode.String(), Run: s.Run}
	if s.Claude == "" {
		s.Claude = "claude"
	}
	if s.Timeout == 0 {
		s.Timeout = 20 * time.Minute
	}
	if s.TestTime == 0 {
		s.TestTime = 3 * time.Minute
	}
	ws, err := os.MkdirTemp("", "goflagent-ws-*")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(ws)
	before, err := os.MkdirTemp("", "goflagent-before-*")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(before)
	// AgentWorkspace wants an empty destination; the temp dirs are empty.
	if err := bench.AgentWorkspace(s.Project, ws); err != nil {
		return r, err
	}
	if err := bench.AgentWorkspace(s.Project, before); err != nil {
		return r, err
	}

	start := time.Now()
	r.AgentOutput, r.AgentError = runClaude(ctx, s, ws, &r)
	r.Seconds = time.Since(start).Seconds()

	truth := s.Bug.Truth()
	r.Localized = readLocalize(ws)
	r.LocFile1 = llm.HitAt(r.Localized, truth, llm.LevelFile, 1, 0)
	r.LocFile3 = llm.HitAt(r.Localized, truth, llm.LevelFile, 3, 0)
	r.LocFunc1 = llm.HitAt(r.Localized, truth, llm.LevelFunc, 1, 0)
	r.LocFunc3 = llm.HitAt(r.Localized, truth, llm.LevelFunc, 3, 0)

	r.Diff, r.ChangedFuncs, r.TestsEdit = changes(before, ws)
	r.RootCause = containsFunc(r.ChangedFuncs, truth.Func)

	r.Reproduced = reproduced(ctx, ws, s)
	r.Regressed = regressions(ctx, ws, s)
	r.OthersPass = len(r.Regressed) == 0
	r.Fixed = r.Reproduced && r.OthersPass && r.RootCause && !r.TestsEdit
	return r, nil
}

// runClaude starts the Agent; it returns the raw stdout and an error text.
func runClaude(ctx context.Context, s Spec, ws string, r *Result) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := []string{
		"-p", "--output-format", "json",
		"--permission-mode", "dontAsk",
		"--allowedTools", strings.Join(allowed, ","),
		"--disallowedTools", "WebFetch,WebSearch",
		"--no-session-persistence",
		"--setting-sources", "project", // no user or local settings, hooks or memory
		"--disable-slash-commands",
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.MaxBudget > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(s.MaxBudget, 'f', 2, 64))
	}
	cmd := exec.CommandContext(ctx, s.Claude, args...)
	cmd.Dir = ws
	cmd.Stdin = strings.NewReader(s.Prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	parseClaudeJSON(out.Bytes(), r)
	switch {
	case ctx.Err() != nil:
		return out.String(), "timeout: " + ctx.Err().Error()
	case err != nil:
		return out.String(), strings.TrimSpace(err.Error() + ": " + tail(errb.String(), 400))
	}
	return out.String(), ""
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// parseClaudeJSON reads the single result object of --output-format json.
// Unknown or missing fields are left as zero.
func parseClaudeJSON(data []byte, r *Result) {
	var j struct {
		IsError bool    `json:"is_error"`
		Result  string  `json:"result"`
		CostUSD float64 `json:"total_cost_usd"`
		Turns   int     `json:"num_turns"`
		Usage   struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
			CR  int `json:"cache_read_input_tokens"`
			CC  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	if json.Unmarshal(data, &j) != nil {
		return
	}
	r.CostUSD, r.Turns = j.CostUSD, j.Turns
	r.InputTokens, r.OutputTokens = j.Usage.In+j.Usage.CR+j.Usage.CC, j.Usage.Out
	var models []string
	for m := range j.ModelUsage {
		models = append(models, m)
	}
	sort.Strings(models)
	r.Model = strings.Join(models, ",")
	if j.IsError {
		r.Notes = append(r.Notes, "agent reported an error: "+tail(j.Result, 300))
	}
}

// readLocalize reads gofl-localize.json from the workspace; a missing or bad
// file gives no locations.
func readLocalize(ws string) []finding.Location {
	data, err := os.ReadFile(filepath.Join(ws, agentprompt.LocalizeFile))
	if err != nil {
		return nil
	}
	var j struct {
		Locations []struct {
			File     string `json:"file"`
			Function string `json:"function"`
			Func     string `json:"func"`
			Line     int    `json:"line"`
		} `json:"locations"`
	}
	if json.Unmarshal(data, &j) != nil {
		return nil
	}
	var out []finding.Location
	for _, l := range j.Locations {
		fn := l.Function
		if fn == "" {
			fn = l.Func
		}
		out = append(out, finding.Location{File: filepath.ToSlash(l.File), Func: fn, Line: l.Line})
	}
	return out
}

// reproduced reports whether every reproduce command now runs without any
// failure or panic.
func reproduced(ctx context.Context, ws string, s Spec) bool {
	for _, trig := range append([]string{s.Bug.Trigger}, s.Bug.RelatedTriggers...) {
		t, err := bench.ParseTrigger(trig)
		if err != nil {
			return false
		}
		res, err := runner.Run(ctx, runner.Spec{Dir: ws, Packages: t.Packages, Env: t.Env, Run: t.Run, Timeout: s.TestTime})
		if err != nil || res.Unrunnable != nil {
			return false
		}
		ran := false
		for _, tr := range res.Tests {
			if tr.Test != "" {
				ran = true
			}
			if !tr.Passed || strings.Contains(tr.Output, "panic:") {
				return false
			}
		}
		if !ran {
			return false // nothing ran: the Agent may have removed the test
		}
	}
	return true
}

// regressions lists the baseline tests that no longer pass.
func regressions(ctx context.Context, ws string, s Spec) []string {
	var bad []string
	for _, id := range s.Baseline {
		pkg, test, ok := strings.Cut(id, " ")
		if !ok {
			continue
		}
		res, err := runner.Run(ctx, runner.Spec{Dir: ws, Packages: []string{pkg}, Run: "^" + regexp.QuoteMeta(test) + "$", Timeout: s.TestTime})
		if err != nil || res.Unrunnable != nil || !allPassed(res, test) {
			bad = append(bad, id)
		}
	}
	return bad
}

func allPassed(res runner.Result, test string) bool {
	found := false
	for _, tr := range res.Tests {
		if tr.Test == test {
			found = true
		}
		if tr.Test != "" && !tr.Passed {
			return false
		}
	}
	return found
}

// BaselineTests returns the tests that pass in the unmodified project, one
// test per "go test" run so that one panic does not hide the others.
func BaselineTests(ctx context.Context, dir string, timeout time.Duration) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-list", ".", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("go test -list: %w", err)
	}
	var names, ids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "ok\t"):
			f := strings.Fields(line)
			if len(f) >= 2 {
				for _, n := range names {
					ids = append(ids, f[1]+" "+n)
				}
			}
			names = nil
		case strings.HasPrefix(line, "Test") && !strings.ContainsAny(line, " \t"):
			names = append(names, line)
		}
	}
	var pass []string
	for _, id := range ids {
		pkg, test, _ := strings.Cut(id, " ")
		res, err := runner.Run(ctx, runner.Spec{Dir: dir, Packages: []string{pkg}, Run: "^" + regexp.QuoteMeta(test) + "$", Timeout: timeout})
		if err == nil && res.Unrunnable == nil && allPassed(res, test) {
			pass = append(pass, id)
		}
	}
	return pass, nil
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// changes compares the two trees. It returns the unified diff, the names of
// the functions with changed lines ("Type.Method" or "Func") and whether any
// test file changed.
func changes(before, after string) (diff string, funcs []string, testsEdited bool) {
	var all strings.Builder
	set := map[string]bool{}
	filepath.WalkDir(after, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(after, p)
		if rel == agentprompt.LocalizeFile {
			return nil
		}
		old := filepath.Join(before, rel)
		a, _ := os.ReadFile(old)
		b, _ := os.ReadFile(p)
		if bytes.Equal(a, b) {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if strings.HasSuffix(slash, "_test.go") {
			testsEdited = true
		}
		out, _ := exec.Command("git", "diff", "--no-index", "--unified=0", "--", old, p).Output()
		all.WriteString("### " + slash + "\n")
		all.Write(out)
		if strings.HasSuffix(slash, ".go") {
			for _, fn := range funcsAt(p, hunkLines(string(out))) {
				set[fn] = true
			}
		}
		return nil
	})
	for f := range set {
		funcs = append(funcs, f)
	}
	sort.Strings(funcs)
	return all.String(), funcs, testsEdited
}

// hunkLines returns the lines (new side) touched by a "-U0" diff; a pure
// deletion counts as the line where it happened.
func hunkLines(diff string) []int {
	var lines []int
	for _, l := range strings.Split(diff, "\n") {
		m := hunkRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		n := 1
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		if n == 0 {
			lines = append(lines, start, start+1)
			continue
		}
		for i := 0; i < n; i++ {
			lines = append(lines, start+i)
		}
	}
	return lines
}

// funcsAt names the functions of file that enclose the given lines.
func funcsAt(file string, lines []int) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		from, to := fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line
		for _, l := range lines {
			if l >= from && l <= to {
				set[funcName(fd)] = true
			}
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	return out
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.IndexExpr:
			t = x.X
			continue
		case *ast.IndexListExpr:
			t = x.X
			continue
		case *ast.Ident:
			return x.Name + "." + fd.Name.Name
		}
		return fd.Name.Name
	}
}

func containsFunc(funcs []string, want string) bool {
	w := llm.NormFunc(want)
	for _, f := range funcs {
		if llm.NormFunc(f) == w {
			return true
		}
	}
	return false
}

// ErrNoClaude is returned by Check when the executable cannot be found.
var ErrNoClaude = errors.New("agentrun: claude executable not found")

// Check verifies that the Agent executable exists.
func Check(claude string) error {
	if claude == "" {
		claude = "claude"
	}
	if _, err := exec.LookPath(claude); err != nil {
		return ErrNoClaude
	}
	return nil
}
