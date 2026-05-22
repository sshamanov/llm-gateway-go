#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"

echo "=== Smoke: Metrics ==="

RESP=$(curl -sSf "$BASE_URL/metrics")

echo "Response (first 20 lines):"
echo "$RESP" | head -20

if ! echo "$RESP" | grep -q "proxy_uptime_seconds"; then
  echo "FAIL: missing proxy_uptime_seconds metric"
  exit 1
fi

CT=$(echo "$RESP" | grep -c "^# HELP" || echo 0)
echo "Metric families found: $CT"
if [ "$CT" -lt 1 ]; then
  echo "FAIL: no metric families found"
  exit 1
fi

echo "PASS: /metrics returned $CT metric families"

echo "=== Smoke: Debug UI ==="

UI_RESP=$(curl -sSf "$BASE_URL/debug/")
if echo "$UI_RESP" | grep -qi '<html\|<!DOCTYPE'; then
  echo "PASS: /debug/ returned HTML dashboard"
else
  echo "FAIL: /debug/ did not return HTML"
  exit 1
fi

echo "=== Smoke: Debug Config ==="

CFG_RESP=$(curl -sSf "$BASE_URL/debug/config")
if echo "$CFG_RESP" | grep -q '"config"'; then
  echo "PASS: /debug/config returned config JSON"
else
  echo "FAIL: /debug/config did not return expected JSON"
  exit 1
fi

echo "=== Smoke: Health ==="

HEALTH=$(curl -sSf "$BASE_URL/healthz")
if echo "$HEALTH" | grep -q '"ok"'; then
  echo "PASS: /healthz returned ok"
else
  echo "FAIL: /healthz returned unexpected response"
  exit 1
fi

exit 0
