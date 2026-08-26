package sqlite

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const SchemaVersion = 1

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("Open: sql.Open: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("Open: execute schema: %w", err)
	}

	if _, err := db.Exec(
		`INSERT OR IGNORE INTO schema_meta
            (id, schema_version, tool_version, built_at, resources_sha256, embed_model, embed_dim)
         VALUES (?, ?, ?, ?, ?, ?, ?)`,
		1,
		SchemaVersion,
		"dev",
		time.Now().UTC().Format(time.RFC3339),
		"",
		"",
		0,
	); err != nil {
		db.Close()
		return nil, fmt.Errorf("Open: initialize schema metadata: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("Close: db.Close: %w", err)
	}
	return nil
}
