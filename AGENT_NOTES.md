# Agent Notes

## AI tools used

- **Claude Code** (model Opus 4.8) was the only coding agent. It did the entire
  implementation in one session: workspace inspection, schema/migrations, the Go
  backend (API, receipts/idempotency, mock accounting, worker), the Next.js `/sync`
  page, the test suite, and all documentation. No other AI tools or human-written code
  were involved.

## What was delegated to the agent

Everything in this repository. There is no human-authored code to distinguish; the
agent wrote, compiled, and tested all of it.

## Key design decisions

- **Quantity invariant via a `BEFORE INSERT` trigger** that locks the parent issue
  (`SELECT ... FOR UPDATE`) and checks `SUM + new <= issued`. Chosen over an
  application-only check so direct inserts also obey it, and over a CHECK constraint
  because a cross-row SUM cannot be enforced by CHECK. Receipts are made immutable (a
  second trigger blocks UPDATE/DELETE) so there is exactly one enforcement point.
- **Idempotency** uses `INSERT ... ON CONFLICT (key) DO NOTHING RETURNING key` inside
  the same transaction that writes the receipt, voucher, and stored response. The DB
  conflict both serializes concurrent same-key requests and ensures a rolled-back
  request never consumes the key.
- **`attempt_baseline`** separates lifetime `attempts` from the current retry budget,
  so a manual retry preserves history while resetting the budget.
- **Server-owned context in the mock's 5-second path**: the delayed commit runs on a
  `context.Background()` with its own timeout, so a client timeout or worker kill does
  not cancel the simulated remote posting — modelling a genuinely uncertain outcome.
- **Fencing**: every finalizing UPDATE carries `AND claim_token = $token` and checks
  the row count, so an expired/stale worker cannot overwrite a newer one.

## What generated output needed correction

These were all caught and fixed during this session:

1. **Concrete bug caught by a test — idempotency replay was not byte-identical.**
   The first implementation stored the original response in a `jsonb` column. PostgreSQL
   re-serializes `jsonb` (reordered keys, normalized whitespace), so the replayed bytes
   differed from the original 201 body. `TestIdempotentReplay` (and
   `TestConcurrentSameKey`) failed with "replay body differs". Fix: store the response
   in a `text` column and return the exact stored bytes, so replays are byte-for-byte
   identical. This is exactly the kind of guarantee the mandatory replay test exists to
   protect, and the test caught it immediately.

2. **Package name collision**, caught at review before compiling: the mock imported
   both `crypto/rand` and `math/rand/v2`, which both bind to `rand`. Aliased
   `crypto/rand` as `crand`.

3. **A no-op self-assignment** (`acquired = acquired`) left in an early version of the
   worker's slot-acquisition loop; replaced with a labeled `break` before `go vet`.

## Test meaningfulness (mutation verification)

Per the brief, both mandatory tests were proven to fail when their protection is
removed, then restored:

- Overshoot trigger disabled → `TestConcurrentOvershoot` failed with `10 created / 0
  conflict` (all requests overshot) instead of `5 / 5`. Restored.
- Replay reservation changed to `ON CONFLICT DO UPDATE` (always treat key as new) →
  `TestIdempotentReplay` failed (second insert hit the receipt unique key, returned
  500). Restored.

No deliberately broken code remains on the branch; the full suite is green after
restoring both.

## Tests actually executed (real embedded PostgreSQL 18.3)

All 16 tests pass (`go test ./tests/ -count=1`):

- Integration: `TestConcurrentOvershoot`, `TestIdempotentReplay`,
  `TestConcurrentSameKey`, `TestSameKeyDifferentPayload`,
  `TestOvershootDoesNotConsumeKey`, `TestInvalidInputs`, `TestUnknownIssue`,
  `TestSmokeDatabase`.
- Reliability: `TestReliabilityNormalSuccess`, `TestReliabilityBackoffThenFailure`,
  `TestReliabilityManualRetry`, `TestReliabilityTimeoutThenReconcile`,
  `TestReliabilityRealWorkerCrash` (real subprocess, killed and restarted),
  `TestReliabilityTwoWorkers`, `TestReliabilitySlowCallIsolation`,
  `TestReliabilityStaleClaimFencing`.

Also run and passing: `gofmt -l` (clean), `go vet ./...`, `go build ./...`, frontend
`npm run typecheck` and `npm run build` (Next lint runs during build).

## Remaining limitations

- **Neon unverified.** No Neon credentials were present in the build environment. The
  system was verified against real PostgreSQL (embedded 18.3 for tests; a local Postgres
  URL also works). The README documents exact Neon setup. Nothing else was silently
  substituted as "Neon".
- **`go test -race` not run.** The race detector needs cgo and a C compiler, which were
  not installed. The concurrency tests pass without it.
- **No live curl smoke against standalone servers.** Running `cmd/api` / `cmd/worker` /
  `cmd/mock-accounting` requires a persistent `DATABASE_URL`, and there was no
  persistent Postgres (no local psql/Docker, no Neon) in the environment — only the
  disposable embedded Postgres used by the test harness. The full HTTP stack is
  exercised end-to-end by the integration tests (real API handler + real DB) and the
  reliability tests (real worker/mock over HTTP), so the behaviour is verified; the
  manual curl walkthrough in the README is written but was not executed here.
- **Screen recording not captured** (no screen-capture tooling available); see
  `RECORDING_SCRIPT.md`, marked as not yet recorded.

## Optional LLM feature (`/ask`)

**Omitted.** Mandatory correctness and reliability work was prioritized within the time
budget. A read-only, parameterized helper (`OutstandingForSKUParty`) exists in the
store layer as a safe foundation, but no `/ask` endpoint or model integration was built.

## Approximate time

Single continuous agent session. The largest single sinks were the real-Postgres test
harness (embedded Postgres startup ~20–25s per run) and iterating the reliability tests
(the subprocess crash test runs ~9s).
