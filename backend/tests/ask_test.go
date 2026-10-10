package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

type askResult struct {
	Answer         string   `json:"answer"`
	SKU            string   `json:"sku"`
	Party          string   `json:"party"`
	OutstandingQty int      `json:"outstanding_qty"`
	ToolCalls      []string `json:"tool_calls"`
	ModelStubbed   bool     `json:"model_stubbed"`
}

func postAsk(t *testing.T, apiURL, question string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/ask", bytes.NewBufferString(fmt.Sprintf(`{"question":%q}`, question)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ask request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAskOutstanding(t *testing.T) {
	truncateAll(t)
	if err := testDB.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	api := startAPI(t)
	const issue1 = "33333333-3333-3333-3333-333333330001" // SKU-COTTON / Radha Weavers / qty 10

	status, body := postAsk(t, api.URL, "how much of SKU-COTTON is still with Radha Weavers?")
	if status != http.StatusOK {
		t.Fatalf("ask: want 200, got %d (%s)", status, body)
	}
	var r askResult
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.OutstandingQty != 10 {
		t.Fatalf("want outstanding 10 before any receipt, got %d", r.OutstandingQty)
	}
	if len(r.ToolCalls) < 3 {
		t.Fatalf("expected at least 3 logged tool calls, got %v", r.ToolCalls)
	}
	if !r.ModelStubbed {
		t.Fatalf("expected model_stubbed=true with no API key configured")
	}

	// Receive 4 units, then outstanding must drop to 6 (derived from the DB, not hardcoded).
	if s, b := postReceipt(t, api.URL, "ask-key-1", fmt.Sprintf(`{"issue_id":%q,"qty":4,"received_on":"2026-10-10"}`, issue1)); s != http.StatusCreated {
		t.Fatalf("seed receipt: want 201, got %d (%s)", s, b)
	}
	status, body = postAsk(t, api.URL, "how much of SKU-COTTON is still with Radha Weavers?")
	if status != http.StatusOK {
		t.Fatalf("ask after receipt: want 200, got %d (%s)", status, body)
	}
	_ = json.Unmarshal(body, &r)
	if r.OutstandingQty != 6 {
		t.Fatalf("want outstanding 6 after receiving 4, got %d", r.OutstandingQty)
	}

	// Unresolved SKU/party -> 400.
	if s, _ := postAsk(t, api.URL, "how much of SKU-NOTREAL is with Nobody Weavers?"); s != http.StatusBadRequest {
		t.Fatalf("unresolved question: want 400, got %d", s)
	}
}
