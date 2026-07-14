#!/usr/bin/env bash
# Local Scheme2 multi-process smoke with SQLite + Memory state.
# Memory is NOT shared across processes — this only checks binaries boot and HTTP surfaces.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export PATH="/c/Program Files/Go/bin:${PATH:-}"

mkdir -p bin data/smoke
CGO_ENABLED=0 go build -o bin/gateway.exe ./cmd/gateway
CGO_ENABLED=0 go build -o bin/worker.exe ./cmd/worker
CGO_ENABLED=0 go build -o bin/controlplane.exe ./cmd/controlplane
CGO_ENABLED=0 go build -o bin/refresher.exe ./cmd/refresher
CGO_ENABLED=0 go build -o bin/seed-sqlite.exe ./cmd/seed-sqlite

DB="data/smoke/pool.db"
rm -f "$DB" "$DB-wal" "$DB-shm"
./bin/seed-sqlite.exe "$DB"

./bin/controlplane.exe --store sqlite --db "$DB" --state memory --workset 100 --shards 8 &
CP_PID=$!
./bin/worker.exe --store sqlite --db "$DB" --state memory --worker-id worker-0 --listen 127.0.0.1:18081 --hot-size 100 --shards 8 &
W_PID=$!
./bin/gateway.exe --listen 127.0.0.1:18080 --workers http://127.0.0.1:18081 --state memory &
G_PID=$!
./bin/refresher.exe --store sqlite --db "$DB" --state memory --enqueue-expiring=false &
R_PID=$!

cleanup() {
  kill $G_PID $W_PID $CP_PID $R_PID 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

echo -n "gateway_healthz="
curl -fsS http://127.0.0.1:18080/healthz
echo
code=$(curl -s -o /tmp/ready.body -w "%{http_code}" http://127.0.0.1:18080/readyz || true)
echo "gateway_readyz=$code $(cat /tmp/ready.body 2>/dev/null || true)"
code2=$(curl -s -o /tmp/wready.body -w "%{http_code}" http://127.0.0.1:18081/readyz || true)
echo "worker_readyz=$code2 $(cat /tmp/wready.body 2>/dev/null || true)"
echo -n "worker_status="
curl -fsS http://127.0.0.1:18081/internal/v1/status || true
echo
echo "smoke done"
