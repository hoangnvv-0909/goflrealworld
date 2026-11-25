package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gofl/internal/finding"
)

func stack(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stacks", id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- Redact ---------------------------------------------------------------

func TestRedactMustRemove(t *testing.T) {
	tests := []struct{ name, in, gone string }{
		{"openai key", "key sk-abcdefghijklmnopqrstuv1234 end", "sk-abcdefghijklmnopqrstuv1234"},
		{"aws key", "id AKIAIOSFODNN7EXAMPLE x", "AKIAIOSFODNN7EXAMPLE"},
		{"github token", "t ghp_abcdefghijklmnopqrstuvwxyz0123456789 x", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"slack token", "xoxb-1234567890-abcdefghij", "xoxb-1234567890-abcdefghij"},
		{"bearer", "Authorization: Bearer abcDEF123456789xyz", "abcDEF123456789xyz"},
		{"jwt", "tok eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U x", "eyJhbGciOiJIUzI1NiJ9"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\nabc\n-----END RSA PRIVATE KEY-----", "MIIBOgIBAAJBAK"},
		{"url credentials", "dial postgres://admin:hunter2pass@db.internal:5432/app", "hunter2pass"},
		{"password assign", `password = "s3cr3tvalue"`, "s3cr3tvalue"},
		{"password colon", "password: s3cr3tvalue", "s3cr3tvalue"},
		{"api_key env", "API_KEY=abcdef123456", "abcdef123456"},
		{"token field", `{"token": "abcdef123456"}`, "abcdef123456"},
		{"email", "mail me at jane.doe+x@example.co.uk now", "jane.doe+x@example.co.uk"},
		{"ipv4", "from 192.168.10.25:8080", "192.168.10.25"},
		{"ipv6", "from 2001:0db8:85a3:0000:0000:8a2e:0370:7334 ok", "2001:0db8:85a3:0000:0000:8a2e:0370:7334"},
		{"ipv6 short", "from fe80::1 ok", "fe80::1"},
		{"windows home", `C:\Users\nguyenvana\go\src\x.go`, "nguyenvana"},
		{"windows home slash", `C:/Users/nguyenvana/go/x.go`, "nguyenvana"},
		{"linux home", "/home/alice/project/main.go", "alice"},
		{"mac home", "/Users/bob/project/main.go", "bob"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			if strings.Contains(got, tc.gone) {
				t.Errorf("secret survived:\n in: %q\nout: %q", tc.in, got)
			}
			if got == tc.in {
				t.Errorf("unchanged: %q", tc.in)
			}
		})
	}
}

func TestRedactKeepsWhatThePromptNeeds(t *testing.T) {
	keep := []string{
		"goflsample/crash.(*Registry).Inc(...)",
		"\tD:/NL&TKTT/gofl/testdata/goflsample/crash/crash.go:29 +0x1d",
		"panic: runtime error: index out of range [3] with length 3",
		"goroutine 6 [running]:",
		"testing.tRunner.func1.2({0x7ff72e1aa3a0, 0xc00001a270})",
		"x := token + 1",
		"token := nextToken()",
		"if password == \"\" {",
		"for i := 0; i < 10; i++ {",
		"version go1.23.4 and v1.2.3",
		"sha 3f786850e387550fdab836ed7e6dc881de23001b",
		"Lock(mu sync.Mutex) { c.m[k]++ }",
		"fmt.Sprintf(\"value=%s\", s)",
		"var m map[string]int // 10:20:30",
		"foo::bar is not Go",
	}
	for _, s := range keep {
		if got := Redact(s); got != s {
			t.Errorf("changed what must stay:\n in: %q\nout: %q", s, got)
		}
	}
}

func TestRedactKeepsRelativePartOfHome(t *testing.T) {
	got := Redact(`C:\Users\nguyenvana\go\src\x.go and /home/alice/p/main.go`)
	for _, want := range []string{`\go\src\x.go`, "/p/main.go", "<USER>"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q misses %q", got, want)
		}
	}
}

func TestRedactIdempotent(t *testing.T) {
	inputs := []string{
		"password = 'abcdef123456' mail a@b.com ip 10.1.2.3",
		"postgres://u:p4ssw0rd@h/db Bearer abcdefgh1234",
		`C:\Users\x\a /home/y/b token: abcdef123456`,
		"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
	}
	for i := 1; i <= 13; i++ {
		inputs = append(inputs, stack(t, "C"+itoa(i)))
	}
	for _, in := range inputs {
		once := Redact(in)
		if twice := Redact(once); twice != once {
			t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
		}
	}
}

