package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/accounting"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

type Config struct {
	WorkerID    string
	Poll        time.Duration
	Lease       time.Duration
	HTTPTimeout time.Duration
	BackoffBase time.Duration
	BackoffMax  time.Duration
	MaxAttempts int
	Concurrency int
}

func (c *Config) withDefaults() {
	if c.WorkerID == "" {
		c.WorkerID = "worker"
	}
	if c.Poll <= 0 {
		c.Poll = 500 * time.Millisecond
	}
	if c.Lease <= 0 {
		c.Lease = 10 * time.Second
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 2 * time.Second
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 500 * time.Millisecond
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
}

type Worker struct {
	db     *store.DB
	client *accounting.Client
	cfg    Config
	logger *log.Logger
}

func New(db *store.DB, client *accounting.Client, cfg Config, logger *log.Logger) *Worker {
	cfg.withDefaults()
	if logger == nil {
		logger = log.Default()
	}
	return &Worker{db: db, client: client, cfg: cfg, logger: logger}
}

func (w *Worker) Run(ctx context.Context) {
	w.logger.Printf("worker=%s starting poll=%s lease=%s concurrency=%d maxAttempts=%d",
		w.cfg.WorkerID, w.cfg.Poll, w.cfg.Lease, w.cfg.Concurrency, w.cfg.MaxAttempts)

	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	ticker := time.NewTicker(w.cfg.Poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Printf("worker=%s shutting down, waiting for in-flight work", w.cfg.WorkerID)
			wg.Wait()
			w.logger.Printf("worker=%s stopped", w.cfg.WorkerID)
			return
		case <-ticker.C:
			w.dispatch(ctx, sem, &wg)
		}
	}
}

func (w *Worker) dispatch(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	if n, err := w.db.ReapExhausted(ctx, w.cfg.MaxAttempts, "retry budget exhausted after repeated claims/crashes"); err != nil {
		if ctx.Err() == nil {
			w.logger.Printf("worker=%s reap error: %v", w.cfg.WorkerID, err)
		}
	} else if n > 0 {
		w.logger.Printf("worker=%s reaped %d voucher(s) with exhausted budget to failed", w.cfg.WorkerID, n)
	}

	acquired := 0
acquire:
	for acquired < w.cfg.Concurrency {
		select {
		case sem <- struct{}{}:
			acquired++
		default:
			break acquire
		}
	}
	if acquired == 0 {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	claims, err := w.db.Claim(claimCtx, w.cfg.WorkerID, w.cfg.Lease, acquired, w.cfg.MaxAttempts)
	cancel()
	if err != nil {
		for i := 0; i < acquired; i++ {
			<-sem
		}
		if ctx.Err() == nil {
			w.logger.Printf("worker=%s claim error: %v", w.cfg.WorkerID, err)
		}
		return
	}
	for i := len(claims); i < acquired; i++ {
		<-sem
	}
	for _, c := range claims {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.process(ctx, c)
		}()
	}
}

func (w *Worker) DrainOnce(ctx context.Context) (int, error) {
	if _, err := w.db.ReapExhausted(ctx, w.cfg.MaxAttempts, "retry budget exhausted after repeated claims/crashes"); err != nil {
		return 0, err
	}
	claims, err := w.db.Claim(ctx, w.cfg.WorkerID, w.cfg.Lease, w.cfg.Concurrency, w.cfg.MaxAttempts)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.cfg.Concurrency)
	for _, c := range claims {
		c := c
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.process(ctx, c)
		}()
	}
	wg.Wait()
	return len(claims), nil
}

func (w *Worker) process(ctx context.Context, c store.Claim) {
	attempt := c.Attempts - c.AttemptBaseline
	w.logger.Printf("worker=%s voucher=%s claimed attempt=%d client_ref=%s", w.cfg.WorkerID, c.ID, attempt, c.ClientRef)

	reqCtx, cancel := context.WithTimeout(ctx, w.cfg.HTTPTimeout)
	res, err := w.client.Push(reqCtx, accounting.PushRequest{
		ClientRef:   c.ClientRef,
		Type:        c.Type,
		AmountPaise: c.AmountPaise,
		SourceID:    c.SourceID,
	})
	cancel()

	w.finalize(c, attempt, res, err)
}

func (w *Worker) finalize(c store.Claim, attempt int, res accounting.Result, pushErr error) {
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if pushErr == nil {
		ok, dbErr := w.db.MarkPushed(fctx, c.ID, c.ClaimToken, res.ExternalRef)
		if dbErr != nil {
			w.logger.Printf("worker=%s voucher=%s mark pushed db error: %v", w.cfg.WorkerID, c.ID, dbErr)
			return
		}
		if !ok {
			w.logger.Printf("worker=%s voucher=%s stale claim, newer worker owns it (fenced)", w.cfg.WorkerID, c.ID)
			return
		}
		if res.Duplicate {
			w.logger.Printf("worker=%s voucher=%s reconciled via 409 external_ref=%s status=pushed", w.cfg.WorkerID, c.ID, res.ExternalRef)
		} else {
			w.logger.Printf("worker=%s voucher=%s pushed external_ref=%s", w.cfg.WorkerID, c.ID, res.ExternalRef)
		}
		return
	}

	var pe *accounting.PushError
	retryable := true
	timeout := false
	if errors.As(pushErr, &pe) {
		retryable = pe.Retryable
		timeout = pe.Timeout
	}

	lastErr := pushErr.Error()
	if timeout {
		lastErr = fmt.Sprintf("timeout after %s; remote outcome unknown, will re-dispatch and reconcile via 409", w.cfg.HTTPTimeout)
	}

	if retryable && attempt < w.cfg.MaxAttempts {
		delay := w.backoff(attempt)
		ok, dbErr := w.db.Reschedule(fctx, c.ID, c.ClaimToken, delay, lastErr)
		if dbErr != nil {
			w.logger.Printf("worker=%s voucher=%s reschedule db error: %v", w.cfg.WorkerID, c.ID, dbErr)
			return
		}
		if !ok {
			w.logger.Printf("worker=%s voucher=%s stale claim on reschedule (fenced)", w.cfg.WorkerID, c.ID)
			return
		}
		w.logger.Printf("worker=%s voucher=%s attempt=%d failed (%s); next attempt in %s", w.cfg.WorkerID, c.ID, attempt, lastErr, delay)
		return
	}

	ok, dbErr := w.db.MarkFailed(fctx, c.ID, c.ClaimToken, lastErr)
	if dbErr != nil {
		w.logger.Printf("worker=%s voucher=%s mark failed db error: %v", w.cfg.WorkerID, c.ID, dbErr)
		return
	}
	if !ok {
		w.logger.Printf("worker=%s voucher=%s stale claim on mark failed (fenced)", w.cfg.WorkerID, c.ID)
		return
	}
	reason := "retry budget exhausted"
	if !retryable {
		reason = "non-retryable error"
	}
	w.logger.Printf("worker=%s voucher=%s status=failed attempt=%d reason=%s last_error=%s", w.cfg.WorkerID, c.ID, attempt, reason, lastErr)
}

func (w *Worker) backoff(attempt int) time.Duration {
	d := w.cfg.BackoffBase
	for i := 1; i < attempt && d < w.cfg.BackoffMax; i++ {
		d *= 2
	}
	if d > w.cfg.BackoffMax {
		d = w.cfg.BackoffMax
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
