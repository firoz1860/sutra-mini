# Sutra Mini — project guide

Jobwork receipts → accounting voucher sync. Go backend + PostgreSQL + Next.js `/sync`,
a separate mock accounting server, and independently runnable sync workers.

## Stack and layout

- Go (std-lib HTTP routing, `pgx/v5`), PostgreSQL (Neon in deployment), Next.js/TS.
- `backend/cmd/{api,worker,mock-accounting,migrate}` entrypoints.
- `backend/internal/{config,store,receipts,accounting,mockacct,worker}` packages.
- `backend/migrations/` explicit SQL (schema migration + separate seed).
- `backend/tests/` integration + reliability tests against real (embedded) Postgres.
- `frontend/app/sync` page + `frontend/app/api` same-origin proxy.

## Invariants to preserve (do not regress)

- `SUM(receipt.qty) <= issue.qty`, enforced by the `enforce_receipt_qty` BEFORE INSERT
  trigger locking the parent issue. Receipts are immutable.
- Receipt + its single voucher + stored idempotency response commit or roll back
  together (one transaction in `internal/receipts`).
- One idempotency key ↔ one normalized request (fingerprint) ↔ one persisted response.
  Replays are byte-for-byte identical (response stored as `text`, not `jsonb`).
- `client_ref` is stable (`jwr-<receipt_id>`), never rewritten.
- Worker claims with `FOR UPDATE SKIP LOCKED`, commits the claim before any HTTP call,
  and fences every finalizing UPDATE with `claim_token`. Backoff/claims persist in the DB.
- HTTP delivery is at-least-once; the accounting effect is idempotent (dedup on
  `client_ref`). Never claim exactly-once delivery.

## Conventions

- No explanatory comments in source; explanations live in README / AGENT_NOTES.
- Money is integer paise in `BIGINT`; never floating point.
- Keep HTTP handlers thin; validation in `internal/api`, logic in services.

## Verify before done

`gofmt -l .` (clean), `go vet ./...`, `go build ./...`, `go test ./tests/ -count=1`;
frontend `npm run typecheck` and `npm run build`. Review `git diff`; never commit `.env`
or secrets.
