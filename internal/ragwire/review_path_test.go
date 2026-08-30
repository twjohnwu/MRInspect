package ragwire

import (
	"context"
	"slices"
	"testing"

	"mrinspect/internal/lane"
	"mrinspect/internal/rag/resources"
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
