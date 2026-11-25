// Package testutil builds a fresh application (SQLite file DB, migrations,
// seed data, router) for trigger tests and benchmarks.
//
// goflrealworld: not in upstream. Requests go through app.NewRouter with
// ServeHTTP, so a seeded bug panics through the real chain
// mux -> middleware -> handler -> service -> repository.
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"goflrealworld/internal/app"
	"goflrealworld/internal/db"
	"goflrealworld/internal/model"
	"goflrealworld/internal/repository"
	"goflrealworld/internal/seed"
	"goflrealworld/internal/service"
	"goflrealworld/internal/utils"
)

// Secret is the JWT secret used by every test environment.
const Secret = "gofl-test-secret"

// User is a seeded user with a ready-to-use token.
type User struct {
	ID       int
	Email    string
	Username string
	Token    string
}

// Env is one isolated application instance.
type Env struct {
	tb       testing.TB
	DB       *db.Database
	Router   http.Handler
	Alice    User // author of the seeded articles
	Bob      User // follows Alice, has commented
	Carol    User // no articles, follows nobody
	Articles []string
	Comments []int // ids of Bob's comments on Articles[0]
}

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// New creates an application with an empty database (migrations applied).
func New(tb testing.TB) *Env {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "realworld.db")
	database, err := db.NewDatabase(path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { database.Close() })
	if err := database.MigrateDir(filepath.Join(moduleRoot(), "migrations")); err != nil {
		tb.Fatal(err)
	}
	return &Env{tb: tb, DB: database, Router: app.NewRouter(database.DB, Secret)}
}

// Seeded creates an application with three users, two tagged articles by
// Alice, Bob following Alice and two comments by Bob.
func Seeded(tb testing.TB) *Env {
	tb.Helper()
	e := New(tb)
	e.Alice = e.AddUser("alice")
	e.Bob = e.AddUser("bob")
	e.Carol = e.AddUser("carol")

	e.Articles = append(e.Articles,
		e.AddArticle(e.Alice, "How to train your dragon", "go", "testing"),
		e.AddArticle(e.Alice, "Profiling Go services", "go", "performance"))

	users := repository.NewUserRepository(e.DB.DB)
	if err := users.FollowUser(e.Bob.ID, e.Alice.ID); err != nil {
		tb.Fatal(err)
	}
	comments := repository.NewCommentRepository(e.DB.DB)
	articleID, err := comments.GetArticleIDBySlug(e.Articles[0])
	if err != nil {
		tb.Fatal(err)
	}
	for _, body := range []string{"Great read", "Thanks for sharing"} {
		c := &model.Comment{Body: body, AuthorID: e.Bob.ID, ArticleID: articleID}
		if err := comments.Create(c); err != nil {
			tb.Fatal(err)
		}
		e.Comments = append(e.Comments, c.ID)
	}
	return e
}

// fastHash is the bcrypt hash of "password123", computed once so seeding
// does not run bcrypt for every user.
var fastHash = func() string {
	h, err := utils.HashPassword("password123")
	if err != nil {
		panic(err)
	}
	return h
}()

// AddUser inserts a user directly through the repository.
func (e *Env) AddUser(name string) User {
	e.tb.Helper()
	u := &model.User{Email: name + "@example.com", Username: name, PasswordHash: fastHash}
	if err := repository.NewUserRepository(e.DB.DB).Create(u); err != nil {
		e.tb.Fatal(err)
	}
	return User{ID: u.ID, Email: u.Email, Username: u.Username, Token: e.Token(u.ID, u.Email)}
}

// Fill bulk-loads data with package seed (password "password123" for every user).
func (e *Env) Fill(spec seed.Spec) *seed.Result {
	e.tb.Helper()
	if spec.PasswordHash == "" {
		spec.PasswordHash = fastHash
	}
	res, err := seed.Fill(e.DB.DB, spec)
	if err != nil {
		e.tb.Fatal(err)
	}
	return res
}

// Token issues a valid JWT for any user id, existing or not.
func (e *Env) Token(id int, email string) string {
	tok, err := utils.GenerateToken(id, email, Secret)
	if err != nil {
		e.tb.Fatal(err)
	}
	return tok
}

// AddArticle creates an article through the service layer and returns its slug.
func (e *Env) AddArticle(author User, title string, tags ...string) string {
	e.tb.Helper()
	svc := e.ArticleService()
	var req model.CreateArticleRequest
	req.Article.Title = title
	req.Article.Description = "About " + title
	req.Article.Body = "Body of " + title
	req.Article.TagList = tags
	a, err := svc.CreateArticle(req, author.ID)
	if err != nil {
		e.tb.Fatal(err)
	}
	return a.Slug
}

// ArticleService returns a service wired like app.NewRouter does.
func (e *Env) ArticleService() *service.ArticleService {
	return service.NewArticleService(
		repository.NewArticleRepository(e.DB.DB),
		repository.NewUserRepository(e.DB.DB),
		service.NewTagService(repository.NewTagRepository(e.DB.DB)))
}

// Do sends one request through the router and returns the recorder.
// token may be empty. body is JSON-encoded when not nil.
func (e *Env) Do(method, path, token string, body any) *httptest.ResponseRecorder {
	e.tb.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.tb.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.Router.ServeHTTP(rec, req)
	return rec
}

// Expect fails the test unless the response has the wanted status.
func (e *Env) Expect(rec *httptest.ResponseRecorder, want int) {
	e.tb.Helper()
	if rec.Code != want {
		e.tb.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

// Path formats a request path.
func Path(format string, args ...any) string { return fmt.Sprintf(format, args...) }
