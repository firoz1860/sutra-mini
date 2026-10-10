package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/api"
	"github.com/firoz1860/sutra-mini/backend/internal/config"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

func main() {
	config.Load()

	url := config.Str("DATABASE_URL", "")
	if url == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	port := config.Str("PORT", "8080")
	origins := splitCSV(config.Str("CORS_ALLOWED_ORIGINS", "*"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	db, err := store.Open(openCtx, url)
	cancel()
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if config.Str("AUTO_MIGRATE", "") == "true" {
		if err := db.Migrate(ctx); err != nil {
			log.Fatalf("auto-migrate: %v", err)
		}
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.New(db, origins...).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("api listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("api shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
