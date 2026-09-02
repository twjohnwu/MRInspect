// Package ragwire contains production-only adapters between store resolution,
// retrieval, prompt lane composition, and the reviewer seam.
package ragwire

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"mrinspect/internal/config"
	"mrinspect/internal/lane"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	_ "mrinspect/internal/rag/sqlite"
	"mrinspect/internal/reviewer"
)

// ReviewPathConfig supplies the already-loaded review inputs to the production
// RAG review adapter.
type ReviewPathConfig struct {
	ResolverConfig rag.ResolverConfig
	ResourceSets   []resources.Set
	RAGEmbedding   *config.RAGEmbeddingConfig
}

// ReviewPath is the production implementation of reviewer.RAGReviewPath.
type ReviewPath struct {
	config ReviewPathConfig
	store  *resolvedStore
}

// NewReviewPath constructs the production RAG review adapter.
func NewReviewPath(reviewConfig ReviewPathConfig) *ReviewPath {
	reviewConfig = withRAGEmbeddingConfig(reviewConfig)
	RegisterBuiltinBackends(*reviewConfig.RAGEmbedding)
	return &ReviewPath{config: reviewConfig, store: newResolvedStore(reviewConfig.ResolverConfig, nil)}
}

// ProductionReviewDependencies are the review-only RAG adapters shared by the
// single and multi-lane production paths.
type ProductionReviewDependencies struct {
	ReviewPath reviewer.RAGReviewPath
	Retriever  rag.Retriever
	FullLoader rag.FullLoader
}

// RetrieveForReview resolves a store, retrieves context, and composes its lane.
func (p *ReviewPath) RetrieveForReview(ctx context.Context, diff string) (reviewer.ReviewRAGState, error) {
	resolution, err := p.store.resolve(ctx)
	if err != nil {
		degraded := degradedEntries(resolution.Degraded)
		degraded = append(degraded, fmt.Sprintf("store unavailable: %v", err))
		return reviewer.ReviewRAGState{Degraded: degraded}, nil
	}
	state := reviewer.ReviewRAGState{
		StorePresent:         true,
		Store:                resolution,
		PackageVersionPinned: os.Getenv("MRI_RAG_PACKAGE_VERSION") != "",
		Degraded:             degradedEntries(resolution.Degraded),
	}
	if err := loadStoreProvenance(ctx, resolution.Path, &state); err != nil {
		return state, fmt.Errorf("read store provenance: %w", err)
	}
	retriever, err := rag.New(resolution.Path, p.config.ResourceSets)
	if err != nil {
		return state, fmt.Errorf("create RAG retriever: %w", err)
	}
	defer retriever.Close()
	terms := lane.TermsFromDiff(diff)
	if err := retrieveResourceSets(ctx, retriever, p.config.ResourceSets, terms, &state); err != nil {
		return state, err
	}
	return state, nil
}

func retrieveResourceSets(ctx context.Context, retriever rag.Retriever, sets []resources.Set, terms []string, state *reviewer.ReviewRAGState) error {
	for _, set := range sets {
		if set.Mode != resources.ModeRetrieval {
			continue
		}
		chunksBefore := len(state.Chunks)
		result, err := retriever.Retrieve(ctx, rag.Query{Terms: terms, SetRef: set.Name, Intent: "review", TopK: 5})
		if err != nil {
			return fmt.Errorf("retrieve resource set %q: %w", set.Name, err)
		}
		state.Chunks = append(state.Chunks, result.Chunks...)
		state.Degraded = append(state.Degraded, result.Degraded...)
		if len(state.Chunks) == chunksBefore {
			chunks, skipped := directPathChunks(set)
			state.Chunks = append(state.Chunks, chunks...)
			state.SkippedFiles += skipped
		}
	}
	return nil
}

// NewProductionReviewDependencies constructs the production adapters used by
// both reviewer modes. Construction stays lazy: store resolution and opening
// happen only on the review path, never during process wiring.
func NewProductionReviewDependencies(reviewConfig ReviewPathConfig) ProductionReviewDependencies {
	reviewConfig = withRAGEmbeddingConfig(reviewConfig)
	RegisterBuiltinBackends(*reviewConfig.RAGEmbedding)
	if reviewConfig.ResolverConfig.MaxBytes == 0 {
		reviewConfig.ResolverConfig = rag.DefaultResolverConfig()
	}
	store := newResolvedStore(reviewConfig.ResolverConfig, nil)
	return ProductionReviewDependencies{
		ReviewPath: &ReviewPath{config: reviewConfig, store: store},
		Retriever:  &resolvingRetriever{config: reviewConfig, store: store},
		FullLoader: resourceFullLoader{sets: reviewConfig.ResourceSets},
	}
}

func withRAGEmbeddingConfig(reviewConfig ReviewPathConfig) ReviewPathConfig {
	if reviewConfig.RAGEmbedding == nil {
		embeddingConfig := config.LoadRAGEmbedding()
		reviewConfig.RAGEmbedding = &embeddingConfig
	}
	return reviewConfig
}

func degradedEntries(entries []rag.DegradedEntry) []string {
	degraded := make([]string, 0, len(entries))
	for _, entry := range entries {
		degraded = append(degraded, fmt.Sprintf("%s: %v", entry.Source, entry.Err))
	}
	return degraded
}

func loadStoreProvenance(ctx context.Context, path string, state *reviewer.ReviewRAGState) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.QueryRowContext(ctx, `SELECT built_at, resources_sha256 FROM schema_meta WHERE id = 1`).Scan(&state.Store.BuiltAt, &state.ResourcesSHA256)
}

// directPathChunks covers a store assembled from an explicit file path. The
// indexer's walker intentionally treats a root file as a non-candidate, while
// a review still needs that declared resource to be observable.
func directPathChunks(set resources.Set) ([]rag.Chunk, int) {
	chunks := make([]rag.Chunk, 0)
	skipped := 0
	for _, path := range set.Paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			skipped++
			continue
		}
		chunks = append(chunks, rag.Chunk{Source: path, ResourceSet: set.Name, Text: string(bytes)})
	}
	return chunks, skipped
}
