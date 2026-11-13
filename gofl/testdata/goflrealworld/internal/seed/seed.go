// Package seed bulk-loads deterministic data straight into the database, so
// perf benchmarks and a running server can have realistic volumes (thousands
// of users, articles and tags) without going through the slow API.
//
// goflrealworld: not in upstream. Used by cmd/seed and the perf benchmarks.
package seed

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Spec says how much data to insert. Zero fields insert nothing of that kind.
type Spec struct {
	Users           int
	Articles        int // authors are spread round-robin over Users
	Tags            int
	TagsPerArticle  int // must be <= Tags
	FollowsPerUser  int // user u follows u+1 .. u+FollowsPerUser (mod Users)
	FavsPerArticle  int
	CommentsPerArt  int
	BodyBytes       int    // article body length; 0 means 400
	PasswordHash    string // stored for every user; required when Users > 0
	UsernamePrefix  string // default "user"
	ArticleIDOffset int    // added to slugs and titles, to run Fill twice on one DB
}

// Result holds the ids of what was inserted, in insertion order.
type Result struct {
	UserIDs    []int
	ArticleIDs []int
	Usernames  []string
	Slugs      []string
}

// Base is the created_at of the first seeded article; later articles are one
// minute apart, so seeded data is always older than data created at run time.
var Base = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// Fill inserts the data described by spec in one transaction.
func Fill(db *sql.DB, spec Spec) (*Result, error) {
	if spec.Users > 0 && spec.PasswordHash == "" {
		return nil, fmt.Errorf("seed: PasswordHash is required")
	}
	if spec.TagsPerArticle > spec.Tags {
		return nil, fmt.Errorf("seed: TagsPerArticle %d > Tags %d", spec.TagsPerArticle, spec.Tags)
	}
	if (spec.Articles > 0 || spec.FollowsPerUser > 0 || spec.FavsPerArticle > 0) && spec.Users == 0 {
		return nil, fmt.Errorf("seed: articles, follows and favorites need Users > 0")
	}
	if spec.UsernamePrefix == "" {
		spec.UsernamePrefix = "user"
	}
	if spec.BodyBytes == 0 {
		spec.BodyBytes = 400
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res := &Result{}

	exec := func(query string, n int, args func(i int) []any, ids *[]int) error {
		stmt, err := tx.Prepare(query)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := 0; i < n; i++ {
			r, err := stmt.Exec(args(i)...)
			if err != nil {
				return fmt.Errorf("seed: %w", err)
			}
			if ids != nil {
				id, _ := r.LastInsertId()
				*ids = append(*ids, int(id))
			}
		}
		return nil
	}

	for i := 0; i < spec.Users; i++ {
		res.Usernames = append(res.Usernames, fmt.Sprintf("%s%05d", spec.UsernamePrefix, i))
	}
	err = exec(`INSERT INTO users (email, username, password_hash) VALUES (?, ?, ?)`, spec.Users,
		func(i int) []any {
			return []any{res.Usernames[i] + "@example.com", res.Usernames[i], spec.PasswordHash}
		}, &res.UserIDs)
	if err != nil {
		return nil, err
	}

	var tagIDs []int
	err = exec(`INSERT INTO tags (name) VALUES (?)`, spec.Tags,
		func(i int) []any { return []any{fmt.Sprintf("tag%05d", i)} }, &tagIDs)
	if err != nil {
		return nil, err
	}

	body := strings.Repeat("Seeded article body. ", spec.BodyBytes/21+1)[:spec.BodyBytes]
	for i := 0; i < spec.Articles; i++ {
		res.Slugs = append(res.Slugs, fmt.Sprintf("seeded-article-%d", spec.ArticleIDOffset+i))
	}
	err = exec(`INSERT INTO articles (slug, title, description, body, author_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, spec.Articles,
		func(i int) []any {
			at := Base.Add(time.Duration(spec.ArticleIDOffset+i) * time.Minute)
			return []any{res.Slugs[i], fmt.Sprintf("Seeded article %d", spec.ArticleIDOffset+i),
				"A seeded article", body, res.UserIDs[i%spec.Users], at, at}
		}, &res.ArticleIDs)
	if err != nil {
		return nil, err
	}

	// Article i gets tags i*7, i*7+1, ... (mod Tags): distinct, and the
	// tag usage counts differ so "popular" has a real order.
	err = exec(`INSERT INTO article_tags (article_id, tag_id) VALUES (?, ?)`, spec.Articles*spec.TagsPerArticle,
		func(k int) []any {
			i, j := k/spec.TagsPerArticle, k%spec.TagsPerArticle
			return []any{res.ArticleIDs[i], tagIDs[(i*7+j)%spec.Tags]}
		}, nil)
	if err != nil {
		return nil, err
	}

	follows := min(spec.FollowsPerUser, spec.Users-1)
	err = exec(`INSERT INTO follows (follower_id, followed_id) VALUES (?, ?)`, spec.Users*max(follows, 0),
		func(k int) []any {
			u, j := k/follows, k%follows
			return []any{res.UserIDs[u], res.UserIDs[(u+1+j)%spec.Users]}
		}, nil)
	if err != nil {
		return nil, err
	}

	favs := min(spec.FavsPerArticle, spec.Users)
	err = exec(`INSERT INTO favorites (user_id, article_id) VALUES (?, ?)`, spec.Articles*max(favs, 0),
		func(k int) []any {
			i, j := k/favs, k%favs
			return []any{res.UserIDs[(i+j)%spec.Users], res.ArticleIDs[i]}
		}, nil)
	if err != nil {
		return nil, err
	}

	err = exec(`INSERT INTO comments (body, author_id, article_id) VALUES (?, ?, ?)`, spec.Articles*spec.CommentsPerArt,
		func(k int) []any {
			i, j := k/spec.CommentsPerArt, k%spec.CommentsPerArt
			return []any{fmt.Sprintf("Seeded comment %d", j), res.UserIDs[(i+j+1)%spec.Users], res.ArticleIDs[i]}
		}, nil)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`ANALYZE`); err != nil {
		return nil, err
	}
	return res, tx.Commit()
}
