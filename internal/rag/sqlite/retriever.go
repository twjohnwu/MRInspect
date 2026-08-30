package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
)

const (
	embeddingsEnv = "MRI_RAG_EMBEDDINGS"
	embedKeyEnv   = "MRI_RAG_EMBED_KEY"
)

// Retriever reads chunks from one SQLite store.
type Retriever struct {
	mu     sync.RWMutex
	db     *sql.DB
	path   string
	modes  map[string]string
	closed bool

	// embedder is deliberately package-private: it is a construction seam for
	// the SQLite backend, not an API exposed to review callers.
	embedder embedder
}

// embedder supplies vectors for the optional retrieval reranker.
type embedder interface {
	Embed(context.Context, string) ([]float64, error)
}

// OpenRetriever opens a retriever for path using the declared resource sets.
func OpenRetriever(path string, sets []resources.Set) (*Retriever, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("OpenRetriever: sql.Open: %w", err)
	}
	modes := make(map[string]string, len(sets))
	for _, set := range sets {
		modes[set.Name] = set.Mode
	}
	return &Retriever{db: db, path: path, modes: modes}, nil
}

// Retrieve returns chunks for q.
func (r *Retriever) Name() string { return "sqlite" }

func (r *Retriever) Retrieve(ctx context.Context, q rag.Query) (rag.Result, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return rag.Result{}, fmt.Errorf("Retrieve: retriever is closed")
	}

	mode, known := r.modes[q.SetRef]
	if !known {
		return degraded("unknown resource set %q", q.SetRef), nil
	}
	if mode == resources.ModeFull {
		return rag.Result{}, fmt.Errorf("Retrieve: resource set %q uses mode full", q.SetRef)
	}
	if len(q.Terms) == 0 || q.TopK <= 0 {
		return rag.Result{}, nil
	}

	if result, ok := r.validateStore(ctx, q.SetRef); !ok {
		return result, nil
	}
	result, err := r.bm25(ctx, q)
	if err != nil {
		return degraded("store retrieval unavailable: %v", err), nil
	}
	return r.rerank(ctx, strings.Join(q.Terms, " "), result), nil
}

func (r *Retriever) validateStore(ctx context.Context, setRef string) (rag.Result, bool) {
	if _, err := os.Stat(r.path); err != nil {
		return degraded("store unavailable: %v", err), false
	}

	var actual int
	if err := r.db.QueryRowContext(ctx, `SELECT schema_version FROM schema_meta WHERE id = 1`).Scan(&actual); err != nil {
		return degraded("store unavailable: %v", err), false
	}
	if actual != SchemaVersion {
		return degraded("store schema version %d does not match expected %d", actual, SchemaVersion), false
	}

	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM resource_sets WHERE name = ?)`, setRef).Scan(&exists); err != nil {
		return degraded("store unavailable: %v", err), false
	}
	if !exists {
		return degraded("resource set %q is not indexed in store", setRef), false
	}
	return rag.Result{}, true
}

func (r *Retriever) bm25(ctx context.Context, q rag.Query) (rag.Result, error) {
	terms := strings.Join(q.Terms, " OR ")
	rows, err := r.db.QueryContext(ctx, `SELECT c.id, c.text, d.rel_path, s.name, c.heading, c.start_line, c.end_line, c.token_est, bm25(chunks_fts) FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid JOIN documents d ON d.id = c.document_id JOIN resource_sets s ON s.id = d.set_id WHERE chunks_fts MATCH ? AND s.name = ? ORDER BY bm25(chunks_fts) LIMIT ?`, terms, q.SetRef, q.TopK+1)
	if err != nil {
		return rag.Result{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	result := rag.Result{}
	for rows.Next() {
		item, err := scanChunk(rows)
		if err != nil {
			return rag.Result{}, err
		}
		result.Chunks = append(result.Chunks, item)
	}
	if err := rows.Err(); err != nil {
		return rag.Result{}, fmt.Errorf("rows: %w", err)
	}
	if len(result.Chunks) > q.TopK {
		result.Truncated = true
		result.Chunks = result.Chunks[:q.TopK]
	}
	return result, nil
}

func scanChunk(rows *sql.Rows) (rag.Chunk, error) {
	var item rag.Chunk
	var id int64
	var score float64
	if err := rows.Scan(&id, &item.Text, &item.Source, &item.ResourceSet, &item.Heading, &item.StartLine, &item.EndLine, &item.TokenEst, &score); err != nil {
		return rag.Chunk{}, fmt.Errorf("scan: %w", err)
	}
	item.ID = strconv.FormatInt(id, 10)
	item.Score = &score
	return item, nil
}

func (r *Retriever) rerank(ctx context.Context, query string, result rag.Result) rag.Result {
	if !embeddingsEnabled() {
		return result
	}
	if os.Getenv(embedKeyEnv) == "" {
		result.Degraded = append(result.Degraded, "embedding rerank disabled: missing embedding key")
		return result
	}
	if r.embedder == nil {
		result.Degraded = append(result.Degraded, "embedding rerank disabled: embedder unavailable")
		return result
	}

	queryVector, err := r.embedder.Embed(ctx, query)
	if err != nil {
		result.Degraded = append(result.Degraded, fmt.Sprintf("embedding rerank disabled: %v", err))
		return result
	}
	type rankedChunk struct {
		chunk rag.Chunk
		score float64
	}
	ranked := make([]rankedChunk, 0, len(result.Chunks))
	for _, item := range result.Chunks {
		vector, err := r.embedder.Embed(ctx, item.Text)
		if err != nil {
			result.Degraded = append(result.Degraded, fmt.Sprintf("embedding rerank disabled: %v", err))
			return result
		}
		ranked = append(ranked, rankedChunk{chunk: item, score: cosine(queryVector, vector)})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	for i := range ranked {
		result.Chunks[i] = ranked[i].chunk
	}
	return result
}

func embeddingsEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(embeddingsEnv))
	return err == nil && enabled
}

func cosine(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}

func degraded(format string, args ...any) rag.Result {
	return rag.Result{Degraded: []string{fmt.Sprintf(format, args...)}}
}

// Close releases retriever resources. It waits for in-flight retrievals, and
// repeated calls are harmless.
func (r *Retriever) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("Close: db.Close: %w", err)
	}
	return nil
}
