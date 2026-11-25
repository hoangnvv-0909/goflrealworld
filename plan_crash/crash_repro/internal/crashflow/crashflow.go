// Package crashflow is the whole "gofl crash" pipeline as a library, so the
// command and the benchmark runner run exactly the same code:
//
//	crash text -> logprofile -> crashsig.Relativize -> crashrank.GroupEvents
//	-> crashrank.Rank per group -> merge into one finding list
//	-> (optional) llm.Triage on the biggest group.
//
// Owner: Group C (paper C, sections II and IV).
package crashflow

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gofl/internal/crashrank"
	"gofl/internal/crashsig"
	"gofl/internal/finding"
	"gofl/internal/llm"
	"gofl/internal/logprofile"
	"gofl/internal/runner"
)

// MaxSourceBytes caps one source file sent to the model.
const MaxSourceBytes = 200 << 10

// Options describe where the crash text came from.
type Options struct {
	Root       string // absolute project root; output paths are relative to it
	ModulePath string // module path from go.mod, "" if unknown
	Source     string // label copied to Event.Source
	Loc        *time.Location
}

// Analysis is the deterministic part of the result.
type Analysis struct {
	Events []crashrank.Event
	Groups []crashrank.Group // largest first
	Report finding.Report    // Findings ranked, Notes about counts and bad blocks
	Bad    int               // blocks that did not parse
}

// Analyze reads crash text through the profile and ranks it. It never calls
// a model.
func Analyze(p logprofile.Profile, text io.Reader, o Options) (Analysis, error) {
	loc := o.Loc
	if loc == nil {
		loc = time.Local
	}
	var a Analysis
	for ev, err := range p.Entries(text, loc) {
		if err != nil {
			a.Bad++
			continue
		}
		ev.Signature = crashsig.Relativize(ev.Signature, o.Root, o.ModulePath)
		if ev.Source == "" || ev.Source == "log" {
			ev.Source = o.Source
		}
		a.Events = append(a.Events, ev)
	}
	groups, err := crashrank.GroupEvents(a.Events)
	if err != nil {
		return a, err
	}
	a.Groups = groups
	ranked := make([][]finding.Finding, len(groups))
	for i, g := range groups {
		if ranked[i], err = crashrank.Rank(g, groups); err != nil {
			return a, err
		}
	}
	a.Report = finding.Report{Version: finding.SchemaVersion, Command: "crash", Findings: MergeGroups(ranked)}
	if a.Bad > 0 {
		a.Report.Notes = append(a.Report.Notes, fmt.Sprintf("%d crash block(s) could not be parsed and were skipped", a.Bad))
	}
	if len(a.Events) > 0 {
		a.Report.Notes = append(a.Report.Notes, fmt.Sprintf("%d crash event(s) in %d group(s)", len(a.Events), len(groups)))
	}
	return a, nil
}

