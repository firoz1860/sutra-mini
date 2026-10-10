package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/ask"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIsolationLeavesBaseDataUntouched(t *testing.T) {
	ctx := context.Background()
	if testURL == baseURL {
		t.Fatal("test database URL must differ from the base database URL")
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatalf("open base pool: %v", err)
	}
	defer admin.Close()

	sentinel := fmt.Sprintf("isolation_sentinel_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE TABLE "`+sentinel+`" (marker text)`); err != nil {
		t.Fatalf("create sentinel: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `DROP TABLE IF EXISTS "`+sentinel+`"`) })
	if _, err := admin.Exec(ctx, `INSERT INTO "`+sentinel+`" VALUES ('preexisting')`); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	// Destructive test operations run only against the isolated test database.
	truncateAll(t)
	createIssue(t, 5, 100)
	truncateAll(t)

	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM "`+sentinel+`"`).Scan(&n); err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if n != 1 {
		t.Fatalf("pre-existing data in the base database was affected by test cleanup (count=%d)", n)
	}
}

func TestIssueQtyGuard(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issue := createIssue(t, 10, 1500)
	ctx := context.Background()

	if s, b := postReceipt(t, api.URL, "iqg-1", fmt.Sprintf(`{"issue_id":%q,"qty":6,"received_on":"2026-10-09"}`, issue)); s != http.StatusCreated {
		t.Fatalf("seed receipt: want 201, got %d (%s)", s, b)
	}

	if _, err := testDB.Pool.Exec(ctx, `UPDATE jobwork_issues SET qty=5 WHERE id=$1`, issue); err == nil {
		t.Fatal("reducing issue qty below received must be rejected")
	}
	if _, err := testDB.Pool.Exec(ctx, `UPDATE jobwork_issues SET qty=6 WHERE id=$1`, issue); err != nil {
		t.Fatalf("qty equal to received should be allowed: %v", err)
	}
	if _, err := testDB.Pool.Exec(ctx, `UPDATE jobwork_issues SET qty=20 WHERE id=$1`, issue); err != nil {
		t.Fatalf("increasing qty should be allowed: %v", err)
	}
	if _, err := testDB.Pool.Exec(ctx, `UPDATE jobwork_issues SET sku_id=gen_random_uuid() WHERE id=$1`, issue); err == nil {
		t.Fatal("changing sku_id must be rejected (immutable)")
	}

	var issued, received int
	if err := testDB.Pool.QueryRow(ctx,
		`SELECT qty, COALESCE((SELECT SUM(qty) FROM jobwork_receipts WHERE issue_id=$1),0) FROM jobwork_issues WHERE id=$1`,
		issue).Scan(&issued, &received); err != nil {
		t.Fatalf("read totals: %v", err)
	}
	if issued < received {
		t.Fatalf("invariant issued >= received violated: %d < %d", issued, received)
	}
}

func TestIssueQtyConcurrency(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issue := createIssue(t, 10, 1500)
	ctx := context.Background()

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		postReceipt(t, api.URL, "iqc-1", fmt.Sprintf(`{"issue_id":%q,"qty":8,"received_on":"2026-10-09"}`, issue))
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = testDB.Pool.Exec(ctx, `UPDATE jobwork_issues SET qty=5 WHERE id=$1`, issue)
	}()
	close(start)
	wg.Wait()

	var issued, received int
	if err := testDB.Pool.QueryRow(ctx,
		`SELECT qty, COALESCE((SELECT SUM(qty) FROM jobwork_receipts WHERE issue_id=$1),0) FROM jobwork_issues WHERE id=$1`,
		issue).Scan(&issued, &received); err != nil {
		t.Fatalf("read totals: %v", err)
	}
	if issued < received {
		t.Fatalf("issued(%d) >= received(%d) invariant violated under concurrency", issued, received)
	}
}

func TestLostResponseResubmission(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issue := createIssue(t, 10, 1500)
	body := fmt.Sprintf(`{"issue_id":%q,"qty":3,"received_on":"2026-10-09"}`, issue)
	const key = "lost-response-key"

	// First submit commits; imagine the client never received the response.
	s1, b1 := postReceipt(t, api.URL, key, body)
	if s1 != http.StatusCreated {
		t.Fatalf("first submit: want 201, got %d (%s)", s1, b1)
	}

	// The frontend keeps the SAME key + payload and safely retries.
	s2, b2 := postReceipt(t, api.URL, key, body)
	if s2 != http.StatusCreated {
		t.Fatalf("resubmit: want 201, got %d (%s)", s2, b2)
	}
	if string(b1) != string(b2) {
		t.Fatalf("resubmit returned a different body:\n%s\n%s", b1, b2)
	}
	if r := countRows(t, `SELECT count(*) FROM jobwork_receipts`); r != 1 {
		t.Fatalf("lost-response retry must create no extra receipt, got %d", r)
	}
	if v := countRows(t, `SELECT count(*) FROM vouchers`); v != 1 {
		t.Fatalf("lost-response retry must create no extra voucher, got %d", v)
	}
}

func TestAskModelStubbedEvenWithKey(t *testing.T) {
	truncateAll(t)
	if err := testDB.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := ask.New(testDB, log.New(os.Stdout, "", 0), "fake-api-key-present")
	resp, err := svc.Answer(context.Background(), "how much of SKU-COTTON is still with Radha Weavers?")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !resp.ModelStubbed {
		t.Fatal("model_stubbed must stay true when no real model is actually invoked, even with an API key present")
	}
	if len(resp.ToolCalls) < 3 {
		t.Fatalf("expected at least 3 logged tool calls, got %v", resp.ToolCalls)
	}
}
