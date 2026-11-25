package crashsig

// Step 2 of plan_crash/KE_HOACH_BAI_C.md: Parse on the real stacks of C1-C13
// (testdata/, captured from goflsample), W1-W9 and the goflrealworld server
// log. Ported from the TV2 week-1 delivery (C_week1_crashsig_TV2).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, path string) Signature {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return s
}

// Crash point (first user frame) of every real stack. Expected values come
// from testdata/goflsample/bugs.json ("crash_point" if present, else
// file/func/lines) and from the table in testdata/goflrealworld/README.md.
func TestParseCrashPoint(t *testing.T) {
	const sample = "testdata"
	const real = "../../testdata/goflrealworld/stacks"
	cases := []struct {
		file, fn, src string
		line          int
		exc           string // prefix of Exception
	}{
		{sample + "/C1.txt", "goflsample/crash.EmailOf", "crash/crash.go", 16, "runtime error: invalid memory address"},
		{sample + "/C2.txt", "goflsample/crash.Nth", "crash/crash.go", 21, "runtime error: index out of range [3] with length 3"},
		{sample + "/C3.txt", "goflsample/crash.(*Registry).Inc", "crash/crash.go", 29, "assignment to entry in nil map"},
		{sample + "/C4.txt", "goflsample/crash.Average", "crash/crash.go", 38, "runtime error: integer divide by zero"},
		{sample + "/C5.txt", "goflsample/crash.Describe", "crash/crash.go", 43, "interface conversion"},
		{sample + "/C6.txt", "goflsample/crash.Finish", "crash/crash.go", 49, "close of closed channel"},
		{sample + "/C7.txt", "goflsample/crash.(*Counter).Add", "crash/crash.go", 68, "concurrent map writes"},
		{sample + "/C8.txt", "goflsample/crash.parseKV", "crash/hard.go", 42, "runtime error: index out of range"},
		{sample + "/C9_0.txt", "goflsample/crash.(*Session).Token", "crash/hard.go", 63, "runtime error: invalid memory address"},
		{sample + "/C9_1.txt", "goflsample/crash.(*Session).Token", "crash/hard.go", 63, "runtime error: invalid memory address"},
		{sample + "/C9_2.txt", "goflsample/crash.(*Session).Token", "crash/hard.go", 63, "runtime error: invalid memory address"},
		{sample + "/C10.txt", "goflsample/crash.(*Cache).evict", "crash/hard.go", 97, "runtime error: index out of range"},
		{sample + "/C11.txt", "goflsample/crash.must[...]", "crash/hard.go", 108, "strconv.Atoi"},
		{sample + "/C12.txt", "goflsample/crash.(*Node).Total", "crash/hard.go", 130, "runtime error: invalid memory address"},
		{sample + "/C13.txt", "goflsample/crash.Fanout.func1", "crash/hard.go", 144, "runtime error: index out of range"},
		{sample + "/debugstack.txt", "main.(*Store).Save", "main.go", 18, ""},
		{real + "/W1_CurrentUserDeleted.txt", "goflrealworld/internal/handler.(*UserHandler).GetCurrentUser", "internal/handler/user.go", 147, "runtime error: invalid memory address"},
		{real + "/W2_CreateArticlePunctuatedTitle.txt", "goflrealworld/internal/utils.GenerateSlug", "internal/utils/slug.go", 28, "runtime error: slice bounds"},
		{real + "/W3_RefreshWithTTL.txt", "goflrealworld/internal/handler.(*AuthHandler).RefreshToken", "internal/handler/auth.go", 133, "interface conversion"},
		{real + "/W4_TagsAfterNewArticle.txt", "goflrealworld/internal/service.(*TagService).GetPopularTags", "internal/service/tag.go", 47, "assignment to entry in nil map"},
		{real + "/W5_DeleteOwnComment.txt", "goflrealworld/internal/service.(*CommentService).DeleteComment", "internal/service/comment.go", 95, "runtime error"},
		{real + "/W6_CommentWithLoneAt.txt", "goflrealworld/internal/service.mentions", "internal/service/comment.go", 126, "runtime error: index out of range"},
		{real + "/W7_DeleteCommentBadID.txt", "goflrealworld/internal/utils.Must[...]", "internal/utils/must.go", 7, "strconv.Atoi"},
		{real + "/W9_FeedDefaultLimit.txt", "goflrealworld/internal/handler.(*ArticleHandler).GetArticlesFeed", "internal/handler/article.go", 335, "runtime error: integer divide by zero"},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.file), func(t *testing.T) {
			s := load(t, c.file)
			if s.NoCrashPoint || len(s.Frames) == 0 {
				t.Fatal("no crash point")
			}
			f := s.Frames[0]
			if f.Func != c.fn || !strings.HasSuffix(f.File, "/"+c.src) || f.Line != c.line {
				t.Errorf("crash point = %s %s:%d, want %s %s:%d", f.Func, f.File, f.Line, c.fn, c.src, c.line)
			}
			if !strings.HasPrefix(s.Exception, c.exc) {
				t.Errorf("Exception = %q, want prefix %q", s.Exception, c.exc)
			}
			for _, f := range s.Frames {
				if strings.HasPrefix(f.Func, "testing.") || f.Func == "panic" || strings.Contains(f.File, `\`) {
					t.Errorf("machinery or backslash frame kept: %+v", f)
				}
			}
		})
	}
}

// W8: one cause, four stacks; all four crash in service/article.go.
func TestParseW8(t *testing.T) {
	m, _ := filepath.Glob("../../testdata/goflrealworld/stacks/W8_*.txt")
	if len(m) != 4 {
		t.Fatalf("want 4 W8 stacks, got %d", len(m))
	}
	for _, p := range m {
		if f := load(t, p).Frames[0]; !strings.HasSuffix(f.File, "/internal/service/article.go") {
			t.Errorf("%s: crash point file %s", filepath.Base(p), f.File)
		}
	}
}

// Every panic in the real server log parses and has a crash point. Splitting
// the log is logprofile's job (TV3); here a crude split at each panic line.
func TestParseServerLog(t *testing.T) {
	b, err := os.ReadFile("../../testdata/goflrealworld/stacks/server_crash.log")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(b), "http: panic serving ")
	n := 0
	for _, p := range parts[1:] {
		s, err := Parse([]byte("x http: panic serving " + p))
		if err != nil || s.NoCrashPoint || s.Exception == "" {
			t.Errorf("entry %d: err=%v sig=%+v", n, err, s)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no panic in log")
	}
	t.Logf("%d panics parsed", n)
}
