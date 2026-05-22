package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"llm-go-proxy/internal/mock"
)

// minimalPDF is the smallest possible valid PDF file.
const minimalPDF = `%PDF-1.0
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Count 1/Kids[3 0 R]>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Resources<<>>>>endobj
xref
0 4
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
trailer<</Size 4/Root 1 0 R>>
startxref
206
%%EOF
`

// DocumentProcessing uploads a minimal PDF and verifies the response.
func DocumentProcessing(h *mock.Harness) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.WriteString(part, minimalPDF); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}

	if err := w.WriteField("model", "llama3"); err != nil {
		return fmt.Errorf("write field: %w", err)
	}
	w.Close()

	req, err := http.NewRequest(http.MethodPost, h.ProxyURL+"/proxy/documents/process", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// May return 200 (success) or 422 (no extractable text) — both are valid.
	if resp.StatusCode != 200 && resp.StatusCode != 422 {
		return fmt.Errorf("document: expected 200 or 422, got %d (body: %s)", resp.StatusCode, string(body))
	}

	if resp.StatusCode == 200 {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return fmt.Errorf("document: unmarshal: %w", err)
		}
		if data["id"] == nil {
			return fmt.Errorf("document: missing 'id' field")
		}
	}

	return nil
}
