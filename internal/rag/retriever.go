// Package rag defines the shared retrieval contracts (REQ-02).
package rag

import "context"

// Query describes one retrieval request for one resource set (REQ-02).
// It is the canonical shared form; a later task migrates sqlite to use it.
type Query struct {
	Terms  []string
	SetRef string
	Intent string
	TopK   int
}

// Chunk is one retrieved, citeable resource chunk (REQ-02).
// It is the canonical shared form; a later task migrates sqlite to use it.
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

// Result is the outcome of a retrieval request (REQ-02).
// It is the canonical shared form; a later task migrates sqlite to use it.
type Result struct {
	Chunks    []Chunk
	Truncated bool
	Degraded  []string
}

// Retriever is the backend contract selected by the REQ-02 registry.
// Name is required by S-05 and S-07; Retrieve and Close match sqlite's API.
type Retriever interface {
	Name() string
	Retrieve(context.Context, Query) (Result, error)
	Close() error
}

// FullDoc is one normative resource loaded as its original bytes (REQ-13).
type FullDoc struct {
	Source      string
	ResourceSet string
	Bytes       []byte
	TokenEst    int
}

// FullResult reports full documents and named loading degradations (REQ-13).
type FullResult struct {
	Docs     []FullDoc
	Degraded []string
}

// FullLoader loads full-mode resources independently of Retriever (REQ-13).
type FullLoader interface {
	LoadFull(context.Context, []string) (FullResult, error)
}
