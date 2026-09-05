package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/intake"
	"mrinspect/internal/rag/resources"
)

// IndexOptions configures one full rebuild of a SQLite resource store.
type IndexOptions struct {
	OutputPath string
	Sets       []resources.Set
	Embedder   embed.Embedder
	Progress   io.Writer

	// writeError is an unexported test seam. Production callers cannot set it.
	writeError error
}

// IndexStats reports the work completed by Index.
type IndexStats struct {
	Embeddings int
	// FilesIndexed counts documents indexed by this rebuild (REQ-03 / T28).
	FilesIndexed int
	FilesSkipped int
	Failures     []chunk.Failure
}

// Index rebuilds the store at opts.OutputPath from opts.Sets.
func Index(ctx context.Context, opts IndexOptions) (stats IndexStats, err error) {
	if opts.OutputPath == "" {
		return stats, fmt.Errorf("Index: output path is required")
	}

	releaseLock, err := acquireIndexLock(opts.OutputPath)
	if err != nil {
		return stats, err
	}
	defer releaseLock()

	tempPath, err := createTempStorePath(opts.OutputPath)
	if err != nil {
		return stats, err
	}
	defer os.Remove(tempPath)

	store, err := Open(tempPath)
	if err != nil {
		return stats, fmt.Errorf("Index: open temporary store: %w", err)
	}
	if _, err := store.db.Exec(`PRAGMA journal_mode = DELETE`); err != nil {
		store.Close()
		return stats, fmt.Errorf("Index: disable WAL for atomic replacement: %w", err)
	}
	progress := opts.Progress
	if progress == nil {
		progress = os.Stderr
	}
	if err := buildStore(ctx, store.db, opts.Sets, opts.Embedder, progress, &stats); err != nil {
		store.Close()
		return stats, err
	}
	if err := store.Close(); err != nil {
		return stats, fmt.Errorf("Index: close temporary store: %w", err)
	}
	if err := syncFile(tempPath); err != nil {
		return stats, err
	}

	// This seam fires after the complete replacement store is durable but before
	// rename; a direct-write implementation would already have altered OutputPath.
	if opts.writeError != nil {
		return stats, opts.writeError
	}
	if err := os.Rename(tempPath, opts.OutputPath); err != nil {
		return stats, fmt.Errorf("Index: rename temporary store: %w", err)
	}
	return stats, nil
}

func acquireIndexLock(outputPath string) (release func(), err error) {
	lockPath := outputPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("Index: create output directory: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("Index: another index run holds %s; remove it if stale", lockPath)
		}
		return nil, fmt.Errorf("Index: create lock: %w", err)
	}
	removeLock := func() {
		_ = os.Remove(lockPath)
	}
	if _, err := fmt.Fprintf(lock, "%d\n", os.Getpid()); err != nil {
		_ = lock.Close()
		removeLock()
		return nil, fmt.Errorf("Index: write lock: %w", err)
	}
	if err := lock.Close(); err != nil {
		removeLock()
		return nil, fmt.Errorf("Index: close lock: %w", err)
	}
	return removeLock, nil
}

func createTempStorePath(outputPath string) (string, error) {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Index: create output directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(outputPath)+"-*")
	if err != nil {
		return "", fmt.Errorf("Index: create temporary store: %w", err)
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("Index: close temporary file: %w", err)
	}
	return path, nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Index: open temporary store for sync: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("Index: sync temporary store: %w", err)
	}
	return nil
}

