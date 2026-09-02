package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mrinspect/internal/config"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/embed"
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
	embedder    embed.Embedder
	embedderErr error

	embeddingsEnabled *bool
	embedKeyPresent   *bool
}

// RetrieverOption configures an optional retrieval dependency.
type RetrieverOption func(*Retriever)

// WithEmbeddingConfig supplies the config-owned reranking decision. Direct
// package callers that omit it retain the legacy environment-backed behavior.
func WithEmbeddingConfig(enabled, keyPresent bool) RetrieverOption {
	return func(retriever *Retriever) {
		retriever.embeddingsEnabled = &enabled
		retriever.embedKeyPresent = &keyPresent
	}
}

// WithEmbedder injects the embedder used by the optional retrieval reranker.
func WithEmbedder(value embed.Embedder) RetrieverOption {
	return func(retriever *Retriever) {
		retriever.embedder = value
		retriever.embedderErr = nil
	}
}

// WithEmbedderError records an embedder construction failure for degradation.
func WithEmbedderError(err error) RetrieverOption {
	return func(retriever *Retriever) {
		retriever.embedder = nil
		retriever.embedderErr = err
	}
}

// OpenRetriever opens a retriever for path using the declared resource sets.
func OpenRetriever(path string, sets []resources.Set, options ...RetrieverOption) (*Retriever, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("OpenRetriever: sql.Open: %w", err)
	}
	modes := make(map[string]string, len(sets))
	for _, set := range sets {
		modes[set.Name] = set.Mode
	}
	retriever := &Retriever{db: db, path: path, modes: modes}
	for _, option := range options {
		option(retriever)
	}
	return retriever, nil
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
	plan := r.planRerank(ctx, q.SetRef)
	result, err := r.bm25(ctx, q, plan.ready)
	if err != nil {
		return degraded("store retrieval unavailable: %v", err), nil
	}
	if plan.degradation != nil {
		return degradeRerank(result, q.TopK, *plan.degradation), nil
	}
	if !plan.ready {
		return result, nil
	}
	return r.rerank(ctx, strings.Join(q.Terms, " "), q.TopK, result, plan), nil
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

