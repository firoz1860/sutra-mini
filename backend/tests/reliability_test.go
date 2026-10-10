package tests

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/firoz1860/sutra-mini/backend/internal/mockacct"
	"github.com/firoz1860/sutra-mini/backend/internal/worker"
)

func createVoucher(t *testing.T, clientRef string, amount int64) string {
	t.Helper()
	var id string
	err := testDB.Pool.QueryRow(context.Background(),
		`INSERT INTO vouchers (type, source_id, amount_paise, client_ref, status, next_attempt_at)
		 VALUES ('jobwork_receipt', gen_random_uuid(), $2, $1, 'pending', now()) RETURNING id`,
		clientRef, amount).Scan(&id)
	if err != nil {
		t.Fatalf("create voucher: %v", err)
	}
	return id
}

type voucherState struct {
	Status      string
	Attempts    int
	Baseline    int
	ClientRef   string
	ExternalRef *string
	LastError   *string
}

func getVoucher(t *testing.T, id string) voucherState {
	t.Helper()
	var v voucherState
	err := testDB.Pool.QueryRow(context.Background(),
		`SELECT status, attempts, attempt_baseline, client_ref, external_ref, last_error FROM vouchers WHERE id=$1`, id).
		Scan(&v.Status, &v.Attempts, &v.Baseline, &v.ClientRef, &v.ExternalRef, &v.LastError)
	if err != nil {
		t.Fatalf("get voucher: %v", err)
	}
	return v
}

func relCfg(id string) worker.Config {
	return worker.Config{
		WorkerID:    id,
		Poll:        50 * time.Millisecond,
		Lease:       10 * time.Second,
		HTTPTimeout: 2 * time.Second,
		BackoffBase: 50 * time.Millisecond,
		BackoffMax:  200 * time.Millisecond,
		MaxAttempts: 5,
		Concurrency: 4,
	}
}

func driveUntil(t *testing.T, w *worker.Worker, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := w.DrainOnce(context.Background()); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out driving worker")
}

func TestReliabilityNormalSuccess(t *testing.T) {
	truncateAll(t)
	mock, _ := startMock(t, mockacct.ModeSuccess, time.Second)
	w := newWorker(t, mock.URL, relCfg("rel-success"))
	id := createVoucher(t, "A-normal", 15000)

	driveUntil(t, w, 5*time.Second, func() bool { return getVoucher(t, id).Status == "pushed" })

	v := getVoucher(t, id)
	if v.ExternalRef == nil || *v.ExternalRef == "" {
		t.Fatal("expected a non-empty external_ref")
	}
	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='A-normal'`); n != 1 {
		t.Fatalf("want exactly 1 accounting record, got %d", n)
	}
}

func TestReliabilityBackoffThenFailure(t *testing.T) {
	truncateAll(t)
	mock, _ := startMock(t, mockacct.ModeFail, time.Second)
	cfg := relCfg("rel-fail")
	cfg.MaxAttempts = 3
	cfg.HTTPTimeout = time.Second
	w := newWorker(t, mock.URL, cfg)
	id := createVoucher(t, "B-fail", 15000)

	start := time.Now()
	driveUntil(t, w, 10*time.Second, func() bool { return getVoucher(t, id).Status == "failed" })
	elapsed := time.Since(start)

	v := getVoucher(t, id)
	if v.Attempts != 3 {
		t.Fatalf("want 3 dispatch attempts, got %d", v.Attempts)
	}
	if v.LastError == nil || *v.LastError == "" {
		t.Fatal("failed voucher must preserve a last_error")
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("retries happened too fast (%s) - expected backoff spacing, not a tight loop", elapsed)
	}
	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='B-fail'`); n != 0 {
		t.Fatalf("failed pushes must not create accounting records, got %d", n)
	}
}

