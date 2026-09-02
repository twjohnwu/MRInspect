package ragwire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mrinspect/internal/lane"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/testfake"
)

func TestRetrieveResourceSets_BatchesLaneTermsPerSet(t *testing.T) {
	diff := `--- a/internal/payment/batchProcessor.go
+++ b/internal/payment/batchProcessor.go
@@ -1 +1 @@
-if the oldHandler != nil {}
+if the retryHandler != nil {}
+itemalpha itembravo itemcharlie itemdelta itemecho itemfoxtrot itemgolf itemhotel itemindia itemjuliet itemkilo itemlima itemmike itemnovember itemoscar itempapa itemquebec itemromeo itemsierra itemtango itemuniform itemvictor itemwhiskey itemxray itemyankee itemzulu itemamber itembirch itemcedar itemdahlia itemelmwood itemfirwood itemgranite itemhazel itemivory itemjuniper itemkrypton itemlilac itemmaple itemnickel itemonyx itemquartz itemruby itemsilver itemtopaz
`
	sets := []resources.Set{
		{Name: "standards", Mode: resources.ModeRetrieval},
		{Name: "runbooks", Mode: resources.ModeRetrieval},
		{Name: "normative", Mode: resources.ModeFull},
	}
	retriever := &testfake.FakeRetriever{}
	state := reviewer.ReviewRAGState{}
	terms := lane.TermsFromDiff(diff)

	if err := retrieveResourceSets(context.Background(), retriever, sets, terms, &state); err != nil {
		t.Fatalf("retrieveResourceSets: %v", err)
	}

	calls := retriever.RetrieveCalls()
	if len(calls) != 2 {
		t.Fatalf("Retrieve call count = %d, want one per retrieval set (2)", len(calls))
	}
	for index, call := range calls {
		if !slices.Equal(call.Query.Terms, terms) {
			t.Errorf("Retrieve call %d terms = %v, want batched lane terms %v", index, call.Query.Terms, terms)
		}
		if call.Query.Intent != "review" || call.Query.TopK != 5 {
			t.Errorf("Retrieve call %d query = %+v, want intent review and TopK 5", index, call.Query)
		}
	}
	for _, want := range []string{"batch", "processor", "retry", "handler"} {
		if !slices.Contains(terms, want) {
			t.Errorf("terms = %v, want camel-split term %q", terms, want)
		}
	}
	for _, unwanted := range []string{"the", "if", "nil"} {
		if slices.Contains(terms, unwanted) {
			t.Errorf("terms = %v, want stopword %q filtered", terms, unwanted)
		}
	}
	if len(terms) != 40 {
		t.Errorf("len(terms) = %d, want exact cap of 40", len(terms))
	}
	if slices.Contains(terms, "itemtopaz") {
		t.Errorf("terms = %v, want term beyond cap absent", terms)
	}
}

func TestReviewPath_StoreResolutionFailureNamesReason(t *testing.T) {
	resolverFailure := errors.New("fake resolver failed")
	path := &ReviewPath{
		store: newResolvedStore(rag.ResolverConfig{}, func(context.Context, rag.ResolverConfig) (rag.StoreResolution, error) {
			return rag.StoreResolution{}, resolverFailure
		}),
	}

	state, err := path.RetrieveForReview(context.Background(), "diff")
	if err != nil {
		t.Fatalf("RetrieveForReview: %v", err)
	}
	want := "store unavailable: " + resolverFailure.Error()
	if !slices.ContainsFunc(state.Degraded, func(entry string) bool { return strings.Contains(entry, want) }) {
		t.Errorf("Degraded = %#v, want named store resolution failure %q", state.Degraded, want)
	}
}

// TestS10_ProviderMissingDegradesThroughWiring verifies REQ-03 / S-10: a
// missing embedding provider degrades to BM25 through the production RAG wiring.
func TestS10_ProviderMissingDegradesThroughWiring(t *testing.T) {
	t.Setenv("MRI_RAG_EMBEDDINGS", "true")
	t.Setenv("MRI_RAG_EMBED_KEY", "fixture-key")
	t.Setenv("MRI_RAG_EMBED_PROVIDER", "temporary-value-restored-by-testing")
	if err := os.Unsetenv("MRI_RAG_EMBED_PROVIDER"); err != nil {
		t.Fatalf("Unsetenv MRI_RAG_EMBED_PROVIDER: %v", err)
	}
	t.Setenv("MRI_RAG_SOURCE", "path")
	t.Setenv("MRI_RAG_BACKEND", "sqlite")

	dir := t.TempDir()
	var document strings.Builder
	document.WriteString("# Ranked guidance\n\n")
	for rank := 1; rank <= 6; rank++ {
		fmt.Fprintf(&document, "## Rank %02d\n%s%smarker-%02d\n\n",
			rank,
			strings.Repeat("needle ", rank),
			strings.Repeat("filler ", 7-rank),
			rank,
		)
	}
	documentPath := filepath.Join(dir, "ranked.md")
	if err := os.WriteFile(documentPath, []byte(document.String()), 0o600); err != nil {
		t.Fatalf("write fixture document: %v", err)
	}
	set := resources.Set{Name: "review", Mode: resources.ModeRetrieval, Paths: []string{dir}}
	storePath := filepath.Join(dir, "store.sqlite")
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{
		OutputPath: storePath,
		Sets:       []resources.Set{set},
	}); err != nil {
		t.Fatalf("index fixture without vectors: %v", err)
	}
	t.Setenv("MRI_RAG_STORE", storePath)
	rag.RegisterBuiltinSources(rag.BuiltinSourcesConfig{})

	path := NewReviewPath(ReviewPathConfig{
		ResolverConfig: rag.DefaultResolverConfig(),
		ResourceSets:   []resources.Set{set},
	})
	state, err := path.RetrieveForReview(context.Background(), `--- a/review.go
+++ b/review.go
@@ -1 +1 @@
-old guidance
+needle
`)
	if err != nil {
		t.Fatalf("RetrieveForReview: %v", err)
	}
	if len(state.Chunks) != 5 {
		t.Fatalf("BM25 chunks = %d, want TopK 5", len(state.Chunks))
	}
	for index := 1; index < len(state.Chunks); index++ {
		previous, current := state.Chunks[index-1].Score, state.Chunks[index].Score
		if previous == nil || current == nil {
			t.Fatalf("BM25 scores at ranks %d/%d = (%v, %v), want non-nil", index, index+1, previous, current)
		}
		if *previous >= *current {
			t.Errorf("BM25 scores at ranks %d/%d = (%g, %g), want ascending SQLite BM25 order", index, index+1, *previous, *current)
		}
	}
	reason := strings.Join(state.Degraded, " | ")
	if !strings.Contains(reason, "MRI_EMBED_PROVIDER") {
		t.Errorf("Degraded = %q, want missing provider initialization reason containing MRI_EMBED_PROVIDER", reason)
	}
}
