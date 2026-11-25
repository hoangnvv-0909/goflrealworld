// Package crashsig parses Go panic stacks into normalised signatures.
//
// Owner: TV2. Paper: C, section II-A (claims C1-C3 in the reading guide of
// phan_cong_fault_localization_v2.tex).
//
// Not tied to any application: input comes from "go test" output, process
// stderr or runtime/debug.Stack(). Frames of the panic machinery
// (runtime.gopanic, runtime.sigpanic, testing.tRunner, ...) must be dropped,
// otherwise every crash gets the same signature.
//
// Pipeline for one crash: Parse (raw frames, crash point first) ->
// Relativize (paths relative to the project) -> Normalize (differences between
// runs removed) -> Hash / Subsumes (grouping levels 2 and 3).
package crashsig

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotImplemented is kept for source compatibility with the skeleton.
var ErrNotImplemented = errors.New("crashsig: not implemented yet")

// ErrNoPanic means the input holds no "panic:" or "fatal error:" line.
var ErrNoPanic = errors.New("crashsig: no panic or fatal error in input")

// Frame is one stack frame. File is slash-separated.
type Frame struct {
	Func string
	File string
	Line int
}

// Signature is a normalised crash stack.
type Signature struct {
	Exception    string  // e.g. "runtime error: index out of range [3] with length 3"
	Frames       []Frame // crash point first
	NoCrashPoint bool    // true if no frame after panic/sigpanic was found

	// Additions that do not take part in Hash or Subsumes.

	Fatal     bool   // "fatal error:" (unrecoverable runtime failure), not "panic:"
	Elided    bool   // the traceback had a "...N frames elided..." line
	CreatedBy *Frame // "created by" frame of the crashing goroutine, if printed
	Raw       string // the stack text as read, for prompts and reports
}

var (
	goroutineRe = regexp.MustCompile(`^goroutine \d+ \[[^\]]*\]:?\s*$`)
	fileLineRe  = regexp.MustCompile(`^\s+(.+?):(\d+)(?:\s+\+0x[0-9a-fA-F]+)?\s*$`)
	recoveredRe = regexp.MustCompile(`\s*\[recovered[^\]]*\]\s*$`)
	elidedRe    = regexp.MustCompile(`^\.\.\.\d+ frames elided\.\.\.$`)
	createdRe   = regexp.MustCompile(`^created by (\S+)`)
	// net/http logs "http: panic serving 127.0.0.1:5000: <message>" when it
	// recovers a handler panic; the client address is dropped.
	httpPanicRe = regexp.MustCompile(`(?:^|\s)http: panic serving \S+: (.*)$`)
)

// Parse reads one panic stack. Only the first goroutine after the panic line
// (the crashing one, as the Go runtime prints it) is used; other goroutines
// are ignored. Panic machinery frames at the top and runtime/testing frames at
// the bottom are dropped, so Frames[0] is the crash point.
func Parse(stack []byte) (Signature, error) {
	text := strings.ReplaceAll(string(stack), "\r\n", "\n")
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	sig := Signature{Raw: strings.TrimRight(text, "\n")}
	i := 0
	found := false
	for ; i < len(lines); i++ {
		l := lines[i]
		switch m := httpPanicRe.FindStringSubmatch(l); {
		case strings.HasPrefix(l, "panic: "):
			sig.Exception, sig.Fatal, found = strings.TrimPrefix(l, "panic: "), false, true
		case strings.HasPrefix(l, "fatal error: "):
			sig.Exception, sig.Fatal, found = strings.TrimPrefix(l, "fatal error: "), true, true
		case m != nil:
			sig.Exception, sig.Fatal, found = m[1], false, true
		}
		if found {
			i++
			break
		}
	}
	if !found {
		// runtime/debug.Stack() prints no "panic:" line, only the goroutine
		// block. Accept it with an empty Exception.
		i = 0
		for i < len(lines) && !goroutineRe.MatchString(lines[i]) {
			i++
		}
		if i == len(lines) {
			return Signature{}, ErrNoPanic
		}
	}
	// Nested panics: "panic: A [recovered]\n\tpanic: B" - the last one is the
	// exception that killed the program.
	for ; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "panic: ") && strings.HasPrefix(l, "\t") {
			sig.Exception = strings.TrimPrefix(t, "panic: ")
			continue
		}
		if goroutineRe.MatchString(l) {
			break
		}
	}
	sig.Exception = strings.TrimSpace(recoveredRe.ReplaceAllString(sig.Exception, ""))

	if i >= len(lines) { // no goroutine header: nothing to locate
		sig.NoCrashPoint = true
		return sig, nil
	}
	i++ // skip the goroutine header
	var frames []Frame
	for ; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" || goroutineRe.MatchString(l) {
			break
		}
		if elidedRe.MatchString(strings.TrimSpace(l)) {
			sig.Elided = true
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			continue // file line without a function line: ignore
		}
		if m := createdRe.FindStringSubmatch(l); m != nil {
			f := Frame{Func: m[1]}
			if i+1 < len(lines) {
				if fm := fileLineRe.FindStringSubmatch(lines[i+1]); fm != nil {
					f.File, f.Line = slash(fm[1]), atoi(fm[2])
					i++
				}
			}
			sig.CreatedBy = &f
			continue
		}
		name := funcName(l)
		if name == "" {
			continue
		}
		f := Frame{Func: name}
		if i+1 < len(lines) {
			if fm := fileLineRe.FindStringSubmatch(lines[i+1]); fm != nil {
				f.File, f.Line = slash(fm[1]), atoi(fm[2])
				i++
			}
		}
		frames = append(frames, f)
	}

	// Drop panic machinery at the top, runtime/testing frames at the bottom.
	// Everything above the LAST "panic" frame is a recover handler
	// (runtime/debug.Stack, testing.tRunner.func1.2, net/http serve.func1).
	start := 0
	for k, f := range frames {
		if f.Func == "panic" {
			start = k + 1
		}
	}
	for start < len(frames) && isMachinery(frames[start]) {
		start++
	}
	end := len(frames)
	for end > start && isMachinery(frames[end-1]) {
		end--
	}
	sig.Frames = frames[start:end]
	sig.NoCrashPoint = len(sig.Frames) == 0
	return sig, nil
}

