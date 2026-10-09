package tests

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
)

func postReceipt(t *testing.T, apiURL, key, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, apiURL+"/jobwork/receipts", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestConcurrentOvershoot(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 1500)

	const n = 10
	start := make(chan struct{})
	results := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, issueID)
			<-start
			status, _ := postReceipt(t, api.URL, fmt.Sprintf("overshoot-key-%d", i), body)
			results <- status
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	var created, conflict, other int
	for s := range results {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			other++
		}
	}
	if created != 5 || conflict != 5 || other != 0 {
		t.Fatalf("want 5 created / 5 conflict / 0 other, got %d / %d / %d", created, conflict, other)
	}

	if got := countRows(t, `SELECT COALESCE(SUM(qty),0) FROM jobwork_receipts WHERE issue_id=$1`, issueID); got != 10 {
		t.Fatalf("want total received 10, got %d", got)
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_receipts WHERE issue_id=$1`, issueID); got != 5 {
		t.Fatalf("want 5 receipts, got %d", got)
	}
	if got := countRows(t, `SELECT count(*) FROM vouchers WHERE type='jobwork_receipt'`); got != 5 {
		t.Fatalf("want 5 vouchers, got %d", got)
	}
	var issued, received int
	if err := testDB.Pool.QueryRow(t.Context(),
		`SELECT i.qty, COALESCE(SUM(r.qty),0) FROM jobwork_issues i LEFT JOIN jobwork_receipts r ON r.issue_id=i.id WHERE i.id=$1 GROUP BY i.qty`,
		issueID).Scan(&issued, &received); err != nil {
		t.Fatalf("totals query: %v", err)
	}
	if issued-received != 0 {
		t.Fatalf("want outstanding 0, got %d", issued-received)
	}
	if orphan := countRows(t,
		`SELECT count(*) FROM jobwork_receipts r WHERE NOT EXISTS (SELECT 1 FROM vouchers v WHERE v.source_id=r.id AND v.type='jobwork_receipt')`); orphan != 0 {
		t.Fatalf("found %d receipts without a voucher", orphan)
	}
}

func TestIdempotentReplay(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 1500)
	body := fmt.Sprintf(`{"issue_id":%q,"qty":4,"received_on":"2026-10-09"}`, issueID)

	status1, body1 := postReceipt(t, api.URL, "replay-key", body)
	if status1 != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d (%s)", status1, body1)
	}
	status2, body2 := postReceipt(t, api.URL, "replay-key", body)
	if status2 != http.StatusCreated {
		t.Fatalf("replay: want 201, got %d (%s)", status2, body2)
	}
	if string(body1) != string(body2) {
		t.Fatalf("replay body differs:\nfirst:  %s\nsecond: %s", body1, body2)
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_receipts WHERE issue_id=$1`, issueID); got != 1 {
		t.Fatalf("want 1 receipt after replay, got %d", got)
	}
	if got := countRows(t, `SELECT count(*) FROM vouchers`); got != 1 {
		t.Fatalf("want 1 voucher after replay, got %d", got)
	}
	if got := countRows(t, `SELECT COALESCE(SUM(qty),0) FROM jobwork_receipts WHERE issue_id=$1`, issueID); got != 4 {
		t.Fatalf("want total received 4 (changed once), got %d", got)
	}
}

func TestConcurrentSameKey(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 100, 1000)
	body := fmt.Sprintf(`{"issue_id":%q,"qty":3,"received_on":"2026-10-09"}`, issueID)

	const n = 8
	start := make(chan struct{})
	bodies := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, b := postReceipt(t, api.URL, "same-key", body)
			if status != http.StatusCreated {
				t.Errorf("want 201 for identical key, got %d (%s)", status, b)
			}
			bodies <- string(b)
		}()
	}
	close(start)
	wg.Wait()
	close(bodies)

	first := ""
	for b := range bodies {
		if first == "" {
			first = b
		} else if b != first {
			t.Fatalf("identical-key responses differ:\n%s\n%s", first, b)
		}
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_receipts`); got != 1 {
		t.Fatalf("want exactly 1 receipt for identical key, got %d", got)
	}
	if got := countRows(t, `SELECT count(*) FROM vouchers`); got != 1 {
		t.Fatalf("want exactly 1 voucher for identical key, got %d", got)
	}
}

func TestSameKeyDifferentPayload(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 1500)

	status1, _ := postReceipt(t, api.URL, "conflict-key", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, issueID))
	if status1 != http.StatusCreated {
		t.Fatalf("first: want 201, got %d", status1)
	}
	status2, _ := postReceipt(t, api.URL, "conflict-key", fmt.Sprintf(`{"issue_id":%q,"qty":3,"received_on":"2026-10-09"}`, issueID))
	if status2 != http.StatusConflict {
		t.Fatalf("different payload same key: want 409, got %d", status2)
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_receipts`); got != 1 {
		t.Fatalf("want 1 receipt, got %d", got)
	}
}

func TestOvershootDoesNotConsumeKey(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 1500)

	status1, _ := postReceipt(t, api.URL, "reuse-key", fmt.Sprintf(`{"issue_id":%q,"qty":20,"received_on":"2026-10-09"}`, issueID))
	if status1 != http.StatusConflict {
		t.Fatalf("overshoot: want 409, got %d", status1)
	}
	if got := countRows(t, `SELECT count(*) FROM idempotency_requests WHERE key='reuse-key'`); got != 0 {
		t.Fatalf("overshoot must roll back the idempotency reservation, found %d rows", got)
	}
	status2, _ := postReceipt(t, api.URL, "reuse-key", fmt.Sprintf(`{"issue_id":%q,"qty":5,"received_on":"2026-10-09"}`, issueID))
	if status2 != http.StatusCreated {
		t.Fatalf("reuse after rolled-back failure: want 201, got %d", status2)
	}
	if got := countRows(t, `SELECT count(*) FROM jobwork_receipts`); got != 1 {
		t.Fatalf("want 1 receipt, got %d", got)
	}
}

func TestInvalidInputs(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 1500)
	good := fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, issueID)

	t.Run("missing key", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "", good); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("zero qty", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k1", fmt.Sprintf(`{"issue_id":%q,"qty":0,"received_on":"2026-10-09"}`, issueID)); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("negative qty", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k2", fmt.Sprintf(`{"issue_id":%q,"qty":-3,"received_on":"2026-10-09"}`, issueID)); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("bad date", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k3", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-13-40"}`, issueID)); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k4", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09","extra":true}`, issueID)); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("trailing json", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k5", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}{}`, issueID)); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		if s, _ := postReceipt(t, api.URL, "k6", `{"issue_id":`); s != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", s)
		}
	})
	t.Run("wrong content type", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, api.URL+"/jobwork/receipts", bytes.NewBufferString(good))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Idempotency-Key", "k7")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", resp.StatusCode)
		}
	})
}

func TestUnknownIssue(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	unknown := "00000000-0000-0000-0000-0000000000ff"
	if s, _ := postReceipt(t, api.URL, "unknown-issue", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, unknown)); s != http.StatusNotFound {
		t.Fatalf("want 404 for unknown issue, got %d", s)
	}
}
