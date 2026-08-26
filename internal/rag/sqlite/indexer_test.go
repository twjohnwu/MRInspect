package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mrinspect/internal/rag/resources"
)

func indexTestSet(t *testing.T, mode, source string) resources.Set {
	t.Helper()

	docs := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("MkdirAll docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "guide.md"), []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile guide.md: %v", err)
	}
	return resources.Set{Name: "test-set", Mode: mode, Paths: []string{docs}}
}

func assertChunkManifestConsistent(t *testing.T, path string) {
	t.Helper()

	manifestChunks, err := ManifestChunkCount(path)
	if err != nil {
		t.Fatalf("ManifestChunkCount: %v", err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open store for consistency check: %v", err)
	}
	defer store.Close()
	if chunks := countRows(t, store.db, `SELECT count(*) FROM chunks`); chunks != manifestChunks {
		t.Errorf("chunk count = %d, manifest chunk count = %d; want equal", chunks, manifestChunks)
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

// TestIndex_EmbeddingsOffByDefault verifies REQ-08 / S-28: when embeddings are
// not enabled, indexing records no embeddings, leaves the embeddings table empty,
// and retrieves in BM25 order.
func TestIndex_EmbeddingsOffByDefault(t *testing.T) {
	t.Setenv("MRI_RAG_EMBEDDINGS", "")
	output := filepath.Join(t.TempDir(), "store.sqlite")
	// Document order deliberately reverses the expected BM25 rank order: an
	// implementation that returns rowid order instead of BM25 must fail here.
	set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n\n## Error only\nerror secondary result\n\n## Error handling\nerror handling primary result\n")

	stats, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if stats.Embeddings != 0 {
		t.Errorf("IndexStats.Embeddings = %d, want 0 when MRI_RAG_EMBEDDINGS is unset", stats.Embeddings)
	}

	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()
	if got := countRows(t, store.db, `SELECT count(*) FROM embeddings`); got != 0 {
		t.Errorf("embeddings row count = %d, want 0 when MRI_RAG_EMBEDDINGS is unset", got)
	}

	retriever, err := OpenRetriever(output, []resources.Set{set})
	if err != nil {
		t.Fatalf("OpenRetriever: %v", err)
	}
	result, err := retriever.Retrieve(context.Background(), Query{Terms: []string{"error", "handling"}, SetRef: set.Name, TopK: 2})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Chunks) < 2 {
		t.Fatalf("Retrieve returned %d chunks, want at least 2 to verify BM25 ordering", len(result.Chunks))
	}
	if result.Chunks[0].Text != "error handling primary result" {
		t.Errorf("first BM25 chunk text = %q, want %q", result.Chunks[0].Text, "error handling primary result")
	}
}

// TestIndex_FailedWriteLeavesNoPartialStore verifies REQ-03 / S-38: a write
// failure leaves no new store, preserves an existing readable store, and never
// leaves a store whose chunk count differs from its manifest chunk count.
func TestIndex_FailedWriteLeavesNoPartialStore(t *testing.T) {
	writeFailure := errors.New("injected write failure")
	set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n\nold store marker\n")

	t.Run("new output remains absent", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "store.sqlite")
		opts := IndexOptions{
			OutputPath: output,
			Sets:       []resources.Set{set},
		}
		opts.writeError = writeFailure
		_, err := Index(context.Background(), opts)
		if !errors.Is(err, writeFailure) {
			t.Fatalf("Index error = %v, want injected write failure", err)
		}
		if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed index created output %q; stat error = %v, want not exist", output, err)
		}
	})

	t.Run("existing store remains readable and unchanged", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "store.sqlite")
		if _, err := Index(context.Background(), IndexOptions{OutputPath: output, Sets: []resources.Set{set}}); err != nil {
			t.Fatalf("Index old store: %v", err)
		}

		opts := IndexOptions{
			OutputPath: output,
			Sets:       []resources.Set{set},
		}
		opts.writeError = writeFailure
		_, err := Index(context.Background(), opts)
		if !errors.Is(err, writeFailure) {
			t.Fatalf("Index error = %v, want injected write failure", err)
		}

		preserved, err := Open(output)
		if err != nil {
			t.Fatalf("Open preserved store: %v", err)
		}
		defer preserved.Close()
		if got := countRows(t, preserved.db, `SELECT count(*) FROM chunks WHERE text = ?`, "old store marker"); got != 1 {
			t.Errorf("old marker chunks = %d, want 1; failed indexing must not replace a readable store with a partial one", got)
		}
		assertChunkManifestConsistent(t, output)
	})
}

// TestIndex_FullModeSetsAreNotChunked verifies REQ-13 / S-53: indexing a full
// resource set creates no chunks for it and Retrieve rejects that set with an error.
func TestIndex_FullModeSetsAreNotChunked(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := indexTestSet(t, resources.ModeFull, "# Official standard\n\nMust be loaded in full.\n")

	if _, err := Index(context.Background(), IndexOptions{OutputPath: output, Sets: []resources.Set{set}}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()
	if got := countRows(t, store.db, `SELECT count(*) FROM chunks c JOIN documents d ON d.id = c.document_id JOIN resource_sets s ON s.id = d.set_id WHERE s.name = ?`, set.Name); got != 0 {
		t.Errorf("chunks for full set %q = %d, want 0", set.Name, got)
	}

	retriever, err := OpenRetriever(output, []resources.Set{set})
	if err != nil {
		t.Fatalf("OpenRetriever: %v", err)
	}
	if _, err := retriever.Retrieve(context.Background(), Query{SetRef: set.Name}); err == nil {
		t.Errorf("Retrieve(%q) error = nil, want an error because full sets must use FullLoader", set.Name)
	}
}
