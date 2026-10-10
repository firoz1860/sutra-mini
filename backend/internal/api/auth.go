package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/auth"
)

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func (s *Server) isOperator(r *http.Request) bool {
	return s.auth.Verify(bearerToken(r))
}

// requireOperator wraps a handler so only requests carrying a valid operator
// session token reach it. Server-side authorization: the token is verified by
// signature and expiry, not by any client-supplied flag.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isOperator(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "operator authentication required")
			return
		}
		next(w, r)
	}
}

type loginInput struct {
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeErr(w, http.StatusBadRequest, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in loginInput
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}

	token, exp, err := s.auth.Login(in.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"token":      token,
			"expires_at": exp.UTC().Format(time.RFC3339),
		})
	case errors.Is(err, auth.ErrNotConfigured):
		writeErr(w, http.StatusServiceUnavailable, "auth_not_configured",
			"operator authentication is not configured on this server")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "incorrect operator password")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": s.isOperator(r),
		"configured":    s.auth.Configured(),
	})
}