func TestRedactStacksKeepStructure(t *testing.T) {
	for i := 1; i <= 13; i++ {
		s := stack(t, "C"+itoa(i))
		got := Redact(s)
		if len(ParseStackFrames(got)) != len(ParseStackFrames(s)) {
			t.Errorf("C%d: redaction changed the frames", i)
		}
	}
}

// ---- stack reader ---------------------------------------------------------

func TestParseStackFrames(t *testing.T) {
	fr := UserFrames(stack(t, "C8"))
	var names []string
	for _, f := range fr {
		names = append(names, f.Func)
	}
	want := []string{"goflsample/crash.parseKV", "goflsample/crash.stageBuild", "goflsample/crash.stageClean", "goflsample/crash.stageSplit", "goflsample/crash.Run"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("user frames %v, want %v", names, want)
	}
	if fr[0].Line != 42 || !strings.HasSuffix(fr[0].File, "crash/hard.go") {
		t.Errorf("crash point %+v", fr[0])
	}
	// Goroutine stack ending in "created by" (C13).
	fr = UserFrames(stack(t, "C13"))
	if len(fr) == 0 || !strings.HasSuffix(fr[0].Func, "Fanout.func1") || fr[0].Line != 144 {
		t.Errorf("C13 frames %+v", fr)
	}
}

func TestMatchFile(t *testing.T) {
	known := []string{"crash/hard.go", "crash/crash.go"}
	for in, want := range map[string]string{
		"crash/hard.go":                  "crash/hard.go",
		"D:/x/goflsample/crash/hard.go":  "crash/hard.go",
		`D:\x\goflsample\crash\crash.go`: "crash/crash.go",
		"./crash/crash.go":               "crash/crash.go",
		"hard.go":                        "crash/hard.go", // unique basename
		"other/hard.go":                  "",
		"":                               "",
	} {
		if got := matchFile(in, known); got != want {
			t.Errorf("matchFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- prompt ---------------------------------------------------------------

const hardSrc = `package crash

func parseKV(p string) (string, string) {
	kv := strings.SplitN(p, "=", 2)
	return kv[0], kv[1]
}
`

func inputC8(t *testing.T) Input {
	return Input{
		Stack: stack(t, "C8"),
		Ranked: []finding.Finding{
			{Location: finding.Location{File: "crash/hard.go", Func: "crash.parseKV"}, Rank: 1},
			{Location: finding.Location{File: "crash/hard.go", Func: "crash.stageBuild"}, Rank: 2},
			{Location: finding.Location{File: "crash/crash.go", Func: "crash.Nth"}, Rank: 3},
		},
		Files: map[string]string{"crash/hard.go": hardSrc, "crash/crash.go": "package crash\n", "crash/other.go": "package crash\n"},
	}
}

func TestPromptHasFiveParts(t *testing.T) {
	p, err := BuildPrompt(inputC8(t))
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{"Role-Play-Part:", "Suspicious-Code-Part:", "Stack-Trace-Information-Part:", "Ranking-Information-Part:", "Asking-Part:"}
	last := -1
	for _, part := range parts {
		i := strings.Index(p, part)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", part, p)
		}
		if i < last {
			t.Errorf("%q out of order", part)
		}
		last = i
	}
	for _, want := range []string{"File: crash/hard.go", "   5| \treturn kv[0], kv[1]", "1) crash/hard.go:", " - crash.parseKV", "2) crash/crash.go:", `"locations"`, "ONLY a JSON object"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q", want)
		}
	}
	if strings.Contains(p, "crash/other.go") {
		t.Error("file nobody pointed at was included")
	}
}

func TestPromptFourConfigurations(t *testing.T) {
	in := inputC8(t)
	for _, c := range AllConfigs() {
		t.Run(c.Name(), func(t *testing.T) {
			p, err := BuildPromptWith(in, c)
			if err != nil {
				t.Fatal(err)
			}
			hasST := strings.Contains(p, "Stack-Trace-Information-Part") && strings.Contains(p, "goroutine 6 [running]")
			hasSM := strings.Contains(p, "Stack-Message-Information-Part")
			hasRank := strings.Contains(p, "Ranking-Information-Part")
			if (c.Detail == DetailST) != hasST || (c.Detail == DetailSM) != hasSM {
				t.Errorf("detail %v: ST=%v SM=%v", c.Detail, hasST, hasSM)
			}
			if c.WithRank != hasRank {
				t.Errorf("rank: want %v got %v", c.WithRank, hasRank)
			}
			if c.Detail == DetailSM {
				if strings.Contains(p, "tRunner") || !strings.Contains(p, "panic: runtime error: index out of range [1] with length 1") {
					t.Errorf("SM must be the exception message only:\n%s", p)
				}
			}
		})
	}
}

func TestPromptRankFromStackWhenNoRanking(t *testing.T) {
	in := inputC8(t)
	in.Ranked = nil
	p, err := BuildPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "File: crash/hard.go") {
		t.Error("files named in the stack should be chosen when there is no ranking")
	}
	if strings.Contains(p, "Ranking-Information-Part") {
		t.Error("no ranking given, yet a ranking part was written")
	}
}

func TestPromptMaxFiles(t *testing.T) {
	in := inputC8(t)
	cfg := DefaultConfig()
	cfg.MaxFiles = 1
	p, _ := BuildPromptWith(in, cfg)
	if strings.Count(p, "File: ") != 1 {
		t.Errorf("want 1 file:\n%s", p)
	}
}

func TestPromptIsRedacted(t *testing.T) {
	in := inputC8(t)
	in.Exception = "panic: dial postgres://admin:hunter2pass@10.0.0.5/db failed"
	in.Files["crash/hard.go"] = hardSrc + "\nvar apiKey = \"sk-abcdefghijklmnopqrstuv1234\"\n// owner jane@corp.com\n"
	p, err := BuildPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2pass", "10.0.0.5", "sk-abcdefghijklmnopqrstuv1234", "jane@corp.com"} {
		if strings.Contains(p, secret) {
			t.Errorf("prompt still holds %q", secret)
		}
	}
	d, _ := DryRun(in, DefaultConfig())
	if d != p {
		t.Error("DryRun must return exactly the prompt that is sent")
	}
}

