package service_test

import (
	"fmt"
	"testing"

	"goflrealworld/internal/repository"
	"goflrealworld/internal/seed"
	"goflrealworld/internal/service"
	"goflrealworld/internal/testutil"
)

// WP3: one page of the global article list. The API caps limit at 100, so
// the sizes are page sizes, not the 100/1000/5000 used elsewhere.
//
// The SQLite driver is pure Go (modernc.org/sqlite), so query time shows up
// in the CPU profile as modernc.org/sqlite/lib frames: the hotspot is in the
// driver, the fix is the loop in ArticleService.GetArticles.
func BenchmarkGetArticles(b *testing.B) {
	for _, n := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			e := testutil.New(b)
			authors := make([]testutil.User, 10)
			for i := range authors {
				authors[i] = e.AddUser(fmt.Sprintf("author%d", i))
			}
			for i := 0; i < n; i++ {
				e.AddArticle(authors[i%len(authors)], fmt.Sprintf("Article %d", i), "go", fmt.Sprintf("t%d", i%7))
			}
			svc := e.ArticleService()
			reader := authors[0].ID
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				res, err := svc.GetArticles(service.ArticleListParams{Limit: n}, reader)
				if err != nil || len(res.Articles) != n {
					b.Fatal(err, len(res.Articles))
				}
			}
		})
	}
}

// WP6: one page (10 articles) of the feed of a user who follows 5 of 50
// authors, with n articles in the whole database. Upstream joins follows in
// SQL and reads one page; the seeded code reads all n articles.
func BenchmarkGetArticlesFeed(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			e := testutil.New(b)
			res := e.Fill(seed.Spec{Users: 50, Articles: n, FollowsPerUser: 5})
			svc := e.ArticleService()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := svc.GetArticlesFeed(service.ArticleListParams{Limit: 10}, res.UserIDs[0])
				if err != nil || len(got.Articles) != 10 {
					b.Fatal(err, len(got.Articles))
				}
			}
		})
	}
}

// The feed must hold the same articles, in the same order, as upstream
// GetFeedArticles (which is still in the repository, unused).
func TestFeedMatchesUpstream(t *testing.T) {
	e := testutil.New(t)
	res := e.Fill(seed.Spec{Users: 20, Articles: 300, FollowsPerUser: 3})
	svc := e.ArticleService()
	articles := repository.NewArticleRepository(e.DB.DB)
	for _, p := range []service.ArticleListParams{{Limit: 10}, {Limit: 7, Offset: 20}, {Limit: 50, Offset: 30}, {Limit: 10, Offset: 1000}} {
		got, err := svc.GetArticlesFeed(p, res.UserIDs[3])
		if err != nil {
			t.Fatal(err)
		}
		want, wantCount, err := articles.GetFeedArticles(p.Limit, p.Offset, res.UserIDs[3])
		if err != nil {
			t.Fatal(err)
		}
		if got.ArticlesCount != wantCount || len(got.Articles) != len(want) {
			t.Fatalf("%+v: got %d/%d articles, want %d/%d", p, len(got.Articles), got.ArticlesCount, len(want), wantCount)
		}
		for i := range want {
			if got.Articles[i].Slug != want[i].Slug {
				t.Fatalf("%+v: article %d is %s, want %s", p, i, got.Articles[i].Slug, want[i].Slug)
			}
		}
	}
}
