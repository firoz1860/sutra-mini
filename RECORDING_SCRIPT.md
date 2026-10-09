# Recording Script — Sutra Mini (≈3 minutes)

> **STATUS: NOT YET RECORDED.** No screen-capture tooling was available in the build
> environment, so this is the exact script to follow when recording. Every step uses
> deterministic commands (fixed mock modes), so nothing depends on random failures.

## One-time setup before recording

```bash
# Terminal 0 — set a throwaway DATABASE_URL (local Postgres or Neon), then:
cd backend
go run ./cmd/migrate all          # schema + seed
```

Keep these terminals ready:

- **T1 – API:**            `cd backend && go run ./cmd/api`
- **T2 – mock (success):** `cd backend && MOCK_MODE=success go run ./cmd/mock-accounting`
- **T3 – worker:**         `cd backend && go run ./cmd/worker`
- **T4 – scratch** (curl commands)
- **Browser:** `cd frontend && BACKEND_URL=http://localhost:8080 npm run dev`, open `http://localhost:3000/sync`

Seeded issue used in the demo: `33333333-3333-3333-3333-333333330001` (qty 10, ₹15/unit).

---

## 0:00–0:20 — Intro

- Show the repo tree (`backend/`, `frontend/`, `migrations/`, `tests/`).
- One sentence: *"Sutra Mini records jobwork receipts and reliably syncs an accounting
  voucher for each one — transactionally correct quantities, idempotent requests, and a
  crash-safe worker."*

## 0:20–0:50 — Receipt + idempotent replay

```bash
# create a receipt
curl -s -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: rec-demo' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":4,"received_on":"2026-10-09"}'

# issue totals: received 4, outstanding 6
curl -s http://localhost:8080/issues/33333333-3333-3333-3333-333333330001

# replay SAME key + body → identical response, nothing new inserted
curl -s -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: rec-demo' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":4,"received_on":"2026-10-09"}'
```

- Point out the replay returns the same receipt/voucher ids; totals still show received 4.

## 0:50–1:15 — Concurrent overshoot (invariant)

```bash
cd backend
go test ./tests/ -run TestConcurrentOvershoot -v -count=1
```

- Narrate: 10 concurrent receipts of qty 2 on a qty-10 issue → exactly 5 succeed, 5 get
  409, total received = 10 (never exceeds issued).

## 1:15–1:45 — Failed voucher + Retry on /sync

```bash
# stop T2, restart the mock forced to fail, create a receipt so the worker fails it:
#   T2:  MOCK_MODE=fail go run ./cmd/mock-accounting
curl -s -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: fail-demo' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330002","qty":2,"received_on":"2026-10-09"}'
```

- On `/sync`, show the voucher land in **Failed** with a `last_error` and attempt count.
- Restart T2 with `MOCK_MODE=success`, click **Retry**, show it move to **Pushed** with
  an `external_ref`.

## 1:45–2:25 — Delayed call, worker crash, 409 reconciliation

```bash
cd backend
go test ./tests/ -run TestReliabilityRealWorkerCrash -v -count=1
```

- Narrate while it runs: the worker claims the voucher and dispatches a 5s(test:1s)
  delayed accounting call; the worker is **killed** before recording success; the mock
  commits the record server-side anyway; after the lease expires a **restarted** worker
  resends, gets **HTTP 409 with the original external_ref**, and marks it pushed —
  **exactly one** accounting record.

Manual 409 proof:

```bash
curl -i -X POST http://localhost:8090/api/vouchers -H 'Content-Type: application/json' \
  -H 'X-Mock-Mode: success' -d '{"client_ref":"dup-demo","amount_paise":15000,"type":"jobwork_receipt"}'
curl -i -X POST http://localhost:8090/api/vouchers -H 'Content-Type: application/json' \
  -d '{"client_ref":"dup-demo","amount_paise":15000,"type":"jobwork_receipt"}'   # 409 + same external_ref
```

## 2:25–2:45 — Two workers

```bash
cd backend
go test ./tests/ -run TestReliabilityTwoWorkers -v -count=1
```

- Narrate: two independent workers drain 12 vouchers; each `client_ref` produces exactly
  one accounting record and every voucher is dispatched once — safe claim distribution.

## 2:45–3:00 — Agent notes + honesty

- Open `AGENT_NOTES.md`; state the **money assumption** (`qty × unit_rate_paise`,
  integer paise, snapshotted on the issue) and the honest limitation that **Neon is
  unverified** (verified against real embedded/local PostgreSQL instead).
