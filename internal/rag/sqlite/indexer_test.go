package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/embed"
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

func fileSHA256(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %q for sha256: %v", path, err)
	}
	return sha256.Sum256(content)
}

func indexedResourcesFingerprint(t *testing.T, output string, sets []resources.Set) string {
	t.Helper()

	if _, err := Index(context.Background(), IndexOptions{OutputPath: output, Sets: sets}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()

	var fingerprint string
	if err := store.db.QueryRow(`SELECT resources_sha256 FROM schema_meta WHERE id = 1`).Scan(&fingerprint); err != nil {
		t.Fatalf("query resources_sha256: %v", err)
	}
	return fingerprint
}

func TestIndex_WritesResourcesFingerprint(t *testing.T) {
	retrievalSet := indexTestSet(t, resources.ModeRetrieval, "# Guide\n\nretrieval content\n")
	retrievalSet.Name = "retrieval-set"
	fullSet := indexTestSet(t, resources.ModeFull, "# Standard\n\nfull content\n")
	fullSet.Name = "full-set"
	sets := []resources.Set{retrievalSet, fullSet}

	first := indexedResourcesFingerprint(t, filepath.Join(t.TempDir(), "first.sqlite"), sets)
	second := indexedResourcesFingerprint(t, filepath.Join(t.TempDir(), "second.sqlite"), sets)

	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Errorf("resources_sha256 = %q, want exactly 64 lowercase hex characters", first)
	}
	if second != first {
		t.Errorf("resources_sha256 differs for identical fixture tree: first %q, second %q", first, second)
	}
}

func TestIndex_ResourcesFingerprintTracksContent(t *testing.T) {
	set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n\noriginal content\n")
	path := filepath.Join(set.Paths[0], "guide.md")
	baseline := indexedResourcesFingerprint(t, filepath.Join(t.TempDir(), "baseline.sqlite"), []resources.Set{set})

	if err := os.WriteFile(path, []byte("# Guide\n\noriginal contenT\n"), 0o644); err != nil {
		t.Fatalf("change one byte in guide.md: %v", err)
	}
	contentChanged := indexedResourcesFingerprint(t, filepath.Join(t.TempDir(), "content.sqlite"), []resources.Set{set})
	if contentChanged == baseline {
		t.Errorf("resources_sha256 unchanged after one-byte content change: %q", baseline)
	}

	renamedPath := filepath.Join(set.Paths[0], "renamed.md")
	if err := os.Rename(path, renamedPath); err != nil {
		t.Fatalf("rename guide.md: %v", err)
	}
	renamed := indexedResourcesFingerprint(t, filepath.Join(t.TempDir(), "renamed.sqlite"), []resources.Set{set})
	if renamed == contentChanged {
		t.Errorf("resources_sha256 unchanged after file rename: %q", contentChanged)
	}
}

func indexTestSetWithChunks(t *testing.T, count int) resources.Set {
	t.Helper()

	var source strings.Builder
	for index := range count {
		fmt.Fprintf(&source, "## Chunk %03d\nretrieval body %03d\n\n", index, index)
	}
	return indexTestSet(t, resources.ModeRetrieval, source.String())
}

func TestIndex_EmbedsHeadingWhenTextEmpty(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := indexTestSet(t, resources.ModeRetrieval, "# Heading only   \n\n## Normal\nnormal chunk body\n")
	fixture := embed.NewFixture(4)
	var received []string
	fixture.FailOn = func(_ int, texts []string) error {
		received = append(received, append([]string(nil), texts...)...)
		return nil
	}

	if _, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
		Embedder:   fixture,
	}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()

	rows, err := store.db.Query(`SELECT heading, text FROM chunks ORDER BY id`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	var headings, texts []string
	for rows.Next() {
		var heading, text string
		if err := rows.Scan(&heading, &text); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		headings = append(headings, heading)
		texts = append(texts, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate chunks: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("embedder received %d inputs, want 2: %q", len(received), received)
	}
	if len(headings) != 2 || len(texts) != 2 {
		t.Fatalf("stored chunks = %d, want 2", len(texts))
	}
	if received[0] != strings.TrimSpace(headings[0]) {
		t.Errorf("heading-only embed input = %q, want trimmed heading %q", received[0], strings.TrimSpace(headings[0]))
	}
	if received[1] != texts[1] {
		t.Errorf("normal embed input = %q, want chunk text %q", received[1], texts[1])
	}
	if got := countRows(t, store.db, `SELECT count(*) FROM embeddings`); got != 2 {
		t.Errorf("embeddings row count = %d, want 2", got)
	}
}

func TestIndex_FailsWhenChunkHasNoTextOrHeading(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := indexTestSet(t, resources.ModeRetrieval, " \n")

	_, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
		Embedder:   embed.NewFixture(4),
	})
	if err == nil {
		t.Fatal("Index error = nil, want empty embedding input failure")
	}
	if want := "embed: chunk 1 has no text or heading"; !strings.Contains(err.Error(), want) {
		t.Errorf("Index error = %q, want it to contain %q", err, want)
	}
}

