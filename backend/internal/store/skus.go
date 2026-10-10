package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type SKU struct {
	ID       string
	Code     string
	Name     string
	ImageURL string
}

func (d *DB) ListSKUsFull(ctx context.Context) ([]SKU, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, code, name, image_url FROM skus ORDER BY code, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SKU, 0)
	for rows.Next() {
		var s SKU
		if err := rows.Scan(&s.ID, &s.Code, &s.Name, &s.ImageURL); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) CreateSKU(ctx context.Context, code, name, imageURL string) (SKU, error) {
	var s SKU
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO skus (code, name, image_url) VALUES ($1, $2, $3) RETURNING id, code, name, image_url`,
		code, name, imageURL).Scan(&s.ID, &s.Code, &s.Name, &s.ImageURL)
	if isUniqueViolation(err) {
		return SKU{}, ErrConflict
	}
	return s, err
}

func (d *DB) UpdateSKU(ctx context.Context, id string, name, imageURL *string) (SKU, error) {
	var s SKU
	err := d.Pool.QueryRow(ctx,
		`UPDATE skus SET name=COALESCE($2, name), image_url=COALESCE($3, image_url)
		 WHERE id=$1 RETURNING id, code, name, image_url`,
		id, name, imageURL).Scan(&s.ID, &s.Code, &s.Name, &s.ImageURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return SKU{}, ErrNotFound
	}
	return s, err
}

func (d *DB) SKUExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM skus WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}
