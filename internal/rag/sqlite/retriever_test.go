package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/resources"
)

func retrievalSet(t *testing.T, name string, files map[string]string) resources.Set {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for file, text := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", file, err)
		}
	}
	return resources.Set{Name: name, Mode: resources.ModeRetrieval, Paths: []string{dir}}
}

func indexedRetriever(t *testing.T, sets ...resources.Set) (*Retriever, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.sqlite")
	if _, err := Index(context.Background(), IndexOptions{OutputPath: path, Sets: sets}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	r, err := OpenRetriever(path, sets)
	if err != nil {
		t.Fatalf("OpenRetriever: %v", err)
	}
	t.Cleanup(func() {
		if r.db != nil {
			_ = r.db.Close()
		}
	})
	return r, path
}

func retrieve(t *testing.T, r *Retriever, set string, terms []string, topK int) rag.Result {
	t.Helper()
	got, err := r.Retrieve(context.Background(), rag.Query{Terms: terms, SetRef: set, TopK: topK})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	return got
}

func ids(chunks []rag.Chunk) []string {
	got := make([]string, len(chunks))
	for i := range chunks {
		got[i] = chunks[i].ID
	}
	return got
}

func sameResult(got, want rag.Result) bool {
	if got.Truncated != want.Truncated || !reflect.DeepEqual(got.Degraded, want.Degraded) || len(got.Chunks) != len(want.Chunks) {
		return false
	}
	for i := range got.Chunks {
		left, right := got.Chunks[i], want.Chunks[i]
		if left.ID != right.ID || left.Text != right.Text || left.Source != right.Source || left.ResourceSet != right.ResourceSet || left.Heading != right.Heading || left.StartLine != right.StartLine || left.EndLine != right.EndLine || left.TokenEst != right.TokenEst || (left.Score == nil) != (right.Score == nil) || (left.Score != nil && *left.Score != *right.Score) {
			return false
		}
	}
	return true
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if strings.Contains(v, want) {
			return true
		}
	}
	return false
}

// TestRetrieve_BM25RanksAndCitesSource verifies REQ-04 / S-14.
func TestRetrieve_BM25RanksAndCitesSource(t *testing.T) {
	set := retrievalSet(t, "guides", map[string]string{"guide.md": "# Guide\n\n## Noise\nunrelated gardening\n\n## Error handling\nerror handling primary result\n"})
	r, _ := indexedRetriever(t, set)
	got := retrieve(t, r, set.Name, []string{"error", "handling"}, 2)
	if len(got.Chunks) == 0 {
		t.Fatal("Retrieve returned no matching chunks")
	}
	first := got.Chunks[0]
	if first.Text != "error handling primary result" || first.Source != "guide.md" || first.Heading != "Guide > Error handling" || first.StartLine != 6 || first.Score == nil {
		t.Errorf("first chunk = %+v, want cited error-handling chunk with non-nil Score", first)
	}
}

// TestRetrieve_SetRefIsolatesSet verifies REQ-04 / S-15.
func TestRetrieve_SetRefIsolatesSet(t *testing.T) {
	official := retrievalSet(t, "official-standards", map[string]string{"official.md": "# Official\n\n## Validation\nofficial validation marker\n"})
	api := retrievalSet(t, "api-contracts", map[string]string{"api.md": "# API\n\n## Validation\napi validation marker\n"})
	r, _ := indexedRetriever(t, official, api)
	got := retrieve(t, r, api.Name, []string{"validation"}, 10)
	if len(got.Chunks) == 0 {
		t.Fatal("Retrieve returned no api-contract chunks")
	}
	for _, item := range got.Chunks {
		if item.ResourceSet != api.Name || strings.Contains(item.Text, "official") {
			t.Errorf("chunk = %+v, want only %q content", item, api.Name)
		}
	}
}

// TestRetrieve_RespectsTopK verifies REQ-04 / S-16.
func TestRetrieve_RespectsTopK(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Guide\n\n")
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "## Item %02d\nneedle %s\n\n", i, strings.Repeat("needle ", i))
	}
	set := retrievalSet(t, "many", map[string]string{"many.md": b.String()})
	r, _ := indexedRetriever(t, set)
	all := retrieve(t, r, set.Name, []string{"needle"}, 20)
	got := retrieve(t, r, set.Name, []string{"needle"}, 5)
	if len(got.Chunks) != 5 {
		t.Errorf("chunks = %d, want exactly 5", len(got.Chunks))
	}
	if len(all.Chunks) != 20 {
		t.Fatalf("unbounded candidate result = %d, want 20", len(all.Chunks))
	}
	if !reflect.DeepEqual(ids(got.Chunks), ids(all.Chunks[:5])) {
		t.Errorf("TopK IDs = %v, want BM25-best five %v", ids(got.Chunks), ids(all.Chunks[:5]))
	}
}

