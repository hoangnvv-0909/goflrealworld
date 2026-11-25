package logprofile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"regexp"
	"strings"
	"time"

	"gofl/internal/crashrank"
	"gofl/internal/crashsig"
)

// Default reads standard Go panic and fatal-error stacks embedded in a log
// (plain "go test" output, stderr captures, container logs with a timestamp
// in front of every line).
//
// How a block is found: a line that, after an optional timestamp, log level
// and container prefix, starts with "panic: " or "fatal error: " opens a
// block. The block continues over stack lines (indented lines, "goroutine N",
// "[signal ...]", function lines "pkg.F(args)", "created by", "...N frames
// elided...", nested "panic:") and over blank lines that are followed by
// "goroutine". Anything else (a new log line, "FAIL", "exit status") closes
// it. Each block goes to Parse (crashsig.Parse by default) and becomes one
// crashrank.Event. A block that does not parse yields an error and the scan
// goes on.
//
// Event.Time is the timestamp of the panic line, or else of the closest
// earlier line that has one, or zero.
//
// Memory: the log is read line by line; one block (at most MaxBlockLines
// lines of MaxLineBytes) is held at a time.
type Default struct {
	// ID is the profile name; "" means DefaultName.
	ID string
	// Source is copied to Event.Source (e.g. the log file name). Default "log".
	Source string
	// MaxBlockLines caps one block; the rest of a longer block is dropped.
	MaxBlockLines int
	// MaxLineBytes caps one line; the rest of a longer line is dropped.
	MaxLineBytes int
	// Parse turns one block into a signature. Nil means crashsig.Parse.
	Parse func([]byte) (crashsig.Signature, error)
	// StripPrefix, if set, is removed from the start of every line before
	// the built-in decorations (a custom profile, see Load).
	StripPrefix *regexp.Regexp
}

// NewDefault returns the default profile with its limits set.
func NewDefault() *Default {
	return &Default{Source: "log", MaxBlockLines: 4000, MaxLineBytes: 1 << 20}
}

// Name implements Profile.
func (d *Default) Name() string {
	if d.ID != "" {
		return d.ID
	}
	return DefaultName
}

// Decorations removed in front of a log message. Separators are spaces only,
// never \s, so the tab that starts the file line of a frame survives.
// Levels never include "panic" or "fatal": Go's own "panic: " and
// "fatal error: " lines must stay.
var (
	levelRe   = regexp.MustCompile(`(?i)^\[?(?:trace|debug|info|warn(?:ing)?|error|crit(?:ical)?)\]?:? +`)
	kvLevelRe = regexp.MustCompile(`^level=\w+ +`)
	// "api-1  | " in front of every line of "docker compose logs".
	composeRe = regexp.MustCompile(`^[\w.-]+ +\| ?`)
)

// Entries implements Profile.
func (d *Default) Entries(r io.Reader, loc *time.Location) iter.Seq2[crashrank.Event, error] {
	return func(yield func(crashrank.Event, error) bool) {
		s := &scan{d: d, loc: loc, yield: yield}
		s.defaults()
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			line, err := readLine(br, s.maxLine)
			if err != nil {
				if err != io.EOF {
					s.flush()
					if !s.stop {
						yield(crashrank.Event{}, fmt.Errorf("logprofile: reading log: %w", err))
					}
					return
				}
				s.flush()
				return
			}
			s.lineNo++
			s.feed(line)
			if s.stop {
				return
			}
		}
	}
}

// readLine reads one line without its line ending (LF or CRLF), keeping at
// most max bytes. Long lines are consumed fully so the stream stays in step.
func readLine(br *bufio.Reader, max int) (string, error) {
	var out []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if room := max - len(out); room > 0 {
			if len(chunk) > room {
				chunk = chunk[:room]
			}
			out = append(out, chunk...)
		}
		if err != nil {
			return string(out), err
		}
		if !isPrefix {
			return string(out), nil
		}
	}
}