// TestS04_IndexWritesEmbeddings verifies REQ-02 / S-04: enabling embeddings
// writes one correctly sized vector per retrieval chunk and records its model,
// dimension, and completed count.
func TestS04_IndexWritesEmbeddings(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := indexTestSetWithChunks(t, 3)
	fixture := embed.NewFixture(4)

	stats, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
		Embedder:   fixture,
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()

	if got := countRows(t, store.db, `SELECT count(*) FROM embeddings`); got != 3 {
		t.Errorf("embeddings row count = %d, want 3", got)
	}
	rows, err := store.db.Query(`SELECT chunk_id, length(vec) FROM embeddings ORDER BY chunk_id`)
	if err != nil {
		t.Fatalf("query embedding vector sizes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var chunkID int64
		var blobBytes int
		if err := rows.Scan(&chunkID, &blobBytes); err != nil {
			t.Fatalf("scan embedding vector size: %v", err)
		}
		if blobBytes != 4*4 {
			t.Errorf("embedding for chunk %d is %d bytes, want 16 (4 float32 values)", chunkID, blobBytes)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate embedding vector sizes: %v", err)
	}

	var model string
	var dimension int
	if err := store.db.QueryRow(`SELECT embed_model, embed_dim FROM schema_meta WHERE id = 1`).Scan(&model, &dimension); err != nil {
		t.Fatalf("query embedding schema metadata: %v", err)
	}
	if model != fixture.Model() {
		t.Errorf("schema_meta.embed_model = %q, want %q", model, fixture.Model())
	}
	if dimension != fixture.Dim() {
		t.Errorf("schema_meta.embed_dim = %d, want %d", dimension, fixture.Dim())
	}
	if stats.Embeddings != 3 {
		t.Errorf("IndexStats.Embeddings = %d, want 3", stats.Embeddings)
	}
	// Index currently has no logger/writer seam, so RED cannot capture the
	// required pre-embedding chunk-total cost line. IndexStats is asserted here;
	// add the output assertion once production exposes an existing-style seam.
}

// TestS05_FlagOffIndexUnchanged verifies REQ-02 / S-05: without an embedder,
// indexing leaves vector rows and embedding metadata at their flag-off values.
func TestS05_FlagOffIndexUnchanged(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := indexTestSetWithChunks(t, 3)

	stats, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	store, err := Open(output)
	if err != nil {
		t.Fatalf("Open indexed store: %v", err)
	}
	defer store.Close()
	if got := countRows(t, store.db, `SELECT count(*) FROM embeddings`); got != 0 {
		t.Errorf("embeddings row count = %d, want 0", got)
	}

	var model string
	var dimension int
	if err := store.db.QueryRow(`SELECT embed_model, embed_dim FROM schema_meta WHERE id = 1`).Scan(&model, &dimension); err != nil {
		t.Fatalf("query embedding schema metadata: %v", err)
	}
	if model != "" {
		t.Errorf("schema_meta.embed_model = %q, want empty", model)
	}
	if dimension != 0 {
		t.Errorf("schema_meta.embed_dim = %d, want 0", dimension)
	}
	if stats.Embeddings != 0 {
		t.Errorf("IndexStats.Embeddings = %d, want 0", stats.Embeddings)
	}
}

// TestS06_EmbedFailureLeavesStoreIntact verifies REQ-02 / S-06: an embedding
// failure aborts a rebuild before atomic replacement, preserving the prior
// valid store byte-for-byte.
func TestS06_EmbedFailureLeavesStoreIntact(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	baselineSet := indexTestSet(t, resources.ModeRetrieval, "# Baseline\n\nold store marker\n")
	if _, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{baselineSet},
	}); err != nil {
		t.Fatalf("Index baseline store: %v", err)
	}
	before := fileSHA256(t, output)

	fixture := embed.NewFixture(4)
	fixture.ErrAt = 2
	replacementSet := indexTestSetWithChunks(t, 65)
	_, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{replacementSet},
		Embedder:   fixture,
	})
	if err == nil {
		t.Errorf("Index error = nil, want fixture failure on second Embed call")
	}
	if calls := fixture.Calls(); calls != 2 {
		t.Errorf("fixture Embed calls = %d, want 2", calls)
	}

	after := fileSHA256(t, output)
	if after != before {
		t.Errorf("OutputPath sha256 changed after embedding failure: before %x, after %x", before, after)
	}
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
	result, err := retriever.Retrieve(context.Background(), rag.Query{Terms: []string{"error", "handling"}, SetRef: set.Name, TopK: 2})
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
	if _, err := retriever.Retrieve(context.Background(), rag.Query{SetRef: set.Name}); err == nil {
		t.Errorf("Retrieve(%q) error = nil, want an error because full sets must use FullLoader", set.Name)
	}
}

