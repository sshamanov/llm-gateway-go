package documents

import "strings"

// ResolveMode normalizes a requested mode string to a Mode constant.
// Empty string or invalid values fall back to the provided defaultMode.
func ResolveMode(requestedMode string, defaultMode Mode) Mode {
	switch requestedMode {
	case string(ModeAuto):
		return ModeAuto
	case string(ModeTextOnly):
		return ModeTextOnly
	case string(ModeVisionPages):
		return ModeVisionPages
	case string(ModeOCR):
		return ModeOCR
	case string(ModeHybrid):
		return ModeHybrid
	default:
		return defaultMode
	}
}

// EnoughText returns true if the extracted text has sufficient content for
// text-only processing (at least MinTextThreshold characters).
func EnoughText(text string) bool {
	return len(strings.TrimSpace(text)) >= MinTextThreshold
}

// DetectAutoMode implements the auto mode decision logic based on file type,
// extracted text availability, vision model support, and OCR status.
// Returns the resolved mode and a method description string.
func DetectAutoMode(fileType FileType, extractedText string, hasVisionModel bool, ocrEnabled bool) (Mode, string) {
	switch fileType {
	case FileTypePNG, FileTypeJPG, FileTypeJPEG, FileTypeWEBP:
		if hasVisionModel {
			return ModeVisionPages, "vision"
		}
		if ocrEnabled {
			return ModeOCR, "ocr"
		}
		return "", ErrNoVisionModel.Error()

	case FileTypePDF:
		if extractedText != "" && EnoughText(extractedText) {
			return ModeTextOnly, "pdf_text_extraction"
		}
		if hasVisionModel {
			return ModeVisionPages, "vision"
		}
		if ocrEnabled {
			return ModeOCR, "ocr"
		}
		return ModeTextOnly, "pdf_text_extraction_sparse"

	case FileTypeTXT, FileTypeMD:
		return ModeTextOnly, "text"

	default:
		return "", ErrUnsupportedFileType.Error()
	}
}
