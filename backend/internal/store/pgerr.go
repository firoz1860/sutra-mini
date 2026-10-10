package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isUniqueViolation(err error) bool { return pgCode(err) == "23505" }
