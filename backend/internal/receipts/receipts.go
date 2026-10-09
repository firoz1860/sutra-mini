package receipts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrIssueNotFound = errors.New("issue not found")
	ErrOvershoot     = errors.New("receipt quantity exceeds outstanding")
	ErrKeyConflict   = errors.New("idempotency key reused with a different payload")
)

type Request struct {
	IssueID    string
	Qty        int
	ReceivedOn string
}

func (r Request) fingerprint() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", r.IssueID, r.Qty, r.ReceivedOn)))
	return hex.EncodeToString(sum[:])
}

type receiptView struct {
	ID         string `json:"id"`
	IssueID    string `json:"issue_id"`
	Qty        int    `json:"qty"`
	ReceivedOn string `json:"received_on"`
}

type voucherView struct {
	ID          string `json:"id"`
	ClientRef   string `json:"client_ref"`
	Status      string `json:"status"`
	AmountPaise int64  `json:"amount_paise"`
}

type issueView struct {
	ID          string `json:"id"`
	IssuedQty   int    `json:"issued_qty"`
	ReceivedQty int    `json:"received_qty"`
	Outstanding int    `json:"outstanding_qty"`
}

type response struct {
	Receipt receiptView `json:"receipt"`
	Voucher voucherView `json:"voucher"`
	Issue   issueView   `json:"issue"`
}

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) Create(ctx context.Context, key string, req Request) (int, []byte, error) {
	fp := req.fingerprint()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var reservedKey string
	err = tx.QueryRow(ctx,
		`INSERT INTO idempotency_requests (key, fingerprint) VALUES ($1, $2)
		 ON CONFLICT (key) DO NOTHING RETURNING key`, key, fp).Scan(&reservedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.replay(ctx, tx, key, fp)
	}
	if err != nil {
		return 0, nil, err
	}

	var rate int64
	if err := tx.QueryRow(ctx, `SELECT unit_rate_paise FROM jobwork_issues WHERE id=$1`, req.IssueID).Scan(&rate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrIssueNotFound
		}
		return 0, nil, err
	}

	amount, err := amountPaise(req.Qty, rate)
	if err != nil {
		return 0, nil, err
	}

	var receiptID, receivedOn string
	err = tx.QueryRow(ctx,
		`INSERT INTO jobwork_receipts (issue_id, qty, received_on, idempotency_key)
		 VALUES ($1, $2, $3, $4) RETURNING id, to_char(received_on, 'YYYY-MM-DD')`,
		req.IssueID, req.Qty, req.ReceivedOn, key).Scan(&receiptID, &receivedOn)
	if err != nil {
		return 0, nil, mapReceiptErr(err)
	}

	clientRef := "jwr-" + receiptID
	var voucherID string
	err = tx.QueryRow(ctx,
		`INSERT INTO vouchers (type, source_id, amount_paise, client_ref, status, next_attempt_at)
		 VALUES ('jobwork_receipt', $1, $2, $3, 'pending', now()) RETURNING id`,
		receiptID, amount, clientRef).Scan(&voucherID)
	if err != nil {
		return 0, nil, err
	}

	var issued, received int
	if err := tx.QueryRow(ctx,
		`SELECT i.qty, COALESCE((SELECT SUM(qty) FROM jobwork_receipts r WHERE r.issue_id=i.id),0)
		 FROM jobwork_issues i WHERE i.id=$1`, req.IssueID).Scan(&issued, &received); err != nil {
		return 0, nil, err
	}

	body, err := json.Marshal(response{
		Receipt: receiptView{ID: receiptID, IssueID: req.IssueID, Qty: req.Qty, ReceivedOn: receivedOn},
		Voucher: voucherView{ID: voucherID, ClientRef: clientRef, Status: "pending", AmountPaise: amount},
		Issue:   issueView{ID: req.IssueID, IssuedQty: issued, ReceivedQty: received, Outstanding: issued - received},
	})
	if err != nil {
		return 0, nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE idempotency_requests SET response_status=$2, response_body=$3 WHERE key=$1`,
		key, 201, string(body)); err != nil {
		return 0, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return 201, body, nil
}

func (s *Service) replay(ctx context.Context, tx pgx.Tx, key, fp string) (int, []byte, error) {
	var storedFP string
	var status *int
	var body []byte
	err := tx.QueryRow(ctx,
		`SELECT fingerprint, response_status, response_body FROM idempotency_requests WHERE key=$1`, key).
		Scan(&storedFP, &status, &body)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	if storedFP != fp {
		return 0, nil, ErrKeyConflict
	}
	if status == nil || body == nil {
		return 0, nil, fmt.Errorf("idempotency record for key has no persisted response")
	}
	return *status, body, nil
}

func amountPaise(qty int, rate int64) (int64, error) {
	if qty <= 0 || rate < 0 {
		return 0, fmt.Errorf("invalid qty or rate")
	}
	if rate != 0 && int64(qty) > math.MaxInt64/rate {
		return 0, fmt.Errorf("amount overflow for qty=%d rate=%d", qty, rate)
	}
	return int64(qty) * rate, nil
}

func mapReceiptErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23514":
			return ErrOvershoot
		case "23503":
			return ErrIssueNotFound
		}
	}
	return err
}
