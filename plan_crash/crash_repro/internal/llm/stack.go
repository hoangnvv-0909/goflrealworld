package llm

import (
	"regexp"
	"strconv"
	"strings"
)

// StackFrame is one frame read from raw stack text.
//
// This is a deliberately light reader used only to choose which files go
// into a prompt and to feed the grounding check. It is NOT crashsig: grouping
// and signatures stay in package crashsig (TV2).
type StackFrame struct {
	Func string
	File string // slash-separated, as printed (usually absolute)
	Line int
}

var (
	fileLineRe = regexp.MustCompile(`^\s+(.+?\.go):(\d+)(?:\s+\+0x[0-9a-fA-F]+)?\s*$`)
	// The name runs up to the LAST "(...)" (the arguments, which hold no
	// parentheses), so methods such as "pkg.(*T).M(0xc0, 0x1)" keep their
	// receiver.
	funcLineRe = regexp.MustCompile(`^(\S+)\([^()]*\)\s*$`)
)

// ParseStackFrames reads the frames of every goroutine in stack, in order.
// A frame is a function line followed by a tab-indented "file:line" line.
func ParseStackFrames(stack string) []StackFrame {
	var frames []StackFrame
	lines := strings.Split(strings.ReplaceAll(stack, "\r\n", "\n"), "\n")
	for i := 0; i+1 < len(lines); i++ {
		fm := funcLineRe.FindStringSubmatch(lines[i])
		if fm == nil {
			continue
		}
		lm := fileLineRe.FindStringSubmatch(lines[i+1])
		if lm == nil {
			continue
		}
		n, _ := strconv.Atoi(lm[2])
		frames = append(frames, StackFrame{
			Func: fm[1],
			File: strings.ReplaceAll(lm[1], `\`, "/"),
			Line: n,
		})
		i++
	}
	return frames
}

// IsRuntimeFrame reports frames of the Go runtime, the testing package and
// the standard library (they are never the place to fix).
func IsRuntimeFrame(f StackFrame) bool {
	switch {
	case strings.HasPrefix(f.Func, "runtime."),
		strings.HasPrefix(f.Func, "testing."),
		f.Func == "panic":
		return true
	}
	return strings.Contains(f.File, "/src/runtime/") ||
		strings.Contains(f.File, "/src/testing/") ||
		strings.HasSuffix(f.File, "_test.go")
}

// UserFrames returns the frames that are not runtime, testing or test files,
// crash point first.
func UserFrames(stack string) []StackFrame {
	var out []StackFrame
	for _, f := range ParseStackFrames(stack) {
		if !IsRuntimeFrame(f) {
			out = append(out, f)
		}
	}
	return out
}

// matchFile maps a path seen in a stack or in a model answer to one of the
// known project-relative paths. Matching is by path suffix on a "/" boundary,
// in either direction. It returns "" when nothing matches.
func matchFile(path string, known []string) string {
	path = normPath(path)
	if path == "" {
		return ""
	}
	for _, k := range known {
		if normPath(k) == path {
			return k
		}
	}
	// path is longer than a known file ("/abs/dir/crash/hard.go" for
	// "crash/hard.go"): the longest such known file wins.
	best := ""
	for _, k := range known {
		if strings.HasSuffix(path, "/"+normPath(k)) && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return best
	}
	// path is shorter ("hard.go" for "crash/hard.go"): only if unambiguous.
	var found []string
	for _, k := range known {
		if strings.HasSuffix(normPath(k), "/"+path) {
			found = append(found, k)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

func normPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	p = strings.TrimPrefix(p, "./")
	return p
}
