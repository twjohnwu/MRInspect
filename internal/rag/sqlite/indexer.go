package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
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
	for sequence, set := range sets {
		count, err := indexSet(ctx, tx, set, sequence, indexedAt, stats)
		if err != nil {
			return err
		}
		chunkCount += count
	}
	if embedder != nil {
		if err := embedChunks(ctx, tx, embedder, progress, indexedAt, chunkCount, stats); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET built_at = ?, chunk_count = ? WHERE id = 1`, indexedAt, chunkCount); err != nil {
		return fmt.Errorf("Index: update manifest: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Index: commit transaction: %w", err)
	}
	return nil
}

const embeddingBatchSize = 64

type embeddingChunk struct {
	id   int64
	text string
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
		end := min(start+embeddingBatchSize, len(chunks))
		texts := make([]string, end-start)
		for index, item := range chunks[start:end] {
			texts[index] = item.text
		}

		vectors, err := embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("Index: embed chunk batch %d: %w", start/embeddingBatchSize+1, err)
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("Index: embed chunk batch %d returned %d vectors, want %d", start/embeddingBatchSize+1, len(vectors), len(texts))
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

func embeddingChunks(ctx context.Context, tx *sql.Tx) ([]embeddingChunk, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, text FROM chunks ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("Index: query chunks for embedding: %w", err)
	}
	defer rows.Close()

	var chunks []embeddingChunk
	for rows.Next() {
		var item embeddingChunk
		if err := rows.Scan(&item.id, &item.text); err != nil {
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

func indexSet(ctx context.Context, tx *sql.Tx, set resources.Set, sequence int, indexedAt string, stats *IndexStats) (int, error) {
	result, err := intake.Walk(intake.WalkOptions{
		Paths:   set.Paths,
		Include: set.Include,
		Exclude: set.Exclude,
	})
	if err != nil {
		return 0, fmt.Errorf("Index: walk set %q: %w", set.Name, err)
	}
	stats.FilesSkipped += result.FilesSkipped

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
		documentID, err := insertDocument(ctx, tx, setID, relativePath(set.Paths, path), path, content, indexedAt)
		if err != nil {
			return 0, err
		}
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
