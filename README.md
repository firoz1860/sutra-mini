# Sutra Mini

Jobwork **receipts → accounting vouchers** with transactionally-correct quantity
control, database-backed idempotency, and an independently runnable sync worker
that delivers vouchers to an accounting system **at least once** with an
**idempotent** remote effect.

Built for the Shree Radha Studio Round 2 assessment.

- **Backend:** Go (standard-library HTTP routing + `pgx/v5`)
- **Database:** PostgreSQL (Neon in deployment; disposable embedded Postgres for tests)
- **Frontend:** Next.js (TypeScript), single page at `/sync`
- **Mock accounting server:** separate Go service
- **Sync workers:** separate Go process, run one or many

---

## Tested toolchain

| Tool | Version used |
|------|--------------|
| Go | 1.26.0 (windows/amd64) |
| Node.js | 24.14.0 |
| npm | 11.9.0 |
| PostgreSQL | 18.3 (via `embedded-postgres` in the test suite) |
| Next.js | 14.2.35 |
| pgx | v5.7.2 |
| embedded-postgres (test only) | v1.34.0 |

> **Neon status:** No Neon credentials were available in the build environment, so
> the system was verified against a real but **embedded/local** PostgreSQL. Neon
> itself is **unverified**. Exact Neon setup steps are in [Neon setup](#neon-setup).

---

## Repository layout

```
backend/
  cmd/api/              HTTP API server
  cmd/worker/           sync worker (run 1..N instances)
  cmd/mock-accounting/  mock accounting server
  cmd/migrate/          migration + seed runner
  internal/
    config/             env helpers
    store/              pgx pool, migrations, all SQL queries
    receipts/           receipt + voucher transaction, idempotency
    accounting/         HTTP client for the accounting server
    mockacct/           mock accounting server logic
    worker/             claim / lease / fencing / backoff
  migrations/           0001_init.sql (schema + triggers), 0002_seed.sql
  tests/                integration + reliability tests (real Postgres)
frontend/
  app/sync/             the /sync page
  app/api/vouchers/     same-origin server-side proxy to the backend
```

---

## Prerequisites

- Go 1.26+
- Node.js 20+ and npm
- A PostgreSQL 13+ database (Neon, or local). The **test suite needs no database** —
  it starts a disposable embedded PostgreSQL automatically (first run downloads the
  binary, so it needs network access once).

---

## 1. Configure environment

```bash
cp .env.example .env
# edit .env and set DATABASE_URL (Neon or local)
```

`DATABASE_URL` is the only required variable for the servers. All services read it.

### Neon setup

1. Create a project at https://neon.tech and copy the connection string.
2. It must use TLS — keep `?sslmode=require` on the URL:
   ```
   DATABASE_URL=postgres://USER:PASSWORD@ep-xxxx.region.aws.neon.tech/neondb?sslmode=require
   ```
3. Run migrations and seed against it (next section).

### Local Postgres fallback (development only)

Any local Postgres works, e.g. via Docker if you have it:

```bash
docker run -d --name sutra-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
```

> The local database is a reproducibility convenience and is **not** a substitute
> for verifying Neon.

---

## 2. Migrate and seed

```bash
cd backend
go run ./cmd/migrate up      # apply schema (idempotent; tracked in schema_migrations)
go run ./cmd/migrate seed    # insert seed data (safe to re-run; ON CONFLICT DO NOTHING)
# or both at once:
go run ./cmd/migrate all
```

Seeded identifiers used throughout this README:

| Issue ID | SKU | Party | qty | unit rate |
|----------|-----|-------|-----|-----------|
| `33333333-3333-3333-3333-333333330001` | SKU-COTTON | Radha Weavers | 10 | ₹15.00 (1500 paise) |
| `33333333-3333-3333-3333-333333330002` | SKU-SILK | Radha Weavers | 20 | ₹25.00 (2500 paise) |
| `33333333-3333-3333-3333-333333330003` | SKU-DENIM | Shyam Tailoring | 50 | ₹8.00 (800 paise) |
| `33333333-3333-3333-3333-333333330004` | SKU-LINEN | Shyam Tailoring | 100 | ₹12.00 (1200 paise) |

---

## 3. Run the services

Each of these runs in its own terminal (all read `DATABASE_URL` from the environment).

```bash
cd backend

# API (port 8080)
go run ./cmd/api

# Mock accounting server (port 8090)
go run ./cmd/mock-accounting

# Sync worker
go run ./cmd/worker
```

### Two workers simultaneously

Give each a distinct identifier:

```bash
WORKER_ID=worker-1 go run ./cmd/worker
WORKER_ID=worker-2 go run ./cmd/worker
```

They coordinate safely through `SELECT ... FOR UPDATE SKIP LOCKED` claims, durable
leases, and claim-token fencing.

### Deterministic mock modes (for demos / tests)

The mock defaults to the required random mix (20% HTTP 500, 10% five-second delay,
70% success). Override globally for a deterministic demo:

```bash
MOCK_MODE=success go run ./cmd/mock-accounting   # always succeed
MOCK_MODE=fail    go run ./cmd/mock-accounting   # always HTTP 500
MOCK_MODE=delay   go run ./cmd/mock-accounting   # always delay then succeed
```

A per-request override header `X-Mock-Mode: success|fail|delay` is also honored.

---

## 4. Frontend (/sync)

```bash
cd frontend
npm install
BACKEND_URL=http://localhost:8080 npm run dev
# open http://localhost:3000/sync
```

The page shows **Pending / Pushed / Failed** groups, polls every 4s, has a manual
Refresh, and a **Retry** button on failed vouchers. The browser only talks to the
Next.js server-side proxy (`/api/vouchers`), so `DATABASE_URL` and the backend URL
are never exposed to the client.

Production build:

```bash
cd frontend
npm run typecheck
npm run build
```

---

## 5. API contract and curl examples

Status codes: **201** created / exact replay, **400** invalid input, **404** unknown
issue, **409** overshoot or key reused with a different payload, **500** unexpected.

**Create a receipt** (requires a non-empty `Idempotency-Key`):

```bash
curl -i -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-key-1' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":4,"received_on":"2026-10-09"}'
```

**Replay the same key + body** → returns the original 201 response byte-for-byte,
inserts nothing new:

```bash
curl -i -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-key-1' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":4,"received_on":"2026-10-09"}'
```

**Reuse the key with a different payload** → `409 idempotency_conflict`:

```bash
curl -i -X POST http://localhost:8080/jobwork/receipts \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-key-1' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":5,"received_on":"2026-10-09"}'
```

**Fetch issue totals** (issued / received / outstanding):

```bash
curl -s http://localhost:8080/issues/33333333-3333-3333-3333-333333330001
```

**List vouchers by status** (omit `status` for all):

```bash
curl -s 'http://localhost:8080/vouchers?status=pending'
curl -s 'http://localhost:8080/vouchers?status=failed'
```

**Retry a failed voucher** (take an `id` from the failed list):

```bash
curl -i -X POST http://localhost:8080/vouchers/<VOUCHER_ID>/retry
```

**Duplicate accounting submission** (same `client_ref` twice → second returns
`409` with the original `external_ref`, no second record):

```bash
curl -i -X POST http://localhost:8090/api/vouchers \
  -H 'Content-Type: application/json' -H 'X-Mock-Mode: success' \
  -d '{"client_ref":"dup-demo-1","type":"jobwork_receipt","amount_paise":15000}'

curl -i -X POST http://localhost:8090/api/vouchers \
  -H 'Content-Type: application/json' \
  -d '{"client_ref":"dup-demo-1","type":"jobwork_receipt","amount_paise":15000}'
```

**Health:** `curl -s http://localhost:8080/healthz`

---

## 6. Tests

The suite uses **real PostgreSQL**. With no `TEST_DATABASE_URL` set it boots a
disposable embedded PostgreSQL (downloaded once), applies migrations into it, and
tears it down afterwards. It never truncates an arbitrary supplied database.

```bash
cd backend
go test ./tests/ -count=1            # all integration + reliability tests
go test ./tests/ -v -count=1         # with per-test names
# against your own throwaway DB instead of embedded:
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/sutra_test?sslmode=disable' go test ./tests/ -count=1
```

What runs:

- **Concurrent overshoot** — 10 concurrent receipts of qty 2 on an issue of qty 10,
  distinct keys: exactly 5 succeed, 5 get 409, total received = 10, 5 receipts, 5
  vouchers, outstanding 0, no receipt without a voucher.
- **Idempotency replay** — identical key+body returns identical status/body and the
  same receipt/voucher ids; totals change once.
- Concurrent identical-key requests, same-key-different-payload 409, overshoot does
  not consume the key, invalid inputs, unknown issue.
- **Reliability A–H**: normal success, backoff→terminal failure, manual retry,
  timeout→409 reconciliation, **real worker crash subprocess** + restart recovery,
  two workers, slow-call isolation, stale-claim fencing.

### Failure-mode demos (deterministic, no reliance on randomness)

Start the mock in a fixed mode and watch a voucher move:

```bash
# force failure then watch it go failed, then retry to success:
MOCK_MODE=fail    go run ./cmd/mock-accounting   # terminal A
go run ./cmd/api                                  # terminal B
go run ./cmd/worker                               # terminal C
# create a receipt (curl above), watch /sync; switch mock to success and click Retry.
```

### Race detector

`go test -race` requires cgo and a C toolchain, which was **not available** in the
build environment, so the race detector was not run here. The concurrency tests pass
without it.

---

## 7. How the guarantees are implemented

- **Receipt transaction boundary.** The receipt row, its single voucher, and the
  persisted idempotency response are written in one `pgx` transaction and committed
  together; any error rolls the whole thing back. See `internal/receipts/receipts.go`.
- **Quantity locking.** A `BEFORE INSERT` trigger on `jobwork_receipts`
  (`enforce_receipt_qty`) does `SELECT qty ... FOR UPDATE` on the parent issue, then
  checks `SUM(received) + new <= issued`. The row lock serializes concurrent receipts
  for the same issue, so the `SUM(receipt.qty) <= issue.qty` invariant holds under
  concurrency. Receipts are immutable (a second trigger forbids UPDATE/DELETE), so the
  invariant has a single enforcement point.
- **Database-backed idempotency.** `idempotency_requests(key PK, fingerprint,
  response_status, response_body)`. The transaction does `INSERT ... ON CONFLICT (key)
  DO NOTHING RETURNING key`. A new key proceeds; a conflicting key blocks on the
  in-flight transaction, then replays the committed response if the fingerprint
  matches, or returns 409 if it differs. The original response is stored as text so
  replays are byte-for-byte identical. A rolled-back transaction leaves no reservation,
  so a failed request does not consume the key.
- **Stable client_ref.** `client_ref = "jwr-" + receipt_id`, written once at creation
  and never changed; it is the accounting system's dedup key across retries and worker
  restarts.
- **Claim leases and fencing.** The worker claims due vouchers with
  `FOR UPDATE SKIP LOCKED`, stamping a fresh `claim_token`, `lease_until`, and bumping
  `attempts` — all committed **before** any HTTP call. Every finalizing UPDATE carries
  `AND claim_token = $token` and checks the affected row count, so a stale worker whose
  lease expired and was reclaimed cannot overwrite the newer worker's result.
- **Backoff.** Capped exponential backoff with jitter, persisted as `next_attempt_at`,
  so it survives process restarts.
- **Crash recovery.** Attempts are incremented durably at claim time, so a crash is
  counted. An expired lease makes the voucher reclaimable; a reclaim + resend reconciles
  via 409.
- **409 reconciliation.** A 409 carrying the original `external_ref` is treated as
  success (the remote effect already exists); a malformed 409 is treated as a retryable
  error, not success.
- **Why at-least-once.** A network timeout does not prove the remote did or did not
  post. The worker therefore re-dispatches; the accounting server deduplicates on
  `client_ref` (unique, persisted in Postgres, surviving restarts), so the **effect** is
  exactly-once even though **delivery** is at-least-once. We do not claim exactly-once
  network delivery.

### Money / rate assumption

The brief does not define a valuation rate, so this is an **assessment assumption**:
each issue carries a `unit_rate_paise` (integer paise, snapshotted on the issue), and a
receipt voucher amount is `qty × unit_rate_paise`, computed with an overflow guard and
stored as `BIGINT`. No floating point is used for money.

### Meaning of attempt counts

`attempts` counts **dispatch attempts**, including attempts interrupted by a crash. A
manual retry preserves the lifetime `attempts` and resets the current retry budget by
moving `attempt_baseline` up to the current `attempts`.

---

## 8. Reset / cleanup (disposable data only)

```bash
# drop and recreate ONLY if the target is a throwaway database:
# psql "$DATABASE_URL" -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'
# then re-run: go run ./cmd/migrate all
```

Do not run destructive commands against a database that holds real data.

---

## Known limitations

- Neon itself is unverified (no credentials in the build environment); verified against
  real embedded/local PostgreSQL instead.
- `go test -race` not run (no C toolchain for cgo in the build environment).
- The optional `/ask` LLM endpoint is **not implemented** (out of scope under the time
  budget; mandatory work was prioritized).
