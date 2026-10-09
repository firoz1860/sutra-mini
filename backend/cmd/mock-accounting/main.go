package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/config"
	"github.com/firoz1860/sutra-mini/backend/internal/mockacct"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

func main() {
	url := config.Str("DATABASE_URL", "")
	if url == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	port := config.Str("MOCK_PORT", "8090")
	mode := mockacct.Mode(config.Str("MOCK_MODE", string(mockacct.ModeRandom)))
	delay := config.Dur("MOCK_DELAY", 5*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	db, err := store.Open(openCtx, url)
	cancel()
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	server := mockacct.New(db.Pool, mode, delay, log.Default())
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("mock-accounting listening on :%s mode=%s delay=%s", port, mode, delay)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("mock-accounting shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}
