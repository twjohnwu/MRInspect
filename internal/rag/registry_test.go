package rag

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/rag/resources"
)

func retrievalSets() []resources.Set {
	return []resources.Set{{Name: "review", Mode: resources.ModeRetrieval}}
}

// TestNew_DefaultsToSqlite verifies REQ-02 / S-05: an unset backend selects sqlite.
func TestNew_DefaultsToSqlite(t *testing.T) {
	t.Setenv("MRI_RAG_BACKEND", "")

	retriever, err := New("test-store.sqlite", retrievalSets())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := retriever.Name(); got != "sqlite" {
		t.Errorf("Retriever.Name() = %q, want %q", got, "sqlite")
	}
}

// TestNew_UnknownBackendListsRegistered verifies REQ-02 / S-06: unknown names report the registry.
func TestNew_UnknownBackendListsRegistered(t *testing.T) {
	t.Setenv("MRI_RAG_BACKEND", "pinecone")
	Register("custom", func(string, []resources.Set) (Retriever, error) { return fakeRetriever{}, nil })

	_, err := New("test-store.sqlite", retrievalSets())
	if err == nil {
		t.Fatal("New() error = nil, want an error for an unknown backend")
	}
	for _, want := range []string{"pinecone", "sqlite", "custom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("New() error = %q, want it to contain %q", err, want)
		}
	}
}

// TestNew_DisabledYieldsNoop verifies REQ-02 / S-07: disabled RAG returns a degraded noop.
func TestNew_DisabledYieldsNoop(t *testing.T) {
	t.Setenv("MRI_RAG_ENABLED", "false")

	retriever, err := New("test-store.sqlite", retrievalSets())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := retriever.Name(); got != "noop" {
		t.Errorf("Retriever.Name() = %q, want %q", got, "noop")
	}

	result, err := retriever.Retrieve(context.Background(), Query{
		Terms:  []string{"selector"},
		SetRef: "review",
		TopK:   1,
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(result.Chunks) != 0 {
		t.Errorf("len(Retrieve().Chunks) = %d, want 0", len(result.Chunks))
	}
	if !strings.Contains(strings.Join(result.Degraded, "\n"), "rag not configured") {
		t.Errorf("Retrieve().Degraded = %q, want it to contain %q", result.Degraded, "rag not configured")
	}
}

type fakeRetriever struct{}

func (fakeRetriever) Name() string { return "custom" }

func (fakeRetriever) Retrieve(context.Context, Query) (Result, error) { return Result{}, nil }

func (fakeRetriever) Close() error { return nil }
