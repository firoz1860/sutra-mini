package api

import (
	"errors"
	"net/http"

	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

type skuOut struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
}

func toSKUOut(s store.SKU) skuOut {
	return skuOut{ID: s.ID, Code: s.Code, Name: s.Name, ImageURL: s.ImageURL}
}

func (s *Server) listSKUs(w http.ResponseWriter, r *http.Request) {
	skus, err := s.db.ListSKUsFull(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	out := make([]skuOut, 0, len(skus))
	for _, sk := range skus {
		out = append(out, toSKUOut(sk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skus": out})
}

type skuInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
}

func (s *Server) createSKU(w http.ResponseWriter, r *http.Request) {
	var in skuInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	code, ok := validCode(in.Code)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_code", "code is required: letters, digits, hyphen/underscore, up to 64 chars")
		return
	}
	name, ok := validName(in.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_name", "name is required and must be at most 200 characters")
		return
	}
	imageURL, ok := validImageURL(in.ImageURL)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_image_url", "image_url must be an http(s) URL")
		return
	}
	sk, err := s.db.CreateSKU(r.Context(), code, name, imageURL)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, toSKUOut(sk))
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "duplicate_sku_code", "a SKU with that code already exists")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

type skuPatch struct {
	Name     *string `json:"name"`
	ImageURL *string `json:"image_url"`
}

func (s *Server) updateSKU(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "invalid_sku_id", "sku id must be a valid UUID")
		return
	}
	var in skuPatch
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if in.Name == nil && in.ImageURL == nil {
		writeErr(w, http.StatusBadRequest, "no_fields", "provide at least one of name or image_url to update")
		return
	}
	var name, imageURL *string
	if in.Name != nil {
		v, ok := validName(*in.Name)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_name", "name must be non-empty and at most 200 characters")
			return
		}
		name = &v
	}
	if in.ImageURL != nil {
		v, ok := validImageURL(*in.ImageURL)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_image_url", "image_url must be an http(s) URL")
			return
		}
		imageURL = &v
	}
	sk, err := s.db.UpdateSKU(r.Context(), id, name, imageURL)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toSKUOut(sk))
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "sku_not_found", "no SKU with that id")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
