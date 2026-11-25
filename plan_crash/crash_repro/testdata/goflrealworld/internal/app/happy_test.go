package app_test

import (
	"net/http"
	"testing"

	"goflrealworld/internal/testutil"
)

// TestHappyPaths exercises every endpoint that a seeded bug lives on, with
// normal inputs. It must PASS: each bug fires only on its own trigger input,
// so the bugs stay independent of each other.
func TestHappyPaths(t *testing.T) {
	e := testutil.Seeded(t)
	a, b := e.Alice, e.Bob
	slug := e.Articles[0]

	e.Expect(e.Do("GET", "/api/articles", "", nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/articles?limit=5", b.Token, nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/articles/"+slug, "", nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/articles/feed?limit=10", b.Token, nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/user", a.Token, nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/tags", "", nil), http.StatusOK)
	e.Expect(e.Do("GET", "/api/articles/"+slug+"/comments", "", nil), http.StatusOK)
	e.Expect(e.Do("POST", "/api/auth/refresh", a.Token, nil), http.StatusOK)

	e.Expect(e.Do("POST", "/api/articles", a.Token, map[string]any{"article": map[string]any{
		"title": "A short title", "description": "d", "body": "b", "tagList": []string{"x"}}}), http.StatusCreated)
	e.Expect(e.Do("POST", "/api/articles/"+slug+"/favorite", b.Token, nil), http.StatusOK)
	e.Expect(e.Do("POST", "/api/articles/"+slug+"/comments", b.Token,
		map[string]any{"comment": map[string]any{"body": "ping @alice"}}), http.StatusCreated)
	e.Expect(e.Do("PUT", "/api/articles/"+e.Articles[1], a.Token,
		map[string]any{"article": map[string]any{"body": "new body"}}), http.StatusOK)
	e.Expect(e.Do("DELETE", "/api/articles/"+e.Articles[1], a.Token, nil), http.StatusOK)
}
