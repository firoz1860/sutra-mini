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
Every service automatically loads a `.env` file from the working directory (or a
parent) on startup via `config.Load()`; real environment variables always take
precedence, so no configuration (including the connection string) is hardcoded in
source. Neon's pooled `-pooler` endpoint and `channel_binding=require` are both
supported — pooled endpoints switch to pgx's exec query mode automatically.

The API also sends CORS headers so a browser can call it directly cross-origin. Set
`CORS_ALLOWED_ORIGINS` (comma-separated, default `*`) to scope it, e.g.
`CORS_ALLOWED_ORIGINS=http://localhost:3000`. The `/sync` page itself uses the
same-origin Next.js proxy, so it does not depend on CORS.

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

The page requires **operator sign-in** (top right) before it loads any operational
data; signed out, it shows a sign-in-required state and issues no protected requests.
Once signed in it shows **Pending / Pushed / Failed** groups, polls every 4s, has a
manual Refresh, and a **Retry** button on failed vouchers, plus the Job-work issues /
Job-workers / SKUs tabs (see §9). The browser only talks to the Next.js server-side
proxy under `/api/*` (which forwards the operator token), so `DATABASE_URL`, the backend
URL, and the operator secret are never exposed to the client. Set `OPERATOR_PASSWORD`
and `SESSION_SECRET` on the backend (see §9) so sign-in works.

Production build:

```bash
cd frontend
npm run typecheck
npm run build
```

### Deploy (Render backend + Vercel frontend)

The backend is deployed on Render; the frontend on Vercel. Configuration is entirely
through environment variables (nothing hardcoded).

**Render (backend service)** — build each Go service, set env vars:

| Variable | Value |
|----------|-------|
| `DATABASE_URL` | your Neon pooled URL with `?sslmode=require&channel_binding=require` |
| `CORS_ALLOWED_ORIGINS` | `https://<your-app>.vercel.app` (or `*` while testing) |
| `PORT` | Render sets this automatically; the API honors it |

Run migrations once against Neon (`go run ./cmd/migrate all` with `DATABASE_URL` set),
and run the worker and mock-accounting as their own services if you want the full push
loop in the cloud.

**Vercel (frontend)** — set **one** env variable (Project → Settings → Environment
Variables, all environments), then redeploy:

| Variable | Value |
|----------|-------|
| `BACKEND_URL` | `https://sutra-mini-backend.onrender.com` |

The browser only calls same-origin `/api/*` routes, which the Next.js server forwards to
`BACKEND_URL`. Nothing cross-origin happens in the browser, so **there is no CORS** — and
a Render cold-start just surfaces as a brief "retrying" state instead of a CORS failure.
Do **not** set `NEXT_PUBLIC_API_URL` (direct browser calls would reintroduce cross-origin
requests and cold-start CORS errors).

---

## 5. API contract and curl examples

Status codes: **201** created / exact replay, **400** invalid input, **404** unknown
issue, **409** overshoot or key reused with a different payload, **401**
unauthenticated, **500** unexpected.

> **Authentication.** Every endpoint below requires an operator bearer token (only
> `/healthz` and the `/auth/*` endpoints are public). Obtain one with the operator
> password (set `OPERATOR_PASSWORD` + `SESSION_SECRET` on the server), then send it as
> `Authorization: Bearer <token>` on every request. Without it the API returns `401`.

```bash
# Sign in and capture a token (requires jq, or copy the token manually):
TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"password":"'"$OPERATOR_PASSWORD"'"}' | jq -r .token)
```

**Create a receipt** (requires a non-empty `Idempotency-Key`):

```bash
curl -i -X POST http://localhost:8080/jobwork/receipts \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-key-1' \
  -d '{"issue_id":"33333333-3333-3333-3333-333333330001","qty":4,"received_on":"2026-10-09"}'
```

(The remaining `curl` examples in this section likewise need
`-H "Authorization: Bearer $TOKEN"`; it is omitted below for brevity.)

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

**Ask** (optional LLM feature) — answers "how much of SKU X is still with job-worker Y?"
using read-only DB tools; each tool call is logged and returned. With no API key set it
runs a deterministic stubbed model and reports `"model_stubbed": true`:

```bash
curl -s -X POST http://localhost:8080/ask \
  -H 'Content-Type: application/json' \
  -d '{"question":"how much of SKU-COTTON is still with Radha Weavers?"}'
# -> {"answer":"...","sku":"...","party":"...","outstanding_qty":N,"tool_calls":[...],"model_stubbed":true}
```

**Health:** `curl -s http://localhost:8080/healthz`

