package tests

import (
	"context"
	"testing"
)

func TestSmokeDatabase(t *testing.T) {
	truncateAll(t)
	id := createIssue(t, 10, 1500)
	if id == "" {
		t.Fatal("expected a non-empty issue id")
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_issues`); got != 1 {
		t.Fatalf("want 1 issue, got %d", got)
	}
	var qty int
	if err := testDB.Pool.QueryRow(context.Background(), `SELECT qty FROM jobwork_issues WHERE id=$1`, id).Scan(&qty); err != nil {
		t.Fatalf("select issue: %v", err)
	}
	if qty != 10 {
		t.Fatalf("want qty 10, got %d", qty)
	}
}
