package documents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrQueueStopped is returned by Submit when the queue has been stopped.
var ErrQueueStopped = fmt.Errorf("prepare queue: stopped")

// PrepareQueue is a fixed-size goroutine pool for CPU-bound document
// preparation tasks. Tasks are submitted via Submit, dequeued and processed
// by worker goroutines, and results are delivered on the task's ResultCh.
type PrepareQueue struct {
	tasks   chan *PrepareTask
	stopped chan struct{}
	workers int
	once    sync.Once
	wg      sync.WaitGroup
}

// NewPrepareQueue creates a new prepare queue. If workers <= 0 it defaults to
// 1. The internal task channel is buffered to 100 entries.
//
// The caller is expected to supply e.g. cfg.PreparationWorkers for workers.
func NewPrepareQueue(workers int) *PrepareQueue {
	if workers <= 0 {
		workers = 1
	}
	return &PrepareQueue{
		tasks:   make(chan *PrepareTask, 100),
		stopped: make(chan struct{}),
		workers: workers,
	}
}

// Start launches the worker goroutines.
func (q *PrepareQueue) Start() {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
}

// Stop gracefully shuts down the queue. It closes the task channel so that
// workers finish their remaining tasks, then waits for all workers to exit.
// Subsequent calls to Submit return ErrQueueStopped. Safe to call multiple
// times.
func (q *PrepareQueue) Stop() {
	q.once.Do(func() {
		close(q.stopped)
		close(q.tasks)
	})
	q.wg.Wait()
}

// Submit enqueues a task for preparation. Returns ErrQueueStopped if the
// queue has already been stopped. Blocks if the internal buffer is full
// (backpressure).
func (q *PrepareQueue) Submit(task *PrepareTask) error {
	select {
	case <-q.stopped:
		return ErrQueueStopped
	case q.tasks <- task:
		return nil
	}
}

// worker is the inner loop shared by every worker goroutine.
func (q *PrepareQueue) worker() {
	defer q.wg.Done()
	for task := range q.tasks {
		result := q.processTask(task)
		task.ResultCh <- result
	}
}

// processTask dispatches to the appropriate handler based on file type.
func (q *PrepareQueue) processTask(task *PrepareTask) *PrepareResult {
	switch task.FileType {
	case FileTypePDF:
		return q.processPDF(task)
	case FileTypeTXT, FileTypeMD:
		return q.processTextFile(task)
	case FileTypePNG, FileTypeJPG, FileTypeJPEG, FileTypeWEBP:
		return q.processImage(task)
	default:
		return &PrepareResult{Err: ErrUnsupportedFileType}
	}
}

// --------------------------------------------------------------------------
// PDF preparation pipeline
// --------------------------------------------------------------------------

func (q *PrepareQueue) processPDF(task *PrepareTask) *PrepareResult {
	// 1. Extract per-page text.
	pageTexts, extractErr := ExtractPDFText(task.FilePath)

	var combinedText string
	if extractErr == nil {
		combinedText = strings.TrimSpace(strings.Join(pageTexts, "\n"))
	} else if !errors.Is(extractErr, ErrPDFNoText) {
		return &PrepareResult{Err: fmt.Errorf("extract text: %w", extractErr)}
	}

	// 2. Resolve mode (auto-detect if ModeAuto or empty).
	mode := task.Mode
	method := ""
	if mode == ModeAuto || mode == "" {
		mode, method = DetectAutoMode(FileTypePDF, combinedText, task.HasVisionModel, false)
	}

	// 3. Execute according to the resolved mode.
	switch mode {
	case ModeTextOnly:
		if combinedText == "" {
			return &PrepareResult{Err: ErrPDFNoText}
		}
		chunks := ChunkText(combinedText, DefaultChunkSize)
		if method == "" {
			if EnoughText(combinedText) {
				method = "pdf_text_extraction"
			} else {
				method = "pdf_text_extraction_sparse"
			}
		}
		return &PrepareResult{
			Chunks:       chunks,
			ResolvedMode: ModeTextOnly,
			Method:       method,
		}

	case ModeVisionPages:
		pageDir := filepath.Join(os.TempDir(), "llm-proxy-doc", task.ID, "pages")
		pagePaths, err := RenderPDFPages(task.FilePath, pageDir)
		if err != nil {
			return &PrepareResult{Err: fmt.Errorf("render pages: %w", err)}
		}
		chunks, err := ChunkPages(pagePaths)
		if err != nil {
			return &PrepareResult{Err: fmt.Errorf("chunk pages: %w", err)}
		}
		return &PrepareResult{
			Chunks:         chunks,
			ResolvedMode:   ModeVisionPages,
			Method:         "vision",
			PageImagePaths: pagePaths,
		}

	case ModeOCR:
		return &PrepareResult{Err: ErrOCRNotEnabled}

	default:
		return &PrepareResult{Err: fmt.Errorf("unhandled mode %q", mode)}
	}
}

// --------------------------------------------------------------------------
// Text file preparation (.txt, .md)
// --------------------------------------------------------------------------

func (q *PrepareQueue) processTextFile(task *PrepareTask) *PrepareResult {
	data, err := os.ReadFile(task.FilePath)
	if err != nil {
		return &PrepareResult{Err: fmt.Errorf("read file: %w", err)}
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return &PrepareResult{Err: ErrEmptyFile}
	}

	chunks := ChunkText(text, DefaultChunkSize)

	method := "text"
	if task.FileType == FileTypeMD {
		method = "markdown"
	}

	return &PrepareResult{
		Chunks:       chunks,
		ResolvedMode: ModeTextOnly,
		Method:       method,
	}
}

// --------------------------------------------------------------------------
// Image file preparation (.png, .jpg, .jpeg, .webp)
// --------------------------------------------------------------------------

func (q *PrepareQueue) processImage(task *PrepareTask) *PrepareResult {
	data, err := os.ReadFile(task.FilePath)
	if err != nil {
		return &PrepareResult{Err: fmt.Errorf("read image: %w", err)}
	}

	chunks := []Chunk{{
		Index:     0,
		Type:      ChunkTypeImage,
		ImageData: data,
		PageNum:   1,
	}}

	return &PrepareResult{
		Chunks:       chunks,
		ResolvedMode: ModeVisionPages,
		Method:       "image",
	}
}
