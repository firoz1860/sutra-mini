package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// signTestToken crafts a token in the server's `v1.<exp>.<sig>` format signed
// with the test secret, so tests can produce valid-signature-but-expired tokens.
func signTestToken(expUnix int64) string {
	payload := "v1." + strconv.FormatInt(expUnix, 10)
	mac := hmac.New(sha256.New, []byte(testSessionSecret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// protectedEndpoints is every business endpoint that must require a valid
// operator token. Health and the auth surface are intentionally excluded.
func protectedEndpoints(issueID string) []struct{ method, path, body string } {
	return []struct{ method, path, body string }{
		{http.MethodPost, "/jobwork/receipts", fmt.Sprintf(`{"issue_id":%q,"qty":1,"received_on":"2026-10-09"}`, issueID)},
		{http.MethodGet, "/issues", ""},
		{http.MethodGet, "/issues/" + issueID, ""},
		{http.MethodGet, "/jobwork/receipts?issue_id=" + issueID, ""},
		{http.MethodGet, "/vouchers", ""},
		{http.MethodPost, "/vouchers/" + issueID + "/retry", ""},
		{http.MethodPost, "/ask", `{"question":"how much of SKU-COTTON is still with Radha Weavers?"}`},
		{http.MethodGet, "/parties", ""},
		{http.MethodPost, "/parties", `{"name":"X","phone":"9876543210"}`},
		{http.MethodPatch, "/parties/" + issueID, `{"name":"Y"}`},
		{http.MethodGet, "/skus", ""},
		{http.MethodPost, "/skus", `{"code":"X1","name":"Y"}`},
		{http.MethodPatch, "/skus/" + issueID, `{"name":"Y"}`},
		{http.MethodPost, "/jobwork/issues", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":1,"issued_on":"2026-10-09","unit_rate_rupees":"10"}`, issueID, issueID)},
	}
}

func TestProtectedEndpointsRejectBadTokens(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 2500)

	valid := testBearer(t)
	tampered := valid[:len(valid)-1] + flipChar(valid[len(valid)-1])
	expired := signTestToken(time.Now().Add(-time.Hour).Unix())

	tokens := map[string]string{
		"missing":  "",
		"garbage":  "not-a-token",
		"tampered": tampered,
		"expired":  expired,
	}

	for _, ep := range protectedEndpoints(issueID) {
		for label, tok := range tokens {
			t.Run(fmt.Sprintf("%s %s/%s", ep.method, ep.path, label), func(t *testing.T) {
				st, _ := doReq(t, ep.method, api.URL+ep.path, tok, ep.body)
				if st != http.StatusUnauthorized {
					t.Fatalf("want 401, got %d", st)
				}
			})
		}
	}
}

func flipChar(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

func TestUnconfiguredAuthFailsClosed(t *testing.T) {
	truncateAll(t)
	api := startUnconfiguredAPI(t)
	issueID := createIssue(t, 10, 2500)
	// Even a well-formed token cannot verify when the server has no secret.
	tok := testBearer(t)
	for _, ep := range protectedEndpoints(issueID) {
		st, _ := doReq(t, ep.method, api.URL+ep.path, tok, ep.body)
		if st != http.StatusUnauthorized {
			t.Fatalf("%s %s unconfigured: want 401, got %d", ep.method, ep.path, st)
		}
	}
}

func TestUnauthorizedMutationsLeaveDBUnchanged(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	issueID := createIssue(t, 10, 2500) // seeds 1 party, 1 sku, 1 issue

	before := map[string]int{
		"receipts":    countRows(t, `SELECT count(*) FROM jobwork_receipts`),
		"vouchers":    countRows(t, `SELECT count(*) FROM vouchers`),
		"idempotency": countRows(t, `SELECT count(*) FROM idempotency_requests`),
		"issues":      countRows(t, `SELECT count(*) FROM jobwork_issues`),
		"parties":     countRows(t, `SELECT count(*) FROM parties`),
		"skus":        countRows(t, `SELECT count(*) FROM skus`),
	}

	// Unauthenticated mutations (no token) must all be rejected.
	unauth := []struct{ method, path, body, key string }{
		{http.MethodPost, "/jobwork/receipts", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, issueID), "nope-receipt"},
		{http.MethodPost, "/jobwork/issues", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":1,"issued_on":"2026-10-09","unit_rate_rupees":"10"}`, issueID, issueID), "nope-issue"},
		{http.MethodPost, "/parties", `{"name":"Intruder","phone":"9876543210"}`, ""},
		{http.MethodPost, "/skus", `{"code":"INTRUDER","name":"X"}`, ""},
	}
	for _, m := range unauth {
		var st int
		if m.key != "" {
			st, _ = doReqKey(t, api.URL+m.path, "", m.key, m.body)
		} else {
			st, _ = doReq(t, m.method, api.URL+m.path, "", m.body)
		}
		if st != http.StatusUnauthorized {
			t.Fatalf("%s %s: want 401, got %d", m.method, m.path, st)
		}
	}

	after := map[string]int{
		"receipts":    countRows(t, `SELECT count(*) FROM jobwork_receipts`),
		"vouchers":    countRows(t, `SELECT count(*) FROM vouchers`),
		"idempotency": countRows(t, `SELECT count(*) FROM idempotency_requests`),
		"issues":      countRows(t, `SELECT count(*) FROM jobwork_issues`),
		"parties":     countRows(t, `SELECT count(*) FROM parties`),
		"skus":        countRows(t, `SELECT count(*) FROM skus`),
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("table %s changed after unauthorized mutations: before=%d after=%d", k, v, after[k])
		}
	}

	// Create a real voucher (authenticated), then confirm an unauthenticated
	// retry cannot change its state.
	tok := testBearer(t)
	st, body := postReceipt(t, api.URL, "authed-recv", fmt.Sprintf(`{"issue_id":%q,"qty":2,"received_on":"2026-10-09"}`, issueID))
	if st != http.StatusCreated {
		t.Fatalf("authed receipt: want 201, got %d (%s)", st, body)
	}
	_, vb := doReq(t, http.MethodGet, api.URL+"/vouchers", tok, "")
	var vl struct {
		Vouchers []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Attempts int    `json:"attempts"`
		} `json:"vouchers"`
	}
	if err := json.Unmarshal(vb, &vl); err != nil || len(vl.Vouchers) != 1 {
		t.Fatalf("expected one voucher, got %v (%s)", err, vb)
	}
	v := vl.Vouchers[0]

	if st, _ := doReq(t, http.MethodPost, api.URL+"/vouchers/"+v.ID+"/retry", "", ""); st != http.StatusUnauthorized {
		t.Fatalf("unauthenticated retry: want 401, got %d", st)
	}
	_, vb2 := doReq(t, http.MethodGet, api.URL+"/vouchers", tok, "")
	var vl2 struct {
		Vouchers []struct {
			Status   string `json:"status"`
			Attempts int    `json:"attempts"`
		} `json:"vouchers"`
	}
	_ = json.Unmarshal(vb2, &vl2)
	if len(vl2.Vouchers) != 1 || vl2.Vouchers[0].Status != v.Status || vl2.Vouchers[0].Attempts != v.Attempts {
		t.Fatalf("voucher state changed after unauthorized retry: %+v -> %+v", v, vl2.Vouchers)
	}
}

func TestUnauthorizedAskIsRejectedBeforeExecution(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	// No token: /ask must 401 before any model or DB-tool execution, so the
	// response carries no answer/tool_calls business payload.
	st, body := doReq(t, http.MethodPost, api.URL+"/ask", "", `{"question":"how much of SKU-COTTON is still with Radha Weavers?"}`)
	if st != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /ask: want 401, got %d", st)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if _, ok := m["answer"]; ok {
		t.Fatalf("/ask leaked an answer without auth: %s", body)
	}
	if _, ok := m["tool_calls"]; ok {
		t.Fatalf("/ask ran tools without auth: %s", body)
	}
}