func TestPromptTruncatesLongFiles(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 2000; i++ {
		sb.WriteString("// filler line number to make the file big enough to cut\n")
	}
	sb.WriteString(hardSrc)
	in := inputC8(t)
	in.Files["crash/hard.go"] = sb.String()
	// Make the stack point into the end of the file.
	in.Stack = strings.ReplaceAll(in.Stack, "hard.go:42", "hard.go:2005")
	cfg := DefaultConfig()
	cfg.MaxChars = 6000
	p, err := BuildPromptWith(in, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "lines omitted") {
		t.Error("cut text must say so")
	}
	if !strings.Contains(p, "2005|") {
		t.Errorf("the window around the crash line is missing")
	}
	if len(p) > cfg.MaxChars*2 {
		t.Errorf("prompt is %d chars for a budget of %d", len(p), cfg.MaxChars)
	}
}

func TestPromptEmptyInput(t *testing.T) {
	if _, err := BuildPrompt(Input{}); !errors.Is(err, ErrEmptyInput) {
		t.Errorf("err = %v", err)
	}
}

func TestStackMessage(t *testing.T) {
	got := StackMessage(stack(t, "C1"))
	if !strings.HasPrefix(got, "panic: runtime error: invalid memory address or nil pointer dereference") || !strings.Contains(got, "[signal") || strings.Contains(got, "goroutine") {
		t.Errorf("got %q", got)
	}
}

// ---- ParseResponse --------------------------------------------------------

const goodJSON = `{"locations":[{"file":"crash/hard.go","function":"crash.parseKV","line":42},{"file":"crash/hard.go","function":"crash.stageBuild","line":34}],"explanation":"kv[1] is read without checking."}`

