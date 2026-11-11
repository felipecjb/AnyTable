#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
export DISPLAY="${DISPLAY:-:0}"

cd "${REPO_ROOT}"
echo "[anytable-debug] Building anytable-server (CGO_ENABLED=0)..."
CGO_ENABLED=0 go build -trimpath -o /tmp/anytable-server ./server/src

echo "[anytable-debug] Starting server on DISPLAY=${DISPLAY} with config server/config/anytable.conf..."
exec /tmp/anytable-server --config "${REPO_ROOT}/server/config/anytable.conf" --display "${DISPLAY}" "$@"