// TestRetrieve_ConcurrentSafe verifies REQ-04 / S-17.
func TestRetrieve_ConcurrentSafe(t *testing.T) {
	sets := make([]resources.Set, 8)
	for i := range sets {
		sets[i] = retrievalSet(t, fmt.Sprintf("set-%d", i), map[string]string{"doc.md": fmt.Sprintf("# Doc\n\n## Match\nneedle set-%d\n", i)})
	}
	r, _ := indexedRetriever(t, sets...)
	want := make([]rag.Result, len(sets))
	for i, set := range sets {
		want[i] = retrieve(t, r, set.Name, []string{"needle"}, 5)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(sets))
	for i, set := range sets {
		wg.Add(1)
		go func(i int, set resources.Set) {
			defer wg.Done()
			result, err := r.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 5})
			if err != nil {
				errs <- err
				return
			}
			if !sameResult(result, want[i]) {
				errs <- fmt.Errorf("%s result = %+v, want %+v", set.Name, result, want[i])
			}
		}(i, set)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestRetrieve_MissingStoreDegrades verifies REQ-07 / S-24.
func TestRetrieve_MissingStoreDegrades(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	set := resources.Set{Name: "known", Mode: resources.ModeRetrieval}
	r, err := OpenRetriever(missing, []resources.Set{set})
	if err != nil {
		t.Fatalf("OpenRetriever: %v", err)
	}
	got, err := r.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 1})
	if err != nil || len(got.Chunks) != 0 || !contains(got.Degraded, "store") {
		t.Errorf("Retrieve = (%+v, %v), want zero chunks, nil error, named missing-store degradation", got, err)
	}
}