func TestParseTiers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		tier int
		n    int
	}{
		{"plain", goodJSON, 1, 2},
		{"plain padded and BOM", "\xef\xbb\xbf  \n" + goodJSON + "\n", 1, 2},
		{"fenced json", "Here you go:\n```json\n" + goodJSON + "\n```\nHope it helps.", 2, 2},
		{"fenced no lang", "```\n" + goodJSON + "\n```", 2, 2},
		{"text around", "The bug is clear. " + goodJSON + " Let me know if you need more.", 3, 2},
		{"bare array", `[{"file":"crash/hard.go","function":"parseKV","line":42}]`, 1, 1},
		{"trailing commas", `{"locations":[{"file":"crash/hard.go","function":"crash.parseKV","line":42,},],"explanation":"x",}`, 5, 1},
		{"trailing commas in fence", "```json\n" + `{"locations":[{"file":"crash/hard.go","line":42},]}` + "\n```", 5, 1},
		{"truncated mid object", `{"locations":[{"file":"crash/hard.go","function":"crash.parseKV","line":42},{"file":"crash/har`, 4, 1},
		{"truncated in explanation", `{"locations":[{"file":"crash/hard.go","function":"crash.parseKV","line":42}],"explanation":"kv[1] is rea`, 4, 1},
		{"truncated after fence open", "```json\n" + `{"locations":[{"file":"crash/hard.go","line":42},{"file":"x.go"`, 4, 1},
		{"line as string", `{"locations":[{"file":"crash/hard.go","line":"42"}]}`, 1, 1},
		{"line as range", `{"locations":[{"file":"crash/hard.go","line":"42-45"}]}`, 1, 1},
		{"alt keys", `{"locations":[{"path":"crash/hard.go","func":"parseKV()","lines":[42,43]}]}`, 1, 1},
		{"windows slashes", `{"locations":[{"file":"crash\\hard.go","line":42}]}`, 1, 1},
		{"braces inside strings", `{"locations":[{"file":"crash/hard.go","line":42}],"explanation":"use {x} and ] carefully"}`, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseResponse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("err %v", err)
			}
			if res.Tier != tc.tier {
				t.Errorf("tier %d, want %d", res.Tier, tc.tier)
			}
			if len(res.Locations) != tc.n {
				t.Fatalf("locations %+v, want %d", res.Locations, tc.n)
			}
			if res.Locations[0].File != "crash/hard.go" || res.Locations[0].Line != 42 {
				t.Errorf("first location %+v", res.Locations[0])
			}
		})
	}
}

func TestParseDetails(t *testing.T) {
	res, err := ParseResponse([]byte(goodJSON))
	if err != nil || res.Explanation != "kv[1] is read without checking." {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Locations[0].Func != "crash.parseKV" {
		t.Errorf("%+v", res.Locations[0])
	}
	// Explanation outside the JSON is kept when the JSON has none.
	res, _ = ParseResponse([]byte("The crash is in parseKV.\n```json\n" + `{"locations":[{"file":"a.go","line":3}]}` + "\n```"))
	if !strings.Contains(res.Explanation, "The crash is in parseKV") {
		t.Errorf("explanation %q", res.Explanation)
	}
	// Truncated explanation is recovered as far as it goes.
	res, _ = ParseResponse([]byte(`{"locations":[{"file":"a.go","line":3}],"explanation":"cut he`))
	if res.Explanation != "cut he" {
		t.Errorf("explanation %q", res.Explanation)
	}
	// Function-only and file-only answers are kept.
	res, _ = ParseResponse([]byte(`{"locations":[{"function":"crash.parseKV"},{"file":"a.go"}]}`))
	if len(res.Locations) != 2 {
		t.Errorf("%+v", res.Locations)
	}
	// Empty objects are dropped.
	res, err = ParseResponse([]byte(`{"locations":[{},{"file":"a.go","line":1}]}`))
	if err != nil || len(res.Locations) != 1 {
		t.Errorf("%+v %v", res.Locations, err)
	}
}

func TestParseErrors(t *testing.T) {
	for raw, want := range map[string]error{
		"":                        ErrEmptyResponse,
		"   \n\t":                 ErrEmptyResponse,
		"I cannot help":           ErrNoJSON,
		`{"locations":[]}`:        ErrNoJSON,
		`{"foo":1}`:               ErrNoJSON,
		`{"locations":[{"x":1}]}`: ErrNoJSON,
		"```go\nfunc f() {}\n```": ErrNoJSON,
		`{"locations":[{"file":`:  ErrNoJSON,
	} {
		if _, err := ParseResponse([]byte(raw)); !errors.Is(err, want) {
			t.Errorf("%q: err %v, want %v", raw, err, want)
		}
	}
}

func TestParseGrounding(t *testing.T) {
	hint := HintFor(inputC8(t))
	raw := `{"locations":[
	  {"file":"D:\\work\\goflsample\\crash\\hard.go","function":"crash.parseKV","line":42},
	  {"file":"internal/auth/token.go","function":"auth.Verify","line":9},
	  {"file":"crash/crash.go","line":16}]}`
	res, err := ParseResponseWith([]byte(raw), hint)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Locations) != 2 || res.Locations[0].File != "crash/hard.go" {
		t.Errorf("locations %+v (absolute path should map to the project file)", res.Locations)
	}
	if len(res.Ungrounded) != 1 || res.Ungrounded[0].File != "internal/auth/token.go" {
		t.Errorf("ungrounded %+v", res.Ungrounded)
	}
	// Without a hint nothing is rejected.
	res, _ = ParseResponse([]byte(raw))
	if len(res.Locations) != 3 || len(res.Ungrounded) != 0 {
		t.Errorf("no-hint result %+v", res)
	}
}

