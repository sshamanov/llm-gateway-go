#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"
MODEL="${PROXY_MODEL:-llama3.2}"

echo "=== Smoke: non-streaming chat ==="

RESP=$(curl -sSf -X POST "$BASE_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"$MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": \"Say hello in one word.\"}],
    \"max_tokens\": 20
  }")

echo "Response: $RESP"

ID=$(echo "$RESP" | grep -o '"id":"[^"]*"' | head -1)
CHOICES=$(echo "$RESP" | grep -o '"index":[0-9]' | wc -l)

if [ -z "$ID" ]; then
  echo "FAIL: missing id field"
  exit 1
fi

if [ "$CHOICES" -lt 1 ]; then
  echo "FAIL: no choices returned"
  exit 1
fi

echo "PASS: chat completions returned id=$ID with $CHOICES choice(s)"
exit 0
