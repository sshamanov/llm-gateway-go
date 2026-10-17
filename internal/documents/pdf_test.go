package documents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPDF builds a one-page PDF with a correct classic xref table.
func minimalPDF() []byte {
	objs := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>\nendobj\n",
		"4 0 obj\n<< /Length 30 >>\nstream\nBT (Hello PDF) Tj ET\nendstream\nendobj\n",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		b.WriteString(o)
	}
	xrefPos := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefPos)
	return []byte(b.String())
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractPDFText_Minimal(t *testing.T) {
	pages, err := ExtractPDFText(writeTemp(t, minimalPDF()))
	if err != nil {
		t.Fatalf("ExtractPDFText: %v", err)
	}
	if len(pages) != 1 || !strings.Contains(pages[0], "Hello PDF") {
		t.Fatalf("pages = %q, want one page containing Hello PDF", pages)
	}
}

// TestParseXref_OffsetOutOfRange checks that a startxref value pointing
// outside the file is rejected without panicking.
func TestParseXref_OffsetOutOfRange(t *testing.T) {
	for _, off := range []string{"999999999", "-5"} {
		data := []byte("%PDF-1.4\ntrailer\n<< /Root 1 0 R >>\nstartxref\n" + off + "\n%%EOF\n")
		if _, err := parseXref(data); err == nil {
			t.Errorf("startxref %s: expected error", off)
		}
	}
}

// TestExtractPDFText_TruncatedNeverPanics feeds every prefix of a valid PDF
// to the extractor; each must return a result or an error, never crash.
func TestExtractPDFText_TruncatedNeverPanics(t *testing.T) {
	full := minimalPDF()
	for n := 1; n < len(full); n++ {
		_, _ = ExtractPDFText(writeTemp(t, full[:n]))
	}
}

func TestParseXrefStream_RejectsBadWidths(t *testing.T) {
	data := []byte("1 0 obj\n<< /Type /XRef /Filter /FlateDecode /W [1 -2 1] /Size 1 /Length 1 >>\nstream\nx\nendstream\nendobj\n")
	err := parseXrefStream(data, 0, map[int]int64{})
	if err == nil {
		t.Fatal("expected error for negative /W width")
	}
}