// funcName strips the argument list from a function line:
// "pkg.(*T).M(0xc000, 0x1)" -> "pkg.(*T).M", "pkg.f[...](...)" -> "pkg.f[...]".
func funcName(l string) string {
	l = strings.TrimSpace(l)
	if !strings.HasSuffix(l, ")") {
		return ""
	}
	depth := 0
	for i := len(l) - 1; i >= 0; i-- {
		switch l[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return strings.TrimSpace(l[:i])
			}
		}
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func slash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// stdlibFileRe matches files of the Go installation: ".../go/src/runtime/x.go".
// The directory after src must not contain a dot, which keeps GOPATH projects
// ("src/github.com/me/p") out.
var stdlibFileRe = regexp.MustCompile(`(?i)(^|/)go[0-9.\-]*/src/[^./]+(/|$)`)

// IsStdlibFile tells whether file is not project code: the Go installation
// (also the "<goroot>" placeholder used in sanitised logs) or a dependency
// (module cache, vendor directory). Such frames are never a crash point and
// never ranked.
func IsStdlibFile(file string) bool {
	file = slash(file)
	switch {
	case file == "<autogenerated>",
		strings.HasPrefix(file, "$GOROOT/"), strings.HasPrefix(file, "<goroot>/"),
		strings.HasPrefix(file, "<modcache>/"), strings.Contains(file, "/pkg/mod/"),
		strings.HasPrefix(file, "vendor/"), strings.Contains(file, "/vendor/"):
		return true
	}
	return stdlibFileRe.MatchString(file)
}

// isMachinery reports frames of the panic and test machinery.
func isMachinery(f Frame) bool {
	p := pkgOf(f.Func)
	switch {
	case f.Func == "panic" || f.Func == "":
		return true
	case p == "runtime" || p == "testing" || strings.HasPrefix(p, "internal/runtime/"):
		return true
	}
	return IsStdlibFile(f.File)
}

// pkgOf returns the import path part of a function name:
// "goflsample/crash.(*Node).Total" -> "goflsample/crash".
func pkgOf(fn string) string {
	slashAt := strings.LastIndex(fn, "/")
	dot := strings.Index(fn[slashAt+1:], ".")
	if dot < 0 {
		return fn
	}
	return fn[:slashAt+1+dot]
}

// PackageOf is pkgOf for other packages.
func PackageOf(fn string) string { return pkgOf(fn) }

// Relativize rewrites file paths to be relative to the project, so the same
// fault gives the same file on every machine. It tries, per frame: the
// absolute root prefix; then the module path (the package path of the
// function minus the module path gives the directory). Frames that match
// neither keep their path.
func Relativize(s Signature, root, modulePath string) Signature {
	root = strings.TrimRight(slash(root), "/")
	out := s
	out.Frames = make([]Frame, len(s.Frames))
	for i, f := range s.Frames {
		out.Frames[i] = relFrame(f, root, modulePath)
	}
	if s.CreatedBy != nil {
		c := relFrame(*s.CreatedBy, root, modulePath)
		out.CreatedBy = &c
	}
	return out
}

func relFrame(f Frame, root, modulePath string) Frame {
	file := slash(f.File)
	if file == "" || IsStdlibFile(file) {
		f.File = file
		return f
	}
	if strings.HasPrefix(file, "<root>/") { // placeholder used in sanitised logs
		f.File = strings.TrimPrefix(file, "<root>/")
		return f
	}
	if root != "" && len(file) > len(root) && strings.EqualFold(file[:len(root)], root) && file[len(root)] == '/' {
		f.File = file[len(root)+1:]
		return f
	}
	if modulePath != "" {
		p := pkgOf(f.Func)
		if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
			dir := strings.TrimPrefix(strings.TrimPrefix(p, modulePath), "/")
			f.File = path.Join(dir, path.Base(file))
			return f
		}
	}
	f.File = file
	return f
}