func TestParseProseFallback(t *testing.T) {
	hint := HintFor(inputC8(t))
	raw := "The bug is in the `parseKV` function of `crash/hard.go:42`: it indexes kv[1] even when " +
		"there is no equals sign. Also see `stageBuild`."
	res, err := ParseResponseWith([]byte(raw), hint)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != 6 {
		t.Errorf("tier %d", res.Tier)
	}
	var sawFunc, sawFile bool
	for _, l := range res.Locations {
		sawFunc = sawFunc || strings.HasSuffix(l.Func, "parseKV")
		sawFile = sawFile || (l.File == "crash/hard.go" && l.Line == 42)
	}
	if !sawFunc || !sawFile {
		t.Errorf("locations %+v", res.Locations)
	}
	// Prose that names nothing known is not an answer.
	if _, err := ParseResponseWith([]byte("This looks like a nil pointer problem."), hint); !errors.Is(err, ErrNoJSON) {
		t.Errorf("err %v", err)
	}
}

// ---- Triage and vote ------------------------------------------------------

func loc(file, fn string, line int) finding.Location {
	return finding.Location{File: file, Func: fn, Line: line}
}

func TestAggregateVotes(t *testing.T) {
	rs := []Result{
		{Locations: []finding.Location{loc("a.go", "p.A", 10), loc("b.go", "p.B", 5)}},
		{Locations: []finding.Location{loc("a.go", "p.A", 10)}},
		{Locations: []finding.Location{loc("b.go", "p.B", 6), loc("a.go", "(*T).A", 10)}},
		{Locations: []finding.Location{loc("a.go", "p.A", 11), loc("a.go", "p.A", 10)}}, // same pair twice counts once
	}
	fs := Aggregate(rs, 5)
	if len(fs) != 3 {
		t.Fatalf("%+v", fs)
	}
	if fs[0].Location.File != "a.go" || fs[0].Score != 3.0/5 || fs[0].Rank != 1 {
		t.Errorf("first %+v", fs[0])
	}
	if fs[0].Location.Line != 10 {
		t.Errorf("most voted line should win, got %d", fs[0].Location.Line)
	}
	if fs[1].Location.File != "b.go" || fs[1].Score != 2.0/5 {
		t.Errorf("second %+v", fs[1])
	}
	for i, f := range fs {
		if f.Rank != i+1 || f.Evidence[0].Kind != "llm" {
			t.Errorf("finding %d: %+v", i, f)
		}
	}
	if !strings.Contains(fs[0].Evidence[0].Detail, "3/5") {
		t.Errorf("detail %q", fs[0].Evidence[0].Detail)
	}
}

func TestAggregateDeterministicTies(t *testing.T) {
	rs := []Result{{Locations: []finding.Location{loc("z.go", "", 0), loc("a.go", "", 0)}}}
	a, b := Aggregate(rs, 1), Aggregate(rs, 1)
	if a[0].Location.File != "z.go" || a[0].Location != b[0].Location {
		t.Errorf("order not stable / best position ignored: %+v", a)
	}
}

func TestQuorum(t *testing.T) {
	for runs, want := range map[int]int{1: 1, 3: 2, 5: 3, 4: 3} {
		if got := Quorum(runs); got != want {
			t.Errorf("Quorum(%d) = %d, want %d", runs, got, want)
		}
	}
}

