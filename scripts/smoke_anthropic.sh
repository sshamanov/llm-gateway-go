#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"
MODEL="${PROXY_MODEL:-llama3.2}"

echo "=== Smoke: Anthropic Messages API ==="

# Non-streaming
RESP=$(curl -sSf -X POST "$BASE_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d "{
    \"model\": \"$MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": [{\"type\": \"text\", \"text\": \"Say hello in one word.\"}]}],
    \"max_tokens\": 30
  }")

echo "Response: $RESP"

if ! echo "$RESP" | grep -q '"id":"msg_'; then
  echo "FAIL: missing or invalid id field"
  exit 1
fi

if ! echo "$RESP" | grep -q '"type":"message"'; then
  echo "FAIL: missing type field"
  exit 1
fi

if ! echo "$RESP" | grep -q '"content"'; then
  echo "FAIL: missing content field"
  exit 1
fi

echo "PASS: Messages API returned valid response"

echo "=== Smoke: Count Tokens ==="

CT_RESP=$(curl -sSf -X POST "$BASE_URL/v1/messages/count_tokens" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d "{
    \"model\": \"$MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": [{\"type\": \"text\", \"text\": \"Count these tokens.\"}]}]
  }")

echo "Response: $CT_RESP"

if ! echo "$CT_RESP" | grep -q '"input_tokens"'; then
  echo "FAIL: missing input_tokens field"
  exit 1
fi

echo "PASS: Count tokens returned valid response"
exit 0
