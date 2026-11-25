// Command seed fills the server's SQLite database with a large, deterministic
// data set, so that the perf bugs (WP3-WP6) show up when the server runs.
//
// goflrealworld: not in upstream. Run it from the module root while the
// server is stopped, then start the server on the same file:
//
//	go run ./cmd/seed                       # default: realworld.db, 5000 users, 20000 articles
//	go run ./cmd/seed -users 500 -articles 2000 -tags 300
//	go run ./cmd/server 2> crash.log
//
// Every seeded user (user00000, user00001, ...) has the password
// "password123". Seeding an existing database adds a second batch with
// fresh slugs; use -fresh to delete the file first.
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"goflrealworld/internal/db"
	"goflrealworld/internal/seed"
	"goflrealworld/internal/utils"
)

func main() {
	path := flag.String("db", "realworld.db", "SQLite database file (the server's DATABASE_URL)")
	fresh := flag.Bool("fresh", false, "delete the database file first")
	var s seed.Spec
	flag.IntVar(&s.Users, "users", 5000, "users")
	flag.IntVar(&s.Articles, "articles", 20000, "articles, spread over the users")
	flag.IntVar(&s.Tags, "tags", 2000, "distinct tags")
	flag.IntVar(&s.TagsPerArticle, "tags-per-article", 3, "tags on each article")
	flag.IntVar(&s.FollowsPerUser, "follows", 20, "users each user follows")
	flag.IntVar(&s.FavsPerArticle, "favs", 5, "favorites on each article")
	flag.IntVar(&s.CommentsPerArt, "comments", 3, "comments on each article")
	flag.IntVar(&s.BodyBytes, "body", 2000, "article body length in bytes")
	flag.StringVar(&s.UsernamePrefix, "prefix", "user", "username prefix")
	flag.Parse()

	if *fresh {
		if err := os.Remove(*path); err != nil && !os.IsNotExist(err) {
			log.Fatal(err)
		}
	}
	database, err := db.NewDatabase(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if err := database.MigrateDir("migrations"); err != nil {
		log.Fatal("migrations (run from the module root): ", err)
	}

	// Offset slugs past what is already there, so seeding twice works.
	if err := database.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&s.ArticleIDOffset); err != nil {
		log.Fatal(err)
	}
	if s.PasswordHash, err = utils.HashPassword("password123"); err != nil {
		log.Fatal(err)
	}

	start := time.Now()
	res, err := seed.Fill(database.DB, s)
	if err != nil {
		log.Fatal(err, " (users already seeded? use -prefix or -fresh)")
	}
	log.Printf("seeded %s in %v: %d users, %d articles, %d tags, %d follows, %d favorites, %d comments",
		*path, time.Since(start).Round(time.Millisecond), len(res.UserIDs), len(res.ArticleIDs), s.Tags,
		len(res.UserIDs)*min(s.FollowsPerUser, max(s.Users-1, 0)), len(res.ArticleIDs)*min(s.FavsPerArticle, s.Users),
		len(res.ArticleIDs)*s.CommentsPerArt)
	if len(res.Usernames) > 0 {
		log.Printf("log in as %s@example.com / password123", res.Usernames[0])
	}
}
