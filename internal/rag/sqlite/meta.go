package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Meta is the store metadata consumed by retrieval evaluation.
type Meta struct {
	BuiltAt         string
	ResourcesSHA256 string
	EmbedModel      string
	EmbedDim        int
}

// ReadMeta reads schema metadata without opening the store for writes.
func ReadMeta(ctx context.Context, path string) (Meta, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return Meta{}, fmt.Errorf("ReadMeta: sql.Open: %w", err)
	}
	defer db.Close()

	var meta Meta
	err = db.QueryRowContext(ctx, `SELECT built_at, resources_sha256, embed_model, embed_dim FROM schema_meta WHERE id = 1`).Scan(
		&meta.BuiltAt,
		&meta.ResourcesSHA256,
		&meta.EmbedModel,
		&meta.EmbedDim,
	)
	if err != nil {
		return Meta{}, fmt.Errorf("ReadMeta: query schema_meta: %w", err)
	}
	return meta, nil
}