func (r *Retriever) bm25(ctx context.Context, q rag.Query, widen bool) (rag.Result, error) {
	terms := strings.Join(q.Terms, " OR ")
	limit := q.TopK + 1
	if widen {
		limit = 4 * q.TopK
	}
	rows, err := r.db.QueryContext(ctx, `SELECT c.id, c.text, d.rel_path, s.name, c.heading, c.start_line, c.end_line, c.token_est, bm25(chunks_fts) FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid JOIN documents d ON d.id = c.document_id JOIN resource_sets s ON s.id = d.set_id WHERE chunks_fts MATCH ? AND s.name = ? ORDER BY bm25(chunks_fts) LIMIT ?`, terms, q.SetRef, limit)
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
		if !widen {
			result.Chunks = result.Chunks[:q.TopK]
		}
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

func (r *Retriever) rerank(ctx context.Context, query string, topK int, result rag.Result, plan rerankPlan) rag.Result {
	if len(result.Chunks) == 0 {
		return result
	}
	queryVectors, err := r.embedder.Embed(ctx, []string{query})
	if err != nil || len(queryVectors) != 1 || len(queryVectors[0]) != plan.storeDim {
		return degradeRerank(result, topK, rerankDegradation{
			code:     rerankEmbedCallFailed,
			provider: plan.queryModel,
			status:   embeddingStatus(err),
		})
	}
	queryVector := queryVectors[0]
	vectors, err := r.candidateVectors(ctx, result.Chunks, plan.storeDim)
	if err != nil {
		return degradeRerank(result, topK, rerankDegradation{
			code:     rerankCorruptVector,
			provider: plan.queryModel,
		})
	}

	type rankedChunk struct {
		chunk rag.Chunk
		score float64
	}
	ranked := make([]rankedChunk, 0, len(result.Chunks))
	for _, item := range result.Chunks {
		ranked = append(ranked, rankedChunk{chunk: item, score: cosine(queryVector, vectors[item.ID])})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	result.Chunks = make([]rag.Chunk, min(topK, len(ranked)))
	for index := range result.Chunks {
		ranked[index].chunk.Score = &ranked[index].score
		result.Chunks[index] = ranked[index].chunk
	}
	return result
}

type rerankPlan struct {
	ready       bool
	storeModel  string
	storeDim    int
	queryModel  string
	degradation *rerankDegradation
}

func (r *Retriever) planRerank(ctx context.Context, setRef string) rerankPlan {
	embeddingConfig := r.embeddingConfig()
	if !embeddingConfig.enabled {
		return rerankPlan{}
	}

	provider := r.embedderModel()
	if !embeddingConfig.keyPresent {
		return failedRerankPlan(rerankDegradation{
			code:     rerankMissingKey,
			provider: provider,
		})
	}
	if r.embedderErr != nil || r.embedder == nil {
		return failedRerankPlan(rerankDegradation{
			code:     rerankEmbedderInit,
			provider: "none",
			env:      embedderErrorEnv(r.embedderErr),
		})
	}

	queryModel := r.embedder.Model()
	queryDim := r.embedder.Dim()
	var storeModel string
	var storeDim int
	var vectorCount int
	err := r.db.QueryRowContext(ctx, `
		SELECT sm.embed_model,
		       sm.embed_dim,
		       (SELECT count(*)
		          FROM embeddings e
		          JOIN chunks c ON c.id = e.chunk_id
		          JOIN documents d ON d.id = c.document_id
		          JOIN resource_sets s ON s.id = d.set_id
		         WHERE s.name = ?)
		  FROM schema_meta sm
		 WHERE sm.id = 1`, setRef).Scan(&storeModel, &storeDim, &vectorCount)
	if err != nil || storeModel == "" || vectorCount == 0 {
		return failedRerankPlan(rerankDegradation{
			code:     rerankNoVectors,
			provider: queryModel,
		})
	}
	if storeModel != queryModel || storeDim != queryDim {
		return failedRerankPlan(rerankDegradation{
			code:       rerankModelMismatch,
			provider:   queryModel,
			storeModel: storeModel,
			storeDim:   storeDim,
			queryModel: queryModel,
			queryDim:   queryDim,
		})
	}

	return rerankPlan{
		ready:      true,
		storeModel: storeModel,
		storeDim:   storeDim,
		queryModel: queryModel,
	}
}

func failedRerankPlan(degradation rerankDegradation) rerankPlan {
	return rerankPlan{degradation: &degradation}
}

func (r *Retriever) embedderModel() string {
	if r.embedder == nil || r.embedder.Model() == "" {
		return "none"
	}
	return r.embedder.Model()
}

func (r *Retriever) candidateVectors(ctx context.Context, chunks []rag.Chunk, expectedDim int) (map[string][]float32, error) {
	placeholders := make([]string, len(chunks))
	arguments := make([]any, len(chunks))
	for index, item := range chunks {
		placeholders[index] = "?"
		arguments[index] = item.ID
	}
	rows, err := r.db.QueryContext(ctx, `SELECT chunk_id, dim, vec FROM embeddings WHERE chunk_id IN (`+strings.Join(placeholders, ",")+`)`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query candidate vectors: %w", err)
	}
	defer rows.Close()

	vectors := make(map[string][]float32, len(chunks))
	for rows.Next() {
		var chunkID int64
		var dimension int
		var blob []byte
		if err := rows.Scan(&chunkID, &dimension, &blob); err != nil {
			return nil, fmt.Errorf("scan candidate vector: %w", err)
		}
		if dimension != expectedDim {
			return nil, fmt.Errorf("candidate vector dimension %d does not match %d", dimension, expectedDim)
		}
		vector, err := decodeEmbedding(blob, dimension)
		if err != nil {
			return nil, err
		}
		vectors[strconv.FormatInt(chunkID, 10)] = vector
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate vectors: %w", err)
	}
	if len(vectors) != len(chunks) {
		return nil, fmt.Errorf("candidate vectors = %d, want %d", len(vectors), len(chunks))
	}
	return vectors, nil
}

func decodeEmbedding(blob []byte, dimension int) ([]float32, error) {
	if dimension <= 0 || len(blob) != dimension*4 {
		return nil, fmt.Errorf("invalid embedding vector length")
	}
	vector := make([]float32, dimension)
	for index := range vector {
		value := math.Float32frombits(binary.LittleEndian.Uint32(blob[index*4:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("embedding vector coordinate is not finite")
		}
		vector[index] = value
	}
	return vector, nil
}

type rerankDegradationCode string

const (
	rerankMissingKey      rerankDegradationCode = "missing-key"
	rerankEmbedderInit    rerankDegradationCode = "embedder-init"
	rerankNoVectors       rerankDegradationCode = "no-vectors"
	rerankModelMismatch   rerankDegradationCode = "model-mismatch"
	rerankEmbedCallFailed rerankDegradationCode = "embed-call-failed"
	rerankCorruptVector   rerankDegradationCode = "corrupt-vector"
)

type rerankDegradation struct {
	code       rerankDegradationCode
	provider   string
	storeModel string
	storeDim   int
	queryModel string
	queryDim   int
	status     string
	env        string
}

func (degradation rerankDegradation) reason() string {
	provider := degradation.provider
	if provider == "" {
		provider = "none"
	}
	attributes := []string{"provider=" + provider, "component=embedding"}
	switch degradation.code {
	case rerankMissingKey:
		attributes = append(attributes, "env="+embedKeyEnv)
	case rerankEmbedderInit:
		if degradation.env != "" {
			attributes = append(attributes, "env="+degradation.env)
		}
	case rerankNoVectors:
		attributes = append(attributes, "detail=no vectors")
	case rerankModelMismatch:
		attributes = append(attributes,
			"store-model="+degradation.storeModel,
			"store-dim="+strconv.Itoa(degradation.storeDim),
			"query-model="+degradation.queryModel,
			"query-dim="+strconv.Itoa(degradation.queryDim),
		)
	case rerankEmbedCallFailed:
		attributes = append(attributes, "status="+degradation.status)
	}
	reason := fmt.Sprintf("rerank degraded: %s (%s)", degradation.code, strings.Join(attributes, ", "))
	if len(reason) > 200 {
		return reason[:200]
	}
	return reason
}

func degradeRerank(result rag.Result, topK int, degradation rerankDegradation) rag.Result {
	if len(result.Chunks) > topK {
		result.Chunks = result.Chunks[:topK]
	}
	result.Degraded = append(result.Degraded, degradation.reason())
	return result
}

func embeddingStatus(err error) string {
	if err == nil {
		return "unknown"
	}
	message := err.Error()
	for index := 0; index+2 < len(message); index++ {
		if !isDigit(message[index]) || !isDigit(message[index+1]) || !isDigit(message[index+2]) {
			continue
		}
		if index > 0 && isDigit(message[index-1]) {
			continue
		}
		if index+3 < len(message) && isDigit(message[index+3]) {
			continue
		}
		status, parseErr := strconv.Atoi(message[index : index+3])
		if parseErr == nil && status >= 100 && status <= 599 {
			return strconv.Itoa(status)
		}
	}
	return "unknown"
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

type retrievalEmbeddingConfig struct {
	enabled    bool
	keyPresent bool
}

func (r *Retriever) embeddingConfig() retrievalEmbeddingConfig {
	if r.embeddingsEnabled == nil || r.embedKeyPresent == nil {
		legacy := config.LoadRAGEmbedding()
		return retrievalEmbeddingConfig{enabled: legacy.Enabled, keyPresent: legacy.Key != ""}
	}
	return retrievalEmbeddingConfig{enabled: *r.embeddingsEnabled, keyPresent: *r.embedKeyPresent}
}

func embedderErrorEnv(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, name := range []string{"MRI_EMBED_PROVIDER", embedKeyEnv} {
		if strings.Contains(message, name) {
			return name
		}
	}
	return ""
}

func cosine(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		leftValue := float64(left[i])
		rightValue := float64(right[i])
		dot += leftValue * rightValue
		leftNorm += leftValue * leftValue
		rightNorm += rightValue * rightValue
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
