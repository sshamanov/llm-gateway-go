package documents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

// DetectFileType maps a filename extension (case-insensitive) to FileType.
// Returns ErrUnsupportedFileType for unknown extensions.
func DetectFileType(filename string) (FileType, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	ft, ok := SupportedExtensions[ext]
	if !ok {
		return 0, ErrUnsupportedFileType
	}
	return ft, nil
}

// SpoolMultipartFile reads a multipart file part and writes it to disk under
// docDir. The file is named {randomHex}{originalExtension}. Returns the full
// path to the spooled file. Limits read to DefaultMaxBytes.
func SpoolMultipartFile(file multipart.File, header *multipart.FileHeader, docDir string) (string, error) {
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return "", fmt.Errorf("generating random filename: %w", err)
	}
	name := hex.EncodeToString(randBytes) + filepath.Ext(header.Filename)
	dstPath := filepath.Join(docDir, name)

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", fmt.Errorf("creating spool file: %w", err)
	}
	defer dst.Close()

	written, err := io.CopyN(dst, file, DefaultMaxBytes)
	if err != nil && err != io.EOF {
		dst.Close()
		os.Remove(dstPath)
		return "", fmt.Errorf("copying file data: %w", err)
	}

	if written == DefaultMaxBytes {
		var buf [1]byte
		_, readErr := file.Read(buf[:])
		if readErr == nil {
			// More data remains — document exceeds the limit.
			dst.Close()
			os.Remove(dstPath)
			return "", ErrDocumentTooLarge
		}
	}

	return dstPath, nil
}

// SpoolBase64Data writes raw bytes to disk under docDir with the given
// extension. Returns the full path.
func SpoolBase64Data(data []byte, docDir, ext string) (string, error) {
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return "", fmt.Errorf("generating random filename: %w", err)
	}
	name := hex.EncodeToString(randBytes) + ext
	dstPath := filepath.Join(docDir, name)

	if err := os.WriteFile(dstPath, data, 0644); err != nil {
		return "", fmt.Errorf("writing spool file: %w", err)
	}

	return dstPath, nil
}

// ValidateFileSize returns ErrDocumentTooLarge if the file at path exceeds maxBytes.
func ValidateFileSize(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > maxBytes {
		return ErrDocumentTooLarge
	}
	return nil
}
