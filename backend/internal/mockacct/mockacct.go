package mockacct

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Mode string

const (
	ModeRandom  Mode = "random"
	ModeSuccess Mode = "success"
	ModeFail    Mode = "fail"
	ModeDelay   Mode = "delay"
)

func parseMode(s string) (Mode, bool) {
	switch Mode(s) {
	case ModeRandom, ModeSuccess, ModeFail, ModeDelay:
		return Mode(s), true
	default:
		return "", false
	}
}

type Server struct {
	pool   *pgxpool.Pool
	mode   Mode
	delay  time.Duration
	logger *log.Logger

	mu        sync.RWMutex
	perClient map[string]Mode

	OnDelayedAccept func(clientRef string)
}

func New(pool *pgxpool.Pool, mode Mode, delay time.Duration, logger *log.Logger) *Server {
	if mode == "" {
		mode = ModeRandom
	}
	if delay <= 0 {
		delay = 5 * time.Second
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Server{pool: pool, mode: mode, delay: delay, logger: logger, perClient: map[string]Mode{}}
}

func (s *Server) SetClientMode(clientRef string, mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.perClient[clientRef] = mode
}

func (s *Server) clientMode(clientRef string) (Mode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.perClient[clientRef]
	return m, ok
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/vouchers", s.postVoucher)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

type pushRequest struct {
	ClientRef   string `json:"client_ref"`
	Type        string `json:"type"`
	AmountPaise int64  `json:"amount_paise"`
	SourceID    string `json:"source_id"`
}

type okResponse struct {
	ExternalRef string `json:"external_ref"`
	ClientRef   string `json:"client_ref"`
	Status      string `json:"status"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) postVoucher(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.ClientRef == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "client_ref is required"})
		return
	}

	if ref, dup, err := s.lookup(r.Context(), req.ClientRef); err != nil {
		s.logger.Printf("mock: lookup error client_ref=%s err=%v", req.ClientRef, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	} else if dup {
		s.logger.Printf("mock: duplicate client_ref=%s external_ref=%s", req.ClientRef, ref)
		writeJSON(w, http.StatusConflict, okResponse{ExternalRef: ref, ClientRef: req.ClientRef, Status: "duplicate"})
		return
	}

	mode := s.mode
	if h := r.Header.Get("X-Mock-Mode"); h != "" {
		if m, ok := parseMode(h); ok {
			mode = m
		}
	}
	if m, ok := s.clientMode(req.ClientRef); ok {
		mode = m
	}

	switch mode {
	case ModeFail:
		s.logger.Printf("mock: forced failure client_ref=%s", req.ClientRef)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulated accounting failure"})
	case ModeSuccess:
		s.commitAndRespond(r.Context(), w, req)
	case ModeDelay:
		s.delayedCommit(w, r, req)
	default:
		roll := rand.Float64()
		switch {
		case roll < 0.20:
			s.logger.Printf("mock: random failure client_ref=%s", req.ClientRef)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulated accounting failure"})
		case roll < 0.30:
			s.delayedCommit(w, r, req)
		default:
			s.commitAndRespond(r.Context(), w, req)
		}
	}
}

func (s *Server) commitAndRespond(ctx context.Context, w http.ResponseWriter, req pushRequest) {
	ref, dup, err := s.commit(ctx, req)
	if err != nil {
		s.logger.Printf("mock: commit error client_ref=%s err=%v", req.ClientRef, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "commit failed"})
		return
	}
	if dup {
		writeJSON(w, http.StatusConflict, okResponse{ExternalRef: ref, ClientRef: req.ClientRef, Status: "duplicate"})
		return
	}
	s.logger.Printf("mock: posted client_ref=%s external_ref=%s", req.ClientRef, ref)
	writeJSON(w, http.StatusOK, okResponse{ExternalRef: ref, ClientRef: req.ClientRef, Status: "posted"})
}

func (s *Server) delayedCommit(w http.ResponseWriter, r *http.Request, req pushRequest) {
	s.logger.Printf("mock: accepted delayed request client_ref=%s (will commit in %s)", req.ClientRef, s.delay)
	if s.OnDelayedAccept != nil {
		s.OnDelayedAccept(req.ClientRef)
	}
	type outcome struct {
		ref string
		dup bool
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), s.delay+5*time.Second)
		defer cancel()
		select {
		case <-time.After(s.delay):
		case <-bg.Done():
			done <- outcome{err: bg.Err()}
			return
		}
		ref, dup, err := s.commit(bg, req)
		if err == nil {
			s.logger.Printf("mock: delayed commit completed client_ref=%s external_ref=%s dup=%v", req.ClientRef, ref, dup)
		}
		done <- outcome{ref: ref, dup: dup, err: err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "commit failed"})
			return
		}
		if o.dup {
			writeJSON(w, http.StatusConflict, okResponse{ExternalRef: o.ref, ClientRef: req.ClientRef, Status: "duplicate"})
			return
		}
		writeJSON(w, http.StatusOK, okResponse{ExternalRef: o.ref, ClientRef: req.ClientRef, Status: "posted"})
	case <-r.Context().Done():
		s.logger.Printf("mock: client canceled during delay; server-owned commit continues client_ref=%s", req.ClientRef)
	}
}

func (s *Server) lookup(ctx context.Context, clientRef string) (string, bool, error) {
	var ref string
	err := s.pool.QueryRow(ctx, `SELECT external_ref FROM accounting_records WHERE client_ref=$1`, clientRef).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return ref, true, nil
}

func (s *Server) commit(ctx context.Context, req pushRequest) (string, bool, error) {
	newRef := "EXT-" + randomHex()
	var ref string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO accounting_records (client_ref, external_ref, type, amount_paise)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (client_ref) DO NOTHING
		 RETURNING external_ref`,
		req.ClientRef, newRef, req.Type, req.AmountPaise).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		if e := s.pool.QueryRow(ctx, `SELECT external_ref FROM accounting_records WHERE client_ref=$1`, req.ClientRef).Scan(&ref); e != nil {
			return "", false, e
		}
		return ref, true, nil
	}
	if err != nil {
		return "", false, err
	}
	return ref, false, nil
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func randomHex() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
