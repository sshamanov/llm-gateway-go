package documents

import (
	"testing"
)

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name         string
		requested    string
		defaultMode  Mode
		want         Mode
	}{
		{"auto", "auto", ModeTextOnly, ModeAuto},
		{"text_only", "text_only", ModeAuto, ModeTextOnly},
		{"vision_pages", "vision_pages", ModeAuto, ModeVisionPages},
		{"ocr", "ocr", ModeAuto, ModeOCR},
		{"hybrid", "hybrid", ModeAuto, ModeHybrid},
		{"empty falls back to default", "", ModeTextOnly, ModeTextOnly},
		{"invalid falls back to default", "bogus", ModeAuto, ModeAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveMode(tt.requested, tt.defaultMode)
			if got != tt.want {
				t.Errorf("ResolveMode(%q, %q) = %q, want %q",
					tt.requested, tt.defaultMode, got, tt.want)
			}
		})
	}
}

func TestEnoughText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"above threshold", makeString(200), true},
		{"exactly at threshold", makeString(MinTextThreshold), true},
		{"below threshold", makeString(MinTextThreshold - 1), false},
		{"empty string", "", false},
		{"whitespace only", "   \t\n  ", false},
		{"just above with leading spaces", "  " + makeString(MinTextThreshold), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnoughText(tt.text)
			if got != tt.want {
				t.Errorf("EnoughText(len=%d) = %v, want %v", len(tt.text), got, tt.want)
			}
		})
	}
}

func TestDetectAutoMode(t *testing.T) {
	enoughText := makeString(MinTextThreshold)
	sparseText := "short"

	tests := []struct {
		name           string
		fileType       FileType
		extractedText  string
		hasVisionModel bool
		ocrEnabled     bool
		wantMode       Mode
		wantMethod     string
		wantError      bool
	}{
		// --- Image files ---
		{
			name: "png with vision", fileType: FileTypePNG,
			hasVisionModel: true, ocrEnabled: false,
			wantMode: ModeVisionPages, wantMethod: "vision",
		},
		{
			name: "png with ocr no vision", fileType: FileTypePNG,
			hasVisionModel: false, ocrEnabled: true,
			wantMode: ModeOCR, wantMethod: "ocr",
		},
		{
			name: "png no vision no ocr", fileType: FileTypePNG,
			hasVisionModel: false, ocrEnabled: false,
			wantMode: "", wantMethod: ErrNoVisionModel.Error(), wantError: true,
		},
		{
			name: "jpg with vision", fileType: FileTypeJPG,
			hasVisionModel: true, ocrEnabled: false,
			wantMode: ModeVisionPages, wantMethod: "vision",
		},
		{
			name: "webp with vision", fileType: FileTypeWEBP,
			hasVisionModel: true, ocrEnabled: false,
			wantMode: ModeVisionPages, wantMethod: "vision",
		},

		// --- PDF files ---
		{
			name: "pdf with enough text", fileType: FileTypePDF,
			extractedText: enoughText, hasVisionModel: true, ocrEnabled: true,
			wantMode: ModeTextOnly, wantMethod: "pdf_text_extraction",
		},
		{
			name: "pdf sparse text with vision", fileType: FileTypePDF,
			extractedText: sparseText, hasVisionModel: true, ocrEnabled: false,
			wantMode: ModeVisionPages, wantMethod: "vision",
		},
		{
			name: "pdf sparse text with ocr no vision", fileType: FileTypePDF,
			extractedText: sparseText, hasVisionModel: false, ocrEnabled: true,
			wantMode: ModeOCR, wantMethod: "ocr",
		},
		{
			name: "pdf sparse text no vision no ocr", fileType: FileTypePDF,
			extractedText: sparseText, hasVisionModel: false, ocrEnabled: false,
			wantMode: ModeTextOnly, wantMethod: "pdf_text_extraction_sparse",
		},
		{
			name: "pdf empty text with vision", fileType: FileTypePDF,
			extractedText: "", hasVisionModel: true, ocrEnabled: false,
			wantMode: ModeVisionPages, wantMethod: "vision",
		},

		// --- Text files ---
		{
			name: "txt file", fileType: FileTypeTXT,
			wantMode: ModeTextOnly, wantMethod: "text",
		},
		{
			name: "md file", fileType: FileTypeMD,
			wantMode: ModeTextOnly, wantMethod: "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotMethod := DetectAutoMode(tt.fileType, tt.extractedText, tt.hasVisionModel, tt.ocrEnabled)
			if gotMode != tt.wantMode {
				t.Errorf("mode = %q, want %q", gotMode, tt.wantMode)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}
			if tt.wantError && gotMode != "" {
				t.Errorf("expected error return (empty mode), got mode=%q", gotMode)
			}
		})
	}
}

func makeString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
