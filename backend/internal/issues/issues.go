package issues

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPartyNotFound = errors.New("party not found")
	ErrSKUNotFound   = errors.New("sku not found")
	ErrKeyConflict   = errors.New("idempotency key reused with a different payload")
)

type Request struct {
	PartyID       string
	SKUID         string
	Qty           int
	UnitRatePaise int64
	IssuedOn      string
}

func (r Request) fingerprint() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("issue|%s|%s|%d|%d|%s",
		r.PartyID, r.SKUID, r.Qty, r.UnitRatePaise, r.IssuedOn)))
	return hex.EncodeToString(sum[:])
}

type skuView struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
}

type partyView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type response struct {
	ID            string    `json:"id"`
	IssuedQty     int       `json:"issued_qty"`
	ReceivedQty   int       `json:"received_qty"`
	Outstanding   int       `json:"outstanding_qty"`
	IssuedOn      string    `json:"issued_on"`
	UnitRatePaise int64     `json:"unit_rate_paise"`
	SKU           skuView   `json:"sku"`
	Party         partyView `json:"party"`
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

	var sku skuView
	err = tx.QueryRow(ctx, `SELECT id, code, name, image_url FROM skus WHERE id=$1`, req.SKUID).
		Scan(&sku.ID, &sku.Code, &sku.Name, &sku.ImageURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrSKUNotFound
	}
	if err != nil {
		return 0, nil, err
	}

	var party partyView
	err = tx.QueryRow(ctx, `SELECT id, name, phone FROM parties WHERE id=$1`, req.PartyID).
		Scan(&party.ID, &party.Name, &party.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrPartyNotFound
	}
	if err != nil {
		return 0, nil, err
	}

	var issueID, issuedOn string
	err = tx.QueryRow(ctx,
		`INSERT INTO jobwork_issues (party_id, sku_id, qty, unit_rate_paise, issued_on)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, to_char(issued_on, 'YYYY-MM-DD')`,
		req.PartyID, req.SKUID, req.Qty, req.UnitRatePaise, req.IssuedOn).Scan(&issueID, &issuedOn)
	if err != nil {
		return 0, nil, err
	}

	body, err := json.Marshal(response{
		ID: issueID, IssuedQty: req.Qty, ReceivedQty: 0, Outstanding: req.Qty,
		IssuedOn: issuedOn, UnitRatePaise: req.UnitRatePaise, SKU: sku, Party: party,
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