// TestRetrieve_SchemaMismatchDegrades verifies REQ-07 / S-25.
func TestRetrieve_SchemaMismatchDegrades(t *testing.T) {
	set := retrievalSet(t, "known", map[string]string{"doc.md": "# Doc\n\nneedle\n"})
	r, path := indexedRetriever(t, set)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET schema_version = ? WHERE id = 1`, SchemaVersion+1); err != nil {
		t.Fatalf("alter schema_meta: %v", err)
	}
	_ = db.Close()
	got, err := r.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 1})
	if err != nil || len(got.Chunks) != 0 || !contains(got.Degraded, fmt.Sprintf("%d", SchemaVersion+1)) || !contains(got.Degraded, fmt.Sprintf("%d", SchemaVersion)) {
		t.Errorf("Retrieve = (%+v, %v), want zero chunks, nil error, and actual/expected schema versions", got, err)
	}
}

// TestRetrieve_AllUnknownSelectorsReturnEmpty verifies REQ-04, REQ-07 / S-26.
func TestRetrieve_AllUnknownSelectorsReturnEmpty(t *testing.T) {
	a := retrievalSet(t, "indexed-a", map[string]string{"a.md": "# A\n\nneedle from a\n"})
	b := retrievalSet(t, "indexed-b", map[string]string{"b.md": "# B\n\nneedle from b\n"})
	r, _ := indexedRetriever(t, a, b)
	got, err := r.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: "unknown-set", TopK: 5})
	if err != nil || len(got.Chunks) != 0 || !contains(got.Degraded, "unknown-set") {
		t.Errorf("Retrieve = (%+v, %v), want zero chunks, nil error, and named unknown SetRef degradation", got, err)
	}
}

type fixtureEmbedder struct{}

func (fixtureEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		if text == "needle" || !strings.Contains(text, "BM25-first") {
			vectors[index] = []float32{1, 0}
			continue
		}
		vectors[index] = []float32{0, 1}
	}
	return vectors, nil
}

func (fixtureEmbedder) Model() string { return "fixture-rerank" }
func (fixtureEmbedder) Dim() int      { return 2 }

// TestRetrieve_RerankReordersWithinCandidates verifies REQ-08 / S-29.
func TestRetrieve_RerankReordersWithinCandidates(t *testing.T) {
	t.Setenv("MRI_RAG_EMBEDDINGS", "false")
	t.Setenv("MRI_RAG_EMBED_KEY", "fixture-key")
	set := retrievalSet(t, "rerank", map[string]string{"doc.md": "# Doc\n\n## First\nneedle needle BM25-first\n\n## Second\nneedle BM25-second\n"})
	r, path := indexRetrieverWithOptions(t, set, fixtureEmbedder{})
	baseline := retrieve(t, r, set.Name, []string{"needle"}, 2)
	if len(baseline.Chunks) != 2 {
		t.Fatalf("BM25 candidates = %d, want 2", len(baseline.Chunks))
	}
	t.Setenv("MRI_RAG_EMBEDDINGS", "true")
	reranker, err := OpenRetriever(path, []resources.Set{set}, WithEmbedder(fixtureEmbedder{}))
	if err != nil {
		t.Fatalf("OpenRetriever with fixture embedder: %v", err)
	}
	t.Cleanup(func() { _ = reranker.Close() })
	got := retrieve(t, reranker, set.Name, []string{"needle"}, 2)
	if !reflect.DeepEqual(ids(got.Chunks), []string{baseline.Chunks[1].ID, baseline.Chunks[0].ID}) {
		t.Errorf("reranked IDs = %v, want fixture cosine order %v", ids(got.Chunks), []string{baseline.Chunks[1].ID, baseline.Chunks[0].ID})
	}
	if reflect.DeepEqual(ids(got.Chunks), ids(baseline.Chunks)) {
		t.Errorf("reranked IDs = BM25 IDs %v; S-29 requires an order that DIFFERs from BM25", ids(got.Chunks))
	}
	candidates := map[string]bool{}
	for _, item := range baseline.Chunks {
		candidates[item.ID] = true
	}
	for _, item := range got.Chunks {
		if !candidates[item.ID] {
			t.Errorf("rerank introduced non-candidate chunk %q", item.ID)
		}
	}
}

type testVectorEmbedder struct {
	model      string
	targetText string
	allSame    bool
	err        error
	calls      int
}

func (fixture *testVectorEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fixture.calls++
	if fixture.err != nil {
		return nil, fixture.err
	}
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		if fixture.allSame || text == "needle" || text == fixture.targetText {
			vectors[index] = []float32{1, 0}
			continue
		}
		vectors[index] = []float32{0, 1}
	}
	return vectors, nil
}

func (fixture *testVectorEmbedder) Model() string { return fixture.model }
func (*testVectorEmbedder) Dim() int              { return 2 }
func (fixture *testVectorEmbedder) Calls() int    { return fixture.calls }

func rankedNeedleSet(t *testing.T, count int) resources.Set {
	t.Helper()
	var source strings.Builder
	for rank := 1; rank <= count; rank++ {
		fmt.Fprintf(&source, "## Candidate %02d\n%s marker-%02d\n\n", rank, strings.TrimSpace(strings.Repeat("needle ", count-rank+1)), rank)
	}
	return retrievalSet(t, "embedding-ranks", map[string]string{"ranked.md": source.String()})
}

func indexRetrieverWithOptions(t *testing.T, set resources.Set, indexEmbedder embed.Embedder, options ...RetrieverOption) (*Retriever, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.sqlite")
	if _, err := Index(context.Background(), IndexOptions{
		OutputPath: path,
		Sets:       []resources.Set{set},
		Embedder:   indexEmbedder,
	}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	retriever, err := OpenRetriever(path, []resources.Set{set}, options...)
	if err != nil {
		t.Fatalf("OpenRetriever: %v", err)
	}
	t.Cleanup(func() { _ = retriever.Close() })
	return retriever, path
}

func assertStrictBM25Order(t *testing.T, chunks []rag.Chunk) {
	t.Helper()
	for index := 1; index < len(chunks); index++ {
		if chunks[index-1].Score == nil || chunks[index].Score == nil {
			t.Fatalf("BM25 scores at ranks %d/%d = (%v, %v), want non-nil", index, index+1, chunks[index-1].Score, chunks[index].Score)
		}
		if *chunks[index-1].Score >= *chunks[index].Score {
			t.Fatalf("BM25 scores at ranks %d/%d = (%g, %g), want strictly increasing (lower is better)", index, index+1, *chunks[index-1].Score, *chunks[index].Score)
		}
	}
}

// TestS07_RerankWidensAndReorders verifies REQ-03 / S-07: reranking widens
// BM25 to four times TopK, reads candidate vectors from the store, embeds only
// the query, and can promote a candidate from inside (but not outside) that window.
func TestS07_RerankWidensAndReorders(t *testing.T) {
	t.Setenv(embeddingsEnv, "false")
	t.Setenv(embedKeyEnv, "fixture-key")
	set := rankedNeedleSet(t, 12)
	baselineRetriever, _ := indexRetrieverWithOptions(t, set, nil)
	baseline := retrieve(t, baselineRetriever, set.Name, []string{"needle"}, 12)
	if len(baseline.Chunks) != 12 {
		t.Fatalf("BM25 chunks = %d, want 12", len(baseline.Chunks))
	}
	assertStrictBM25Order(t, baseline.Chunks)

	t.Run("rank 5 is promoted for TopK 3", func(t *testing.T) {
		target := baseline.Chunks[4]
		storeEmbedder := &testVectorEmbedder{model: "rank-fixture", targetText: target.Text}
		queryEmbedder := &testVectorEmbedder{model: storeEmbedder.model, targetText: target.Text}
		reranker, _ := indexRetrieverWithOptions(t, set, storeEmbedder, WithEmbedder(queryEmbedder))
		t.Setenv(embeddingsEnv, "true")

		got := retrieve(t, reranker, set.Name, []string{"needle"}, 3)
		if len(got.Chunks) != 3 {
			t.Fatalf("reranked chunks = %d, want 3", len(got.Chunks))
		}
		if got.Chunks[0].Text != target.Text {
			t.Errorf("first reranked chunk = %q, want BM25 rank 5 %q", got.Chunks[0].Text, target.Text)
		}
		if calls := queryEmbedder.Calls(); calls != 1 {
			t.Errorf("query-time Embed calls = %d, want 1 (query only; candidate vectors must come from SQLite)", calls)
		}
	})

	t.Run("rank 9 stays outside TopK 2 window", func(t *testing.T) {
		target := baseline.Chunks[8]
		storeEmbedder := &testVectorEmbedder{model: "rank-fixture", targetText: target.Text}
		queryEmbedder := &testVectorEmbedder{model: storeEmbedder.model, targetText: target.Text}
		reranker, _ := indexRetrieverWithOptions(t, set, storeEmbedder, WithEmbedder(queryEmbedder))
		t.Setenv(embeddingsEnv, "true")

		got := retrieve(t, reranker, set.Name, []string{"needle"}, 2)
		if len(got.Chunks) != 2 {
			t.Fatalf("reranked chunks = %d, want 2", len(got.Chunks))
		}
		for _, item := range got.Chunks {
			if item.Text == target.Text {
				t.Errorf("BM25 rank 9 appeared in TopK=2 results; widening window must stop at rank 8")
			}
		}
		if calls := queryEmbedder.Calls(); calls != 1 {
			t.Errorf("query-time Embed calls = %d, want 1 (query only)", calls)
		}
	})
}

// TestS08_RerankDegrades verifies REQ-03 / S-08: every unavailable or invalid
// rerank input falls back to BM25 with a bounded, safe, actionable reason.
func TestS08_RerankDegrades(t *testing.T) {
	const (
		storeModel = "stored-model-v1"
		queryModel = "query-model-v2"
	)
	testCases := []struct {
		name             string
		keyMissing       bool
		constructionErr  error
		storeModel       string
		queryModel       string
		queryErr         error
		corruptVector    bool
		wantSubstrings   []string
		rejectSubstrings []string
	}{
		{
			name:           "key missing",
			keyMissing:     true,
			storeModel:     storeModel,
			queryModel:     storeModel,
			wantSubstrings: []string{embedKeyEnv},
		},
		{
			name:            "provider invalid",
			constructionErr: errors.New("embedder construction failed: invalid provider"),
			storeModel:      storeModel,
			wantSubstrings:  []string{"embedder"},
		},
		{
			name:             "store has no vectors",
			queryModel:       storeModel,
			wantSubstrings:   []string{"no vectors"},
			rejectSubstrings: []string{"mismatch"},
		},
		{
			name:           "model mismatch",
			storeModel:     storeModel,
			queryModel:     queryModel,
			wantSubstrings: []string{storeModel, queryModel},
		},
		{
			name:           "query embedding 503 is redacted",
			storeModel:     storeModel,
			queryModel:     storeModel,
			queryErr:       errors.New("POST https://secret.example/?key=abc returned status 503"),
			wantSubstrings: []string{"503"},
			rejectSubstrings: []string{
				"secret.example",
				"abc",
			},
		},
		{
			name:           "corrupt stored vector",
			storeModel:     storeModel,
			queryModel:     storeModel,
			corruptVector:  true,
			wantSubstrings: []string{"vector"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(embeddingsEnv, "false")
			if testCase.keyMissing {
				t.Setenv(embedKeyEnv, "temporary-value-restored-by-testing")
				if err := os.Unsetenv(embedKeyEnv); err != nil {
					t.Fatalf("Unsetenv %s: %v", embedKeyEnv, err)
				}
			} else {
				t.Setenv(embedKeyEnv, "fixture-key")
			}

			set := rankedNeedleSet(t, 5)
			var indexEmbedder embed.Embedder
			if testCase.storeModel != "" {
				indexEmbedder = &testVectorEmbedder{model: testCase.storeModel, allSame: true}
			}
			path := filepath.Join(t.TempDir(), "store.sqlite")
			if _, err := Index(context.Background(), IndexOptions{
				OutputPath: path,
				Sets:       []resources.Set{set},
				Embedder:   indexEmbedder,
			}); err != nil {
				t.Fatalf("Index: %v", err)
			}
			if testCase.corruptVector {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatalf("sql.Open for corruption: %v", err)
				}
				if _, err := db.Exec(`UPDATE embeddings SET vec = substr(vec, 1, length(vec) - 1) WHERE chunk_id = (SELECT min(chunk_id) FROM embeddings)`); err != nil {
					_ = db.Close()
					t.Fatalf("truncate stored vector: %v", err)
				}
				if err := db.Close(); err != nil {
					t.Fatalf("close corruption database: %v", err)
				}
			}

			baselineRetriever, err := OpenRetriever(path, []resources.Set{set})
			if err != nil {
				t.Fatalf("OpenRetriever baseline: %v", err)
			}
			baseline := retrieve(t, baselineRetriever, set.Name, []string{"needle"}, 3)
			if err := baselineRetriever.Close(); err != nil {
				t.Fatalf("close baseline retriever: %v", err)
			}

			var options []RetrieverOption
			if testCase.constructionErr != nil {
				t.Setenv("MRI_RAG_EMBED_PROVIDER", "invalid-provider")
				options = append(options, WithEmbedderError(testCase.constructionErr))
			} else {
				options = append(options, WithEmbedder(&testVectorEmbedder{
					model:   testCase.queryModel,
					allSame: true,
					err:     testCase.queryErr,
				}))
			}
			reranker, err := OpenRetriever(path, []resources.Set{set}, options...)
			if err != nil {
				t.Fatalf("OpenRetriever reranker: %v", err)
			}
			defer reranker.Close()
			t.Setenv(embeddingsEnv, "true")

			got, err := reranker.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 3})
			if err != nil {
				t.Errorf("Retrieve error = %v, want nil degradation fallback", err)
			}
			if !reflect.DeepEqual(ids(got.Chunks), ids(baseline.Chunks)) {
				t.Errorf("fallback IDs = %v, want BM25 order %v", ids(got.Chunks), ids(baseline.Chunks))
			}

			reason := strings.Join(got.Degraded, " | ")
			for _, want := range testCase.wantSubstrings {
				if !strings.Contains(reason, want) {
					t.Errorf("Degraded = %q, want substring %q", reason, want)
				}
			}
			for _, rejected := range testCase.rejectSubstrings {
				if strings.Contains(reason, rejected) {
					t.Errorf("Degraded = %q, must not contain %q", reason, rejected)
				}
			}
			for _, item := range got.Degraded {
				if len(item) > 200 {
					t.Errorf("Degraded reason length = %d, want <= 200: %q", len(item), item)
				}
			}
		})
	}
}

// TestS09_FlagOffRetrieveUnchanged verifies REQ-03 / S-09: flag-off retrieval
// retains the existing TopK+1 cap, BM25 order, truncation, and empty degradation.
func TestS09_FlagOffRetrieveUnchanged(t *testing.T) {
	t.Setenv(embeddingsEnv, "false")
	t.Setenv(embedKeyEnv, "temporary-value-restored-by-testing")
	if err := os.Unsetenv(embedKeyEnv); err != nil {
		t.Fatalf("Unsetenv %s: %v", embedKeyEnv, err)
	}
	set := rankedNeedleSet(t, 5)
	retriever, _ := indexRetrieverWithOptions(t, set, nil)
	all := retrieve(t, retriever, set.Name, []string{"needle"}, 5)
	got, err := retriever.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got.Chunks) != 3 {
		t.Errorf("chunks = %d, want 3", len(got.Chunks))
	}
	if !reflect.DeepEqual(ids(got.Chunks), ids(all.Chunks[:3])) {
		t.Errorf("TopK IDs = %v, want BM25 order %v", ids(got.Chunks), ids(all.Chunks[:3]))
	}
	if !got.Truncated {
		t.Errorf("Truncated = false, want true with five hits and TopK=3")
	}
	if len(got.Degraded) != 0 {
		t.Errorf("Degraded = %v, want empty while reranking is off", got.Degraded)
	}
}

// TestRetrieve_NoEmbedKeyFallsBackToBM25 verifies REQ-08 / S-30.
func TestRetrieve_NoEmbedKeyFallsBackToBM25(t *testing.T) {
	t.Setenv("MRI_RAG_EMBEDDINGS", "true")
	t.Setenv("MRI_RAG_EMBED_KEY", "")
	set := retrievalSet(t, "fallback", map[string]string{"doc.md": "# Doc\n\n## First\nneedle needle first\n\n## Second\nneedle second\n"})
	r, _ := indexedRetriever(t, set)
	got := retrieve(t, r, set.Name, []string{"needle"}, 2)
	if len(got.Chunks) != 2 || got.Chunks[0].Text != "needle needle first" || got.Chunks[1].Text != "needle second" || !contains(got.Degraded, "embedding") {
		t.Errorf("Retrieve = %+v, want pure BM25 order [needle needle first, needle second] and named embedding-disabled degradation", got)
	}
}

// TestRetrieve_TruncatedReflectsBackendCap verifies REQ-04 / S-39.
func TestRetrieve_TruncatedReflectsBackendCap(t *testing.T) {
	set := retrievalSet(t, "cap", map[string]string{"doc.md": "# Doc\n\n## One\nneedle one\n\n## Two\nneedle two\n"})
	r, _ := indexedRetriever(t, set)
	if got := retrieve(t, r, set.Name, []string{"needle"}, 1); !got.Truncated {
		t.Errorf("Truncated = false with hits exceeding TopK, want true")
	}
	if got := retrieve(t, r, set.Name, []string{"needle"}, 3); got.Truncated {
		t.Errorf("Truncated = true with hits not exceeding TopK, want false")
	}
}

// TestRetrieve_TokenEstEnablesBudgeting verifies REQ-04 / S-40.
func TestRetrieve_TokenEstEnablesBudgeting(t *testing.T) {
	set := retrievalSet(t, "tokens", map[string]string{"ascii.md": "# ascii\n\nabcdef\n", "cjk.md": "# cjk\n\n中文\n", "emoji.md": "# emoji\n\n😀😀\n", "hiragana.md": "# hiragana\n\nあい\n"})
	r, _ := indexedRetriever(t, set)
	got := retrieve(t, r, set.Name, []string{"ascii", "cjk", "emoji", "hiragana"}, 10)
	byText := map[string]rag.Chunk{}
	for _, item := range got.Chunks {
		byText[item.Text] = item
		if item.TokenEst != chunk.TokenEst(item.Text) {
			t.Errorf("TokenEst for %q = %d, want formula result %d", item.Text, item.TokenEst, chunk.TokenEst(item.Text))
		}
	}
	for text, want := range map[string]int{"abcdef": 2, "中文": 3, "😀😀": 6, "あい": 3} {
		item, ok := byText[text]
		if !ok {
			t.Errorf("missing chunk %q", text)
			continue
		}
		if item.TokenEst != want {
			t.Errorf("TokenEst(%q) = %d, want %d", text, item.TokenEst, want)
		}
	}
}

// TestRetrieve_CloseIsIdempotent verifies REQ-04 / S-41.
func TestRetrieve_CloseIsIdempotent(t *testing.T) {
	set := retrievalSet(t, "close", map[string]string{"doc.md": "# Doc\n\nneedle\n"})
	r, _ := indexedRetriever(t, set)
	_ = retrieve(t, r, set.Name, []string{"needle"}, 1)
	if err := r.Close(); err != nil {
		t.Errorf("first Close = %v, want nil", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if _, err := r.Retrieve(context.Background(), rag.Query{Terms: []string{"needle"}, SetRef: set.Name, TopK: 1}); err == nil {
		t.Error("Retrieve after Close error = nil, want explicit error")
	}
}
