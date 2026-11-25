package llm

import (
	"strings"

	"gofl/internal/finding"
)

// Scoring helpers for the LLM measurement (cmd/llmeval). They compare what a
// run produced with a ground-truth location. The truth comes from
// testdata/goflsample/bugs.json (layer 1) or the fix commit (layer 2); it is
// read ONLY when scoring, never while ranking.

// Level is the granularity of a comparison.
type Level int

const (
	LevelFile Level = iota
	LevelFunc
	LevelLine
)

func (l Level) String() string {
	return [...]string{"file", "func", "line"}[l]
}

// Truth is one ground-truth location.
type Truth struct {
	File  string // project-relative, slash-separated
	Func  string // e.g. "EmailOf" or "Registry.Inc"
	Lines []int  // accepted lines
}

// NormFunc reduces a function name to a comparable form: package prefix,
// pointer receivers, parentheses, generic suffix and closure suffixes are
// removed. "crash.(*Registry).Inc" -> "Registry.Inc", "Fanout.func1" -> "Fanout".
func NormFunc(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimSuffix(f, "()")
	if i := strings.Index(f, "["); i >= 0 {
		f = f[:i]
	}
	f = strings.NewReplacer("(*", "", "(", "", ")", "", "*", "").Replace(f)
	parts := strings.Split(f, ".")
	// Drop closure suffixes (.func1, .func2.1, .glob..func1).
	for len(parts) > 1 {
		last := parts[len(parts)-1]
		if strings.HasPrefix(last, "func") && isDigits(last[4:]) || isDigits(last) && last != "" {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	// Drop the package: keep "Type.Method" or "Func".
	if len(parts) >= 3 {
		parts = parts[len(parts)-2:]
	} else if len(parts) == 2 && isLower(parts[0]) {
		// "crash.EmailOf": a lower-case first part is a package.
		parts = parts[1:]
	}
	return strings.Join(parts, ".")
}

func isDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isLower(s string) bool {
	return s != "" && s[0] >= 'a' && s[0] <= 'z'
}

// Match reports whether loc is the truth at the given level. Line matches
// within tol lines of any accepted line (tol 0 = exact) and also needs the
// file to match.
func Match(loc finding.Location, t Truth, level Level, tol int) bool {
	if matchFile(loc.File, []string{t.File}) == "" {
		return false
	}
	switch level {
	case LevelFile:
		return true
	case LevelFunc:
		return loc.Func != "" && NormFunc(loc.Func) == NormFunc(t.Func)
	default:
		if loc.Line <= 0 {
			return false
		}
		for _, l := range t.Lines {
			if d := loc.Line - l; d >= -tol && d <= tol {
				return true
			}
		}
		return false
	}
}

// HitAt reports whether the truth is among the first k DISTINCT items of
// locs at the given level (a file named twice counts once).
func HitAt(locs []finding.Location, t Truth, level Level, k, tol int) bool {
	seen := map[string]bool{}
	n := 0
	for _, l := range locs {
		key := l.File
		switch level {
		case LevelFunc:
			key += "|" + NormFunc(l.Func)
		case LevelLine:
			key += "|" + NormFunc(l.Func) + "|" + itoa(l.Line)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		n++
		if n > k {
			return false
		}
		if Match(l, t, level, tol) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// FindingLocations lists the locations of findings in rank order.
func FindingLocations(fs []finding.Finding) []finding.Location {
	out := make([]finding.Location, len(fs))
	for i, f := range fs {
		out[i] = f.Location
	}
	return out
}

// Solved implements the paper's per-case rule (C12): the case counts as
// solved when at least Quorum(len(results)) answers have the truth among
// their first k locations. runs is the number of requests made (failed ones
// count as wrong).
func Solved(results []Result, runs int, t Truth, level Level, k, tol int) bool {
	good := 0
	for _, r := range results {
		if HitAt(r.Locations, t, level, k, tol) {
			good++
		}
	}
	return good >= Quorum(runs)
}

// Metric is a share of cases.
type Metric struct{ Hit, Total int }

// Rate is Hit/Total in percent (0 for no cases).
func (m Metric) Rate() float64 {
	if m.Total == 0 {
		return 0
	}
	return 100 * float64(m.Hit) / float64(m.Total)
}

// Add counts one case.
func (m *Metric) Add(hit bool) {
	m.Total++
	if hit {
		m.Hit++
	}
}
