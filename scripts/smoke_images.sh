#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"

echo "=== Smoke: Image Generation ==="

RESP=$(curl -sSf -X POST "$BASE_URL/v1/images/generations" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A blue sky",
    "n": 1
  }')

echo "Response: $RESP"

if ! echo "$RESP" | grep -q '"created"'; then
  echo "FAIL: missing created field"
  exit 1
fi

if ! echo "$RESP" | grep -q '"data"'; then
  echo "FAIL: missing data field"
  exit 1
fi

echo "PASS: image generation returned valid response"
exit 0
