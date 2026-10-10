package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

// decodeJSONBody enforces the same body contract the receipt handler uses:
// application/json, bounded size, no unknown fields, no trailing content. It
// writes the error response and returns false on failure.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeErr(w, http.StatusBadRequest, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, "trailing_json", "request body contains unexpected trailing content")
		return false
	}
	return true
}

type partyOut struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

func toPartyOut(p store.Party) partyOut {
	return partyOut{ID: p.ID, Name: p.Name, Phone: p.Phone}
}

func (s *Server) listParties(w http.ResponseWriter, r *http.Request) {
	parties, err := s.db.ListPartiesFull(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	out := make([]partyOut, 0, len(parties))
	for _, p := range parties {
		out = append(out, toPartyOut(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"parties": out})
}

type partyInput struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

func (s *Server) createParty(w http.ResponseWriter, r *http.Request) {
	var in partyInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	name, ok := validName(in.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_name", "name is required and must be at most 200 characters")
		return
	}
	phone, ok := validPhone(in.Phone)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_phone", "phone must be a valid contact number (digits, spaces, +, -, parentheses)")
		return
	}
	p, err := s.db.CreateParty(r.Context(), name, phone)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	writeJSON(w, http.StatusCreated, toPartyOut(p))
}

type partyPatch struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`
}

func (s *Server) updateParty(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "invalid_party_id", "party id must be a valid UUID")
		return
	}
	var in partyPatch
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if in.Name == nil && in.Phone == nil {
		writeErr(w, http.StatusBadRequest, "no_fields", "provide at least one of name or phone to update")
		return
	}
	var name, phone *string
	if in.Name != nil {
		v, ok := validName(*in.Name)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_name", "name must be non-empty and at most 200 characters")
			return
		}
		name = &v
	}
	if in.Phone != nil {
		v, ok := validPhone(*in.Phone)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_phone", "phone must be a valid contact number (digits, spaces, +, -, parentheses)")
			return
		}
		phone = &v
	}
	p, err := s.db.UpdateParty(r.Context(), id, name, phone)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toPartyOut(p))
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "party_not_found", "no job-worker with that id")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
