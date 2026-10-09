# Architecture

## Components

```
            POST /jobwork/receipts            GET /vouchers, POST /vouchers/{id}/retry
Client ───────────────────────────▶  API (cmd/api) ──────────────▶  PostgreSQL
                                          │   receipts + vouchers + idempotency (one txn)
                                          ▼
Next.js /sync ── /api proxy ─────────▶  API                              ▲
                                                                         │ claim (FOR UPDATE SKIP LOCKED)
Worker(s) (cmd/worker) ── HTTP ──▶ Mock accounting (cmd/mock-accounting) │ finalize (fenced by claim_token)
       │                                   │ dedup on client_ref ────────┘
       └── at-least-once delivery ─────────┘ (records persisted in PostgreSQL)
```

## Data model (see `backend/migrations/0001_init.sql`)

- `skus`, `parties`, `jobwork_issues` (carries `unit_rate_paise`, snapshotted).
- `jobwork_receipts` — `idempotency_key` unique; immutable (trigger-guarded).
- `vouchers` — status `pending|pushed|failed`; `client_ref` unique; `UNIQUE(type,
  source_id)` (one voucher per receipt); lease/claim columns (`claim_token`,
  `lease_until`, `claimed_by`), `attempts`, `attempt_baseline`, `next_attempt_at`.
- `idempotency_requests` — `key` PK, `fingerprint`, persisted `response_status` +
  `response_body` (text, exact bytes).
- `accounting_records` — `client_ref` PK, `external_ref` unique (durable dedup).

## Request transaction (receipt)

One `pgx` transaction: reserve key (`INSERT ... ON CONFLICT DO NOTHING`) → read issue
rate → insert receipt (trigger validates qty under an issue row lock) → insert one
voucher with stable `client_ref` → persist the exact 201 body → commit. Any failure
rolls everything back, so the key is not consumed and no partial state remains.

## Worker lifecycle

Poll → claim a batch with `FOR UPDATE SKIP LOCKED` (stamp fresh `claim_token`,
`lease_until`, bump `attempts`), commit → HTTP POST to accounting with a timeout shorter
than the mock's delay → finalize with a fenced UPDATE (`AND claim_token = $token`):
success/409-with-external_ref → `pushed`; retryable error with budget left → reschedule
with capped jittered backoff; budget exhausted or non-retryable → `failed`. A finalize
uses a separate bounded context so a shutdown still records the result; correctness does
not depend on graceful shutdown because an expired lease is reclaimable.

## Delivery semantics

At-least-once HTTP delivery + idempotent remote effect (dedup on `client_ref`). A
timeout is treated as an uncertain outcome, never as proof of non-delivery; re-dispatch
reconciles via HTTP 409 carrying the original `external_ref`.
