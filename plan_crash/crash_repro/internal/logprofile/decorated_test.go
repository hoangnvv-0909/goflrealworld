package logprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Decorated logs (ported from TV3's logprofile tests): the real stack of W3
// written by different loggers must give the same crash point.
func TestDecoratedLogs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "goflrealworld", "stacks", "W3_RefreshWithTTL.txt"))
	if err != nil {
		t.Fatal(err)
	}
	orig := collect(NewDefault(), strings.NewReader(string(raw)), time.UTC)
	if len(orig) != 1 || orig[0].err != nil {
		t.Fatalf("plain stack: %+v", orig)
	}
	want, _ := orig[0].ev.Signature.CrashPoint()
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	prefixed := func(prefix string) string {
		var b strings.Builder
		b.WriteString(prefix + "request started\n")
		for _, l := range lines {
			b.WriteString(prefix + l + "\n")
		}
		b.WriteString(prefix + "request finished\n")
		return b.String()
	}
	jsonLine := func(fields map[string]any) string {
		fields["stack"] = string(raw)
		b, _ := json.Marshal(fields)
		return `{"level":"info","msg":"starting"}` + "\n" + string(b) + "\n"
	}
	cases := map[string]string{
		"docker compose": prefixed("api-1  | "),
		"rfc3339 level":  prefixed("2026-11-13T15:05:59.120Z ERROR "),
		"bracket level":  prefixed("2026/11/13 15:05:59 [error] "),
		"zap json":       jsonLine(map[string]any{"level": "error", "ts": 1.7630467591e9, "msg": "panic recovered"}),
		"slog json":      jsonLine(map[string]any{"level": "ERROR", "time": "2026-11-13T15:05:59Z", "msg": "panic"}),
	}
	for name, log := range cases {
		t.Run(name, func(t *testing.T) {
			got := collect(NewDefault(), strings.NewReader(log), time.UTC)
			if len(got) != 1 || got[0].err != nil {
				t.Fatalf("got %d results: %+v", len(got), got)
			}
			cp, ok := got[0].ev.Signature.CrashPoint()
			if !ok || cp != want {
				t.Fatalf("crash point %+v, want %+v", cp, want)
			}
			if got[0].ev.Signature.Exception == "" {
				t.Fatal("exception lost")
			}
			if (strings.Contains(name, "json") || strings.Contains(name, "rfc")) && got[0].ev.Time.IsZero() {
				t.Fatal("time lost")
			}
		})
	}
}

// A custom profile file strips the decoration of an unusual logger
// (ported from TV3's logprofile tests).
func TestCustomProfileFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "svc.json")
	if err := os.WriteFile(cfg, []byte(`{"name":"svc","strip_prefix":"^svc\\[\\d+\\]: "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Lookup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "svc" {
		t.Fatalf("name %q", p.Name())
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "goflrealworld", "stacks", "W7_DeleteCommentBadID.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		b.WriteString("svc[4242]: " + l + "\n")
	}
	var got []result
	for ev, err := range p.Entries(strings.NewReader(b.String()), time.UTC) {
		got = append(got, result{ev, err})
	}
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("got %+v", got)
	}
	if cp, _ := got[0].ev.Signature.CrashPoint(); !strings.HasSuffix(cp.File, "internal/utils/must.go") {
		t.Fatalf("crash point %+v", cp)
	}

	if _, err := Lookup(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing profile must fail")
	}
	os.WriteFile(cfg, []byte(`{"strip_prefix":"("}`), 0o644)
	if _, err := Lookup(cfg); err == nil {
		t.Fatal("bad regexp must fail")
	}
	if p, err := Lookup(""); err != nil || p.Name() != DefaultName {
		t.Fatalf("empty name: %v %v", p, err)
	}
}

// A zap-style stack field without a "panic:" line keeps the message as the
// exception.
func TestJSONStackWithoutPanicLine(t *testing.T) {
	stack := "goroutine 7 [running]:\nmain.work(...)\n\t/app/work.go:42 +0x1d\n"
	b, _ := json.Marshal(map[string]any{"level": "error", "msg": "nil map write", "stacktrace": stack, "time": "2026-11-13T15:05:59Z"})
	got := collect(NewDefault(), strings.NewReader("plain line\n"+string(b)+"\n"), time.UTC)
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("got %+v", got)
	}
	ev := got[0].ev
	if !strings.Contains(ev.Signature.Exception, "nil map write") {
		t.Errorf("exception %q", ev.Signature.Exception)
	}
	if !ev.Time.Equal(time.Date(2026, 11, 13, 15, 5, 59, 0, time.UTC)) {
		t.Errorf("time %v", ev.Time)
	}
}
