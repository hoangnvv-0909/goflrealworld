package app_test

// Trigger tests for the seeded crash bugs W1-W9 (see bugs.json).
// Each one is EXPECTED TO PANIC. A panic aborts the whole test binary, so run
// them one at a time:   go test ./internal/app -run '^TestW1_' -count=1
//
// Requests go through the real router with ServeHTTP, so stacks contain
// gorilla/mux, the JWT middleware, the handler, the service and the repository.

import (
	"net/http"
	"testing"

	"goflrealworld/internal/service"
	"goflrealworld/internal/testutil"
)

func article(title string) map[string]any {
	return map[string]any{"article": map[string]any{
		"title": title, "description": "d", "body": "b"}}
}

// W1: a valid token whose user no longer exists.
func TestW1_CurrentUserDeleted(t *testing.T) {
	e := testutil.Seeded(t)
	ghost := e.Token(999, "ghost@example.com")
	e.Do("GET", "/api/user", ghost, nil)
}

// W2: punctuation collapses, so the slug is shorter than the title.
const punctTitle = "Q&A ... ??? !!! --- *** ### $$$ ~~~ ;;; ::: (Go)"

func TestW2_CreateArticlePunctuatedTitle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("POST", "/api/articles", e.Alice.Token, article(punctTitle))
}

func TestW2_UpdateArticlePunctuatedTitle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("PUT", "/api/articles/"+e.Articles[0], e.Alice.Token,
		map[string]any{"article": map[string]any{"title": punctTitle}})
}

// W3: any JSON number for ttl_hours.
func TestW3_RefreshWithTTL(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("POST", "/api/auth/refresh", e.Alice.Token, map[string]any{"ttl_hours": 1})
}

// W4: creating a tagged article invalidates the cache, then /tags is read.
func TestW4_TagsAfterNewArticle(t *testing.T) {
	e := testutil.Seeded(t)
	r := e.Do("POST", "/api/articles", e.Alice.Token, map[string]any{"article": map[string]any{
		"title": "Tagged", "description": "d", "body": "b", "tagList": []string{"go"}}})
	e.Expect(r, http.StatusCreated)
	e.Do("GET", "/api/tags", "", nil)
}

// W5: the author deletes their own comment.
func TestW5_DeleteOwnComment(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("DELETE", testutil.Path("/api/articles/%s/comments/%d", e.Articles[0], e.Comments[0]), e.Bob.Token, nil)
}

// W6: a comment containing a lone "@". The panic happens in a goroutine
// ("created by ... CreateComment"), so it kills the binary.
func TestW6_CommentWithLoneAt(t *testing.T) {
	e := testutil.Seeded(t)
	r := e.Do("POST", "/api/articles/"+e.Articles[0]+"/comments", e.Bob.Token,
		map[string]any{"comment": map[string]any{"body": "thanks @ everyone"}})
	e.Expect(r, http.StatusCreated)
	service.WaitNotifications()
}

// W7: a non-numeric comment id in the URL.
func TestW7_DeleteCommentBadID(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("DELETE", "/api/articles/"+e.Articles[0]+"/comments/abc", e.Bob.Token, nil)
}

// W8: one root cause (GetBySlug), several entry points. All crash points are
// in service/article.go but in different functions.
func TestW8_GetMissingArticle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("GET", "/api/articles/no-such-article", "", nil)
}

func TestW8_UpdateMissingArticle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("PUT", "/api/articles/no-such-article", e.Alice.Token,
		map[string]any{"article": map[string]any{"body": "x"}})
}

func TestW8_DeleteMissingArticle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("DELETE", "/api/articles/no-such-article", e.Alice.Token, nil)
}

func TestW8_FavoriteMissingArticle(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("POST", "/api/articles/no-such-article/favorite", e.Bob.Token, nil)
}

// W9: the feed without ?limit.
func TestW9_FeedDefaultLimit(t *testing.T) {
	e := testutil.Seeded(t)
	e.Do("GET", "/api/articles/feed", e.Bob.Token, nil)
}