---

## 6. Tests

The suite uses **real PostgreSQL** and isolates itself from any existing data. With no
`TEST_DATABASE_URL` set it boots a disposable embedded PostgreSQL (downloaded once).
When `TEST_DATABASE_URL` **is** set, the suite does not use that database directly and
never truncates it: on start it `CREATE DATABASE "sutra_test_<timestamp>"` on the same
server, points every connection (pool, API, worker subprocess, mock) at that fresh
database, and on finish `DROP DATABASE ... WITH (FORCE)` — so only resources it created
are removed. `TEST_DATABASE_URL` must therefore point at a **non-production** server
where the role may create/drop databases; `TestIsolationLeavesBaseDataUntouched` proves
pre-existing data in the base database is untouched. Never point it at the deployed
application database.

```bash
cd backend
go test ./tests/ -count=1            # all integration + reliability tests
go test ./tests/ -v -count=1         # with per-test names
# against your own non-production server (a fresh child DB is created and dropped):
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./tests/ -count=1
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

## 9. Workflow extension: job-worker / SKU / issue management

Beyond the original seeded-data assessment, the `/sync` page now lets studio staff run
the whole workflow through the UI — add a job-worker, add a SKU, record an issue, then
take receipts and watch the voucher sync — without editing the database by hand. The
original assessment workflow, data, and reliability guarantees are preserved; this is
purely **additive**.

### What is seeded vs user-entered

- **Seeded** (via `go run ./cmd/migrate seed`): the original SKUs, parties, and 4
  job-work issues used by the assessment and the curl examples above. Still present and
  untouched. The seeded SKU without any issue remains visible in the SKUs tab.
- **User-entered** (via the UI/API added here): any new job-workers, SKUs, and issues,
  plus the receipts recorded against them.

### UI layout

`/sync` has four tabs on one page: **Sync overview** (unchanged — receipt entry +
voucher sync), **Job-work issues**, **Job-workers**, **SKUs**. Management and
contact-detail views require operator sign-in (top-right).

### New endpoints, forms, and where data lands

| UI form / action | Method + endpoint | Backend table | Auth |
|------------------|-------------------|---------------|------|
| Job-workers → list | `GET /parties` | `parties` | operator |
| Job-workers → Add | `POST /parties` | `parties` | operator |
| Job-workers → Edit | `PATCH /parties/{id}` | `parties` | operator |
| SKUs → list | `GET /skus` | `skus` | operator |
| SKUs → Add | `POST /skus` | `skus` | operator |
| SKUs → Edit | `PATCH /skus/{id}` | `skus` | operator |
| Issues → Create | `POST /jobwork/issues` | `jobwork_issues` | operator |
| Issues → list / detail | `GET /issues`, `GET /issues/{id}` | `jobwork_issues` (+joins) | operator (phone only in the detail view) |
| Issue → receipt history | `GET /jobwork/receipts?issue_id=…` | `jobwork_receipts` + `vouchers` (joined) | operator |
| Record receipt (overview) | `POST /jobwork/receipts` | `jobwork_receipts` + `vouchers` | operator |
| Voucher list / retry sync | `GET /vouchers`, `POST /vouchers/{id}/retry` | `vouchers` | operator |
| Ask (optional) | `POST /ask` | read-only joins | operator |
| Operator sign-in | `POST /auth/login`, `GET /auth/session` | — (stateless token) | public |
| Health | `GET /healthz` | — | public |

**Every business endpoint requires a valid operator bearer token; only
`GET /healthz`, `POST /auth/login`, and `GET /auth/session` are public.**
Authentication is enforced in the Go backend (not just the Next.js proxy), so
direct backend requests are protected too. Missing/invalid/tampered/expired
tokens get `401` before any business handler runs, and unconfigured auth fails
closed.

Each endpoint has an equivalent same-origin Next.js proxy under `frontend/app/api/*`,
so the browser never holds the backend URL or database credentials. The browser keeps
only the operator session token (in `sessionStorage`) and sends it as a bearer token to
the proxy, which forwards it.

### Field-level input rules

- **Job-worker name** — required, trimmed, ≤ 200 chars.
- **Job-worker phone** — **optional** (documented business rule). Stored as `text`,
  entered with `type="tel"`; validated as digits plus `+ - ( ) space` with **6–20
  digits**, so international prefixes work and it is not fixed to 10 digits. Not unique —
  shared numbers are allowed. (Contact detail, not the operator's sign-up.)
- **SKU code** — required, **unique**, normalized to trimmed upper-case
  (`sku-new` → `SKU-NEW`); a duplicate returns `409 duplicate_sku_code`. Immutable on
  edit to preserve references.
- **SKU name** — required. **Image URL** — optional; validated as an absolute
  `http`/`https` URL only (the server never fetches it — no SSRF); the UI shows a text
  fallback if the image fails to load.
- **Issue** — job-worker + SKU chosen from the database (foreign keys validated), a
  positive integer quantity, a valid issued date, and a **rate per unit in rupees**.
  The rupee amount is converted to **integer paise exactly** (no floating-point
  multiply); more than two decimal places, non-numeric input, and overflow are rejected.
  The rate is stored on the issue as its valuation snapshot; a receipt voucher is
  `qty × unit_rate_paise`. SKU/party and (once receipts exist) the rate are immutable.

### Quantity unit convention

Quantities are **whole units** (integers) throughout — issued qty, received qty,
outstanding. There is no decimal/metre support; the integer model is intentional.

### System-managed fields (never user-editable)

Record IDs, idempotency keys and fingerprints, voucher `client_ref`/`external_ref`,
sync `status`, `attempts`/`attempt_baseline`, `claim_token`/`claimed_by`/`lease_until`,
`next_attempt_at`, accounting records, and migration history are all generated or
managed by the system. "Retry sync" calls the existing retry endpoint — there is no way
to mark a voucher pushed by hand.

### Duplicate-submission safety

Issue creation uses the **same server-backed idempotency mechanism as receipts**: the
client sends an `Idempotency-Key`; the server reserves it in `idempotency_requests` with
a request fingerprint and stores the response, so repeated clicks or a lost response
never create a second issue (the UI reuses one key per payload across retries). A reused
key with a different payload returns `409`.

### Authentication and demo limitations

Operator auth is a stateless HMAC-signed session token: `POST /auth/login` checks
`OPERATOR_PASSWORD` (constant-time) and returns a token signed with `SESSION_SECRET`.
**All operational reads and business mutations require a valid token**, verified
server-side (hiding a button is not authorization); only `GET /healthz`,
`POST /auth/login`, and `GET /auth/session` are public. `GET /auth/session` returns
only `{authenticated, configured}` — no secrets or business data. **If
`OPERATOR_PASSWORD`/`SESSION_SECRET` are unset, every protected endpoint is locked by
default** (login → 503, protected → 401 — fail closed).

In the UI the operator signs in (top right); until then the dashboard shows a
sign-in-required state and makes **no** protected requests. On sign-out or session
expiry the UI stops polling, cancels in-flight requests, discards late responses from
the previous session, clears operational data, and returns to the sign-in state; a
`401` surfaces as "Session expired. Please sign in again." A business mutation is never
auto-resubmitted after signing back in. Authenticated responses are returned
`Cache-Control: no-store`. The browser holds only the session token (in
`sessionStorage`); the operator password and signing secret live only on the server and
are never placed in `NEXT_PUBLIC_*`, the browser bundle, API responses, or logs, and the
Next.js proxy never injects a shared server-side credential for anonymous visitors.

The background worker pushes vouchers directly against the database and does **not**
depend on any browser operator session. The public demo should keep using **synthetic**
contacts; this deployment is **not** suitable for confidential, real operational phone
numbers. Phone is shown only in the authenticated issue **detail** view (omitted from
the list). There is no public self-registration.

### Verify the extension end to end

With operator auth configured (set `OPERATOR_PASSWORD` + `SESSION_SECRET`) against a
non-production database (a Neon dev branch or local Postgres):

```bash
cd backend
go test ./tests/ -count=1            # includes the extension suite (auth, parties,
                                     # skus, issues, receipt history, phone redaction)
```

Through the UI (`/sync`): sign in, add a job-worker (synthetic phone), add a SKU, issue
10 units at ₹25/unit, receive 3 (voucher ₹75), confirm received 3 / outstanding 7,
replay the same receipt (nothing duplicates), receive the remaining 7 (outstanding 0),
confirm a further receipt is rejected, and open the issue's receipt history to see the
linked job-worker, SKU, and voucher. Reload to confirm persistence.

Clean-checkout frontend + CI (unchanged order):

```bash
cd frontend
npm ci
npm run typecheck
npm test
npm run build
```

---

## Known limitations

- Neon itself is unverified (no credentials in the build environment); verified against
  real embedded/local PostgreSQL instead.
- `go test -race` not run (no C toolchain for cgo in the build environment).
- The optional `/ask` LLM endpoint is **not implemented** (out of scope under the time
  budget; mandatory work was prioritized).