func buildStore(ctx context.Context, db *sql.DB, sets []resources.Set, embedder embed.Embedder, progress io.Writer, stats *IndexStats) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("Index: begin transaction: %w", err)
	}
	defer tx.Rollback()

	indexedAt := time.Now().UTC().Format(time.RFC3339)
	chunkCount := 0
	fingerprint := resourceFingerprint{}
	for sequence, set := range sets {
		count, err := indexSet(ctx, tx, set, sequence, indexedAt, progress, stats, &fingerprint)
		if err != nil {
			return err
		}
		chunkCount += count
	}
	if stats.FilesIndexed == 0 {
		return errors.New("Index: no files matched any resource set; refusing to publish an empty store")
	}
	if embedder != nil {
		if err := embedChunks(ctx, tx, embedder, progress, indexedAt, chunkCount, stats); err != nil {
			return err
		}
	}
	resourcesHash := fingerprint.sum()
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET built_at = ?, chunk_count = ?, resources_sha256 = ? WHERE id = 1`, indexedAt, chunkCount, resourcesHash); err != nil {
		return fmt.Errorf("Index: update manifest: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Index: commit transaction: %w", err)
	}
	return nil
}

const embeddingBatchSize = 64

var embedRetryWait = func(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type embeddingChunk struct {
	id      int64
	heading string
	text    string
}

func embedChunks(ctx context.Context, tx *sql.Tx, embedder embed.Embedder, progress io.Writer, indexedAt string, expectedChunks int, stats *IndexStats) error {
	chunks, err := embeddingChunks(ctx, tx)
	if err != nil {
		return err
	}
	if len(chunks) != expectedChunks {
		return fmt.Errorf("Index: embedding chunk count = %d, want %d", len(chunks), expectedChunks)
	}

	requests := (len(chunks) + embeddingBatchSize - 1) / embeddingBatchSize
	if _, err := fmt.Fprintf(progress, "embedding %d chunks (~%d requests)\n", len(chunks), requests); err != nil {
		return fmt.Errorf("Index: write embedding progress: %w", err)
	}

	dimension := embedder.Dim()
	if dimension <= 0 {
		return fmt.Errorf("Index: embedder dimension = %d, want positive", dimension)
	}
	written := 0
	for start := 0; start < len(chunks); start += embeddingBatchSize {
		batch := start/embeddingBatchSize + 1
		end := min(start+embeddingBatchSize, len(chunks))
		texts := make([]string, end-start)
		for index, item := range chunks[start:end] {
			input, err := embeddingInput(item)
			if err != nil {
				return err
			}
			texts[index] = input
		}

		vectors, err := embedder.Embed(ctx, texts)
		for retry := 1; err != nil && embed.IsRateLimited(err) && retry <= 3; retry++ {
			delay := time.Duration(retry) * 20 * time.Second
			if _, progressErr := fmt.Fprintf(progress, "embedding batch %d/%d rate limited (HTTP 429); retrying in %ds\n", batch, requests, int(delay/time.Second)); progressErr != nil {
				return fmt.Errorf("Index: write embedding progress: %w", progressErr)
			}
			if waitErr := embedRetryWait(ctx, delay); waitErr != nil {
				return fmt.Errorf("Index: embed chunk batch %d: %w", batch, waitErr)
			}
			vectors, err = embedder.Embed(ctx, texts)
		}
		if err != nil {
			return fmt.Errorf("Index: embed chunk batch %d: %w", batch, err)
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("Index: embed chunk batch %d returned %d vectors, want %d", batch, len(vectors), len(texts))
		}
		for index, vector := range vectors {
			chunkID := chunks[start+index].id
			blob, err := encodeEmbedding(vector, dimension)
			if err != nil {
				return fmt.Errorf("Index: validate embedding for chunk %d: %w", chunkID, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO embeddings (chunk_id, dim, vec, created_at) VALUES (?, ?, ?, ?)`, chunkID, dimension, blob, indexedAt); err != nil {
				return fmt.Errorf("Index: insert embedding for chunk %d: %w", chunkID, err)
			}
			written++
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET embed_model = ?, embed_dim = ? WHERE id = 1`, embedder.Model(), dimension); err != nil {
		return fmt.Errorf("Index: update embedding manifest: %w", err)
	}
	if err := validateStoredEmbeddings(ctx, tx, expectedChunks, dimension); err != nil {
		return err
	}
	stats.Embeddings = written
	return nil
}

func embeddingInput(item embeddingChunk) (string, error) {
	if strings.TrimSpace(item.text) != "" {
		return item.text, nil
	}
	if heading := strings.TrimSpace(item.heading); heading != "" {
		return heading, nil
	}
	return "", fmt.Errorf("embed: chunk %d has no text or heading", item.id)
}

func embeddingChunks(ctx context.Context, tx *sql.Tx) ([]embeddingChunk, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, heading, text FROM chunks ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("Index: query chunks for embedding: %w", err)
	}
	defer rows.Close()

	var chunks []embeddingChunk
	for rows.Next() {
		var item embeddingChunk
		if err := rows.Scan(&item.id, &item.heading, &item.text); err != nil {
			return nil, fmt.Errorf("Index: scan chunk for embedding: %w", err)
		}
		chunks = append(chunks, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Index: iterate chunks for embedding: %w", err)
	}
	return chunks, nil
}

func encodeEmbedding(vector []float32, dimension int) ([]byte, error) {
	if len(vector) != dimension {
		return nil, fmt.Errorf("dimension = %d, want %d", len(vector), dimension)
	}

	blob := make([]byte, dimension*4)
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("coordinate %d is not finite", index)
		}
		binary.LittleEndian.PutUint32(blob[index*4:], math.Float32bits(value))
	}
	return blob, nil
}

func validateStoredEmbeddings(ctx context.Context, tx *sql.Tx, expected, dimension int) error {
	var count int
	var invalid int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(sum(CASE WHEN dim != ? OR length(vec) != ? THEN 1 ELSE 0 END), 0)
		FROM embeddings`, dimension, dimension*4).Scan(&count, &invalid); err != nil {
		return fmt.Errorf("Index: validate stored embeddings: %w", err)
	}
	if count != expected {
		return fmt.Errorf("Index: stored embedding count = %d, want %d", count, expected)
	}
	if invalid != 0 {
		return fmt.Errorf("Index: stored embeddings with invalid dimension = %d, want 0", invalid)
	}
	return nil
}

