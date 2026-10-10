package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testOperatorPassword = "test-operator-pass"
	testSessionSecret    = "test-session-secret-0123456789"
)

func doReq(t *testing.T, method, url, token, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func startAuthedAPI(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("OPERATOR_PASSWORD", testOperatorPassword)
	t.Setenv("SESSION_SECRET", testSessionSecret)
	srv := startAPI(t)
	st, b := doReq(t, http.MethodPost, srv.URL+"/auth/login", "",
		fmt.Sprintf(`{"password":%q}`, testOperatorPassword))
	if st != http.StatusOK {
		t.Fatalf("login: want 200, got %d (%s)", st, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Token == "" {
		t.Fatalf("login token parse: %v (%s)", err, b)
	}
	return srv, out.Token
}

func createPartyAPI(t *testing.T, base, token, name, phone string) string {
	t.Helper()
	st, b := doReq(t, http.MethodPost, base+"/parties", token,
		fmt.Sprintf(`{"name":%q,"phone":%q}`, name, phone))
	if st != http.StatusCreated {
		t.Fatalf("create party: want 201, got %d (%s)", st, b)
	}
	return jsonID(t, b)
}

func createSKUAPI(t *testing.T, base, token, code, name, imageURL string) string {
	t.Helper()
	st, b := doReq(t, http.MethodPost, base+"/skus", token,
		fmt.Sprintf(`{"code":%q,"name":%q,"image_url":%q}`, code, name, imageURL))
	if st != http.StatusCreated {
		t.Fatalf("create sku: want 201, got %d (%s)", st, b)
	}
	return jsonID(t, b)
}

func jsonID(t *testing.T, b []byte) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.ID == "" {
		t.Fatalf("parse id: %v (%s)", err, b)
	}
	return out.ID
}

func doReqKey(t *testing.T, url, token, key, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAuthGuardsAndLogin(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)

	// Unauthenticated management/contact requests are rejected server-side.
	for _, c := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/parties", ""},
		{http.MethodPost, "/parties", `{"name":"X"}`},
		{http.MethodGet, "/skus", ""},
		{http.MethodPost, "/skus", `{"code":"X","name":"Y"}`},
		{http.MethodPost, "/jobwork/issues", `{}`},
	} {
		if st, _ := doReq(t, c.method, api.URL+c.path, "", c.body); st != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: want 401, got %d", c.method, c.path, st)
		}
	}

	// Wrong password is rejected.
	if st, _ := doReq(t, http.MethodPost, api.URL+"/auth/login", "", `{"password":"wrong"}`); st != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401, got %d", st)
	}

	// Valid token unlocks the management list.
	if st, _ := doReq(t, http.MethodGet, api.URL+"/parties", token, ""); st != http.StatusOK {
		t.Fatalf("GET /parties with token: want 200, got %d", st)
	}
}

func TestLoginUnconfiguredReturns503(t *testing.T) {
	// No OPERATOR_PASSWORD/SESSION_SECRET set: login is unavailable and
	// management stays locked by default.
	api := startAPI(t)
	if st, _ := doReq(t, http.MethodPost, api.URL+"/auth/login", "", `{"password":"anything"}`); st != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured login: want 503, got %d", st)
	}
	if st, _ := doReq(t, http.MethodGet, api.URL+"/parties", "", ""); st != http.StatusUnauthorized {
		t.Fatalf("unconfigured management: want 401, got %d", st)
	}
}

func TestCreatePartyValidation(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)

	// Phone is optional.
	createPartyAPI(t, api.URL, token, "No Phone Weavers", "")
	// International prefix is accepted (not fixed to 10 digits).
	createPartyAPI(t, api.URL, token, "Intl Weavers", "+44 20 7946 0958")

	cases := []struct {
		name, body string
		want       int
	}{
		{"empty name", `{"name":"  ","phone":"9876543210"}`, http.StatusBadRequest},
		{"letters in phone", `{"name":"Bad","phone":"call-me"}`, http.StatusBadRequest},
		{"too few digits", `{"name":"Bad","phone":"12345"}`, http.StatusBadRequest},
		{"valid", `{"name":"Radha","phone":"98765 43210"}`, http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if st, b := doReq(t, http.MethodPost, api.URL+"/parties", token, c.body); st != c.want {
				t.Fatalf("want %d, got %d (%s)", c.want, st, b)
			}
		})
	}
}

func TestCreateSKUNormalizationAndConflict(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)

	st, b := doReq(t, http.MethodPost, api.URL+"/skus", token,
		`{"code":"sku-new","name":"New Fabric","image_url":"https://example.test/a.png"}`)
	if st != http.StatusCreated {
		t.Fatalf("create sku: want 201, got %d (%s)", st, b)
	}
	var created struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &created)
	if created.Code != "SKU-NEW" {
		t.Fatalf("code normalization: want SKU-NEW, got %q", created.Code)
	}

	// Duplicate code (different case) conflicts.
	if st, _ := doReq(t, http.MethodPost, api.URL+"/skus", token,
		`{"code":"Sku-New","name":"Dup"}`); st != http.StatusConflict {
		t.Fatalf("duplicate code: want 409, got %d", st)
	}

	// Invalid image URL rejected.
	if st, _ := doReq(t, http.MethodPost, api.URL+"/skus", token,
		`{"code":"SKU-X","name":"X","image_url":"ftp://nope"}`); st != http.StatusBadRequest {
		t.Fatalf("bad image url: want 400, got %d", st)
	}
}

