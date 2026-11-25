package llm

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"gofl/internal/finding"
)

// Errors of ParseResponse. Callers count them to report how many answers
// were unusable.
var (
	ErrEmptyResponse = errors.New("llm: empty response")
	ErrNoJSON        = errors.New("llm: no locations found in the response")
)

// Hint tells ParseResponseWith what the prompt offered, so it can reject
// invented files and recover locations from free text.
type Hint struct {
	// Files are project-relative paths the model could legitimately name:
	// the files put in the prompt, the ranked files and the stack files.
	Files []string
	// Funcs are function names (as printed in stacks) that may be named.
	Funcs []string
}

// HintFor derives a Hint from the same Input the prompt was built from.
func HintFor(in Input) Hint {
	seen := map[string]bool{}
	var h Hint
	addFile := func(f string) {
		if f != "" && !seen[f] {
			seen[f] = true
			h.Files = append(h.Files, f)
		}
	}
	for k := range in.Files {
		addFile(k)
	}
	for _, r := range in.Ranked {
		addFile(normPath(r.Location.File))
		if r.Location.Func != "" {
			h.Funcs = append(h.Funcs, r.Location.Func)
		}
	}
	known := append([]string(nil), h.Files...)
	for _, fr := range UserFrames(in.Stack) {
		h.Funcs = append(h.Funcs, fr.Func)
		if k := matchFile(fr.File, known); k != "" {
			addFile(k)
		}
	}
	return h
}

// ParseResponse parses a closed-schema JSON answer, tolerating text outside
// the code block and truncated output (C13). It does no grounding: use
// ParseResponseWith for that.
func ParseResponse(raw []byte) (Result, error) {
	return ParseResponseWith(raw, Hint{})
}

// ParseResponseWith is ParseResponse plus grounding against hint. With a
// non-empty hint.Files, locations whose file is not among them go to
// Result.Ungrounded instead of Result.Locations.
//
// Recovery tiers (Result.Tier): plain JSON (1), fenced block (2), balanced
// braces in surrounding text (3), the same after removing trailing commas
// (5), objects salvaged from truncated output (4), free text mentioning
// known files and functions (6). They run in the order 1, 2, 3, 5, 4, 6.
func ParseResponseWith(raw []byte, hint Hint) (Result, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), utf8BOM))
	if text == "" {
		return Result{}, ErrEmptyResponse
	}

	steps := []struct {
		tier int
		fn   func(string) (parsed, bool)
	}{
		{1, parsePlain},
		{2, parseFenced},
		{3, parseBalanced},
		{5, func(s string) (parsed, bool) { return parseAll(stripTrailingCommas(s)) }},
		{4, parseTruncated},
	}
	for _, st := range steps {
		if p, ok := st.fn(text); ok {
			return finish(p, st.tier, text, hint), nil
		}
	}
	if p, ok := parseProse(text, hint); ok {
		return finish(p, 6, text, hint), nil
	}
	return Result{}, ErrNoJSON
}

type parsed struct {
	locs        []finding.Location
	explanation string
}

type rawLoc struct {
	File     string          `json:"file"`
	Path     string          `json:"path"`
	Function string          `json:"function"`
	Func     string          `json:"func"`
	Method   string          `json:"method"`
	Line     json.RawMessage `json:"line"`
	Lines    json.RawMessage `json:"lines"`
}

type rawAnswer struct {
	Locations   []rawLoc `json:"locations"`
	Explanation string   `json:"explanation"`
}

func (r rawLoc) toLocation() (finding.Location, bool) {
	file := r.File
	if file == "" {
		file = r.Path
	}
	fn := firstNonEmpty(r.Function, r.Func, r.Method)
	l := finding.Location{
		File: normPath(file),
		Func: cleanFunc(fn),
		Line: lineOf(r.Line),
	}
	if l.Line == 0 {
		l.Line = lineOf(r.Lines)
	}
	return l, l.File != "" || l.Func != ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func cleanFunc(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "()")
	return strings.Trim(s, "` ")
}

var firstInt = regexp.MustCompile(`\d+`)

// lineOf reads a line from 12, "12", "12-15", [12, 15] or null; 0 if unknown.
func lineOf(m json.RawMessage) int {
	if len(m) == 0 {
		return 0
	}
	var arr []json.RawMessage
	if json.Unmarshal(m, &arr) == nil {
		if len(arr) == 0 {
			return 0
		}
		return lineOf(arr[0])
	}
	if d := firstInt.FindString(string(m)); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// decode reads s as the answer schema, also accepting a bare array of
// locations. It succeeds only when it found at least one location.
func decode(s string) (parsed, bool) {
	s = strings.TrimSpace(s)
	var ans rawAnswer
	var locs []rawLoc
	switch {
	case strings.HasPrefix(s, "["):
		if json.Unmarshal([]byte(s), &locs) != nil {
			return parsed{}, false
		}
	case strings.HasPrefix(s, "{"):
		if json.Unmarshal([]byte(s), &ans) != nil {
			return parsed{}, false
		}
		locs = ans.Locations
	default:
		return parsed{}, false
	}
	return build(locs, ans.Explanation)
}

func build(locs []rawLoc, expl string) (parsed, bool) {
	p := parsed{explanation: strings.TrimSpace(expl)}
	for _, r := range locs {
		if l, ok := r.toLocation(); ok {
			p.locs = append(p.locs, l)
		}
	}
	return p, len(p.locs) > 0
}

func parsePlain(s string) (parsed, bool) { return decode(s) }

var fenceRe = regexp.MustCompile("(?s)```[a-zA-Z]*[ \\t]*\\n?(.*?)```")

func parseFenced(s string) (parsed, bool) {
	for _, m := range fenceRe.FindAllStringSubmatch(s, -1) {
		if p, ok := decode(m[1]); ok {
			p = withProse(p, s)
			return p, true
		}
		if p, ok := parseBalanced(m[1]); ok {
			return withProse(p, s), true
		}
	}
	return parsed{}, false
}

// parseBalanced tries every balanced {...} or [...] of the text as an answer.
func parseBalanced(s string) (parsed, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		end := matchClose(s, i)
		if end < 0 {
			// Never closed: everything after it is inside it, so this is
			// a truncated answer (parseTruncated's job), not a balanced one.
			return parsed{}, false
		}
		if p, ok := decode(s[i : end+1]); ok {
			return withProse(p, strings.TrimSpace(s[:i]+" "+s[end+1:])), true
		}
	}
	return parsed{}, false
}

