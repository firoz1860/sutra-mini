package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/accounting"
	"github.com/firoz1860/sutra-mini/backend/internal/config"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
	"github.com/firoz1860/sutra-mini/backend/internal/worker"
)

func main() {
	url := config.Str("DATABASE_URL", "")
	if url == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	accountingURL := config.Str("ACCOUNTING_URL", "http://localhost:8090")

	cfg := worker.Config{
		WorkerID:    config.Str("WORKER_ID", "worker-1"),
		Poll:        config.Dur("WORKER_POLL", 500*time.Millisecond),
		Lease:       config.Dur("WORKER_LEASE", 10*time.Second),
		HTTPTimeout: config.Dur("WORKER_HTTP_TIMEOUT", 2*time.Second),
		BackoffBase: config.Dur("WORKER_BACKOFF_BASE", 500*time.Millisecond),
		BackoffMax:  config.Dur("WORKER_BACKOFF_MAX", 30*time.Second),
		MaxAttempts: config.Int("WORKER_MAX_ATTEMPTS", 5),
		Concurrency: config.Int("WORKER_CONCURRENCY", 4),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	db, err := store.Open(openCtx, url)
	cancel()
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	client := accounting.NewClient(accountingURL, cfg.HTTPTimeout)
	w := worker.New(db, client, cfg, log.Default())
	w.Run(ctx)
}
