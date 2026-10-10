package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/firoz1860/sutra-mini/backend/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.MaxConns < 10 {
		cfg.MaxConns = 10
	}

	if strings.Contains(url, "pooler") || strings.Contains(url, "pgbouncer=true") {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
}

func (d *DB) Health(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return d.Pool.Ping(c)
}

func (d *DB) Migrate(ctx context.Context) error {
	_, err := d.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	names, err := sqlFiles(func(n string) bool { return !strings.Contains(n, "seed") })
	if err != nil {
		return err
	}
	for _, name := range names {
		var exists bool
		if err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := d.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) Seed(ctx context.Context) error {
	names, err := sqlFiles(func(n string) bool { return strings.Contains(n, "seed") })
	if err != nil {
		return err
	}
	for _, name := range names {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := d.Pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	return nil
}

func sqlFiles(keep func(string) bool) ([]string, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if keep(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

type Voucher struct {
	ID              string
	Type            string
	SourceID        string
	AmountPaise     int64
	Status          string
	Attempts        int
	AttemptBaseline int
	LastError       *string
	ClientRef       string
	ExternalRef     *string
	NextAttemptAt   time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const voucherCols = `id, type, source_id, amount_paise, status, attempts, attempt_baseline, last_error, client_ref, external_ref, next_attempt_at, created_at, updated_at`

func scanVoucher(row pgx.Row) (Voucher, error) {
	var v Voucher
	err := row.Scan(&v.ID, &v.Type, &v.SourceID, &v.AmountPaise, &v.Status, &v.Attempts, &v.AttemptBaseline,
		&v.LastError, &v.ClientRef, &v.ExternalRef, &v.NextAttemptAt, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func (d *DB) ListVouchers(ctx context.Context, status string, limit int) ([]Voucher, error) {
	var (
		rows pgx.Rows
		err  error
	)
	base := `SELECT ` + voucherCols + ` FROM vouchers`
	order := ` ORDER BY updated_at DESC, id LIMIT $1`
	if status == "" {
		rows, err = d.Pool.Query(ctx, base+order, limit)
	} else {
		rows, err = d.Pool.Query(ctx, base+` WHERE status=$2`+order, limit, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Voucher, 0, limit)
	for rows.Next() {
		v, err := scanVoucher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) GetVoucher(ctx context.Context, id string) (Voucher, error) {
	return scanVoucher(d.Pool.QueryRow(ctx, `SELECT `+voucherCols+` FROM vouchers WHERE id=$1`, id))
}

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

func (d *DB) RetryVoucher(ctx context.Context, id string) (Voucher, error) {
	row := d.Pool.QueryRow(ctx, `
		UPDATE vouchers
		SET status='pending', attempt_baseline=attempts, last_error=NULL,
		    next_attempt_at=now(), lease_until=NULL, claim_token=NULL, claimed_by=NULL, updated_at=now()
		WHERE id=$1 AND status='failed'
		RETURNING `+voucherCols, id)
	v, err := scanVoucher(row)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Voucher{}, err
	}
	var status string
	if e := d.Pool.QueryRow(ctx, `SELECT status FROM vouchers WHERE id=$1`, id).Scan(&status); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return Voucher{}, ErrNotFound
		}
		return Voucher{}, e
	}
	return Voucher{}, fmt.Errorf("%w: voucher is %s, only failed vouchers can be retried", ErrConflict, status)
}

type Claim struct {
	ID              string
	ClientRef       string
	Type            string
	SourceID        string
	AmountPaise     int64
	ClaimToken      string
	Attempts        int
	AttemptBaseline int
}

func (d *DB) Claim(ctx context.Context, workerID string, lease time.Duration, limit, maxAttempts int) ([]Claim, error) {
	rows, err := d.Pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM vouchers
			WHERE status='pending' AND next_attempt_at <= now()
			  AND (lease_until IS NULL OR lease_until <= now())
			  AND attempts - attempt_baseline < $4
			ORDER BY next_attempt_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE vouchers v
		SET claim_token=gen_random_uuid(), claimed_by=$1,
		    lease_until=now() + make_interval(secs => $3),
		    attempts=attempts+1, updated_at=now()
		FROM due
		WHERE v.id=due.id
		RETURNING v.id, v.client_ref, v.type, v.source_id, v.amount_paise, v.claim_token, v.attempts, v.attempt_baseline`,
		workerID, limit, lease.Seconds(), maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var c Claim
		if err := rows.Scan(&c.ID, &c.ClientRef, &c.Type, &c.SourceID, &c.AmountPaise, &c.ClaimToken, &c.Attempts, &c.AttemptBaseline); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) ReapExhausted(ctx context.Context, maxAttempts int, reason string) (int, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE vouchers
		SET status='failed',
		    last_error = COALESCE(last_error, $2),
		    lease_until=NULL, claim_token=NULL, claimed_by=NULL, updated_at=now()
		WHERE status='pending'
		  AND (lease_until IS NULL OR lease_until <= now())
		  AND attempts - attempt_baseline >= $1`, maxAttempts, reason)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (d *DB) MarkPushed(ctx context.Context, id, claimToken, externalRef string) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE vouchers SET status='pushed', external_ref=$3, last_error=NULL,
		    lease_until=NULL, claim_token=NULL, claimed_by=NULL, updated_at=now()
		WHERE id=$1 AND claim_token=$2`, id, claimToken, externalRef)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (d *DB) Reschedule(ctx context.Context, id, claimToken string, delay time.Duration, lastErr string) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE vouchers SET next_attempt_at=now() + make_interval(secs => $3),
		    last_error=$4, lease_until=NULL, claim_token=NULL, claimed_by=NULL, updated_at=now()
		WHERE id=$1 AND claim_token=$2`, id, claimToken, delay.Seconds(), lastErr)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (d *DB) MarkFailed(ctx context.Context, id, claimToken, lastErr string) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE vouchers SET status='failed', last_error=$3,
		    lease_until=NULL, claim_token=NULL, claimed_by=NULL, updated_at=now()
		WHERE id=$1 AND claim_token=$2`, id, claimToken, lastErr)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

type IssueTotals struct {
	IssueID     string
	IssuedQty   int
	ReceivedQty int
	Outstanding int
	SKU         struct{ ID, Code, Name, ImageURL string }
	Party       struct{ ID, Name, Phone string }
}

func (d *DB) ListIssues(ctx context.Context) ([]IssueTotals, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT i.id, i.qty,
		       COALESCE((SELECT SUM(qty) FROM jobwork_receipts r WHERE r.issue_id=i.id),0) AS received,
		       s.id, s.code, s.name, s.image_url,
		       p.id, p.name, p.phone
		FROM jobwork_issues i
		JOIN skus s ON s.id=i.sku_id
		JOIN parties p ON p.id=i.party_id
		ORDER BY i.issued_on, i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueTotals
	for rows.Next() {
		var t IssueTotals
		if err := rows.Scan(&t.IssueID, &t.IssuedQty, &t.ReceivedQty,
			&t.SKU.ID, &t.SKU.Code, &t.SKU.Name, &t.SKU.ImageURL,
			&t.Party.ID, &t.Party.Name, &t.Party.Phone); err != nil {
			return nil, err
		}
		t.Outstanding = t.IssuedQty - t.ReceivedQty
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) IssueTotals(ctx context.Context, id string) (IssueTotals, error) {
	var t IssueTotals
	err := d.Pool.QueryRow(ctx, `
		SELECT i.id, i.qty,
		       COALESCE((SELECT SUM(qty) FROM jobwork_receipts r WHERE r.issue_id=i.id),0) AS received,
		       s.id, s.code, s.name, s.image_url,
		       p.id, p.name, p.phone
		FROM jobwork_issues i
		JOIN skus s ON s.id=i.sku_id
		JOIN parties p ON p.id=i.party_id
		WHERE i.id=$1`, id).Scan(
		&t.IssueID, &t.IssuedQty, &t.ReceivedQty,
		&t.SKU.ID, &t.SKU.Code, &t.SKU.Name, &t.SKU.ImageURL,
		&t.Party.ID, &t.Party.Name, &t.Party.Phone)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IssueTotals{}, ErrNotFound
		}
		return IssueTotals{}, err
	}
	t.Outstanding = t.IssuedQty - t.ReceivedQty
	return t, nil
}

func (d *DB) OutstandingForSKUParty(ctx context.Context, skuCode, partyName string) (int, bool, error) {
	var outstanding int
	var found bool
	err := d.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(i.qty),0) - COALESCE(SUM(r.received),0), count(*) > 0
		FROM jobwork_issues i
		JOIN skus s ON s.id=i.sku_id
		JOIN parties p ON p.id=i.party_id
		LEFT JOIN (SELECT issue_id, SUM(qty) received FROM jobwork_receipts GROUP BY issue_id) r ON r.issue_id=i.id
		WHERE s.code=$1 AND p.name=$2`, skuCode, partyName).Scan(&outstanding, &found)
	if err != nil {
		return 0, false, err
	}
	return outstanding, found, nil
}

type SKURef struct {
	ID   string
	Code string
	Name string
}

type PartyRef struct {
	ID   string
	Name string
}

func (d *DB) ListSKUs(ctx context.Context) ([]SKURef, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, code, name FROM skus ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SKURef
	for rows.Next() {
		var s SKURef
		if err := rows.Scan(&s.ID, &s.Code, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) ListParties(ctx context.Context) ([]PartyRef, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, name FROM parties ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PartyRef
	for rows.Next() {
		var p PartyRef
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
