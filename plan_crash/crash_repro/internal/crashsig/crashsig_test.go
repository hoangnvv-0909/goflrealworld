package crashsig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stackDir holds real stacks of the seeded bugs C1..C13 of goflsample.
const stackDir = "../llm/testdata/stacks"

func readStack(t *testing.T, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stackDir, id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCrashPoints(t *testing.T) {
	cases := []struct {
		id, exc, fn string
		line        int
		elided      bool
		fatal       bool
	}{
		{"C1", "runtime error: invalid memory address or nil pointer dereference", "goflsample/crash.EmailOf", 16, false, false},
		{"C7", "concurrent map writes", "goflsample/crash.(*Counter).Add", 68, false, true},
		{"C9", "runtime error: invalid memory address or nil pointer dereference", "goflsample/crash.(*Session).Token", 63, false, false},
		{"C11", `strconv.Atoi: parsing "": invalid syntax`, "goflsample/crash.must[...]", 108, false, false},
		{"C12", "runtime error: invalid memory address or nil pointer dereference", "goflsample/crash.(*Node).Total", 130, true, false},
		{"C13", "runtime error: index out of range [3] with length 3", "goflsample/crash.Fanout.func1", 144, false, false},
	}
	for _, c := range cases {
		s, err := Parse(readStack(t, c.id))
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if s.Exception != c.exc {
			t.Errorf("%s: exception %q, want %q", c.id, s.Exception, c.exc)
		}
		cp, ok := s.CrashPoint()
		if !ok || s.NoCrashPoint {
			t.Fatalf("%s: no crash point", c.id)
		}
		if cp.Func != c.fn || cp.Line != c.line {
			t.Errorf("%s: crash point %s:%d, want %s:%d", c.id, cp.Func, cp.Line, c.fn, c.line)
		}
		if s.Elided != c.elided || s.Fatal != c.fatal {
			t.Errorf("%s: elided=%v fatal=%v, want %v %v", c.id, s.Elided, s.Fatal, c.elided, c.fatal)
		}
	}
}

func TestParseAllSamples(t *testing.T) {
	for i := 1; i <= 13; i++ {
		id := fmt.Sprintf("C%d", i)
		s, err := Parse(readStack(t, id))
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if s.NoCrashPoint || len(s.Frames) == 0 {
			t.Errorf("%s: no crash point", id)
			continue
		}
		for _, f := range s.Frames {
			if strings.HasPrefix(f.Func, "runtime.") || strings.HasPrefix(f.Func, "testing.") {
				t.Errorf("%s: machinery frame %s survived", id, f.Func)
			}
		}
	}
}

func TestParseCreatedByAndOtherGoroutines(t *testing.T) {
	s, err := Parse(readStack(t, "C13"))
	if err != nil {
		t.Fatal(err)
	}
	if s.CreatedBy == nil || s.CreatedBy.Func != "goflsample/crash.Fanout" || s.CreatedBy.Line != 142 {
		t.Errorf("created by = %+v", s.CreatedBy)
	}
	// C7 has seven goroutines; only the first is read.
	s, _ = Parse(readStack(t, "C7"))
	for _, f := range s.Frames {
		if strings.Contains(f.Func, "WaitGroup") {
			t.Errorf("frame of another goroutine leaked: %s", f.Func)
		}
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("ok  \tgoflsample/crash\t0.1s\n")); err != ErrNoPanic {
		t.Errorf("err = %v, want ErrNoPanic", err)
	}
	s, err := Parse([]byte("panic: boom\n"))
	if err != nil || !s.NoCrashPoint || s.Exception != "boom" {
		t.Errorf("panic only: %+v %v", s, err)
	}
}

func TestParseNestedPanicAndCRLF(t *testing.T) {
	in := "panic: first [recovered]\r\n\tpanic: second\r\n\r\ngoroutine 1 [running]:\r\n" +
		"panic({0x1, 0x2})\r\n\t/usr/local/go/src/runtime/panic.go:783 +0x132\r\n" +
		"example.com/app/pkg.Do(0x1)\r\n\t/home/u/app/pkg/do.go:10 +0x20\r\n" +
		"main.main()\r\n\t/home/u/app/main.go:5 +0x1d\r\n" +
		"runtime.main()\r\n\t/usr/local/go/src/runtime/proc.go:283 +0x28b\r\n"
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if s.Exception != "second" {
		t.Errorf("exception = %q", s.Exception)
	}
	if len(s.Frames) != 2 || s.Frames[0].Func != "example.com/app/pkg.Do" || s.Frames[1].Func != "main.main" {
		t.Errorf("frames = %+v", s.Frames)
	}
}

func TestRelativize(t *testing.T) {
	s, _ := Parse(readStack(t, "C9"))
	root := "D:/NL&TKTT/gofl/testdata/goflsample"
	a := Relativize(s, root, "goflsample")
	if got := a.Frames[0].File; got != "crash/hard.go" {
		t.Errorf("by root: %q", got)
	}
	// Different machine: root unknown, module path decides.
	b := Relativize(s, "/elsewhere", "goflsample")
	if got := b.Frames[0].File; got != "crash/hard.go" {
		t.Errorf("by module: %q", got)
	}
	// Backslashes and different drive-letter case.
	c := Relativize(s, `d:\NL&TKTT\gofl\testdata\goflsample`, "")
	if got := c.Frames[0].File; got != "crash/hard.go" {
		t.Errorf("by root with backslashes: %q", got)
	}
}

func TestNormalize(t *testing.T) {
	a, _ := Parse(readStack(t, "C11"))
	n := Normalize(a)
	if n.Frames[0].Func != "goflsample/crash.must" {
		t.Errorf("generic suffix kept: %s", n.Frames[0].Func)
	}
	c, _ := Parse(readStack(t, "C13"))
	if got := Normalize(c).Frames[0].Func; got != "goflsample/crash.Fanout" {
		t.Errorf("closure suffix kept: %s", got)
	}
	// recursion collapses
	r, _ := Parse(readStack(t, "C12"))
	if got := len(Normalize(r).Frames); got > 3 {
		t.Errorf("recursion not collapsed: %d frames", got)
	}
	// idempotent
	if Hash(Normalize(n)) != Hash(n) {
		t.Error("Normalize is not idempotent")
	}
	// numbers in the message do not matter
	x := Normalize(Signature{Exception: "index out of range [3] with length 3"})
	y := Normalize(Signature{Exception: "index out of range [7] with length 2"})
	if Hash(x) != Hash(y) {
		t.Error("exception numbers should not change the hash")
	}
}

func TestHashStableAndSensitive(t *testing.T) {
	a, _ := Parse(readStack(t, "C2"))
	b, _ := Parse(readStack(t, "C3"))
	if Hash(Normalize(a)) != Hash(Normalize(a)) {
		t.Error("hash not deterministic")
	}
	if Hash(Normalize(a)) == Hash(Normalize(b)) {
		t.Error("different faults share a hash")
	}
	// a line change changes the hash
	c := a
	c.Frames = append([]Frame(nil), a.Frames...)
	c.Frames[0].Line++
	if Hash(a) == Hash(c) {
		t.Error("line number ignored by Hash")
	}
}

func TestSubsumes(t *testing.T) {
	mk := func(fs ...string) Signature {
		var s Signature
		for _, f := range fs {
			s.Frames = append(s.Frames, Frame{Func: f})
		}
		return s
	}
	long := mk("a", "b", "c", "d")
	cases := []struct {
		a, b Signature
		want bool
	}{
		{long, mk("a", "b"), true},
		{long, mk("b", "c"), true},
		{long, mk("a", "c"), false}, // gap: contiguous run required
		{long, mk("c", "b"), false}, // order matters
		{long, long, true},
		{mk("a"), long, false},
		{long, Signature{}, false},
	}
	for i, c := range cases {
		if got := Subsumes(c.a, c.b); got != c.want {
			t.Errorf("case %d: Subsumes = %v, want %v", i, got, c.want)
		}
	}
}

func TestIsStdlibFile(t *testing.T) {
	yes := []string{"C:/Program Files/Go/src/testing/testing.go", "/usr/local/go/src/runtime/panic.go", "$GOROOT/src/x.go"}
	no := []string{"crash/hard.go", "/home/me/go/src/github.com/me/p/x.go", "D:/work/app/main.go"}
	for _, f := range yes {
		if !IsStdlibFile(f) {
			t.Errorf("%s should be stdlib", f)
		}
	}
	for _, f := range no {
		if IsStdlibFile(f) {
			t.Errorf("%s should not be stdlib", f)
		}
	}
}
