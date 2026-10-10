# Development plan

Status snapshot for the assessment build. All mandatory items are complete and verified.

## Done

- [x] Workspace inspection + toolchain capture (Go 1.26, Node 24, Postgres 18.3 embedded).
- [x] Schema + triggers (`0001_init.sql`) and repeatable seed (`0002_seed.sql`).
- [x] Migration/seed runner (`cmd/migrate`).
- [x] Receipts endpoint with DB-backed idempotency and one-transaction receipt+voucher.
- [x] Remaining endpoints: `GET /issues/{id}`, `GET /vouchers`, `POST /vouchers/{id}/retry`, `GET /healthz`.
- [x] Mock accounting server: durable dedup on `client_ref`, random/success/fail/delay
      modes, server-owned context for the 5s path.
- [x] Sync worker: `FOR UPDATE SKIP LOCKED` claims, leases, claim-token fencing, capped
      jittered backoff, bounded concurrency, graceful shutdown.
- [x] Next.js `/sync` page with three groups, polling, retry, loading/empty/error states,
      same-origin server-side proxy.
- [x] Integration tests (mandatory concurrent-overshoot + idempotency-replay + focused
      coverage) against real embedded Postgres.
- [x] Reliability tests A–H including a real subprocess worker-crash + restart.
- [x] Mutation verification of both mandatory tests (fail-when-broken, then restored).
- [x] Verification: gofmt, vet, build, tests; frontend typecheck + build.
- [x] Docs: README, AGENT_NOTES, RECORDING_SCRIPT, .env.example, .gitignore, this plan.

## Done (follow-up)

- [x] Neon live verification — migrate+seed and full end-to-end flow run against the
      live Neon database (pooled endpoint + channel_binding).
- [x] CORS on the API (configurable via `CORS_ALLOWED_ORIGINS`) + `.env` autoloading.
- [x] Optional `POST /ask` LLM endpoint — read-only DB tools, each call logged, model
      stubbed (no API key) and reported as such; covered by `TestAskOutstanding`.

## Not done (blocked)

- [ ] `go test -race` — no cgo/C toolchain available.
- [ ] Screen recording — no capture tooling; script prepared and marked pending.

## If continued

- Run migrations + a smoke pass against a real Neon database and record the result.
- Add a GitHub Actions workflow (lint/vet/test/build) and a Dockerfile/compose for the
  three Go services.
- Implement `/ask` with read-only, parameterized tools over the existing store helpers.