func TestReliabilityManualRetry(t *testing.T) {
	truncateAll(t)
	api := startAPI(t)
	mock, ms := startMock(t, mockacct.ModeFail, time.Second)
	cfg := relCfg("rel-retry")
	cfg.MaxAttempts = 2
	cfg.HTTPTimeout = time.Second
	w := newWorker(t, mock.URL, cfg)
	id := createVoucher(t, "C-retry", 15000)

	driveUntil(t, w, 10*time.Second, func() bool { return getVoucher(t, id).Status == "failed" })
	before := getVoucher(t, id)

	ms.SetClientMode("C-retry", mockacct.ModeSuccess)

	req, _ := http.NewRequest(http.MethodPost, api.URL+"/vouchers/"+id+"/retry", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("retry request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry: want 200, got %d", resp.StatusCode)
	}

	driveUntil(t, w, 5*time.Second, func() bool { return getVoucher(t, id).Status == "pushed" })

	after := getVoucher(t, id)
	if after.ClientRef != before.ClientRef {
		t.Fatalf("client_ref changed on retry: %s -> %s", before.ClientRef, after.ClientRef)
	}
	if after.Attempts <= before.Attempts {
		t.Fatalf("lifetime attempts should increase (%d -> %d)", before.Attempts, after.Attempts)
	}
	if after.Baseline != before.Attempts {
		t.Fatalf("retry should reset the budget baseline to prior attempts (%d), got %d", before.Attempts, after.Baseline)
	}
	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='C-retry'`); n != 1 {
		t.Fatalf("want 1 accounting record after successful retry, got %d", n)
	}
}

func TestReliabilityTimeoutThenReconcile(t *testing.T) {
	truncateAll(t)
	mock, _ := startMock(t, mockacct.ModeDelay, time.Second)
	cfg := relCfg("rel-timeout")
	cfg.HTTPTimeout = 300 * time.Millisecond
	cfg.BackoffBase = 150 * time.Millisecond
	cfg.BackoffMax = 300 * time.Millisecond
	cfg.MaxAttempts = 50
	w := newWorker(t, mock.URL, cfg)
	id := createVoucher(t, "D-timeout", 15000)

	driveUntil(t, w, 15*time.Second, func() bool { return getVoucher(t, id).Status == "pushed" })

	v := getVoucher(t, id)
	if v.ExternalRef == nil || *v.ExternalRef == "" {
		t.Fatal("expected external_ref after reconciliation")
	}
	var recordRef string
	if err := testDB.Pool.QueryRow(context.Background(),
		`SELECT external_ref FROM accounting_records WHERE client_ref='D-timeout'`).Scan(&recordRef); err != nil {
		t.Fatalf("read accounting record: %v", err)
	}
	if *v.ExternalRef != recordRef {
		t.Fatalf("voucher external_ref %s must match reconciled record %s", *v.ExternalRef, recordRef)
	}
	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='D-timeout'`); n != 1 {
		t.Fatalf("want exactly 1 accounting record despite timeout + resend, got %d", n)
	}
}

func TestReliabilityRealWorkerCrash(t *testing.T) {
	truncateAll(t)
	// The specified five-second delay scenario.
	mock, ms := startMock(t, mockacct.ModeDelay, 5*time.Second)
	accepted := make(chan string, 16)
	ms.OnDelayedAccept = func(clientRef string) {
		select {
		case accepted <- clientRef:
		default:
		}
	}
	workerBin := buildWorker(t)
	id := createVoucher(t, "E-crash", 15000)

	env := append(os.Environ(),
		"DATABASE_URL="+testURL,
		"ACCOUNTING_URL="+mock.URL,
		"WORKER_ID=crash-1",
		"WORKER_POLL=100ms",
		"WORKER_LEASE=2s",
		"WORKER_HTTP_TIMEOUT=2s",
		"WORKER_BACKOFF_BASE=150ms",
		"WORKER_BACKOFF_MAX=300ms",
		"WORKER_MAX_ATTEMPTS=50",
		"WORKER_CONCURRENCY=2",
	)
	cmd := exec.Command(workerBin)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker subprocess: %v", err)
	}
	killed := false
	defer func() {
		if !killed {
			_ = cmd.Process.Kill()
		}
	}()

	// Wait for proof that the mock actually ACCEPTED the delayed HTTP request
	// (not merely that the voucher was claimed), then kill the worker mid-delay
	// before any accounting record exists.
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("mock never accepted a delayed request from the worker")
	}
	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='E-crash'`); n != 0 {
		t.Fatal("record committed too early; cannot prove crash-before-success")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill worker: %v", err)
	}
	_, _ = cmd.Process.Wait()
	killed = true

	waitFor(t, 10*time.Second, 100*time.Millisecond, "accounting record to be committed server-side after the 5s delay", func() bool {
		return countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='E-crash'`) == 1
	})
	if s := getVoucher(t, id).Status; s == "pushed" {
		t.Fatal("voucher should not be pushed yet - worker was killed before recording success")
	}

	waitFor(t, 6*time.Second, 100*time.Millisecond, "lease to expire", func() bool {
		return countRows(t, `SELECT count(*) FROM vouchers WHERE id=$1 AND (lease_until IS NULL OR lease_until <= now())`, id) == 1
	})

	cmd2 := exec.Command(workerBin)
	cmd2.Env = env
	cmd2.Stdout = os.Stdout
	cmd2.Stderr = os.Stderr
	if err := cmd2.Start(); err != nil {
		t.Fatalf("restart worker: %v", err)
	}
	defer func() { _ = cmd2.Process.Kill(); _, _ = cmd2.Process.Wait() }()

	waitFor(t, 10*time.Second, 100*time.Millisecond, "voucher to reconcile to pushed", func() bool {
		return getVoucher(t, id).Status == "pushed"
	})

	if n := countRows(t, `SELECT count(*) FROM accounting_records WHERE client_ref='E-crash'`); n != 1 {
		t.Fatalf("want exactly 1 accounting record after crash + restart, got %d", n)
	}
	v := getVoucher(t, id)
	if v.ExternalRef == nil || *v.ExternalRef == "" {
		t.Fatal("expected external_ref after reconciliation")
	}
}