// TestIndex_AppliesIncludeExclude verifies REQ-03 / T28: each resource set's
// include and exclude patterns determine which files become documents.
func TestIndex_AppliesIncludeExclude(t *testing.T) {
	t.Run("include selects markdown only", func(t *testing.T) {
		set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n")
		if err := os.WriteFile(filepath.Join(set.Paths[0], "config.yaml"), []byte("name: config\n"), 0o644); err != nil {
			t.Fatalf("WriteFile config.yaml: %v", err)
		}
		set.Include = []string{"*.md"}

		output := filepath.Join(t.TempDir(), "store.sqlite")
		if _, err := Index(context.Background(), IndexOptions{OutputPath: output, Sets: []resources.Set{set}}); err != nil {
			t.Fatalf("Index: %v", err)
		}
		store, err := Open(output)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer store.Close()
		if got := countRows(t, store.db, `SELECT count(*) FROM documents`); got != 1 {
			t.Errorf("documents = %d, want 1 (.md only)", got)
		}
		if got := countRows(t, store.db, `SELECT count(*) FROM documents WHERE rel_path = 'guide.md'`); got != 1 {
			t.Errorf("guide.md documents = %d, want 1", got)
		}
	})

	t.Run("exclude drops matching markdown", func(t *testing.T) {
		set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n")
		if err := os.WriteFile(filepath.Join(set.Paths[0], "skip.md"), []byte("# Skip\n"), 0o644); err != nil {
			t.Fatalf("WriteFile skip.md: %v", err)
		}
		set.Exclude = []string{"skip*"}

		output := filepath.Join(t.TempDir(), "store.sqlite")
		if _, err := Index(context.Background(), IndexOptions{OutputPath: output, Sets: []resources.Set{set}}); err != nil {
			t.Fatalf("Index: %v", err)
		}
		store, err := Open(output)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer store.Close()
		if got := countRows(t, store.db, `SELECT count(*) FROM documents`); got != 1 {
			t.Errorf("documents = %d, want 1 (guide.md only)", got)
		}
		if got := countRows(t, store.db, `SELECT count(*) FROM documents WHERE rel_path = 'guide.md'`); got != 1 {
			t.Errorf("guide.md documents = %d, want 1", got)
		}
		if got := countRows(t, store.db, `SELECT count(*) FROM documents WHERE rel_path = 'skip.md'`); got != 0 {
			t.Errorf("skip.md documents = %d, want 0", got)
		}
	})
}

func TestIndex_CreatesMissingOutputDirectory(t *testing.T) {
	output := filepath.Join(t.TempDir(), "missing", "nested", "store.sqlite")
	set := indexTestSet(t, resources.ModeRetrieval, "# Guide\n\nindex this resource\n")

	stats, err := Index(context.Background(), IndexOptions{
		OutputPath: output,
		Sets:       []resources.Set{set},
	})
	if err != nil {
		t.Fatalf("Index into missing output directory: %v", err)
	}
	if stats.FilesIndexed != 1 {
		t.Errorf("IndexStats.FilesIndexed = %d, want 1", stats.FilesIndexed)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("Stat indexed store: %v", err)
	}
}