func TestCreateIssueJourney(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)

	partyID := createPartyAPI(t, api.URL, token, "Radha Weavers", "98765 43210")
	skuID := createSKUAPI(t, api.URL, token, "SKU-COTTON", "Cotton", "https://example.test/c.png")

	// Create an issue of 10 units at Rs 25.00 per unit (2500 paise).
	key := "issue-key-1"
	body := fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":10,"issued_on":"2026-10-01","unit_rate_rupees":"25"}`, partyID, skuID)
	st, b := doReqKey(t, api.URL+"/jobwork/issues", token, key, body)
	if st != http.StatusCreated {
		t.Fatalf("create issue: want 201, got %d (%s)", st, b)
	}
	var issue struct {
		ID            string `json:"id"`
		UnitRatePaise int64  `json:"unit_rate_paise"`
		Outstanding   int    `json:"outstanding_qty"`
		IssuedOn      string `json:"issued_on"`
	}
	if err := json.Unmarshal(b, &issue); err != nil {
		t.Fatalf("parse issue: %v", err)
	}
	if issue.UnitRatePaise != 2500 {
		t.Fatalf("rate: want 2500 paise, got %d", issue.UnitRatePaise)
	}
	if issue.Outstanding != 10 || issue.IssuedOn != "2026-10-01" {
		t.Fatalf("issue fields wrong: %+v", issue)
	}

	// Idempotent replay: same key + body returns byte-identical response, no new row.
	st2, b2 := doReqKey(t, api.URL+"/jobwork/issues", token, key, body)
	if st2 != http.StatusCreated || string(b2) != string(b) {
		t.Fatalf("replay not identical: %d vs %d / bodies differ", st2, st)
	}
	if n := countRows(t, `SELECT count(*) FROM jobwork_issues`); n != 1 {
		t.Fatalf("replay created extra issue, count=%d", n)
	}

	// Same key, different payload -> 409.
	diff := fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":11,"issued_on":"2026-10-01","unit_rate_rupees":"25"}`, partyID, skuID)
	if st, _ := doReqKey(t, api.URL+"/jobwork/issues", token, key, diff); st != http.StatusConflict {
		t.Fatalf("key conflict: want 409, got %d", st)
	}

	// Receive 3 units via the existing public receipt endpoint -> Rs 75.00 voucher.
	rbody := fmt.Sprintf(`{"issue_id":%q,"qty":3,"received_on":"2026-10-02"}`, issue.ID)
	rst, rb := postReceipt(t, api.URL, "recv-3", rbody)
	if rst != http.StatusCreated {
		t.Fatalf("receive 3: want 201, got %d (%s)", rst, rb)
	}
	var recv struct {
		Voucher struct {
			AmountPaise int64 `json:"amount_paise"`
		} `json:"voucher"`
	}
	_ = json.Unmarshal(rb, &recv)
	if recv.Voucher.AmountPaise != 7500 {
		t.Fatalf("voucher amount: want 7500 paise, got %d", recv.Voucher.AmountPaise)
	}

	// Totals: received 3, outstanding 7.
	gst, gb := doReq(t, http.MethodGet, api.URL+"/issues/"+issue.ID, token, "")
	if gst != http.StatusOK {
		t.Fatalf("get issue: %d", gst)
	}
	var det struct {
		ReceivedQty int `json:"received_qty"`
		Outstanding int `json:"outstanding_qty"`
	}
	_ = json.Unmarshal(gb, &det)
	if det.ReceivedQty != 3 || det.Outstanding != 7 {
		t.Fatalf("totals wrong: received=%d outstanding=%d", det.ReceivedQty, det.Outstanding)
	}

	// Receipt history links the correct SKU, party, and voucher.
	hst, hb := doReq(t, http.MethodGet, api.URL+"/jobwork/receipts?issue_id="+issue.ID, "", "")
	if hst != http.StatusOK {
		t.Fatalf("history: %d (%s)", hst, hb)
	}
	var hist struct {
		Total    int `json:"total"`
		Receipts []struct {
			Qty     int                   `json:"qty"`
			SKU     struct{ Code string } `json:"sku"`
			Party   struct{ Name string } `json:"party"`
			Voucher *struct {
				AmountPaise int64  `json:"amount_paise"`
				Status      string `json:"status"`
			} `json:"voucher"`
		} `json:"receipts"`
	}
	if err := json.Unmarshal(hb, &hist); err != nil {
		t.Fatalf("parse history: %v", err)
	}
	if hist.Total != 1 || len(hist.Receipts) != 1 {
		t.Fatalf("history count: total=%d len=%d", hist.Total, len(hist.Receipts))
	}
	r0 := hist.Receipts[0]
	if r0.Qty != 3 || r0.SKU.Code != "SKU-COTTON" || r0.Party.Name != "Radha Weavers" {
		t.Fatalf("history linkage wrong: %+v", r0)
	}
	if r0.Voucher == nil || r0.Voucher.AmountPaise != 7500 {
		t.Fatalf("history voucher wrong: %+v", r0.Voucher)
	}
}