// scan is the state of one Entries call.
type scan struct {
	d     *Default
	loc   *time.Location
	yield func(crashrank.Event, error) bool

	maxLine, maxLines int
	source            string
	parse             func([]byte) (crashsig.Signature, error)

	lineNo int
	lastTS time.Time
	stop   bool

	in           bool
	prefixed     bool // lines of the block carry a timestamp prefix
	pendingBlank bool
	start        int
	ts           time.Time
	buf          bytes.Buffer
	nLines       int
	sawGoroutine bool // the block already has a "goroutine N [...]:" header
}

func (s *scan) defaults() {
	s.maxLine, s.maxLines, s.source, s.parse = s.d.MaxLineBytes, s.d.MaxBlockLines, s.d.Source, s.d.Parse
	if s.maxLine <= 0 {
		s.maxLine = 1 << 20
	}
	if s.maxLines <= 0 {
		s.maxLines = 4000
	}
	if s.source == "" {
		s.source = "log"
	}
	if s.parse == nil {
		s.parse = crashsig.Parse
	}
	if s.loc == nil {
		s.loc = time.UTC
	}
}

func (s *scan) feed(line string) {
	if t := strings.TrimSpace(line); len(t) > 1 && t[0] == '{' && t[len(t)-1] == '}' {
		// Structured log (zap, slog, logrus JSON): one object per line.
		s.flush()
		if !s.stop {
			s.jsonLine(t)
		}
		return
	}
	if !s.in {
		s.outside(line)
		return
	}
	// Go writes stacks to stderr undecorated, so the raw line is tried
	// first; the logger's decorations are removed only when the block
	// started with them and the raw line is not part of a stack.
	l := line
	if s.prefixed && strings.TrimSpace(l) != "" && !isStackLine(l) {
		_, l, _, _ = s.strip(line)
	}
	if strings.TrimSpace(l) == "" {
		if s.pendingBlank { // two blank lines: the block is over
			s.flush()
			return
		}
		s.pendingBlank = true
		return
	}
	if s.pendingBlank {
		if !strings.HasPrefix(l, "goroutine ") {
			s.flush()
			s.outside(line)
			return
		}
		s.pendingBlank = false
		s.add("")
	}
	// A nested panic ("panic: A [recovered]", then an indented "panic: B") is printed
	// before the first goroutine header. A panic or fatal error after a
	// header is a new crash, e.g. a goroutine panic that kills the server
	// right after a recovered "http: panic serving" stack.
	if s.sawGoroutine && (strings.HasPrefix(l, "panic: ") || strings.HasPrefix(l, "fatal error: ")) {
		s.flush()
		s.outside(line)
		return
	}
	if !isStackLine(l) {
		s.flush()
		s.outside(line)
		return
	}
	s.add(l)
}

// strip removes the decorations a logger puts in front of a message, in
// this order: the profile's own StripPrefix, a "docker compose" service
// name, a timestamp (with a container runtime tag), a log level. decorated
// says whether anything was removed.
func (s *scan) strip(line string) (ts time.Time, rest string, tsOK, decorated bool) {
	rest = line
	if re := s.d.StripPrefix; re != nil {
		if m := re.FindStringIndex(rest); m != nil && m[0] == 0 {
			rest = rest[m[1]:]
		}
	}
	rest = composeRe.ReplaceAllString(rest, "")
	ts, rest, tsOK = parseTimestamp(rest, s.loc)
	rest = levelRe.ReplaceAllString(rest, "")
	rest = kvLevelRe.ReplaceAllString(rest, "")
	return ts, rest, tsOK, tsOK || rest != line
}

func isCrashStart(l string) bool {
	// "http: panic serving ADDR: msg" is what net/http logs when a handler
	// panics and the server recovers; the stack follows on the next lines.
	return strings.HasPrefix(l, "panic: ") || strings.HasPrefix(l, "fatal error: ") ||
		strings.HasPrefix(l, "http: panic serving ")
}

