#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="${ROOT}/test/standalone/var/lib/cofiswarm/dispatch"
BIN="${ROOT}/bin/cofiswarm-dispatch"
PORT=18010
[[ -x "$BIN" ]] || { echo "missing $BIN — run make build"; exit 1; }
"$BIN" -listen ":${PORT}" -state "${STATE}" &
PID=$!
trap 'kill $PID 2>/dev/null || true' EXIT
sleep 1
OUT=$(mktemp)
curl -sN -X POST "http://127.0.0.1:${PORT}/api/architect/stream" \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"hello world"}' > "$OUT"
grep -q 'event: session' "$OUT"
grep -q 'event: token' "$OUT"
grep -q 'event: agent_done' "$OUT"
grep -q 'event: metrics' "$OUT"
grep -q 'event: done' "$OUT"
grep -q 'data: \[DONE\]' "$OUT"
SESSIONS="${STATE}/sessions/sessions.json"
[[ -f "$SESSIONS" ]] || { echo "sessions not persisted: $SESSIONS"; exit 1; }
python3 -c "import json; d=json.load(open('$SESSIONS')); assert len(d)>=1"
echo "ok: SSE gate + session persistence"