func TestTriageFakeProvider(t *testing.T) {
	in := inputC8(t)
	fp := &FakeProvider{Responses: []string{
		goodJSON,
		"```json\n" + goodJSON + "\n```",
		`{"locations":[{"file":"crash/hard.go","function":"crash.stageBuild","line":34}]}`,
		"I have no idea",
		`{"locations":[{"file":"elsewhere/x.go","function":"x.F","line":1}]}`,
	}}
	out, err := Triage(context.Background(), fp, in, TriageOptions{Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if fp.Calls() != 5 || len(out.Raw) != 5 {
		t.Errorf("calls %d raw %d", fp.Calls(), len(out.Raw))
	}
	for _, p := range fp.Prompts() {
		if p != out.Prompt {
			t.Error("every request must send the same prompt")
		}
	}
	if out.Failed != 1 { // "I have no idea"
		t.Errorf("failed %d", out.Failed)
	}
	if len(out.Results) != 4 {
		t.Errorf("results %d", len(out.Results))
	}
	top := out.Findings[0]
	if top.Location.File != "crash/hard.go" || top.Location.Func != "crash.stageBuild" || top.Score != 3.0/5 || out.Findings[1].Location.Func != "crash.parseKV" || out.Findings[1].Score != 2.0/5 {
		t.Errorf("top %+v", top)
	}
	joined := strings.Join(out.Notes, "\n")
	if !strings.Contains(joined, "1 of 5 requests failed") || !strings.Contains(joined, "not in the files given") {
		t.Errorf("notes %q", joined)
	}
	for _, f := range out.Findings {
		if strings.HasPrefix(f.Location.File, "elsewhere") {
			t.Error("invented file reached the findings")
		}
	}
}

func TestTriageAllFail(t *testing.T) {
	fp := &FakeProvider{Err: errors.New("boom")}
	out, err := Triage(context.Background(), fp, inputC8(t), TriageOptions{Runs: 3})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
	if out.Failed != 3 {
		t.Errorf("failed %d", out.Failed)
	}
	fp = &FakeProvider{Responses: []string{"nothing useful"}}
	if _, err := Triage(context.Background(), fp, inputC8(t), TriageOptions{Runs: 2}); !errors.Is(err, ErrNoJSON) {
		t.Errorf("err %v", err)
	}
}

func TestTriageContextAndInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Triage(ctx, &FakeProvider{Responses: []string{goodJSON}}, inputC8(t), TriageOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
	fp := &FakeProvider{Responses: []string{goodJSON}}
	if _, err := Triage(context.Background(), fp, Input{}, TriageOptions{}); !errors.Is(err, ErrEmptyInput) || fp.Calls() != 0 {
		t.Errorf("err %v calls %d: nothing may be sent for an empty input", err, fp.Calls())
	}
}

// ---- Provider -------------------------------------------------------------

func TestHTTPProviderRoundTrip(t *testing.T) {
	var got chatRequest
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer srv.Close()
	p := NewHTTPProvider(srv.URL+"/v1/", "m1", "secret-key-123")
	p.Temperature = 0.7
	out, err := p.Complete(context.Background(), "the prompt")
	if err != nil || out != "hello" {
		t.Fatalf("%q %v", out, err)
	}
	if auth != "Bearer secret-key-123" || got.Model != "m1" || got.Temperature != 0.7 || got.Stream ||
		len(got.Messages) != 1 || got.Messages[0].Content != "the prompt" || got.Messages[0].Role != "user" {
		t.Errorf("request %+v auth %q", got, auth)
	}
}

func TestHTTPProviderNoKeyNoHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization sent without a key")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	if _, err := NewHTTPProvider(srv.URL, "m", "").Complete(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPProviderRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.Write([]byte(`{"choices":[{"message":{"content":"finally"}}]}`))
		}
	}))
	defer srv.Close()
	p := NewHTTPProvider(srv.URL, "m", "")
	p.Backoff = time.Millisecond
	out, err := p.Complete(context.Background(), "p")
	if err != nil || out != "finally" || n.Load() != 3 {
		t.Fatalf("%q %v calls %d", out, err, n.Load())
	}
}

func TestHTTPProviderGivesUp(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewHTTPProvider(srv.URL, "m", "")
	p.Backoff, p.MaxRetries = time.Millisecond, 2
	if _, err := p.Complete(context.Background(), "p"); err == nil || n.Load() != 3 {
		t.Fatalf("err %v calls %d", err, n.Load())
	}
}

func TestHTTPProviderNoRetryOnClientError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()
	p := NewHTTPProvider(srv.URL, "m", "k")
	p.Backoff = time.Millisecond
	_, err := p.Complete(context.Background(), "p")
	if err == nil || n.Load() != 1 || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("err %v calls %d", err, n.Load())
	}
}

