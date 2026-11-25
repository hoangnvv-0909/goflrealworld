package llm

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gofl/internal/finding"
)

// Detail says how much crash information goes into the prompt (paper C,
// section III-C): the full stack trace (ST) or only the stack message (SM,
// exception type and message, no call stack).
type Detail int

const (
	DetailST Detail = iota
	DetailSM
)

func (d Detail) String() string {
	if d == DetailSM {
		return "SM"
	}
	return "ST"
}

// PromptConfig selects one of the four prompt combinations of paper C:
// ST, ST+RoSFM, SM, SM+RoSFM (RoSFM = ranking of suspicious files and methods).
type PromptConfig struct {
	Detail   Detail
	WithRank bool
	MaxFiles int // source files in the prompt; paper C uses 3
	MaxChars int // total character budget for source code
	MaxLocs  int // locations the model may return
}

// DefaultConfig is ST + ranking, three files. The default will be revisited
// after the layer-1 measurement (in paper C, ST alone did best on the full
// dataset and the ranking only helped SM).
func DefaultConfig() PromptConfig {
	return PromptConfig{Detail: DetailST, WithRank: true, MaxFiles: 3, MaxChars: 24000, MaxLocs: 3}
}

// Name is a short label such as "ST+RoSFM", used in reports.
func (c PromptConfig) Name() string {
	if c.WithRank {
		return c.Detail.String() + "+RoSFM"
	}
	return c.Detail.String()
}

// AllConfigs returns the four combinations of the paper.
func AllConfigs() []PromptConfig {
	var out []PromptConfig
	for _, d := range []Detail{DetailST, DetailSM} {
		for _, r := range []bool{false, true} {
			c := DefaultConfig()
			c.Detail, c.WithRank = d, r
			out = append(out, c)
		}
	}
	return out
}

