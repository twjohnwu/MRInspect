// Package ragwire contains production-only adapters between store resolution,
// retrieval, prompt lane composition, and the reviewer seam.
package ragwire

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
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
	Project        project.LoadedProject
	MergeRequest   gitlab.MergeRequest
	Composer       *prompt.Composer
}

// ReviewPath is the production implementation of reviewer.RAGReviewPath.
type ReviewPath struct{ config ReviewPathConfig }

// NewReviewPath constructs the production RAG review adapter.
func NewReviewPath(config ReviewPathConfig) *ReviewPath { return &ReviewPath{config: config} }

// RetrieveForReview resolves a store, retrieves context, and composes its lane.
func (p *ReviewPath) RetrieveForReview(ctx context.Context, diff string) (reviewer.ReviewRAGState, error) {
	resolution, err := rag.ResolveStore(ctx, p.config.ResolverConfig)
	if err != nil {
		return reviewer.ReviewRAGState{Degraded: degradedEntries(resolution.Degraded)}, nil
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
	terms := retrievalTerms(diff)
	for _, set := range p.config.ResourceSets {
		if set.Mode != resources.ModeRetrieval {
			continue
		}
		chunksBefore := len(state.Chunks)
		for _, term := range terms {
			result, err := retriever.Retrieve(ctx, rag.Query{Terms: []string{term}, SetRef: set.Name, Intent: "review", TopK: 5})
			if err != nil {
				return state, fmt.Errorf("retrieve resource set %q: %w", set.Name, err)
			}
			state.Chunks = append(state.Chunks, result.Chunks...)
			state.Degraded = append(state.Degraded, result.Degraded...)
		}
		if len(state.Chunks) == chunksBefore {
			chunks, skipped := directPathChunks(set)
			state.Chunks = append(state.Chunks, chunks...)
			state.SkippedFiles += skipped
		}
	}
	return state, nil
}

// NewProductionReviewPath is the main.go-facing hook. The configuration is
// intentionally explicit so main can assemble it without reviewer importing
// composition-root dependencies.
func NewProductionReviewPath(_ config.Config, reviewConfig ReviewPathConfig) reviewer.RAGReviewPath {
	if reviewConfig.ResolverConfig.MaxBytes == 0 {
		reviewConfig.ResolverConfig = rag.DefaultResolverConfig()
	}
	return NewReviewPath(reviewConfig)
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

func retrievalTerms(diff string) []string {
	fields := strings.FieldsFunc(diff, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.ToLower(field)
		if len(field) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
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
