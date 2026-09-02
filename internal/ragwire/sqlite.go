package ragwire

import (
	"context"
	"database/sql"
	"fmt"

	"mrinspect/internal/config"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

// RegisterBuiltinBackends installs production SQLite retrieval and validation wiring.
func RegisterBuiltinBackends(configs ...config.RAGEmbeddingConfig) {
	embeddingConfig := config.LoadRAGEmbedding()
	if len(configs) != 0 {
		embeddingConfig = configs[0]
	}
	options := []sqlite.RetrieverOption{
		sqlite.WithEmbeddingConfig(embeddingConfig.Enabled, embeddingConfig.Key != ""),
	}
	if embeddingConfig.Enabled {
		embedder, err := embed.New(embeddingConfig.Provider, embeddingConfig.Key)
		if err != nil {
			options = append(options, sqlite.WithEmbedderError(err))
		} else {
			options = append(options, sqlite.WithEmbedder(embedder))
		}
	}
	rag.Register("sqlite", func(storePath string, sets []resources.Set) (rag.Retriever, error) {
		return sqlite.OpenRetriever(storePath, sets, options...)
	})
	rag.RegisterStoreOpener(sqliteStoreOpener{})
}

// sqliteStoreOpener validates an existing store without initializing or mutating it.
type sqliteStoreOpener struct{}

func (sqliteStoreOpener) OpenAndValidate(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open SQLite store: %w", err)
	}
	defer db.Close()

	var version, manifestCount, actualCount int
	if err := db.QueryRowContext(ctx, `SELECT schema_version, chunk_count FROM schema_meta WHERE id = 1`).Scan(&version, &manifestCount); err != nil {
		return fmt.Errorf("read schema_meta: %w", err)
	}
	if version != sqlite.SchemaVersion {
		return fmt.Errorf("schema_version %d does not match expected %d", version, sqlite.SchemaVersion)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM chunks`).Scan(&actualCount); err != nil {
		return fmt.Errorf("count chunks: %w", err)
	}
	if manifestCount != actualCount {
		return fmt.Errorf("schema_meta chunk_count %d does not match chunks count %d", manifestCount, actualCount)
	}
	return nil
}
