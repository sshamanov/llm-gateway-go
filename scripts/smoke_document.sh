#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PROXY_BASE_URL:-http://localhost:4000}"
MODEL="${PROXY_MODEL:-llama3.2}"

echo "=== Smoke: Document Processing ==="

TMPFILE=$(mktemp -t smokepdf.XXXXXX.pdf)
trap 'rm -f "$TMPFILE"' EXIT

# Create a minimal valid PDF.
cat > "$TMPFILE" <<'PDFEOF'
%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]
   /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 44 >>
stream
BT /F1 12 Tf 100 700 Td (Hello World) Tj ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
xref
0 6
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000266 00000 n
0000000360 00000 n
trailer
<< /Size 6 /Root 1 0 R >>
startxref
439
%%EOF
PDFEOF

RESP=$(curl -sSf -X POST "$BASE_URL/proxy/documents/process" \
  -F "file=@$TMPFILE;type=application/pdf" \
  -F "model=$MODEL" \
  -F "mode=text")

echo "Response: $RESP"

# Accept 200 or 422 (no text extracted) as both are valid paths.
STATUS=$(echo "$RESP" | grep -o '"status":[0-9]*' | head -1 || echo "no status")
if echo "$STATUS" | grep -q "200\|422"; then
  echo "PASS: document processing returned valid response"
  exit 0
fi

echo "FAIL: unexpected response: $RESP"
exit 1
