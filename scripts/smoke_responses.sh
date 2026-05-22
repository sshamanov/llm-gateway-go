#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"
MODEL="${PROXY_MODEL:-llama3.2}"

echo "=== Smoke: Responses API ==="

# Non-streaming
RESP=$(curl -sSf -X POST "$BASE_URL/v1/responses" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"$MODEL\",
    \"input\": \"Say hello in one word.\",
    \"max_output_tokens\": 20
  }")

echo "Response: $RESP"

if ! echo "$RESP" | grep -q '"id":"resp_'; then
  echo "FAIL: missing or invalid id field"
  exit 1
fi

if ! echo "$RESP" | grep -q '"output"'; then
  echo "FAIL: missing output field"
  exit 1
fi

if ! echo "$RESP" | grep -q '"object":"response"'; then
  echo "FAIL: missing object field"
  exit 1
fi

echo "PASS: Responses API returned valid response"
exit 0