func TestReliabilityTwoWorkerProcesses(t *testing.T) {
	truncateAll(t)
	mock, _ := startMock(t, mockacct.ModeSuccess, time.Second)
	workerBin := buildWorker(t)

	const n = 12
	for i := 0; i < n; i++ {
		createVoucher(t, "F-"+string(rune('a'+i)), 15000)
	}

	env := func(id string) []string {
		return append(os.Environ(),
			"DATABASE_URL="+testURL,
			"ACCOUNTING_URL="+mock.URL,
			"WORKER_ID="+id,
			"WORKER_POLL=100ms",
			"WORKER_LEASE=5s",
			"WORKER_HTTP_TIMEOUT=2s",
			"WORKER_CONCURRENCY=2",
			"WORKER_MAX_ATTEMPTS=5",
		)
	}
	var procs []*exec.Cmd
	for _, id := range []string{"proc-1", "proc-2"} {
		cmd := exec.Command(workerBin)
		cmd.Env = env(id)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start worker %s: %v", id, err)
		}
		procs = append(procs, cmd)
	}
	defer func() {
		for _, c := range procs {
			_ = c.Process.Kill()
			_, _ = c.Process.Wait()
		}
	}()

	waitFor(t, 25*time.Second, 200*time.Millisecond, "all vouchers pushed by two independent worker processes", func() bool {
		return countRows(t, `SELECT count(*) FROM vouchers WHERE status='pushed'`) == n
	})

	if got := countRows(t, `SELECT count(*) FROM accounting_records`); got != n {
		t.Fatalf("want exactly %d accounting records (one per client_ref), got %d", n, got)
	}
	if maxAttempts := countRows(t, `SELECT COALESCE(MAX(attempts),0) FROM vouchers`); maxAttempts != 1 {
		t.Fatalf("with two safe worker processes each voucher should be dispatched once; max attempts=%d", maxAttempts)
	}
}