func indexSet(ctx context.Context, tx *sql.Tx, set resources.Set, sequence int, indexedAt string, progress io.Writer, stats *IndexStats, fingerprint *resourceFingerprint) (int, error) {
	result, err := intake.Walk(intake.WalkOptions{
		Paths:   set.Paths,
		Include: set.Include,
		Exclude: set.Exclude,
	})
	if err != nil {
		return 0, fmt.Errorf("Index: walk set %q: %w", set.Name, err)
	}
	stats.FilesSkipped += result.FilesSkipped
	if len(result.Files) == 0 {
		if _, err := fmt.Fprintf(progress, "warning: resource set %q matched no files\n", set.Name); err != nil {
			return 0, fmt.Errorf("Index: write empty-set warning: %w", err)
		}
	}

	setID, err := insertSet(ctx, tx, set, sequence, indexedAt)
	if err != nil {
		return 0, err
	}
	chunksWritten := 0
	for _, path := range result.Files {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("Index: context: %w", err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("Index: read %q: %w", path, err)
		}
		relPath := relativePath(set.Paths, path)
		documentID, err := insertDocument(ctx, tx, setID, relPath, path, content, indexedAt)
		if err != nil {
			return 0, err
		}
		fingerprint.add(set.Name, relPath, content)
		stats.FilesIndexed++
		if set.Mode == resources.ModeFull {
			continue
		}
		chunks, failures, err := chunksFor(path, string(content))
		if err != nil {
			return 0, err
		}
		stats.Failures = append(stats.Failures, failures...)
		for order, item := range chunks {
			if err := insertChunk(ctx, tx, documentID, order, item, indexedAt); err != nil {
				return 0, err
			}
			chunksWritten++
		}
	}
	return chunksWritten, nil
}

type resourceFingerprint struct {
	lines []string
}

func (fingerprint *resourceFingerprint) add(setName, relPath string, content []byte) {
	fingerprint.lines = append(fingerprint.lines, fmt.Sprintf("%s\t%s\t%s\n", setName, relPath, contentHash(content)))
}

func (fingerprint *resourceFingerprint) sum() string {
	sort.Strings(fingerprint.lines)
	return contentHash([]byte(strings.Join(fingerprint.lines, "")))
}

