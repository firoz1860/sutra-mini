package api

import (
	"net/http"
	"strconv"

	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

type receiptVoucherOut struct {
	ID          string  `json:"id"`
	ClientRef   string  `json:"client_ref"`
	AmountPaise int64   `json:"amount_paise"`
	Status      string  `json:"status"`
	Attempts    int     `json:"attempts"`
	LastError   *string `json:"last_error"`
}

type receiptHistoryOut struct {
	ReceiptID  string `json:"receipt_id"`
	ReceivedOn string `json:"received_on"`
	Qty        int    `json:"qty"`
	CreatedAt  string `json:"created_at"`
	SKU        struct {
		ID   string `json:"id"`
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"sku"`
	Party struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"party"`
	Voucher *receiptVoucherOut `json:"voucher"`
}

func (s *Server) listReceiptHistory(w http.ResponseWriter, r *http.Request) {
	issueID := r.URL.Query().Get("issue_id")
	if !uuidRe.MatchString(issueID) {
		writeErr(w, http.StatusBadRequest, "invalid_issue_id", "issue_id query parameter must be a valid UUID")
		return
	}

	limit := defaultHistLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		if n > maxHistoryLimit {
			n = maxHistoryLimit
		}
		limit = n
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return
		}
		offset = n
	}

	total, err := s.db.CountReceiptsByIssue(r.Context(), issueID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	rows, err := s.db.ListReceiptsByIssue(r.Context(), issueID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	out := make([]receiptHistoryOut, 0, len(rows))
	for _, h := range rows {
		out = append(out, toReceiptHistoryOut(h))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"receipts": out,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

func toReceiptHistoryOut(h store.ReceiptHistoryRow) receiptHistoryOut {
	var o receiptHistoryOut
	o.ReceiptID = h.ReceiptID
	o.ReceivedOn = h.ReceivedOn
	o.Qty = h.Qty
	o.CreatedAt = h.CreatedAt
	o.SKU.ID, o.SKU.Code, o.SKU.Name = h.SKU.ID, h.SKU.Code, h.SKU.Name
	o.Party.ID, o.Party.Name = h.Party.ID, h.Party.Name
	if h.Voucher != nil {
		o.Voucher = &receiptVoucherOut{
			ID:          h.Voucher.ID,
			ClientRef:   h.Voucher.ClientRef,
			AmountPaise: h.Voucher.AmountPaise,
			Status:      h.Voucher.Status,
			Attempts:    h.Voucher.Attempts,
			LastError:   h.Voucher.LastError,
		}
	}
	return o
}
