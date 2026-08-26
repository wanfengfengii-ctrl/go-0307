#!/usr/bin/env bash
# Deterministic smoke test for the lyophilizer sterilization validation
# service. It builds the binary, starts it locally, probes its health and a
# real lock/list API flow over localhost, then cleans up every process and
# temporary file. No external network access and no `go test`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKDIR="$(mktemp -d)"
BIN="${WORKDIR}/server"
DB="${WORKDIR}/lyo.db"
PORT="${PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]]; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORKDIR}"
}
trap cleanup EXIT

cd "${ROOT}"

echo "==> building service"
go build -o "${BIN}" .

echo "==> starting service on ${BASE}"
DB_PATH="${DB}" ADDR=":${PORT}" "${BIN}" &
SERVER_PID=$!

# Wait for the service to become ready (bounded, no external network).
ready=0
for _ in $(seq 1 100); do
  if curl -sf "${BASE}/api/v1/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.05
done
if [[ "${ready}" != "1" ]]; then
  echo "service did not become ready" >&2
  exit 1
fi

# Health probe: capture the response and assert on the variable.
health_resp="$(curl -sf "${BASE}/api/v1/health")"
echo "health: ${health_resp}"
[[ "${health_resp}" == *'"status":"ok"'* ]]

# Lock a plan through the public API.
cat > "${WORKDIR}/plan.json" <<'JSON'
{
  "operation_id": "smoke-lock-1",
  "plan": {
    "id": "v1",
    "structure_digest": "",
    "load_digest": "",
    "regions": [
      { "id": "r-chamber", "name": "chamber", "kind": "chamber" },
      { "id": "r-shelf", "name": "shelf", "kind": "shelf" },
      { "id": "r-condenser", "name": "condenser", "kind": "condenser" },
      { "id": "r-drain", "name": "drain", "kind": "drain" }
    ],
    "positions": [
      { "id": "p1", "region_id": "r-chamber", "load_layer": 0 },
      { "id": "p2", "region_id": "r-chamber", "load_layer": 0 },
      { "id": "p3", "region_id": "r-shelf", "load_layer": 1 },
      { "id": "p4", "region_id": "r-shelf", "load_layer": 1 }
    ],
    "probe_summaries": [
      { "probe_id": "probe-1", "position_id": "p1", "certificate": "cert-1" },
      { "probe_id": "probe-2", "position_id": "p2", "certificate": "cert-2" },
      { "probe_id": "probe-3", "position_id": "p3", "certificate": "cert-3" },
      { "probe_id": "probe-4", "position_id": "p4", "certificate": "cert-4" }
    ],
    "exposure": {
      "min_temperature": 121000,
      "min_pressure": 100000,
      "max_pressure": 200000,
      "min_duration": 60000
    },
    "sampling_interval": 1000,
    "lethality_threshold": 1000000
  }
}
JSON

lock_resp="$(curl -sf -X POST "${BASE}/api/v1/validations/lock" \
  -H 'Content-Type: application/json' \
  --data-binary "@${WORKDIR}/plan.json")"
echo "lock: ${lock_resp}"
[[ "${lock_resp}" == *'"generation":1'* ]]
[[ "${lock_resp}" == *'"validation_id":"v1"'* ]]

# List plans and assert the locked plan is returned.
list_resp="$(curl -sf "${BASE}/api/v1/validations")"
echo "validations: ${list_resp}"
[[ "${list_resp}" == *'"id":"v1"'* ]]

# Get the single plan and assert the status is locked.
get_resp="$(curl -sf "${BASE}/api/v1/validations/v1")"
echo "plan v1: ${get_resp}"
[[ "${get_resp}" == *'"status":"locked"'* ]]

echo "==> smoke passed"