func TestReliabilitySlowCallIsolation(t *testing.T) {
	truncateAll(t)
	mock, ms := startMock(t, mockacct.ModeSuccess, time.Second)
	ms.SetClientMode("G-slow", mockacct.ModeDelay)

	cfg := relCfg("rel-isolation")
	cfg.HTTPTimeout = 3 * time.Second
	cfg.Concurrency = 2
	w := newWorker(t, mock.URL, cfg)

	slow := createVoucher(t, "G-slow", 15000)
	fast := createVoucher(t, "G-fast", 15000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	waitFor(t, 3*time.Second, 20*time.Millisecond, "fast voucher to be pushed", func() bool {
		return getVoucher(t, fast).Status == "pushed"
	})
	if s := getVoucher(t, slow).Status; s == "pushed" {
		t.Fatal("slow voucher should still be in flight while fast one already completed")
	}
	waitFor(t, 5*time.Second, 50*time.Millisecond, "slow voucher to eventually push", func() bool {
		return getVoucher(t, slow).Status == "pushed"
	})
}

func TestReliabilityStaleClaimFencing(t *testing.T) {
	truncateAll(t)
	id := createVoucher(t, "H-fence", 15000)

	claims, err := testDB.Claim(context.Background(), "worker-A", 10*time.Second, 1, 5)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("want 1 claim, got %d", len(claims))
	}
	staleToken := claims[0].ClaimToken

	var newToken string
	if err := testDB.Pool.QueryRow(context.Background(),
		`UPDATE vouchers SET claim_token=gen_random_uuid(), claimed_by='worker-B', lease_until=now()+interval '10 seconds'
		 WHERE id=$1 RETURNING claim_token`, id).Scan(&newToken); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	ok, err := testDB.MarkPushed(context.Background(), id, staleToken, "EXT-stale")
	if err != nil {
		t.Fatalf("mark pushed: %v", err)
	}
	if ok {
		t.Fatal("stale claim token must not be able to mark the voucher pushed")
	}
	if s := getVoucher(t, id).Status; s != "pending" {
		t.Fatalf("voucher must stay pending after fenced update, got %s", s)
	}

	ok, _ = testDB.Reschedule(context.Background(), id, staleToken, time.Second, "stale")
	if ok {
		t.Fatal("stale claim token must not be able to reschedule")
	}
	ok, _ = testDB.MarkFailed(context.Background(), id, staleToken, "stale")
	if ok {
		t.Fatal("stale claim token must not be able to mark failed")
	}

	ok, err = testDB.MarkPushed(context.Background(), id, newToken, "EXT-current")
	if err != nil {
		t.Fatalf("mark pushed current: %v", err)
	}
	if !ok {
		t.Fatal("current claim token should succeed")
	}
	if s := getVoucher(t, id).Status; s != "pushed" {
		t.Fatalf("want pushed with current token, got %s", s)
	}
}

func TestRetryBudgetBoundedAcrossCrashes(t *testing.T) {
	truncateAll(t)
	id := createVoucher(t, "budget-crash", 15000)
	const maxAttempts = 3

	// Simulate a worker that claims then crashes before finalizing, repeatedly.
	// Each claim increments attempts durably; the short lease lets the next claim reclaim.
	for i := 1; i <= maxAttempts; i++ {
		claims, err := testDB.Claim(context.Background(), "crasher", 30*time.Millisecond, 1, maxAttempts)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if len(claims) != 1 {
			t.Fatalf("claim %d: expected 1 claim, got %d", i, len(claims))
		}
		if claims[0].Attempts != i {
			t.Fatalf("claim %d: expected attempts=%d, got %d", i, i, claims[0].Attempts)
		}
		time.Sleep(60 * time.Millisecond)
	}

	// Budget is spent: the voucher must no longer be claimable for dispatch
	// (attempts cannot grow indefinitely across crashes).
	claims, err := testDB.Claim(context.Background(), "crasher", 30*time.Millisecond, 1, maxAttempts)
	if err != nil {
		t.Fatalf("claim after budget: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("exhausted voucher must not be claimable, got %d claims", len(claims))
	}

	// The reaper moves it to failed with a reason; attempts stay bounded at maxAttempts.
	n, err := testDB.ReapExhausted(context.Background(), maxAttempts, "retry budget exhausted after repeated claims/crashes")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 voucher reaped to failed, got %d", n)
	}
	v := getVoucher(t, id)
	if v.Status != "failed" {
		t.Fatalf("want status failed, got %s", v.Status)
	}
	if v.Attempts != maxAttempts {
		t.Fatalf("attempts must be bounded at %d across crashes, got %d", maxAttempts, v.Attempts)
	}
	if v.LastError == nil || *v.LastError == "" {
		t.Fatal("failed voucher must carry a last_error")
	}
}

func buildWorker(t *testing.T) string {
	t.Helper()
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	out := filepath.Join(t.TempDir(), "worker-test.exe")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(cwd)
	cmd := exec.Command(goBin, "build", "-o", out, "./cmd/worker")
	cmd.Dir = moduleRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build worker binary: %v", err)
	}
	return out
}
