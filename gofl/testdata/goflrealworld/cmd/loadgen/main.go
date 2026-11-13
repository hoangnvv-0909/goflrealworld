// Command loadgen drives a running goflrealworld server with a mix of normal
// requests and the trigger inputs of the seeded crash bugs, so that the
// server's stderr fills with many "http: panic serving ..." reports.
//
// goflrealworld: not in upstream.
//
//	go run ./cmd/server 2> crash.log      # terminal 1, from the module root
//	go run ./cmd/loadgen -n 500           # terminal 2
//
// W6 panics in a background goroutine and kills the server, so it is only
// sent with -fatal (as the very last request).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"time"
)

var (
	base  = flag.String("addr", "http://localhost:8080", "server base URL")
	n     = flag.Int("n", 300, "number of requests after setup")
	ratio = flag.Float64("bug-ratio", 0.3, "fraction of requests that hit a seeded bug")
	seed  = flag.Int64("seed", 1, "random seed")
	fatal = flag.Bool("fatal", false, "finish with the W6 trigger (kills the server)")
	// No keep-alives: on a reused connection net/http silently retries a GET
	// that the server dropped (as a panicking handler does), which would
	// double-count crashes.
	client = &http.Client{Timeout: 10 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true}}
)

type user struct{ name, token string }

func call(method, path, token string, body any) (int, []byte) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, *base+path, r)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		// A panicking handler makes net/http drop the connection.
		return -1, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func register(name string) user {
	code, data := call("POST", "/api/users", "", map[string]any{"user": map[string]any{
		"username": name, "email": name + "@example.com", "password": "Password123"}})
	if code != http.StatusCreated && code != http.StatusOK {
		// Already registered by an earlier run: log in instead.
		code, data = call("POST", "/api/users/login", "", map[string]any{"user": map[string]any{
			"email": name + "@example.com", "password": "Password123"}})
	}
	var out struct{ User struct{ Token string } }
	if err := json.Unmarshal(data, &out); err != nil || out.User.Token == "" {
		log.Fatalf("register %s: status %d: %s", name, code, data)
	}
	return user{name, out.User.Token}
}

func createArticle(u user, title string, tags ...string) string {
	code, data := call("POST", "/api/articles", u.token, map[string]any{"article": map[string]any{
		"title": title, "description": "d", "body": "b", "tagList": tags}})
	var out struct{ Article struct{ Slug string } }
	if err := json.Unmarshal(data, &out); err != nil || out.Article.Slug == "" {
		log.Fatalf("create article: status %d: %s", code, data)
	}
	return out.Article.Slug
}

func createComment(u user, slug, body string) int {
	_, data := call("POST", "/api/articles/"+slug+"/comments", u.token,
		map[string]any{"comment": map[string]any{"body": body}})
	var out struct{ Comment struct{ ID int } }
	json.Unmarshal(data, &out)
	return out.Comment.ID
}

func main() {
	flag.Parse()
	rng := rand.New(rand.NewSource(*seed))
	run := fmt.Sprintf("%d", time.Now().Unix()%100000)

	// Setup: users, articles, follows, comments.
	alice, bob := register("alice"+run), register("bob"+run)
	var slugs []string
	for i := 0; i < 5; i++ {
		slugs = append(slugs, createArticle(alice, fmt.Sprintf("Load test article %d", i), "go", "load"))
	}
	call("POST", "/api/profiles/"+alice.name+"/follow", bob.token, nil)
	_, data := call("POST", "/api/auth/test-token", "", map[string]any{"user_id": 987654, "email": "ghost@example.com"})
	var ghost struct{ Token string }
	json.Unmarshal(data, &ghost)

	healthy := []func(){
		func() { call("GET", "/api/articles?limit=10", "", nil) },
		func() { call("GET", "/api/articles/"+slugs[rng.Intn(len(slugs))], bob.token, nil) },
		func() { call("GET", "/api/articles/feed?limit=5", bob.token, nil) },
		func() { call("GET", "/api/user", alice.token, nil) },
		func() { call("GET", "/api/articles/"+slugs[0]+"/comments", "", nil) },
		func() { call("GET", "/api/profiles/"+alice.name, bob.token, nil) },
	}
	bugs := map[string]func(){
		"W1": func() { call("GET", "/api/user", ghost.Token, nil) },
		"W2": func() {
			call("POST", "/api/articles", alice.token, map[string]any{"article": map[string]any{
				"title": "Q&A ... ??? !!! --- *** ### $$$ ~~~ ;;; ::: (Go)", "description": "d", "body": "b"}})
		},
		"W3": func() { call("POST", "/api/auth/refresh", alice.token, map[string]any{"ttl_hours": 1 + rng.Intn(24)}) },
		"W4": func() {
			if rng.Intn(3) == 0 {
				createArticle(alice, fmt.Sprintf("Tagged %d", rng.Int()), "go")
			}
			call("GET", "/api/tags", "", nil)
		},
		"W5": func() {
			id := createComment(bob, slugs[0], "to be deleted")
			call("DELETE", fmt.Sprintf("/api/articles/%s/comments/%d", slugs[0], id), bob.token, nil)
		},
		"W7": func() { call("DELETE", "/api/articles/"+slugs[0]+"/comments/latest", bob.token, nil) },
		"W8": func() {
			missing := fmt.Sprintf("deleted-article-%d", rng.Intn(1000))
			switch rng.Intn(4) {
			case 0:
				call("GET", "/api/articles/"+missing, "", nil)
			case 1:
				call("PUT", "/api/articles/"+missing, alice.token, map[string]any{"article": map[string]any{"body": "x"}})
			case 2:
				call("DELETE", "/api/articles/"+missing, alice.token, nil)
			default:
				call("POST", "/api/articles/"+missing+"/favorite", bob.token, nil)
			}
		},
		"W9": func() { call("GET", "/api/articles/feed", bob.token, nil) },
	}
	ids := make([]string, 0, len(bugs))
	for id := range bugs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	sent := map[string]int{}
	for i := 0; i < *n; i++ {
		if rng.Float64() < *ratio {
			id := ids[rng.Intn(len(ids))]
			bugs[id]()
			sent[id]++
		} else {
			healthy[rng.Intn(len(healthy))]()
			sent["healthy"]++
		}
	}
	if *fatal {
		createComment(bob, slugs[0], "thanks @ everyone")
		sent["W6"]++
	}
	fmt.Println("requests sent:")
	for _, k := range append([]string{"healthy"}, append(ids, "W6")...) {
		if sent[k] > 0 {
			fmt.Printf("  %-8s %d\n", k, sent[k])
		}
	}
}