// ResourcesFingerprint computes the content fingerprint used in schema_meta
// from the files selected by the same intake walker as Index.
func ResourcesFingerprint(sets []resources.Set) (string, error) {
	fingerprint := resourceFingerprint{}
	for _, set := range sets {
		result, err := intake.Walk(intake.WalkOptions{
			Paths:   set.Paths,
			Include: set.Include,
			Exclude: set.Exclude,
		})
		if err != nil {
			return "", fmt.Errorf("ResourcesFingerprint: walk set %q: %w", set.Name, err)
		}
		for _, path := range result.Files {
			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("ResourcesFingerprint: read %q: %w", path, err)
			}
			fingerprint.add(set.Name, relativePath(set.Paths, path), content)
		}
	}
	return fingerprint.sum(), nil
}

func insertSet(ctx context.Context, tx *sql.Tx, set resources.Set, sequence int, indexedAt string) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO resource_sets (name, seq, indexed_at) VALUES (?, ?, ?)`, set.Name, sequence, indexedAt)
	if err != nil {
		return 0, fmt.Errorf("Index: insert set %q: %w", set.Name, err)
	}
	setID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("Index: set %q id: %w", set.Name, err)
	}
	for _, tag := range set.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags (name) VALUES (?)`, tag); err != nil {
			return 0, fmt.Errorf("Index: insert tag %q: %w", tag, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO set_tags (set_id, tag_id) SELECT ?, id FROM tags WHERE name = ?`, setID, tag); err != nil {
			return 0, fmt.Errorf("Index: assign tag %q: %w", tag, err)
		}
	}
	return setID, nil
}

func insertDocument(ctx context.Context, tx *sql.Tx, setID int64, relPath, path string, content []byte, indexedAt string) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO documents (set_id, rel_path, doc_kind, content_sha256, size_bytes, indexed_at) VALUES (?, ?, ?, ?, ?, ?)`, setID, relPath, documentKind(path), contentHash(content), len(content), indexedAt)
	if err != nil {
		return 0, fmt.Errorf("Index: insert document %q: %w", path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("Index: document %q id: %w", path, err)
	}
	return id, nil
}

func insertChunk(ctx context.Context, tx *sql.Tx, documentID int64, order int, item chunk.Chunk, indexedAt string) error {
	text := chunkText(item)
	_, err := tx.ExecContext(ctx, `INSERT INTO chunks (document_id, ord, heading, text, start_line, end_line, token_est, indexed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, documentID, order, item.Heading, text, item.StartLine, item.EndLine, chunk.TokenEst(text), indexedAt)
	if err != nil {
		return fmt.Errorf("Index: insert chunk %d: %w", order, err)
	}
	return nil
}

func chunksFor(path, source string) ([]chunk.Chunk, []chunk.Failure, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		chunks, err := chunk.Markdown(source)
		return chunks, nil, err
	case ".yaml", ".yml", ".json":
		result, err := chunk.Structured(path, source)
		return result.Chunks, result.Failures, err
	default:
		return chunk.Lines(source), nil, nil
	}
}

func relativePath(roots []string, path string) string {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(path))
}

func documentKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "markdown"
	case ".yaml", ".yml", ".json":
		return "structured"
	default:
		return "text"
	}
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}

func chunkText(item chunk.Chunk) string {
	if item.Heading == "" {
		return item.Text
	}
	if _, rest, found := strings.Cut(item.Text, "\n"); found {
		return strings.TrimLeft(rest, "\n")
	}
	return ""
}

// ManifestChunkCount reports the chunk count recorded in schema_meta.
func ManifestChunkCount(path string) (int, error) {
	store, err := Open(path)
	if err != nil {
		return 0, fmt.Errorf("ManifestChunkCount: Open: %w", err)
	}
	defer store.Close()

	var count int
	if err := store.db.QueryRow(`SELECT chunk_count FROM schema_meta WHERE id = 1`).Scan(&count); err != nil {
		return 0, fmt.Errorf("ManifestChunkCount: query schema_meta: %w", err)
	}
	return count, nil
}
