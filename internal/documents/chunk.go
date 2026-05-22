package documents

import (
	"fmt"
	"os"
	"strings"
)

// ChunkText splits text into paragraph-aware chunks of at most maxSize characters.
// Splits on paragraph boundaries (\n\n) when possible. If a single paragraph
// exceeds maxSize, it is hard-split at maxSize (preferring word boundaries).
// Each chunk gets ChunkTypeText.
func ChunkText(text string, maxSize int) []Chunk {
	if text == "" {
		return nil
	}

	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var acc strings.Builder
	acc.Grow(maxSize)

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		addLen := len(p)
		if acc.Len() > 0 {
			addLen += 2 // "\n\n" separator
		}

		if acc.Len()+addLen <= maxSize {
			if acc.Len() > 0 {
				acc.WriteString("\n\n")
			}
			acc.WriteString(p)
			continue
		}

		// Adding this paragraph would exceed maxSize.
		if acc.Len() > 0 {
			chunks = append(chunks, Chunk{
				Index:   len(chunks),
				Type:    ChunkTypeText,
				Text:    acc.String(),
				PageNum: 0,
			})
			acc.Reset()
			acc.Grow(maxSize)
		}

		// Handle the oversized paragraph.
		if len(p) <= maxSize {
			acc.WriteString(p)
		} else {
			remaining := p
			for len(remaining) > maxSize {
				splitAt := maxSize
				if idx := strings.LastIndex(remaining[:maxSize], " "); idx > 0 {
					splitAt = idx
				}
				chunks = append(chunks, Chunk{
					Index:   len(chunks),
					Type:    ChunkTypeText,
					Text:    remaining[:splitAt],
					PageNum: 0,
				})
				remaining = strings.TrimLeft(remaining[splitAt:], " ")
			}
			if remaining != "" {
				acc.WriteString(remaining)
			}
		}
	}

	if acc.Len() > 0 {
		chunks = append(chunks, Chunk{
			Index:   len(chunks),
			Type:    ChunkTypeText,
			Text:    acc.String(),
			PageNum: 0,
		})
	}

	return chunks
}

// ChunkPages creates one Chunk per page image. Each chunk carries the raw PNG
// bytes read from the file path and gets ChunkTypeImage.
func ChunkPages(pageImagePaths []string) ([]Chunk, error) {
	chunks := make([]Chunk, 0, len(pageImagePaths))
	for i, path := range pageImagePaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read page image %q: %w", path, err)
		}
		chunks = append(chunks, Chunk{
			Index:     i,
			Type:      ChunkTypeImage,
			ImageData: data,
			PageNum:   i + 1,
		})
	}
	return chunks, nil
}
