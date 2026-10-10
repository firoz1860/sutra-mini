package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/config"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

func main() {
	config.Load()

	if len(os.Args) < 2 {
		log.Fatal("usage: migrate [up|seed|all]")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := store.Open(ctx, url)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	switch os.Args[1] {
	case "up":
		mustRun(db.Migrate(ctx), "migrate")
		log.Println("migrations applied")
	case "seed":
		mustRun(db.Seed(ctx), "seed")
		log.Println("seed applied")
	case "all":
		mustRun(db.Migrate(ctx), "migrate")
		mustRun(db.Seed(ctx), "seed")
		log.Println("migrations and seed applied")
	default:
		log.Fatalf("unknown command %q (use up|seed|all)", os.Args[1])
	}
}

func mustRun(err error, what string) {
	if err != nil {
		log.Fatalf("%s failed: %v", what, err)
	}
}