// MergeGroups combines the rankings of all groups into one list: a location
// seen in several groups appears once, with the evidence of each; the list is
// ordered by score (ties keep group order, then ranking order).
func MergeGroups(ranked [][]finding.Finding) []finding.Finding {
	type key struct{ file, fn string }
	var out []finding.Finding
	at := map[key]int{}
	for _, fs := range ranked {
		for _, f := range fs {
			k := key{f.Location.File, llm.NormFunc(f.Location.Func)}
			if i, ok := at[k]; ok {
				out[i].Evidence = append(out[i].Evidence, f.Evidence...)
				if f.Score > out[i].Score {
					out[i].Score = f.Score
				}
				continue
			}
			at[k] = len(out)
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// LLMInput builds what the model would be shown for group g: the crash text of
// its first event, the ranking of the group alone, and the sources of the top
// ranked files that lie inside root.
func LLMInput(g crashrank.Group, root string) (llm.Input, error) {
	ev := g.Events[0]
	in := llm.Input{
		Exception: ev.Signature.Exception,
		Stack:     ev.Signature.Raw,
		Files:     map[string]string{},
	}
	ranked, err := crashrank.Rank(g, []crashrank.Group{g})
	if err != nil {
		return in, err
	}
	in.Ranked = ranked
	for _, f := range ranked {
		if _, done := in.Files[f.Location.File]; done || len(in.Files) >= llm.DefaultConfig().MaxFiles {
			continue
		}
		if src, ok := readSource(root, f.Location.File); ok {
			in.Files[f.Location.File] = src
		}
	}
	return in, nil
}

// ApplyLLM runs the optional LLM layer on group g and folds the vote into
// rep.Findings. The exact prompt is always written to show first, so the user
// sees what leaves the machine. With dry set nothing is sent. The outcome is
// nil when no request was made. On error the deterministic ranking in rep is
// left untouched.
func ApplyLLM(ctx context.Context, rep *finding.Report, g crashrank.Group, root, mode string, dry bool,
	provider func(mode string) (llm.Provider, error), show io.Writer) ([]string, *llm.Outcome, error) {
	return ApplyLLMWith(ctx, rep, g, root, LLMOptions{Mode: mode, Dry: dry, Provider: provider, Show: show})
}

// LLMOptions are the knobs of ApplyLLMWith; the zero value of each field
// means the default of "gofl crash".
type LLMOptions struct {
	Mode     string
	Dry      bool
	Config   llm.PromptConfig // zero: llm.DefaultConfig()
	Provider func(mode string) (llm.Provider, error)
	Show     io.Writer // where the prompt is printed before sending; nil: not printed
	// Filter rewrites each source file before it goes into the prompt. The
	// benchmark uses it to remove the "// BUG:" answer tags of the seeded
	// bugs (otherwise the model reads the answer). It must keep the number
	// of lines, so line numbers still match the stack.
	Filter func(path, src string) string
}

// ApplyLLMWith is ApplyLLM with every option.
func ApplyLLMWith(ctx context.Context, rep *finding.Report, g crashrank.Group, root string, o LLMOptions) ([]string, *llm.Outcome, error) {
	in, err := LLMInput(g, root)
	if err != nil {
		return nil, nil, err
	}
	if o.Filter != nil {
		for p, src := range in.Files {
			in.Files[p] = o.Filter(p, src)
		}
	}
	cfg := o.Config
	if cfg == (llm.PromptConfig{}) {
		cfg = llm.DefaultConfig()
	}
	prompt, err := llm.DryRun(in, cfg)
	if err != nil {
		return nil, nil, err
	}
	if o.Show != nil {
		fmt.Fprintf(o.Show, "gofl: -llm %s would send this text (after redaction):\n----8<----\n%s\n---->8----\n", o.Mode, prompt)
	}
	if o.Dry {
		return []string{"LLM dry run: the prompt was printed and nothing was sent"}, nil, nil
	}
	p, err := o.Provider(o.Mode)
	if err != nil {
		return nil, nil, err
	}
	out, err := llm.Triage(ctx, p, in, llm.TriageOptions{Config: cfg, Concurrency: 1})
	if err != nil {
		return out.Notes, &out, err
	}
	rep.Findings = MergeLLM(rep.Findings, out.Findings)
	notes := append([]string{fmt.Sprintf("LLM triage: %d of %d answers used", len(out.Results), llm.DefaultRuns)}, out.Notes...)
	return notes, &out, nil
}

// MergeLLM puts the locations the model voted for first (most votes first),
// above the deterministic ranking, and adds the model's evidence to locations
// both agree on. Scores stay descending: voted locations score above the best
// deterministic score.
func MergeLLM(base, voted []finding.Finding) []finding.Finding {
	best := 0.0
	for _, f := range base {
		best = max(best, f.Score)
	}
	used := make([]bool, len(base))
	var out []finding.Finding
	for _, v := range voted {
		f := v
		for i, b := range base {
			if !used[i] && b.Location.File == v.Location.File && llm.NormFunc(b.Location.Func) == llm.NormFunc(v.Location.Func) {
				used[i] = true
				f = b
				f.Evidence = append(append([]finding.Evidence{}, v.Evidence...), b.Evidence...)
				if v.Location.Line > 0 && f.Location.Line == 0 {
					f.Location.Line = v.Location.Line
				}
				break
			}
		}
		f.Score = best + v.Score
		out = append(out, f)
	}
	for i, b := range base {
		if !used[i] {
			out = append(out, b)
		}
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// readSource reads a project-relative file, refusing paths that leave root.
func readSource(root, rel string) (string, bool) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if r, err := filepath.Rel(root, p); err != nil || strings.HasPrefix(r, "..") {
		return "", false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) > MaxSourceBytes || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

// SniffTestJSON turns "go test -json" output into plain text; any other input
// is passed through untouched (and streamed).
func SniffTestJSON(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		b, err := br.Peek(1)
		if err != nil { // empty input
			return br, nil
		}
		if b[0] == '\n' || b[0] == '\r' || b[0] == ' ' || b[0] == '\t' {
			br.ReadByte()
			continue
		}
		if b[0] != '{' {
			return br, nil
		}
		break
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(runner.ParseTestJSON(data).Output()), nil
}

var moduleRe = regexp.MustCompile(`(?m)^\s*module\s+"?([^\s"]+)"?\s*(?://.*)?$`)

// ModulePath returns the module path of root/go.mod, or "".
func ModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	if m := moduleRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}
