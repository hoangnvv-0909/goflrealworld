package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"

	"goflrealworld/internal/app"
	"goflrealworld/internal/config"
	"goflrealworld/internal/db"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Initialize database
	database, err := db.NewDatabase(cfg.DatabaseURL)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer database.Close()

	// Run migrations
	log.Println("Running database migrations...")
	if err := database.Migrate(); err != nil {
		log.Fatal("Failed to run migrations:", err)
	}
	log.Println("Database migrations completed successfully")

	router := app.NewRouter(database.DB, cfg.JWTSecret)

	// goflrealworld: pprof on a separate local port, for `gofl perf` and
	// `go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30`.
	// PPROF_ADDR=off disables it.
	if pprofAddr := getenv("PPROF_ADDR", "localhost:6060"); pprofAddr != "off" {
		go func() {
			log.Printf("pprof on http://%s/debug/pprof/", pprofAddr)
			log.Println(http.ListenAndServe(pprofAddr, nil)) // nil: DefaultServeMux, where net/http/pprof registers
		}()
	}

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
