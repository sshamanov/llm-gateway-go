#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"
MODEL="${PROXY_MODEL:-llama3.2}"

echo "=== Smoke: streaming chat ==="

RESP=$(curl -sSf -N -X POST "$BASE_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"$MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": \"Say hello in one word.\"}],
    \"max_tokens\": 20,
    \"stream\": true
  }")

echo "Response: $RESP"

if ! echo "$RESP" | grep -q 'data:'; then
  echo "FAIL: no SSE data lines found"
  exit 1
fi

if ! echo "$RESP" | grep -q 'data: \[DONE\]'; then
  echo "FAIL: no [DONE] sentinel found"
  exit 1
fi

# Extract a chunk and verify it has choices with delta.
CHUNK=$(echo "$RESP" | grep 'data:' | grep -v '\[DONE\]' | head -1 | sed 's/^data: //')
if ! echo "$CHUNK" | grep -q '"choices"'; then
  echo "FAIL: chunk missing choices field"
  exit 1
fi

echo "PASS: streaming chat returned SSE chunks with [DONE] sentinel"
exit 0