func parseAll(s string) (parsed, bool) {
	if p, ok := parsePlain(s); ok {
		return p, true
	}
	if p, ok := parseFenced(s); ok {
		return p, true
	}
	return parseBalanced(s)
}

// withProse keeps the text around the JSON as explanation when the JSON had
// none, as the paper observed models explain outside the code block.
func withProse(p parsed, around string) parsed {
	if p.explanation == "" {
		p.explanation = clip(strings.TrimSpace(strings.ReplaceAll(around, "```", "")), 1000)
	}
	return p
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// matchClose returns the index of the bracket closing the one at s[start],
// respecting JSON strings, or -1 if it is never closed.
func matchClose(s string, start int) int {
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stripTrailingCommas removes commas directly before } or ] outside strings.
func stripTrailingCommas(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			b.WriteByte(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		} else if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

var explRe = regexp.MustCompile(`"explanation"\s*:\s*"((?:[^"\\]|\\.)*)`)

// parseTruncated salvages the complete location objects of an answer that
// was cut off (token limit): paper C saw models cut up to half of the time.
func parseTruncated(s string) (parsed, bool) {
	k := strings.Index(s, `"locations"`)
	if k < 0 {
		return parsed{}, false
	}
	open := strings.IndexByte(s[k:], '[')
	if open < 0 {
		return parsed{}, false
	}
	var locs []rawLoc
	for i := k + open + 1; i < len(s); i++ {
		switch s[i] {
		case '{':
			end := matchClose(s, i)
			if end < 0 {
				i = len(s) // incomplete last object: dropped
				break
			}
			var r rawLoc
			if json.Unmarshal([]byte(stripTrailingCommas(s[i:end+1])), &r) == nil {
				locs = append(locs, r)
			}
			i = end
		case ']':
			i = len(s)
		}
	}
	expl := ""
	if m := explRe.FindStringSubmatch(s); m != nil {
		var u string
		if json.Unmarshal([]byte(`"`+m[1]+`"`), &u) == nil {
			expl = u
		} else {
			expl = m[1]
		}
	}
	return build(locs, expl)
}

var pathLineRe = regexp.MustCompile("[`'\"(\\s]([A-Za-z0-9_./\\\\-]+\\.go)(?::(\\d+))?")

// parseProse is the last resort: the model ignored the format. It looks for
// known functions and files mentioned in the text (and file.go:line forms).
func parseProse(s string, hint Hint) (parsed, bool) {
	type hit struct {
		pos int
		loc finding.Location
	}
	var hits []hit

	// Known functions: the short name after the last dot, as a whole word.
	for _, fn := range hint.Funcs {
		short := shortFunc(fn)
		if short == "" {
			continue
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(short) + `\b`)
		if loc := re.FindStringIndex(s); loc != nil {
			hits = append(hits, hit{loc[0], finding.Location{Func: fn}})
		}
	}
	// file.go[:line] mentions.
	for _, m := range pathLineRe.FindAllStringSubmatchIndex(s, -1) {
		path := s[m[2]:m[3]]
		l := finding.Location{File: normPath(path)}
		if m[4] >= 0 {
			l.Line, _ = strconv.Atoi(s[m[4]:m[5]])
		}
		hits = append(hits, hit{m[2], l})
	}
	if len(hits) == 0 {
		return parsed{}, false
	}
	// Order by first mention, merge a function with the file named closest
	// before it is not attempted: keep it simple and honest.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].pos < hits[j-1].pos; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	p := parsed{explanation: clip(s, 1000)}
	seen := map[string]bool{}
	for _, h := range hits {
		key := h.loc.File + "|" + h.loc.Func
		if seen[key] {
			continue
		}
		seen[key] = true
		p.locs = append(p.locs, h.loc)
	}
	return p, len(p.locs) > 0
}

func shortFunc(f string) string {
	f = strings.TrimSpace(f)
	if i := strings.LastIndexByte(f, '.'); i >= 0 {
		f = f[i+1:]
	}
	return strings.TrimSuffix(f, "()")
}

// finish applies normalisation and grounding.
func finish(p parsed, tier int, _ string, hint Hint) Result {
	res := Result{Tier: tier, Explanation: p.explanation}
	for _, l := range p.locs {
		if len(hint.Files) > 0 && l.File != "" {
			if k := matchFile(l.File, hint.Files); k != "" {
				l.File = k
			} else {
				res.Ungrounded = append(res.Ungrounded, l)
				continue
			}
		}
		res.Locations = append(res.Locations, l)
	}
	return res
}

// utf8BOM is the byte order mark some servers put before the answer.
const utf8BOM = "\xef\xbb\xbf"
