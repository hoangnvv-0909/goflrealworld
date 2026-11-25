package repository_test

// goflrealworld: not in upstream. Perf benchmarks (sub-benchmarks n=<size>
// are the base/scaled load pairs used by `gofl perf`) and checks that the
// slow code still returns the same answer as the fast upstream query.

import (
	"fmt"
	"slices"
	"testing"

	"goflrealworld/internal/repository"
	"goflrealworld/internal/seed"
	"goflrealworld/internal/testutil"
)

var sizes = []int{100, 1000, 5000}

// WP4: one profile lookup with n users in the table. With the index the
// cost is about constant; LOWER(username) makes it a full scan, linear in n.
func BenchmarkGetByUsername(b *testing.B) {
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			e := testutil.New(b)
			res := e.Fill(seed.Spec{Users: n})
			users := repository.NewUserRepository(e.DB.DB)
			name := res.Usernames[n/2]
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := users.GetByUsername(name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// WP5: popular tags with n tags in the database (n articles, 3 tags each).
func BenchmarkGetPopularTags(b *testing.B) {
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			e := testutil.New(b)
			e.Fill(seed.Spec{Users: 10, Articles: n, Tags: n, TagsPerArticle: 3})
			tags := repository.NewTagRepository(e.DB.DB)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got, err := tags.GetPopularTags(20); err != nil || len(got) != 20 {
					b.Fatal(err, len(got))
				}
			}
		})
	}
}

// The upstream single-query version, kept as the reference answer.
const popularTagsSQL = `
	SELECT t.name
	FROM tags t
	INNER JOIN article_tags at ON t.id = at.tag_id
	GROUP BY t.id, t.name
	ORDER BY COUNT(at.article_id) DESC, t.name ASC
	LIMIT ?`

func TestPopularTagsMatchesUpstream(t *testing.T) {
	e := testutil.New(t)
	e.Fill(seed.Spec{Users: 5, Articles: 40, Tags: 30, TagsPerArticle: 4})
	// Unused tags must not show up.
	if _, err := e.DB.DB.Exec(`INSERT INTO tags (name) VALUES ('aaa-unused'), ('zzz-unused')`); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 5, 20, 100} {
		got, err := repository.NewTagRepository(e.DB.DB).GetPopularTags(limit)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := e.DB.DB.Query(popularTagsSQL, limit)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for rows.Next() {
			var s string
			rows.Scan(&s)
			want = append(want, s)
		}
		rows.Close()
		if !slices.Equal(got, want) {
			t.Errorf("limit %d:\n got %v\nwant %v", limit, got, want)
		}
	}
}

func TestGetByUsernameCaseInsensitive(t *testing.T) {
	e := testutil.Seeded(t)
	u, err := repository.NewUserRepository(e.DB.DB).GetByUsername("Alice")
	if err != nil || u.ID != e.Alice.ID {
		t.Fatal(u, err)
	}
}