func TestCreateIssueValidation(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)
	partyID := createPartyAPI(t, api.URL, token, "P", "9876543210")
	skuID := createSKUAPI(t, api.URL, token, "SKU-A", "A", "")
	missing := "00000000-0000-0000-0000-000000000000"

	cases := []struct {
		name, body string
		want       int
	}{
		{"bad party ref", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":5,"issued_on":"2026-10-01","unit_rate_rupees":"25"}`, missing, skuID), http.StatusNotFound},
		{"bad sku ref", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":5,"issued_on":"2026-10-01","unit_rate_rupees":"25"}`, partyID, missing), http.StatusNotFound},
		{"too many decimals", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":5,"issued_on":"2026-10-01","unit_rate_rupees":"25.555"}`, partyID, skuID), http.StatusBadRequest},
		{"zero qty", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":0,"issued_on":"2026-10-01","unit_rate_rupees":"25"}`, partyID, skuID), http.StatusBadRequest},
		{"bad date", fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":5,"issued_on":"2026-13-40","unit_rate_rupees":"25"}`, partyID, skuID), http.StatusBadRequest},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if st, b := doReqKey(t, api.URL+"/jobwork/issues", token, fmt.Sprintf("vk-%d", i), c.body); st != c.want {
				t.Fatalf("want %d, got %d (%s)", c.want, st, b)
			}
		})
	}

	// Rs 25.50 -> 2550 paise exactly.
	st, b := doReqKey(t, api.URL+"/jobwork/issues", token, "rate-2550",
		fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":1,"issued_on":"2026-10-01","unit_rate_rupees":"25.5"}`, partyID, skuID))
	if st != http.StatusCreated {
		t.Fatalf("rate 25.5: want 201, got %d (%s)", st, b)
	}
	var out struct {
		UnitRatePaise int64 `json:"unit_rate_paise"`
	}
	_ = json.Unmarshal(b, &out)
	if out.UnitRatePaise != 2550 {
		t.Fatalf("rate 25.5: want 2550 paise, got %d", out.UnitRatePaise)
	}
}

func TestPhoneRedaction(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)
	partyID := createPartyAPI(t, api.URL, token, "Radha Weavers", "98765 43210")
	skuID := createSKUAPI(t, api.URL, token, "SKU-R", "R", "")
	_, b := doReqKey(t, api.URL+"/jobwork/issues", token, "redact-issue",
		fmt.Sprintf(`{"party_id":%q,"sku_id":%q,"qty":5,"issued_on":"2026-10-01","unit_rate_rupees":"10"}`, partyID, skuID))
	issueID := jsonID(t, b)

	// Public list: phone redacted.
	_, lb := doReq(t, http.MethodGet, api.URL+"/issues", "", "")
	if strings.Contains(string(lb), "98765 43210") {
		t.Fatalf("public /issues leaked phone: %s", lb)
	}

	// Public detail: phone redacted.
	_, db := doReq(t, http.MethodGet, api.URL+"/issues/"+issueID, "", "")
	if strings.Contains(string(db), "98765 43210") {
		t.Fatalf("public /issues/{id} leaked phone: %s", db)
	}

	// Operator detail: phone present.
	_, ob := doReq(t, http.MethodGet, api.URL+"/issues/"+issueID, token, "")
	if !strings.Contains(string(ob), "98765 43210") {
		t.Fatalf("operator /issues/{id} missing phone: %s", ob)
	}
}

func TestUpdatePartyAndSKU(t *testing.T) {
	truncateAll(t)
	api, token := startAuthedAPI(t)
	partyID := createPartyAPI(t, api.URL, token, "Old Name", "9876543210")
	skuID := createSKUAPI(t, api.URL, token, "SKU-UP", "Old SKU", "")

	st, b := doReq(t, http.MethodPatch, api.URL+"/parties/"+partyID, token, `{"name":"New Name"}`)
	if st != http.StatusOK || !strings.Contains(string(b), "New Name") || !strings.Contains(string(b), "9876543210") {
		t.Fatalf("patch party: %d (%s)", st, b)
	}

	st, b = doReq(t, http.MethodPatch, api.URL+"/skus/"+skuID, token, `{"name":"New SKU Name","image_url":"https://example.test/n.png"}`)
	if st != http.StatusOK || !strings.Contains(string(b), "New SKU Name") || !strings.Contains(string(b), `"code":"SKU-UP"`) {
		t.Fatalf("patch sku (code must stay SKU-UP): %d (%s)", st, b)
	}
}
