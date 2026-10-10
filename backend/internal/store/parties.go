package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type Party struct {
	ID    string
	Name  string
	Phone string
}

func (d *DB) ListPartiesFull(ctx context.Context) ([]Party, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, name, phone FROM parties ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Party, 0)
	for rows.Next() {
		var p Party
		if err := rows.Scan(&p.ID, &p.Name, &p.Phone); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) CreateParty(ctx context.Context, name, phone string) (Party, error) {
	var p Party
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO parties (name, phone) VALUES ($1, $2) RETURNING id, name, phone`,
		name, phone).Scan(&p.ID, &p.Name, &p.Phone)
	return p, err
}

func (d *DB) UpdateParty(ctx context.Context, id string, name, phone *string) (Party, error) {
	var p Party
	err := d.Pool.QueryRow(ctx,
		`UPDATE parties SET name=COALESCE($2, name), phone=COALESCE($3, phone)
		 WHERE id=$1 RETURNING id, name, phone`,
		id, name, phone).Scan(&p.ID, &p.Name, &p.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	return p, err
}

func (d *DB) PartyExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}
