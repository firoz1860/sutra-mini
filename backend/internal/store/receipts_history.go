package store

import "context"

type ReceiptVoucher struct {
	ID          string
	ClientRef   string
	AmountPaise int64
	Status      string
	Attempts    int
	LastError   *string
}

type ReceiptHistoryRow struct {
	ReceiptID  string
	ReceivedOn string
	Qty        int
	CreatedAt  string
	SKU        struct{ ID, Code, Name string }
	Party      struct{ ID, Name string }
	Voucher    *ReceiptVoucher
}

func (d *DB) CountReceiptsByIssue(ctx context.Context, issueID string) (int, error) {
	var n int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM jobwork_receipts WHERE issue_id=$1`, issueID).Scan(&n)
	return n, err
}

func (d *DB) ListReceiptsByIssue(ctx context.Context, issueID string, limit, offset int) ([]ReceiptHistoryRow, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT r.id, to_char(r.received_on, 'YYYY-MM-DD'), r.qty, to_char(r.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       s.id, s.code, s.name,
		       p.id, p.name,
		       v.id, v.client_ref, v.amount_paise, v.status, v.attempts, v.last_error
		FROM jobwork_receipts r
		JOIN jobwork_issues i ON i.id = r.issue_id
		JOIN skus s ON s.id = i.sku_id
		JOIN parties p ON p.id = i.party_id
		LEFT JOIN vouchers v ON v.type = 'jobwork_receipt' AND v.source_id = r.id
		WHERE r.issue_id = $1
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2 OFFSET $3`, issueID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ReceiptHistoryRow, 0, limit)
	for rows.Next() {
		var h ReceiptHistoryRow
		var (
			vID, vClientRef, vStatus *string
			vAmount                  *int64
			vAttempts                *int
			vLastErr                 *string
		)
		if err := rows.Scan(&h.ReceiptID, &h.ReceivedOn, &h.Qty, &h.CreatedAt,
			&h.SKU.ID, &h.SKU.Code, &h.SKU.Name,
			&h.Party.ID, &h.Party.Name,
			&vID, &vClientRef, &vAmount, &vStatus, &vAttempts, &vLastErr); err != nil {
			return nil, err
		}
		if vID != nil {
			h.Voucher = &ReceiptVoucher{
				ID: *vID, ClientRef: deref(vClientRef), AmountPaise: derefInt64(vAmount),
				Status: deref(vStatus), Attempts: derefInt(vAttempts), LastError: vLastErr,
			}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func derefInt64(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}