func TestHTTPProviderErrorNeverLeaksKey(t *testing.T) {
	p := NewHTTPProvider("http://127.0.0.1:1", "m", "SUPERSECRETKEY")
	p.MaxRetries = 0
	_, err := p.Complete(context.Background(), "p")
	if err == nil || strings.Contains(err.Error(), "SUPERSECRETKEY") {
		t.Fatalf("err %v", err)
	}
}

func TestHTTPProviderHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release) // runs first: lets the handler return so Close does not wait
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewHTTPProvider(srv.URL, "m", "").Complete(ctx, "p")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestHTTPProviderBadBodies(t *testing.T) {
	for name, body := range map[string]string{
		"not json":   "<html>",
		"no choices": `{"choices":[]}`,
		"api error":  `{"error":{"message":"overloaded"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := NewHTTPProvider(srv.URL, "m", "").Complete(context.Background(), "p"); err == nil {
				t.Error("want error")
			}
		})
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestNewProvider(t *testing.T) {
	if _, err := NewProviderFrom("off", env(nil)); !errors.Is(err, ErrOff) {
		t.Errorf("off: %v", err)
	}
	if _, err := NewProviderFrom("", env(nil)); !errors.Is(err, ErrOff) {
		t.Errorf("empty: %v", err)
	}
	if _, err := NewProviderFrom("cloudy", env(nil)); err == nil {
		t.Error("unknown mode accepted")
	}
	// local: model required, URL defaults to Ollama.
	if _, err := NewProviderFrom("local", env(nil)); err == nil || !strings.Contains(err.Error(), EnvModel) {
		t.Errorf("local without model: %v", err)
	}
	p, err := NewProviderFrom("local", env(map[string]string{EnvModel: "llama3"}))
	if err != nil {
		t.Fatal(err)
	}
	hp := p.(*HTTPProvider)
	if hp.BaseURL != DefaultLocalURL || hp.Model != "llama3" || hp.Temperature != 1.0 {
		t.Errorf("%+v", hp)
	}
	// local must stay on this machine.
	for _, u := range []string{"https://api.example.com/v1", "http://10.0.0.7:8000/v1", "http://localhost.evil.com/v1"} {
		if _, err := NewProviderFrom("local", env(map[string]string{EnvModel: "m", EnvURL: u})); err == nil {
			t.Errorf("local accepted %s", u)
		}
	}
	for _, u := range []string{"http://localhost:8080/v1", "http://127.0.0.1:1234/v1", "http://[::1]:11434/v1"} {
		if _, err := NewProviderFrom("local", env(map[string]string{EnvModel: "m", EnvURL: u})); err != nil {
			t.Errorf("local refused %s: %v", u, err)
		}
	}
	// api: URL, key, model all required.
	full := map[string]string{EnvURL: "https://api.example.com/v1", EnvKey: "k", EnvModel: "m", EnvTemperature: "0.3"}
	p, err = NewProviderFrom("api", env(full))
	if err != nil || p.(*HTTPProvider).Temperature != 0.3 || p.(*HTTPProvider).Key != "k" {
		t.Fatalf("%v %+v", err, p)
	}
	for _, drop := range []string{EnvURL, EnvKey, EnvModel} {
		m := map[string]string{}
		for k, v := range full {
			if k != drop {
				m[k] = v
			}
		}
		if _, err := NewProviderFrom("api", env(m)); err == nil || !strings.Contains(err.Error(), drop) {
			t.Errorf("api without %s: %v", drop, err)
		}
	}
	full[EnvTemperature] = "hot"
	if _, err := NewProviderFrom("api", env(full)); err == nil {
		t.Error("bad temperature accepted")
	}
}

func TestFakeProviderCycles(t *testing.T) {
	f := &FakeProvider{Responses: []string{"a", "b"}}
	var got []string
	for i := 0; i < 5; i++ {
		s, _ := f.Complete(context.Background(), "p")
		got = append(got, s)
	}
	if strings.Join(got, "") != "ababa" || f.Calls() != 5 {
		t.Errorf("%v", got)
	}
}

func TestMatchFileAmbiguousBasename(t *testing.T) {
	known := []string{"a/util.go", "b/util.go", "a/b/util.go"}
	if got := matchFile("util.go", known); got != "" {
		t.Errorf("ambiguous basename matched %q", got)
	}
	// Longest known suffix wins for a long path.
	if got := matchFile("/work/a/b/util.go", known); got != "a/b/util.go" {
		t.Errorf("got %q", got)
	}
}