var (
	genericRe = regexp.MustCompile(`\[[^\[\]]*\]`)
	closureRe = regexp.MustCompile(`(\.(?:func|gowrap|deferwrap)\d+(?:\.\d+)*)+$`)
	hexRe     = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	numRe     = regexp.MustCompile(`\d+`)
)

// NormFunc removes generic instantiation suffixes and closure suffixes:
// "pkg.must[...]" -> "pkg.must", "pkg.Fanout.func1" -> "pkg.Fanout".
func NormFunc(fn string) string {
	fn = genericRe.ReplaceAllString(fn, "")
	return closureRe.ReplaceAllString(fn, "")
}

// Normalize removes details that differ between runs of the same fault
// (level 2 grouping: goroutine ids, addresses, generated suffixes).
// Line numbers are kept (they are part of Hash). Consecutive frames of the
// same function at the same line (inlined copies, recursion) collapse to one.
// Normalize is idempotent.
func Normalize(s Signature) Signature {
	out := s
	out.Exception = NormException(s.Exception)
	out.Frames = make([]Frame, 0, len(s.Frames))
	for _, f := range s.Frames {
		n := Frame{Func: NormFunc(f.Func), File: slash(f.File), Line: f.Line}
		if k := len(out.Frames); k > 0 && out.Frames[k-1] == n {
			continue
		}
		out.Frames = append(out.Frames, n)
	}
	if s.CreatedBy != nil {
		c := Frame{Func: NormFunc(s.CreatedBy.Func), File: slash(s.CreatedBy.File), Line: s.CreatedBy.Line}
		out.CreatedBy = &c
	}
	return out
}

// NormException replaces addresses and numbers in the message by "#", so
// "index out of range [3] with length 3" and "[7] with length 2" are equal.
func NormException(e string) string {
	e = hexRe.ReplaceAllString(e, "#")
	return numRe.ReplaceAllString(e, "#")
}

// Hash is a stable identity for a normalised signature: exception, then every
// frame with function, file and line. Stable across runs and operating
// systems (paths are slash-separated, no maps are iterated).
func Hash(s Signature) string {
	var b bytes.Buffer
	b.WriteString(s.Exception)
	for _, f := range s.Frames {
		b.WriteByte('\n')
		b.WriteString(f.Func)
		b.WriteByte('|')
		b.WriteString(slash(f.File))
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(f.Line))
	}
	sum := sha1.Sum(b.Bytes())
	return hex.EncodeToString(sum[:8])
}

// Subsumes reports whether a's frames contain b's frames (level 3 grouping,
// function names only, line numbers ignored).
//
// Decision (TV1, week 1, claim C3; see docs/CONTRACT.md section 5): the
// frames of b must appear in a as one CONTIGUOUS run, in the same order.
// Paper C only says "structurally contained" and compares fully qualified
// method names; it does not say whether gaps are allowed. Contiguous is the
// conservative reading: level 4 already merges groups sharing the crash-point
// file, so level 3 should not add false merges that pollute IBF. Revisit
// after the layer-2 measurement; this function is the only place to change.
// Both arguments are expected to be already Normalize()d.
func Subsumes(a, b Signature) bool {
	if len(b.Frames) == 0 || len(b.Frames) > len(a.Frames) {
		return false
	}
	for start := 0; start+len(b.Frames) <= len(a.Frames); start++ {
		ok := true
		for j, f := range b.Frames {
			if a.Frames[start+j].Func != f.Func {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// CrashPoint returns the first frame, if any.
func (s Signature) CrashPoint() (Frame, bool) {
	if len(s.Frames) == 0 {
		return Frame{}, false
	}
	return s.Frames[0], true
}
