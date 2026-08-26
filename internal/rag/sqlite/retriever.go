package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"mrinspect/internal/rag/resources"
)

// Query describes one retrieval request for one resource set.
type Query struct {
	Terms  []string
	SetRef string
	Intent string
	TopK   int
}

// Chunk is one retrieved, citeable resource chunk.
type Chunk struct {
	ID          string
	Text        string
	Source      string
	ResourceSet string
	Heading     string
	StartLine   int
	EndLine     int
	TokenEst    int
	Score       *float64
}

// Result is the outcome of one retrieval request.
type Result struct {
	Chunks    []Chunk
	Truncated bool
	Degraded  []string
}

// Retriever reads chunks from one SQLite store.
type Retriever struct {
	db    *sql.DB
	modes map[string]string
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
	return &Retriever{db: db, modes: modes}, nil
}

// Retrieve returns chunks for q.
func (r *Retriever) Retrieve(ctx context.Context, q Query) (Result, error) {
	if r.modes[q.SetRef] == resources.ModeFull {
		return Result{}, fmt.Errorf("Retrieve: resource set %q uses mode full", q.SetRef)
	}
	terms := strings.Join(q.Terms, " OR ")
	if terms == "" || q.TopK <= 0 {
		return Result{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT c.id, c.text, d.rel_path, s.name, c.heading, c.start_line, c.end_line, c.token_est, bm25(chunks_fts) FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid JOIN documents d ON d.id = c.document_id JOIN resource_sets s ON s.id = d.set_id WHERE chunks_fts MATCH ? AND s.name = ? ORDER BY bm25(chunks_fts) LIMIT ?`, terms, q.SetRef, q.TopK)
	if err != nil {
		return Result{}, fmt.Errorf("Retrieve: query: %w", err)
	}
	defer rows.Close()

	var result Result
	for rows.Next() {
		var item Chunk
		var id int64
		var score float64
		if err := rows.Scan(&id, &item.Text, &item.Source, &item.ResourceSet, &item.Heading, &item.StartLine, &item.EndLine, &item.TokenEst, &score); err != nil {
			return Result{}, fmt.Errorf("Retrieve: scan: %w", err)
		}
		item.ID = strconv.FormatInt(id, 10)
		item.Score = &score
		result.Chunks = append(result.Chunks, item)
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("Retrieve: rows: %w", err)
	}
	return result, nil
}