// jsonStackFields are the fields of a JSON log line that may hold a stack.
var jsonStackFields = []string{"stack", "stacktrace", "stack_trace", "panic", "error", "err", "msg", "message"}

// jsonLine reads one JSON log line. A field holding a goroutine dump is
// scanned like a log of its own; when the dump has no "panic:" line, the
// message field stands in for it so the exception is not lost. Events take
// the time of the JSON line.
func (s *scan) jsonLine(line string) {
	ts, _, tsOK := parseTimestamp(line, s.loc)
	if tsOK {
		s.lastTS = ts
	}
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return
	}
	msg := ""
	for _, k := range []string{"msg", "message", "error", "err"} {
		if v, ok := m[k].(string); ok && v != "" && !strings.Contains(v, "goroutine ") {
			msg = v
			break
		}
	}
	for _, k := range jsonStackFields {
		text, ok := m[k].(string)
		if !ok || !strings.Contains(text, "goroutine ") {
			continue
		}
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if !strings.Contains("\n"+text, "\npanic: ") && !strings.Contains("\n"+text, "\nfatal error: ") {
			if msg == "" {
				msg = "unknown panic"
			}
			text = "panic: " + msg + "\n\n" + text
		}
		outer := s.lineNo
		inner := &scan{d: s.d, loc: s.loc, yield: func(ev crashrank.Event, err error) bool {
			if err != nil {
				err = fmt.Errorf("logprofile: JSON log line %d: %w", outer, err)
			} else {
				ev.Time = s.lastTS
			}
			s.stop = !s.yield(ev, err)
			return !s.stop
		}}
		inner.defaults()
		for _, l := range strings.Split(text, "\n") {
			inner.lineNo++
			inner.feed(l)
			if inner.stop {
				return
			}
		}
		inner.flush()
		return
	}
}

func (s *scan) outside(line string) {
	ts, rest, ok, decorated := s.strip(line)
	if ok {
		s.lastTS = ts
	}
	if !isCrashStart(rest) {
		return
	}
	s.in, s.prefixed, s.pendingBlank = true, decorated, false
	s.start = s.lineNo
	s.ts = s.lastTS
	s.buf.Reset()
	s.nLines = 0
	s.sawGoroutine = false
	s.add(rest)
}

func (s *scan) add(l string) {
	if s.nLines >= s.maxLines {
		return
	}
	s.nLines++
	if strings.HasPrefix(l, "goroutine ") {
		s.sawGoroutine = true
	}
	s.buf.WriteString(l)
	s.buf.WriteByte('\n')
}

// flush ends the current block, if any, and yields its event.
func (s *scan) flush() {
	if !s.in {
		return
	}
	s.in = false
	block := append([]byte(nil), s.buf.Bytes()...)
	sig, err := s.parse(block)
	if err != nil {
		s.stop = !s.yield(crashrank.Event{}, fmt.Errorf("logprofile: block at line %d: %w", s.start, err))
		return
	}
	s.stop = !s.yield(crashrank.Event{Time: s.ts, Signature: sig, Source: s.source}, nil)
}

var funcLine = regexp.MustCompile(`^[^\s(]+\(.*\)\s*$`)

// isStackLine tells whether l can belong to a panic block.
func isStackLine(l string) bool {
	switch {
	case l[0] == ' ' || l[0] == '\t':
		return true
	case strings.HasPrefix(l, "goroutine "),
		strings.HasPrefix(l, "[signal"),
		strings.HasPrefix(l, "..."),
		strings.HasPrefix(l, "created by "),
		strings.HasPrefix(l, "panic: "),
		strings.HasPrefix(l, "fatal error: "),
		strings.HasPrefix(l, "runtime: "),
		strings.HasPrefix(l, "runtime stack:"):
		return true
	}
	return funcLine.MatchString(l)
}
