package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/firoz1860/sutra-mini/backend/internal/issues"
)

type issueInput struct {
	PartyID        string `json:"party_id"`
	SKUID          string `json:"sku_id"`
	Qty            int    `json:"qty"`
	IssuedOn       string `json:"issued_on"`
	UnitRateRupees string `json:"unit_rate_rupees"`
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeErr(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	if len(key) > maxKeyLen {
		writeErr(w, http.StatusBadRequest, "idempotency_key_too_long", "Idempotency-Key exceeds maximum length")
		return
	}

	var in issueInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if !uuidRe.MatchString(in.PartyID) {
		writeErr(w, http.StatusBadRequest, "invalid_party_id", "party_id must be a valid UUID")
		return
	}
	if !uuidRe.MatchString(in.SKUID) {
		writeErr(w, http.StatusBadRequest, "invalid_sku_id", "sku_id must be a valid UUID")
		return
	}
	if in.Qty <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_qty", "qty must be a positive integer")
		return
	}
	if !validDate(in.IssuedOn) {
		writeErr(w, http.StatusBadRequest, "invalid_issued_on", "issued_on must be a valid YYYY-MM-DD date")
		return
	}
	ratePaise, err := parseRupeesToPaise(in.UnitRateRupees)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_rate", "unit_rate_rupees must be rupees with at most 2 decimal places")
		return
	}

	status, body, err := s.issues.Create(r.Context(), key, issues.Request{
		PartyID:       in.PartyID,
		SKUID:         in.SKUID,
		Qty:           in.Qty,
		UnitRatePaise: ratePaise,
		IssuedOn:      in.IssuedOn,
	})
	switch {
	case err == nil:
		writeRaw(w, status, body)
	case errors.Is(err, issues.ErrPartyNotFound):
		writeErr(w, http.StatusNotFound, "party_not_found", "no job-worker with that id")
	case errors.Is(err, issues.ErrSKUNotFound):
		writeErr(w, http.StatusNotFound, "sku_not_found", "no SKU with that id")
	case errors.Is(err, issues.ErrKeyConflict):
		writeErr(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request payload")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
