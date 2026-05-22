package documents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// RenderPDFPages renders all pages of a PDF to PNG images in outputDir.
// Tries pdftoppm first, falls back to gs (ghostscript).
// Returns sorted page image paths like ["/tmp/doc/pages/page-1.png", ...].
func RenderPDFPages(pdfPath, outputDir string) ([]string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := exec.LookPath("pdftoppm"); err == nil {
		prefix := filepath.Join(outputDir, "page")
		cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", pdfPath, prefix)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("pdftoppm failed: %w\noutput: %s", err, string(out))
		}
	} else if _, err := exec.LookPath("gs"); err == nil {
		outPattern := fmt.Sprintf("-sOutputFile=%s/page-%%d.png", outputDir)
		cmd := exec.CommandContext(ctx, "gs", "-dNOPAUSE", "-dBATCH", "-dQUIET", "-sDEVICE=png16m", "-r150", outPattern, pdfPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("gs failed: %w\noutput: %s", err, string(out))
		}
	} else {
		return nil, fmt.Errorf("no PDF rendering tool available: tried pdftoppm and gs (ghostscript)")
	}

	paths, err := filepath.Glob(filepath.Join(outputDir, "page*.png"))
	if err != nil {
		return nil, fmt.Errorf("globbing page images: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no page images were generated in %s", outputDir)
	}

	sort.Strings(paths)
	return paths, nil
}

// RenderPDFPage renders a single page of a PDF to PNG.
func RenderPDFPage(pdfPath, outputDir string, pageNum int) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("creating output directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pageArg := fmt.Sprintf("%d", pageNum)
	outPath := filepath.Join(outputDir, fmt.Sprintf("page-%d.png", pageNum))

	if _, err := exec.LookPath("pdftoppm"); err == nil {
		prefix := filepath.Join(outputDir, "page")
		cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", "-f", pageArg, "-l", pageArg, pdfPath, prefix)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("pdftoppm failed: %w\noutput: %s", err, string(out))
		}
	} else if _, err := exec.LookPath("gs"); err == nil {
		cmd := exec.CommandContext(ctx, "gs", "-dNOPAUSE", "-dBATCH", "-dQUIET", "-sDEVICE=png16m", "-r150",
			fmt.Sprintf("-dFirstPage=%d", pageNum),
			fmt.Sprintf("-dLastPage=%d", pageNum),
			fmt.Sprintf("-sOutputFile=%s", outPath),
			pdfPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("gs failed: %w\noutput: %s", err, string(out))
		}
	} else {
		return "", fmt.Errorf("no PDF rendering tool available: tried pdftoppm and gs (ghostscript)")
	}

	if _, err := os.Stat(outPath); err != nil {
		return "", fmt.Errorf("rendered page not found: %w", err)
	}

	return outPath, nil
}
