#!/usr/bin/env bash
set -euo pipefail

echo "[entrypoint] applying migrations + seed"
/app/migrate all

echo "[entrypoint] starting mock-accounting on :${MOCK_PORT:-8090} (internal)"
/app/mock-accounting &

echo "[entrypoint] starting worker"
/app/worker &

echo "[entrypoint] starting api on :${PORT:-8080}"
/app/api &

# Exit (and let the platform restart the container) if any process dies.
wait -n
echo "[entrypoint] a backend process exited; stopping container"
exit 1
