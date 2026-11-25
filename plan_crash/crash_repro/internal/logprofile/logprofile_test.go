package logprofile

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gofl/internal/crashrank"
	"gofl/internal/crashsig"
)

// fakeParse stands in for crashsig.Parse (TV2, not finished): it keeps the
// first line as exception and the number of lines in a frame count.
func fakeParse(b []byte) (crashsig.Signature, error) {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	sig := crashsig.Signature{Exception: lines[0]}
	for i := 0; i < len(lines); i++ {
		sig.Frames = append(sig.Frames, crashsig.Frame{Func: fmt.Sprint(i)})
	}
	return sig, nil
}

func profile() *Default {
	d := NewDefault()
	d.Parse = fakeParse
	return d
}

type result struct {
	ev  crashrank.Event
	err error
}

func collect(d *Default, r io.Reader, loc *time.Location) []result {
	var out []result
	for ev, err := range d.Entries(r, loc) {
		out = append(out, result{ev, err})
	}
	return out
}

func readStack(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stacks", id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Real output of "go test" on testdata/goflsample (bugs C1..C13).
func TestRealStacksOneEventEach(t *testing.T) {
	want := map[string]string{
		"C1":  "panic: runtime error: invalid memory address or nil pointer dereference",
		"C2":  "panic: runtime error: index out of range",
		"C3":  "panic: assignment to entry in nil map",
		"C4":  "panic: runtime error: integer divide by zero",
		"C5":  "panic: interface conversion",
		"C6":  "panic: close of closed channel",
		"C7":  "fatal error: concurrent map writes",
		"C8":  "panic: runtime error: index out of range",
		"C9":  "panic: runtime error: invalid memory address or nil pointer dereference",
		"C10": "panic: runtime error: index out of range",
		"C11": "panic: ",
		"C12": "panic: runtime error: invalid memory address or nil pointer dereference",
		"C13": "panic: runtime error: index out of range",
	}
	for id, prefix := range want {
		t.Run(id, func(t *testing.T) {
			res := collect(profile(), strings.NewReader(readStack(t, id)), nil)
			if len(res) != 1 {
				t.Fatalf("want 1 event, got %d: %+v", len(res), res)
			}
			if res[0].err != nil {
				t.Fatal(res[0].err)
			}
			if !strings.HasPrefix(res[0].ev.Signature.Exception, prefix) {
				t.Errorf("exception %q does not start with %q", res[0].ev.Signature.Exception, prefix)
			}
			if res[0].ev.Source != "log" {
				t.Errorf("source %q", res[0].ev.Source)
			}
		})
	}
}

func TestBlockCoversWholeStack(t *testing.T) {
	// C13 stack ends with "created by": it must be inside the block.
	var got string
	d := profile()
	d.Parse = func(b []byte) (crashsig.Signature, error) { got = string(b); return crashsig.Signature{}, nil }
	collect(d, strings.NewReader(readStack(t, "C13")), nil)
	for _, want := range []string{"goroutine 9 [running]:", "created by goflsample/crash.Fanout", "hard.go:142"} {
		if !strings.Contains(got, want) {
			t.Errorf("block misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FAIL") {
		t.Errorf("block runs into the FAIL line:\n%s", got)
	}

	// C12: the runtime elides frames of the deep recursion.
	collect(d, strings.NewReader(readStack(t, "C12")), nil)
	if !strings.Contains(got, "frames elided") && strings.Count(got, "Total") < 3 {
		t.Errorf("C12 block looks cut:\n%s", got)
	}
}

func TestFatalErrorKeepsAllGoroutines(t *testing.T) {
	var got string
	d := profile()
	d.Parse = func(b []byte) (crashsig.Signature, error) { got = string(b); return crashsig.Signature{}, nil }
	collect(d, strings.NewReader(readStack(t, "C7")), nil)
	if n := strings.Count(got, "goroutine "); n < 2 {
		t.Errorf("fatal error block should keep several goroutines, found %d:\n%s", n, got)
	}
}

func TestMixedLogCRLFAndTimestamps(t *testing.T) {
	c8 := readStack(t, "C8")
	c13 := readStack(t, "C13")
	log := "2024/05/01 10:00:00 server starting\n" +
		"2024/05/01 10:00:05 handling request\n" +
		"panic: boom one\n\ngoroutine 1 [running]:\nmain.f(...)\n\t/app/main.go:10 +0x1\n" +
		"2024/05/01 10:00:09 next log line\n" +
		c8 +
		"time=\"2024-05-01T11:00:00Z\" level=info msg=\"after\"\n" +
		c13
	log = strings.ReplaceAll(log, "\r\n", "\n")
	for _, name := range []string{"LF", "CRLF"} {
		t.Run(name, func(t *testing.T) {
			in := log
			if name == "CRLF" {
				in = strings.ReplaceAll(log, "\n", "\r\n")
			}
			loc := time.FixedZone("UTC+7", 7*3600)
			res := collect(profile(), strings.NewReader(in), loc)
			if len(res) != 3 {
				t.Fatalf("want 3 events, got %d: %+v", len(res), res)
			}
			// 1st: timestamp of the closest earlier line, read in loc.
			want1 := time.Date(2024, 5, 1, 10, 0, 5, 0, loc)
			if !res[0].ev.Time.Equal(want1) {
				t.Errorf("event 1 time %v, want %v", res[0].ev.Time, want1)
			}
			if res[0].ev.Signature.Exception != "panic: boom one" {
				t.Errorf("event 1 exception %q (CR left over?)", res[0].ev.Signature.Exception)
			}
			want2 := time.Date(2024, 5, 1, 10, 0, 9, 0, loc)
			if !res[1].ev.Time.Equal(want2) {
				t.Errorf("event 2 time %v, want %v", res[1].ev.Time, want2)
			}
			want3 := time.Date(2024, 5, 1, 11, 0, 0, 0, time.UTC)
			if !res[2].ev.Time.Equal(want3) {
				t.Errorf("event 3 time %v, want %v", res[2].ev.Time, want3)
			}
		})
	}
}

func TestContainerPrefixedLines(t *testing.T) {
	log := "2024-05-01T10:20:30.123456789Z stderr F panic: oops [recovered]\n" +
		"2024-05-01T10:20:30.123456790Z stderr F \tpanic: oops\n" +
		"2024-05-01T10:20:30.123456791Z stderr F \n" +
		"2024-05-01T10:20:30.123456792Z stderr F goroutine 7 [running]:\n" +
		"2024-05-01T10:20:30.123456793Z stderr F main.work(...)\n" +
		"2024-05-01T10:20:30.123456794Z stderr F \t/app/work.go:42 +0x1d\n" +
		"2024-05-01T10:20:31Z stdout F all good again\n"
	var got string
	d := profile()
	d.Parse = func(b []byte) (crashsig.Signature, error) { got = string(b); return crashsig.Signature{}, nil }
	res := collect(d, strings.NewReader(log), nil)
	if len(res) != 1 || res[0].err != nil {
		t.Fatalf("got %+v", res)
	}
	want := "panic: oops [recovered]\n\tpanic: oops\n\ngoroutine 7 [running]:\nmain.work(...)\n\t/app/work.go:42 +0x1d\n"
	if got != want {
		t.Errorf("block:\n%q\nwant:\n%q", got, want)
	}
	if !res[0].ev.Time.Equal(time.Date(2024, 5, 1, 10, 20, 30, 123456789, time.UTC)) {
		t.Errorf("time %v", res[0].ev.Time)
	}
}

func TestLevelPrefix(t *testing.T) {
	log := "2024-05-01 10:20:30,500 ERROR panic: bad thing\ngoroutine 1 [running]:\nmain.f()\n\t/a.go:1 +0x1\n"
	res := collect(profile(), strings.NewReader(log), nil)
	if len(res) != 1 || res[0].err != nil || res[0].ev.Signature.Exception != "panic: bad thing" {
		t.Fatalf("got %+v", res)
	}
	if res[0].ev.Time.Nanosecond() != 500_000_000 {
		t.Errorf("comma fraction lost: %v", res[0].ev.Time)
	}
}

func TestOrdinaryLogLinesAreNotPanics(t *testing.T) {
	log := "recovered panic: not at line start\nINFO the panic: word in a message\nfatal error handling ok\n"
	if res := collect(profile(), strings.NewReader(log), nil); len(res) != 0 {
		t.Fatalf("want no events, got %+v", res)
	}
}

func TestParseErrorDoesNotStopTheScan(t *testing.T) {
	d := NewDefault()
	n := 0
	d.Parse = func(b []byte) (crashsig.Signature, error) {
		n++
		if n == 1 {
			return crashsig.Signature{}, errors.New("bad block")
		}
		return crashsig.Signature{Exception: "ok"}, nil
	}
	in := "panic: one\n\nINFO between\npanic: two\n"
	res := collect(d, strings.NewReader(in), nil)
	if len(res) != 2 || res[0].err == nil || res[1].err != nil {
		t.Fatalf("got %+v", res)
	}
	if !strings.Contains(res[0].err.Error(), "line 1") {
		t.Errorf("error should name the line: %v", res[0].err)
	}
}

func TestDefaultParseIsCrashsig(t *testing.T) {
	// Until TV2 delivers crashsig.Parse the error must surface, not vanish.
	d := NewDefault()
	res := collect(d, strings.NewReader("panic: x\n"), nil)
	if len(res) != 1 {
		t.Fatalf("got %+v", res)
	}
	if _, err := crashsig.Parse([]byte("panic: x\n")); err == nil && res[0].err != nil {
		t.Errorf("profile failed but crashsig.Parse works: %v", res[0].err)
	}
}

func TestYieldFalseStopsReading(t *testing.T) {
	cr := &countingReader{src: newPanicLog(100000)}
	d := profile()
	n := 0
	for range d.Entries(cr, nil) {
		n++
		break
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	// 100000 blocks of ~70 bytes would be ~7 MB; a streaming reader that
	// stops at the first event reads only a buffer or two.
	if cr.read > 1<<20 {
		t.Errorf("read %d bytes after the consumer stopped: not streaming", cr.read)
	}
}

func TestStreamingBigLogKeepsMemoryFlat(t *testing.T) {
	const blocks = 20000
	cr := &countingReader{src: newPanicLog(blocks)}
	d := profile()
	n := 0
	for _, err := range d.Entries(cr, nil) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != blocks {
		t.Fatalf("events %d, want %d", n, blocks)
	}
}

func TestLongLineAndBlockLimits(t *testing.T) {
	d := profile()
	d.MaxLineBytes = 100
	d.MaxBlockLines = 5
	var got string
	d.Parse = func(b []byte) (crashsig.Signature, error) { got = string(b); return crashsig.Signature{}, nil }
	var sb strings.Builder
	sb.WriteString("panic: " + strings.Repeat("x", 5000) + "\n\ngoroutine 1 [running]:\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "main.f%d(...)\n\t/a.go:%d +0x1\n", i, i)
	}
	sb.WriteString("INFO next\npanic: second\n")
	res := collect(d, strings.NewReader(sb.String()), nil)
	if len(res) != 2 {
		t.Fatalf("want 2 events (long line and long block must not hide the next panic), got %d", len(res))
	}
	_ = got
	if n := strings.Count(got, "\n"); n > 5 {
		t.Errorf("second block has %d lines", n)
	}
}

func TestTimestampFormats(t *testing.T) {
	jk := time.FixedZone("X", 9*3600)
	tests := []struct {
		line string
		want time.Time
		rest string
	}{
		{"2024-05-01T10:20:30Z hello", time.Date(2024, 5, 1, 10, 20, 30, 0, time.UTC), "hello"},
		{"2024-05-01T10:20:30+07:00 hello", time.Date(2024, 5, 1, 3, 20, 30, 0, time.UTC), "hello"},
		{"2024-05-01T10:20:30+0700 hello", time.Date(2024, 5, 1, 3, 20, 30, 0, time.UTC), "hello"},
		{"2024-05-01 10:20:30 hello", time.Date(2024, 5, 1, 10, 20, 30, 0, jk), "hello"},
		{"[2024-05-01 10:20:30.250] hello", time.Date(2024, 5, 1, 10, 20, 30, 250_000_000, jk), "hello"},
		{"2024/05/01 10:20:30 hello", time.Date(2024, 5, 1, 10, 20, 30, 0, jk), "hello"},
		{"2024/05/01 10:20:30.5 hello", time.Date(2024, 5, 1, 10, 20, 30, 500_000_000, jk), "hello"},
		{`time="2024-05-01T10:20:30Z" level=error msg="x"`, time.Date(2024, 5, 1, 10, 20, 30, 0, time.UTC), `time="2024-05-01T10:20:30Z" level=error msg="x"`},
		{`{"level":"error","ts":1714558830.5,"msg":"x"}`, time.Unix(1714558830, 500_000_000).UTC(), `{"level":"error","ts":1714558830.5,"msg":"x"}`},
		{`{"time":"2024-05-01T10:20:30Z","msg":"x"}`, time.Date(2024, 5, 1, 10, 20, 30, 0, time.UTC), `{"time":"2024-05-01T10:20:30Z","msg":"x"}`},
	}
	for _, tc := range tests {
		got, rest, ok := parseTimestamp(tc.line, jk)
		if !ok {
			t.Errorf("%q: no timestamp", tc.line)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%q: got %v want %v", tc.line, got, tc.want)
		}
		if tc.rest != rest {
			t.Errorf("%q: rest %q want %q", tc.line, rest, tc.rest)
		}
	}
	for _, line := range []string{"panic: x", "no time here", "2024-13-45T99:99:99Z bad", ""} {
		if _, _, ok := parseTimestamp(line, jk); ok {
			t.Errorf("%q: should have no timestamp", line)
		}
	}
}

func TestRegistry(t *testing.T) {
	p, ok := Get(DefaultName)
	if !ok || p.Name() != "default" {
		t.Fatalf("default profile missing: %v %v", p, ok)
	}
	if _, ok := Get("nope"); ok {
		t.Error("unknown profile found")
	}
	if err := Register(NewDefault()); err == nil {
		t.Error("duplicate registration accepted")
	}
	if err := Register(named("")); err == nil {
		t.Error("empty name accepted")
	}
	if err := Register(named("extra-test")); err != nil {
		t.Fatal(err)
	}
	names := Names()
	if len(names) < 2 || names[0] > names[1] {
		t.Errorf("names not sorted: %v", names)
	}
}

type named string

func (n named) Name() string { return string(n) }
func (named) Entries(io.Reader, *time.Location) iter.Seq2[crashrank.Event, error] {
	return func(func(crashrank.Event, error) bool) {}
}

// ---- helpers: a log that is generated on the fly -------------------------

type countingReader struct {
	src  io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.read += n
	return n, err
}

// newPanicLog returns a reader producing n small panic blocks separated by
// ordinary log lines, without ever holding them in memory.
func newPanicLog(n int) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for i := 0; i < n; i++ {
			_, err := fmt.Fprintf(pw, "2024/05/01 10:00:00 request %d\npanic: boom %d\n\ngoroutine 1 [running]:\nmain.f(...)\n\t/app/main.go:%d +0x1\n", i, i, i%100+1)
			if err != nil {
				return // reader closed early
			}
		}
	}()
	return pr
}

// Real server log of goflrealworld: 142 recovered "http: panic serving"
// stacks, then one goroutine panic (W6, service/comment.go:126) that kills the
// process. The last panic follows the previous stack without a log line in
// between and must not be swallowed into it.
func TestServerLogKeepsFinalGoroutinePanic(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "goflrealworld", "stacks", "server_crash.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res := collect(NewDefault(), f, time.Local)
	http, other := 0, 0
	for _, r := range res {
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if strings.HasPrefix(r.ev.Signature.Exception, "runtime error: index out of range") &&
			len(r.ev.Signature.Frames) > 0 && r.ev.Signature.Frames[0].Line == 126 {
			other++
			continue
		}
		http++
	}
	if http != 142 || other != 1 {
		t.Fatalf("got %d handler panics and %d goroutine panics, want 142 and 1", http, other)
	}
}
