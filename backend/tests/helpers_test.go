package tests

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/firoz1860/sutra-mini/backend/internal/accounting"
	"github.com/firoz1860/sutra-mini/backend/internal/api"
	"github.com/firoz1860/sutra-mini/backend/internal/mockacct"
	"github.com/firoz1860/sutra-mini/backend/internal/store"
	"github.com/firoz1860/sutra-mini/backend/internal/worker"
)

var (
	testDB  *store.DB
	testURL string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")

	var ep *embeddedpostgres.EmbeddedPostgres
	if url == "" {
		port, err := freePort()
		if err != nil {
			fmt.Println("free port:", err)
			os.Exit(1)
		}
		dataPath, err := os.MkdirTemp("", "sutra-pg-data-")
		if err != nil {
			fmt.Println("tempdir:", err)
			os.Exit(1)
		}
		cfg := embeddedpostgres.DefaultConfig().
			Username("postgres").Password("postgres").Database("postgres").
			Port(uint32(port)).DataPath(dataPath).
			StartTimeout(90 * time.Second)
		ep = embeddedpostgres.NewDatabase(cfg)
		if err := ep.Start(); err != nil {
			fmt.Println("embedded postgres start failed:", err)
			os.Exit(1)
		}
		url = fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
	}

	db, err := store.Open(ctx, url)
	if err != nil {
		fmt.Println("open test db:", err)
		if ep != nil {
			_ = ep.Stop()
		}
		os.Exit(1)
	}
	if err := db.Migrate(ctx); err != nil {
		fmt.Println("migrate test db:", err)
		db.Close()
		if ep != nil {
			_ = ep.Stop()
		}
		os.Exit(1)
	}
	testDB = db
	testURL = url

	code := m.Run()

	db.Close()
	if ep != nil {
		_ = ep.Stop()
	}
	os.Exit(code)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(),
		`TRUNCATE jobwork_receipts, vouchers, idempotency_requests, accounting_records, jobwork_issues, parties, skus RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func createIssue(t *testing.T, qty int, ratePaise int64) string {
	t.Helper()
	ctx := context.Background()
	var partyID, skuID, issueID string
	if err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO parties (name, phone) VALUES ('Test Party', '0000') RETURNING id`).Scan(&partyID); err != nil {
		t.Fatalf("create party: %v", err)
	}
	code := fmt.Sprintf("SKU-%d", time.Now().UnixNano())
	if err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO skus (code, name, image_url) VALUES ($1, 'Test SKU', 'https://example.test/x.png') RETURNING id`, code).Scan(&skuID); err != nil {
		t.Fatalf("create sku: %v", err)
	}
	if err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO jobwork_issues (party_id, sku_id, qty, unit_rate_paise, issued_on)
		 VALUES ($1, $2, $3, $4, '2026-10-01') RETURNING id`, partyID, skuID, qty, ratePaise).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	return issueID
}

func startAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(testDB).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func startMock(t *testing.T, mode mockacct.Mode, delay time.Duration) (*httptest.Server, *mockacct.Server) {
	t.Helper()
	ms := mockacct.New(testDB.Pool, mode, delay, log.New(os.Stdout, "", log.LstdFlags))
	srv := httptest.NewServer(ms.Routes())
	t.Cleanup(srv.Close)
	return srv, ms
}

func newWorker(t *testing.T, mockURL string, cfg worker.Config) *worker.Worker {
	t.Helper()
	client := accounting.NewClient(mockURL, cfg.HTTPTimeout)
	return worker.New(testDB, client, cfg, log.New(os.Stdout, cfg.WorkerID+" ", log.LstdFlags))
}

func waitFor(t *testing.T, timeout, interval time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out after %s waiting for: %s", timeout, desc)
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testDB.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query failed (%s): %v", query, err)
	}
	return n
}
