package documents

import "errors"

// FileType enumerates supported document file types.
type FileType int

const (
	FileTypeTXT  FileType = iota
	FileTypeMD
	FileTypePDF
	FileTypePNG
	FileTypeJPG
	FileTypeJPEG
	FileTypeWEBP
)

func (ft FileType) String() string {
	switch ft {
	case FileTypeTXT:
		return "txt"
	case FileTypeMD:
		return "md"
	case FileTypePDF:
		return "pdf"
	case FileTypePNG:
		return "png"
	case FileTypeJPG:
		return "jpg"
	case FileTypeJPEG:
		return "jpeg"
	case FileTypeWEBP:
		return "webp"
	default:
		return "unknown"
	}
}

// Mode defines the document processing strategy.
type Mode string

const (
	ModeAuto        Mode = "auto"
	ModeTextOnly    Mode = "text_only"
	ModeVisionPages Mode = "vision_pages"
	ModeOCR         Mode = "ocr"
	ModeHybrid      Mode = "hybrid"
)

// ChunkType indicates what kind of content a chunk carries.
type ChunkType int

const (
	ChunkTypeText  ChunkType = iota
	ChunkTypeImage
)

// Chunk is a single processable segment of a document.
type Chunk struct {
	Index     int       `json:"index"`
	Type      ChunkType `json:"type"`
	Text      string    `json:"text,omitempty"`
	ImageData []byte    `json:"image_data,omitempty"`
	PageNum   int       `json:"page_num"`
}

// ProcessRequest carries the parsed inputs for a document processing request.
type ProcessRequest struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Mode     Mode   `json:"mode"`
	FilePath string `json:"file_path"`
	FileType FileType
	FileName string `json:"file_name"`
}

// ProcessResponse is the JSON response returned to the client.
type ProcessResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Model   string         `json:"model"`
	Content string         `json:"content"`
	Pages   int            `json:"pages"`
	Chunks  int            `json:"chunks"`
	Mode    string         `json:"mode"`
	Method  string         `json:"method"`
	Usage   map[string]int `json:"usage"`
}

// PrepareTask is submitted to the preparation queue.
type PrepareTask struct {
	ID             string
	FilePath       string
	FileType       FileType
	Mode           Mode
	HasVisionModel bool
	ResultCh       chan *PrepareResult
}

// PrepareResult holds the output of document preparation.
type PrepareResult struct {
	Chunks          []Chunk
	ResolvedMode    Mode
	Method          string
	PageImagePaths  []string
	Err             error
}

// Errors.
var (
	ErrUnsupportedFileType = errors.New("unsupported file type")
	ErrEmptyFile           = errors.New("empty file")
	ErrPDFNoText           = errors.New("no extractable text found in PDF")
	ErrDocumentTooLarge    = errors.New("document exceeds maximum size")
	ErrEncryptedPDF        = errors.New("encrypted PDFs are not supported")
	ErrNoExtractionMethod  = errors.New("no extraction method available for this document")
	ErrNoVisionModel       = errors.New("no vision model available")
	ErrOCRNotEnabled       = errors.New("OCR is not enabled")
)

// Constants.
const (
	DefaultChunkSize   = 4000
	MinTextThreshold   = 100
	DefaultMaxBytes    = 50 * 1024 * 1024 // 50MB
)

// supportedExtensions maps lowercase extensions (without dot) to FileType.
var SupportedExtensions = map[string]FileType{
	"txt":  FileTypeTXT,
	"md":   FileTypeMD,
	"pdf":  FileTypePDF,
	"png":  FileTypePNG,
	"jpg":  FileTypeJPG,
	"jpeg": FileTypeJPEG,
	"webp": FileTypeWEBP,
}
