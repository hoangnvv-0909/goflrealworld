package benchrun

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"gofl/internal/contract"
)

// result is one benchmark result line.
type result struct {
	Name    string // full name without the -GOMAXPROCS suffix
	N       int64
	NsPerOp float64
}

// scanner reads the output of a test binary run with -test.v as it streams
// by, and keeps only what gofl needs: the results, whether the benchmark
// skipped or failed, and the last lines for error messages.
type scanner struct {
	partial  []byte
	last     string // last benchmark name seen
	results  []result
	skipped  bool
	failed   []string // "--- FAIL" and "panic:" lines, with the message that follows
	reason   int      // indented lines still to append to the last failed entry
	indented []string // the indented lines just seen (at most two)
	tail     []string
}

const tailLines = 40

var (
	resultRE = regexp.MustCompile(`(?:^|\s)(\d+)\s+(\d+(?:\.\d+)?(?:[eE][+-]?\d+)?) ns/op`)
	procsRE  = regexp.MustCompile(`-\d+$`)
)

func (s *scanner) Write(p []byte) (int, error) {
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		s.line(string(s.partial[:i]))
		s.partial = s.partial[i+1:]
	}
	if len(s.partial) > 1<<20 { // a huge line without newline: do not keep it all
		s.line(string(s.partial))
		s.partial = nil
	}
	return len(p), nil
}

// flush handles a last line without a newline.
func (s *scanner) flush() {
	if len(s.partial) > 0 {
		s.line(string(s.partial))
		s.partial = nil
	}
}

func (s *scanner) line(l string) {
	l = strings.TrimRight(l, "\r")
	s.tail = append(s.tail, l)
	if len(s.tail) > tailLines {
		s.tail = s.tail[1:]
	}
	// The message of b.Fatal or b.Skip is indented. With -test.v it comes
	// before the "--- FAIL" line, without it after.
	trim := strings.TrimSpace(l)
	indented := trim != "" && (l[0] == ' ' || l[0] == '\t')
	switch {
	case strings.HasPrefix(trim, "--- SKIP: "):
		s.skipped = true
		s.reason = 0
	case strings.HasPrefix(trim, "--- FAIL: "), strings.HasPrefix(trim, "panic: "):
		s.reason = 0
		if len(s.failed) < 5 {
			entry := trim
			for _, m := range s.indented {
				entry += " | " + m
			}
			s.failed = append(s.failed, entry)
			if len(s.indented) == 0 {
				s.reason = 2 // take the next indented lines instead
			}
		}
	case indented && s.reason > 0:
		s.failed[len(s.failed)-1] += " | " + trim
		s.reason--
	case !indented:
		s.reason = 0
	}
	if indented {
		if len(s.indented) < 2 {
			s.indented = append(s.indented, trim)
		}
	} else {
		s.indented = s.indented[:0]
	}
	m := resultRE.FindStringSubmatch(l)
	if strings.HasPrefix(l, "Benchmark") {
		f0 := strings.Fields(l)[0]
		name := f0
		switch {
		case s.last != "" && strings.HasPrefix(f0, s.last+"-") && digits(f0[len(s.last)+1:]):
			name = s.last // result line of the name printed by -test.v
		case m != nil:
			name = procsRE.ReplaceAllString(f0, "")
		}
		s.last = name
	}
	if m == nil || s.last == "" {
		return
	}
	n, err1 := strconv.ParseInt(m[1], 10, 64)
	ns, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return
	}
	s.results = append(s.results, result{Name: s.last, N: n, NsPerOp: ns})
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// sizePattern is the -test.bench pattern that selects the sub-benchmarks of
// bench for one size. The size must appear in the sub-benchmark name as a
// whole token: 100 matches n=100, size_100 and 100, but not 1000. A size that
// is not a number (small, large) must be delimited by non-alphanumerics.
func sizePattern(bench, size string) string {
	edge := "[^0-9]"
	if !digits(size) {
		edge = "[^0-9A-Za-z]"
	}
	return "^" + regexp.QuoteMeta(bench) + "$/^(?:.*" + edge + ")?" + regexp.QuoteMeta(size) + "(?:" + edge + ".*)?$"
}

// classifyBuild maps the output of a failed go list or go test -c to an
// error code.
func classifyBuild(text string) contract.Code {
	low := strings.ToLower(text)
	for _, h := range toolchainHints {
		if strings.Contains(low, h) {
			return contract.NoGo
		}
	}
	for _, h := range moduleHints {
		if strings.Contains(low, h) {
			return contract.NoModule
		}
	}
	return contract.BuildFail
}

var toolchainHints = []string{
	"requires go >=",
	"go.mod requires go",
	"toolchain not available",
	"downloading go1",
}

var moduleHints = []string{
	"go.mod file not found",
	"cannot find main module",
	"not in a module",
	"missing go.sum entry",
	"no required module provides package",
	"cannot find module providing package",
	"updates to go.mod needed",
	"inconsistent vendoring",
	"checksum mismatch",
	"verifying module",
	"unknown revision",
	"no matching versions",
	"module lookup disabled",
	"malformed module path",
	"is not in std",
	"dial tcp",
	"i/o timeout",
	"proxy.golang.org",
	"reading http",
	"go.mod:",
}

// firstError picks the line of a go command's output that says what went
// wrong, skipping "# package" headers and download progress.
func firstError(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "go: downloading") || strings.HasPrefix(l, "go: finding") {
			continue
		}
		return l
	}
	return "no output"
}
