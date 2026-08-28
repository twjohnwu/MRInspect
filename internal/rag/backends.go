package rag

import (
	"context"

	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

func init() {
	Register("sqlite", newSQLiteRetriever)
}

// sqliteRetriever adapts sqlite's currently separate retrieval types to the
// canonical rag contract. sqlite owns its types until their planned migration.
type sqliteRetriever struct {
	inner *sqlite.Retriever
}

func newSQLiteRetriever(storePath string, sets []resources.Set) (Retriever, error) {
	inner, err := sqlite.OpenRetriever(storePath, sets)
	if err != nil {
		return nil, err
	}
	return sqliteRetriever{inner: inner}, nil
}

func (r sqliteRetriever) Name() string { return "sqlite" }

func (r sqliteRetriever) Retrieve(ctx context.Context, query Query) (Result, error) {
	result, err := r.inner.Retrieve(ctx, sqlite.Query{
		Terms:  query.Terms,
		SetRef: query.SetRef,
		Intent: query.Intent,
		TopK:   query.TopK,
	})
	if err != nil {
		return Result{}, err
	}
	chunks := make([]Chunk, len(result.Chunks))
	for i, chunk := range result.Chunks {
		chunks[i] = Chunk{
			ID:          chunk.ID,
			Text:        chunk.Text,
			Source:      chunk.Source,
			ResourceSet: chunk.ResourceSet,
			Heading:     chunk.Heading,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			TokenEst:    chunk.TokenEst,
			Score:       chunk.Score,
		}
	}
	return Result{
		Chunks:    chunks,
		Truncated: result.Truncated,
		Degraded:  result.Degraded,
	}, nil
}

func (r sqliteRetriever) Close() error { return r.inner.Close() }

type noopRetriever struct{}

func (noopRetriever) Name() string { return "noop" }

func (noopRetriever) Retrieve(context.Context, Query) (Result, error) {
	return Result{Degraded: []string{"rag not configured"}}, nil
}

func (noopRetriever) Close() error { return nil }