// ParseConfigs reads a comma list of prompt configurations for the
// benchmark: "all" (the four of the paper), "default", or names such as
// "st", "st+rank", "sm", "sm+rosfm" (case does not matter).
func ParseConfigs(s string) ([]PromptConfig, error) {
	var out []PromptConfig
	seen := map[string]bool{}
	for _, w := range strings.Split(s, ",") {
		w = strings.ToLower(strings.TrimSpace(w))
		var add []PromptConfig
		switch w {
		case "":
			continue
		case "all":
			add = AllConfigs()
		case "default":
			add = []PromptConfig{DefaultConfig()}
		default:
			c := DefaultConfig()
			base, rank, _ := strings.Cut(w, "+")
			switch base {
			case "st":
				c.Detail = DetailST
			case "sm":
				c.Detail = DetailSM
			default:
				return nil, fmt.Errorf("llm: unknown prompt configuration %q (want all, default, st, st+rank, sm, sm+rank)", w)
			}
			switch rank {
			case "":
				c.WithRank = false
			case "rank", "rosfm":
				c.WithRank = true
			default:
				return nil, fmt.Errorf("llm: unknown prompt configuration %q", w)
			}
			add = []PromptConfig{c}
		}
		for _, c := range add {
			if !seen[c.Name()] {
				seen[c.Name()] = true
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("llm: no prompt configuration in %q", s)
	}
	return out, nil
}

// ErrEmptyInput means there is no crash information to build a prompt from.
var ErrEmptyInput = errors.New("llm: no exception or stack to build a prompt from")

// BuildPrompt builds the five-part zero-shot prompt with DefaultConfig.
func BuildPrompt(in Input) (string, error) {
	return BuildPromptWith(in, DefaultConfig())
}

// BuildPromptWith builds the five-part zero-shot prompt (C8, Listing 1):
//
//  1. role specification
//  2. suspicious source files (at most cfg.MaxFiles, line numbered)
//  3. crash information: stack trace or stack message
//  4. ranking of files and functions (only when cfg.WithRank)
//  5. request
//
// Difference from the paper: part 5 asks for LOCATIONS as JSON, not for
// patched code. The whole text goes through Redact, so the returned string is
// exactly what leaves the machine (use it for the dry run).
func BuildPromptWith(in Input, cfg PromptConfig) (string, error) {
	cfg = cfg.withDefaults()
	crash := crashText(in, cfg.Detail)
	if strings.TrimSpace(crash) == "" {
		return "", ErrEmptyInput
	}

	var b strings.Builder
	b.WriteString("Role-Play-Part: I want you to act as a fault localization expert for Go programs. " +
		"Given a crash, you will tell which source locations most likely contain the bug that causes it. " +
		"Do not write patches.\n\n")

	files := selectFiles(in, cfg)
	b.WriteString("Suspicious-Code-Part: This is my code")
	if len(files) == 0 {
		b.WriteString(" (no source files were provided)")
	}
	b.WriteString(":\n")
	budget := cfg.MaxChars
	if len(files) > 0 {
		budget = cfg.MaxChars / len(files)
	}
	for _, f := range files {
		body := excerpt(in.Files[f.path], f.lines, budget)
		fmt.Fprintf(&b, "File: %s\n```go\n%s```\n", f.path, body)
	}
	b.WriteString("\n")

	if cfg.Detail == DetailSM {
		b.WriteString("Stack-Message-Information-Part: I am getting:\n```\n")
	} else {
		b.WriteString("Stack-Trace-Information-Part: I am getting:\n```\n")
	}
	b.WriteString(strings.TrimRight(crash, "\n"))
	b.WriteString("\n```\n\n")

	if cfg.WithRank && len(in.Ranked) > 0 {
		b.WriteString("Ranking-Information-Part: This is the ranking of files and functions most suspected of containing the bug:\n")
		b.WriteString(rankingText(in.Ranked))
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Asking-Part: Tell me where the bug is. Answer with ONLY a JSON object of this exact form, "+
		"no other text:\n"+
		`{"locations":[{"file":"path relative to the project root","function":"pkg.Func or pkg.(*Type).Method","line":123}],"explanation":"one or two sentences"}`+"\n"+
		"List at most %d locations, most suspicious first. Use file paths exactly as written above.\n", cfg.MaxLocs)

	return Redact(b.String()), nil
}

func (c PromptConfig) withDefaults() PromptConfig {
	d := DefaultConfig()
	if c.MaxFiles <= 0 {
		c.MaxFiles = d.MaxFiles
	}
	if c.MaxChars <= 0 {
		c.MaxChars = d.MaxChars
	}
	if c.MaxLocs <= 0 {
		c.MaxLocs = d.MaxLocs
	}
	return c
}

// StackMessage keeps only what the paper calls the stack message: the
// exception line(s) without the call stack.
func StackMessage(stack string) string {
	var keep []string
	for _, ln := range strings.Split(strings.ReplaceAll(stack, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "panic:") || strings.HasPrefix(t, "fatal error:") || strings.HasPrefix(t, "[signal") {
			keep = append(keep, t)
		}
	}
	return strings.Join(keep, "\n")
}

func crashText(in Input, d Detail) string {
	if d == DetailSM {
		if m := StackMessage(in.Stack); m != "" {
			return m
		}
		return strings.TrimSpace(in.Exception)
	}
	st := strings.TrimSpace(in.Stack)
	ex := strings.TrimSpace(in.Exception)
	switch {
	case st == "":
		return ex
	case ex != "" && !strings.Contains(st, ex):
		return ex + "\n" + st
	}
	return st
}

type chosenFile struct {
	path  string
	lines []int // lines worth keeping when the file has to be cut
}

// selectFiles picks up to cfg.MaxFiles files from in.Files, ranked files
// first (in ranking order), then files named in the stack (crash point first).
func selectFiles(in Input, cfg PromptConfig) []chosenFile {
	known := make([]string, 0, len(in.Files))
	for k := range in.Files {
		known = append(known, k)
	}
	sort.Strings(known)

	var out []chosenFile
	idx := map[string]int{}
	add := func(path string, line int) {
		k := matchFile(path, known)
		if k == "" {
			return
		}
		i, ok := idx[k]
		if !ok {
			if len(out) >= cfg.MaxFiles {
				return
			}
			idx[k] = len(out)
			out = append(out, chosenFile{path: k})
			i = len(out) - 1
		}
		if line > 0 {
			out[i].lines = append(out[i].lines, line)
		}
	}
	for _, r := range in.Ranked {
		add(r.Location.File, r.Location.Line)
	}
	for _, f := range ParseStackFrames(in.Stack) {
		if IsRuntimeFrame(f) {
			continue
		}
		add(f.File, f.Line)
	}
	// Lines from the stack for files chosen through the ranking.
	for _, f := range ParseStackFrames(in.Stack) {
		if k := matchFile(f.File, known); k != "" {
			if i, ok := idx[k]; ok && f.Line > 0 {
				out[i].lines = append(out[i].lines, f.Line)
			}
		}
	}
	return out
}

// rankingText renders ranked findings grouped by file, in ranking order.
func rankingText(ranked []finding.Finding) string {
	var order []string
	funcs := map[string][]string{}
	for _, r := range ranked {
		f := r.Location.File
		if _, ok := funcs[f]; !ok {
			order = append(order, f)
			funcs[f] = nil
		}
		if fn := r.Location.Func; fn != "" {
			dup := false
			for _, x := range funcs[f] {
				dup = dup || x == fn
			}
			if !dup {
				funcs[f] = append(funcs[f], fn)
			}
		}
	}
	var b strings.Builder
	for i, f := range order {
		fmt.Fprintf(&b, "%d) %s:\n", i+1, f)
		for _, fn := range funcs[f] {
			fmt.Fprintf(&b, " - %s\n", fn)
		}
	}
	return b.String()
}

const excerptWindow = 40 // lines kept on each side of an interesting line

// excerpt returns src with line numbers. If src is longer than budget
// characters it keeps windows around the given lines and says how many lines
// were left out, so the model knows the text is cut. The windows shrink until
// the result fits the budget (down to the interesting lines alone).
func excerpt(src string, lines []int, budget int) string {
	all := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if n := len(all); n > 0 && all[n-1] == "" {
		all = all[:n-1]
	}
	number := func(from, to int) string { // 1-based inclusive
		var b strings.Builder
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "%4d| %s\n", i, all[i-1])
		}
		return b.String()
	}
	if full := number(1, len(all)); len(full) <= budget {
		return full
	}
	if len(lines) == 0 {
		// Nothing to aim at: keep the head of the file.
		keep, size := 0, 0
		for keep < len(all) && size+len(all[keep])+7 <= budget {
			size += len(all[keep]) + 7
			keep++
		}
		return number(1, keep) + fmt.Sprintf("// ... %d more lines omitted ...\n", len(all)-keep)
	}
	var out string
	for w := excerptWindow; w >= 0; w /= 2 {
		out = render(all, windows(lines, w, len(all)), number)
		if len(out) <= budget || w == 0 {
			break
		}
	}
	return out
}

// windows merges [l-w, l+w] around every line l (clamped to 1..n).
func windows(lines []int, w, n int) [][2]int {
	ls := append([]int(nil), lines...)
	sort.Ints(ls)
	var ws [][2]int
	for _, l := range ls {
		if l < 1 || l > n {
			continue
		}
		a, b := max(1, l-w), min(n, l+w)
		if k := len(ws); k > 0 && a <= ws[k-1][1]+1 {
			ws[k-1][1] = max(ws[k-1][1], b)
		} else {
			ws = append(ws, [2]int{a, b})
		}
	}
	return ws
}

func render(all []string, ws [][2]int, number func(from, to int) string) string {
	var b strings.Builder
	prev := 0
	for _, w := range ws {
		if w[0] > prev+1 {
			fmt.Fprintf(&b, "// ... %d lines omitted ...\n", w[0]-prev-1)
		}
		b.WriteString(number(w[0], w[1]))
		prev = w[1]
	}
	if prev < len(all) {
		fmt.Fprintf(&b, "// ... %d lines omitted ...\n", len(all)-prev)
	}
	return b.String()
}
