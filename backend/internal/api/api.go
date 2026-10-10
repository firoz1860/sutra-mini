package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/receipts"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

const (
	maxBodyBytes = 1 << 20
	maxKeyLen    = 200
	listLimit    = 200
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Server struct {
	db             *store.DB
	receipts       *receipts.Service
	allowedOrigins []string
}

func New(db *store.DB, allowedOrigins ...string) *Server {
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}
	return &Server{db: db, receipts: receipts.New(db.Pool), allowedOrigins: allowedOrigins}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobwork/receipts", s.createReceipt)
	mux.HandleFunc("GET /issues/{id}", s.getIssue)
	mux.HandleFunc("GET /vouchers", s.listVouchers)
	mux.HandleFunc("POST /vouchers/{id}/retry", s.retryVoucher)
	mux.HandleFunc("GET /healthz", s.healthz)
	return s.withCORS(mux)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allow := s.resolveOrigin(r.Header.Get("Origin")); allow != "" {
			w.Header().Set("Access-Control-Allow-Origin", allow)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) resolveOrigin(origin string) string {
	if origin == "" {
		return ""
	}
	for _, o := range s.allowedOrigins {
		if o == "*" {
			return "*"
		}
		if o == origin {
			return origin
		}
	}
	return ""
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: msg, Code: code})
}

type receiptInput struct {
	IssueID    string `json:"issue_id"`
	Qty        int    `json:"qty"`
	ReceivedOn string `json:"received_on"`
}

func (s *Server) createReceipt(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeErr(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	if len(key) > maxKeyLen {
		writeErr(w, http.StatusBadRequest, "idempotency_key_too_long", "Idempotency-Key exceeds maximum length")
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeErr(w, http.StatusBadRequest, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in receiptInput
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, "trailing_json", "request body contains unexpected trailing content")
		return
	}

	if !uuidRe.MatchString(in.IssueID) {
		writeErr(w, http.StatusBadRequest, "invalid_issue_id", "issue_id must be a valid UUID")
		return
	}
	if in.Qty <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_qty", "qty must be a positive integer")
		return
	}
	if !validDate(in.ReceivedOn) {
		writeErr(w, http.StatusBadRequest, "invalid_received_on", "received_on must be a valid YYYY-MM-DD date")
		return
	}

	status, body, err := s.receipts.Create(r.Context(), key, receipts.Request{
		IssueID:    in.IssueID,
		Qty:        in.Qty,
		ReceivedOn: in.ReceivedOn,
	})
	switch {
	case err == nil:
		writeRaw(w, status, body)
	case errors.Is(err, receipts.ErrIssueNotFound):
		writeErr(w, http.StatusNotFound, "issue_not_found", "no jobwork issue with that id")
	case errors.Is(err, receipts.ErrOvershoot):
		writeErr(w, http.StatusConflict, "overshoot", "receipt quantity exceeds outstanding quantity for the issue")
	case errors.Is(err, receipts.ErrKeyConflict):
		writeErr(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request payload")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

type issueResponse struct {
	ID          string `json:"id"`
	IssuedQty   int    `json:"issued_qty"`
	ReceivedQty int    `json:"received_qty"`
	Outstanding int    `json:"outstanding_qty"`
	SKU         struct {
		ID       string `json:"id"`
		Code     string `json:"code"`
		Name     string `json:"name"`
		ImageURL string `json:"image_url"`
	} `json:"sku"`
	Party struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Phone string `json:"phone"`
	} `json:"party"`
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "invalid_issue_id", "issue id must be a valid UUID")
		return
	}
	t, err := s.db.IssueTotals(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "issue_not_found", "no jobwork issue with that id")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	var resp issueResponse
	resp.ID = t.IssueID
	resp.IssuedQty = t.IssuedQty
	resp.ReceivedQty = t.ReceivedQty
	resp.Outstanding = t.Outstanding
	resp.SKU.ID, resp.SKU.Code, resp.SKU.Name, resp.SKU.ImageURL = t.SKU.ID, t.SKU.Code, t.SKU.Name, t.SKU.ImageURL
	resp.Party.ID, resp.Party.Name, resp.Party.Phone = t.Party.ID, t.Party.Name, t.Party.Phone
	writeJSON(w, http.StatusOK, resp)
}

type voucherResponse struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	SourceID      string  `json:"source_id"`
	AmountPaise   int64   `json:"amount_paise"`
	Status        string  `json:"status"`
	Attempts      int     `json:"attempts"`
	LastError     *string `json:"last_error"`
	ClientRef     string  `json:"client_ref"`
	ExternalRef   *string `json:"external_ref"`
	NextAttemptAt string  `json:"next_attempt_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func toVoucherResponse(v store.Voucher) voucherResponse {
	return voucherResponse{
		ID: v.ID, Type: v.Type, SourceID: v.SourceID, AmountPaise: v.AmountPaise,
		Status: v.Status, Attempts: v.Attempts, LastError: v.LastError, ClientRef: v.ClientRef,
		ExternalRef:   v.ExternalRef,
		NextAttemptAt: v.NextAttemptAt.UTC().Format(time.RFC3339),
		CreatedAt:     v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     v.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) listVouchers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "pending", "pushed", "failed":
	default:
		writeErr(w, http.StatusBadRequest, "invalid_status", "status must be one of pending, pushed, failed")
		return
	}
	vouchers, err := s.db.ListVouchers(r.Context(), status, listLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	out := make([]voucherResponse, 0, len(vouchers))
	for _, v := range vouchers {
		out = append(out, toVoucherResponse(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"vouchers": out})
}

func (s *Server) retryVoucher(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "invalid_voucher_id", "voucher id must be a valid UUID")
		return
	}
	v, err := s.db.RetryVoucher(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toVoucherResponse(v))
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "voucher_not_found", "no voucher with that id")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "retry_not_allowed", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Health(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return false
	}
	return t.Format("2006-01-02") == s
}
