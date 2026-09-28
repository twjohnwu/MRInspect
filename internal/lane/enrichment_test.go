package lane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/enrich"
	"mrinspect/internal/testfake"
)

// enrichmentLaneJSON builds a valid lane JSON response (matching Parse's
// expected shape) for the given lane id, mirroring fanoutPromptProvider's
// format so laneId/finding/rationale checks stay consistent across this
// package's fanout tests.
func enrichmentLaneJSON(laneID string) string {
	return fmt.Sprintf(
		`{"laneId":%q,"findings":[{"title":%q,"severity":"medium","rationale":%q}]}`,
		laneID, "finding-"+laneID, "rationale-"+laneID,
	)
}

// TestEnrichment_PerLaneContinuationAndRetrievalInvariant pins REQ-04 / S-11:
// with the enrichment switch on, each lane runs its own independent turn 1
// -> tool execution -> turn 2 chain (no cross-lane leakage of tool results),
// and enabling enrichment does not change the number of rag.Retriever
// Retrieve calls a fan-out makes.
func TestEnrichment_PerLaneContinuationAndRetrievalInvariant(t *testing.T) {
	const laneAID = "lane-a"
	const laneBID = "lane-b"

	registry := loadComposeResourceRegistry(t, `  - name: shared-set
    mode: retrieval
    paths: []
`)
	lanes := []Lane{
		{ID: laneAID, Enabled: true, Intent: "review " + laneAID, Resources: Resources{Sets: []string{"shared-set"}}, TopK: 3},
		{ID: laneBID, Enabled: true, Intent: "review " + laneBID, Resources: Resources{Sets: []string{"shared-set"}}, TopK: 3},
	}

	provider := &testfake.FakeProvider{
		ResponsesByPromptContains: map[string][]testfake.TurnResponse{
			laneAID: {
				{ToolCalls: []ai.ToolCall{{ID: "call_a", Name: "repo_search", Args: []byte(`{"query":"x"}`)}}},
				{Text: enrichmentLaneJSON(laneAID)},
			},
			laneBID: {
				{ToolCalls: []ai.ToolCall{{ID: "call_b", Name: "repo_search", Args: []byte(`{"query":"x"}`)}}},
				{Text: enrichmentLaneJSON(laneBID)},
			},
		},
	}
	retriever := &testfake.FakeRetriever{}

	tempRoot := t.TempDir()
	exec, err := enrich.New(tempRoot, enrich.Limits{MaxCalls: 3, ResultBytes: 8192, ToolTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("enrich.New: %v", err)
	}

	input := fanoutTestInput(t, lanes, provider, "S11-ENRICHMENT-DIFF")
	input.ResourceRegistry = registry
	input.Retriever = retriever

	ctx := context.Background()

	retrievesBeforeEnrichment := len(retriever.RetrieveCalls())

	enrichedInput := input
	enrichedInput.Enrichment = exec
	enrichedResult, err := Fanout(ctx, enrichedInput)
	if err != nil {
		t.Fatalf("Fanout (enrichment on): %v", err)
	}

	retrievesAfterEnrichment := len(retriever.RetrieveCalls())
	enrichmentRetrieveCount := retrievesAfterEnrichment - retrievesBeforeEnrichment

	turnCallsAfterEnrichment := provider.GenerateTurnCalls()
	if got := len(turnCallsAfterEnrichment); got != 4 {
		t.Errorf("GenerateTurnCalls() length after enrichment run = %d, want 4", got)
	}

	var laneATurn2, laneBTurn2 *testfake.TurnCall
	for i, call := range turnCallsAfterEnrichment {
		if len(call.Request.ToolResults) == 0 {
			continue
		}
		switch call.Request.ToolResults[0].ID {
		case "call_a":
			laneATurn2 = &turnCallsAfterEnrichment[i]
		case "call_b":
			laneBTurn2 = &turnCallsAfterEnrichment[i]
		}
	}
	if laneATurn2 == nil {
		t.Errorf("no turn 2 request found with ToolResults[0].ID == %q (no crossover expected)", "call_a")
	}
	if laneBTurn2 == nil {
		t.Errorf("no turn 2 request found with ToolResults[0].ID == %q (no crossover expected)", "call_b")
	}
	if laneATurn2 != nil && laneATurn2.Request.ToolResults[0].ID != "call_a" {
		t.Errorf("lane A turn 2 ToolResults[0].ID = %q, want %q (no crossover)", laneATurn2.Request.ToolResults[0].ID, "call_a")
	}
	if laneBTurn2 != nil && laneBTurn2.Request.ToolResults[0].ID != "call_b" {
		t.Errorf("lane B turn 2 ToolResults[0].ID = %q, want %q (no crossover)", laneBTurn2.Request.ToolResults[0].ID, "call_b")
	}

	if _, ok := fanoutResultByID(enrichedResult.LaneResults, laneAID); !ok {
		t.Errorf("LaneResults missing lane %q with enrichment on: %#v", laneAID, enrichedResult)
	}
	if _, ok := fanoutResultByID(enrichedResult.LaneResults, laneBID); !ok {
		t.Errorf("LaneResults missing lane %q with enrichment on: %#v", laneBID, enrichedResult)
	}
	if len(enrichedResult.Failures) != 0 {
		t.Errorf("Failures with enrichment on = %#v, want none", enrichedResult.Failures)
	}

	disabledInput := input
	disabledInput.Enrichment = nil
	generateCallsBeforeDisabled := len(provider.GenerateCalls())

	if _, err := Fanout(ctx, disabledInput); err != nil {
		t.Fatalf("Fanout (enrichment off): %v", err)
	}

	if got := len(provider.GenerateTurnCalls()); got != len(turnCallsAfterEnrichment) {
		t.Errorf("GenerateTurnCalls() length after disabled run = %d, want unchanged from %d", got, len(turnCallsAfterEnrichment))
	}
	if got := len(provider.GenerateCalls()) - generateCallsBeforeDisabled; got == 0 {
		t.Errorf("GenerateCalls() did not grow with enrichment off, want Generate (not GenerateTurn) used")
	}

	retrievesAfterDisabled := len(retriever.RetrieveCalls())
	disabledRetrieveCount := retrievesAfterDisabled - retrievesAfterEnrichment
	if disabledRetrieveCount != enrichmentRetrieveCount {
		t.Errorf("Retrieve call count with enrichment off = %d, want equal to enrichment-on count %d", disabledRetrieveCount, enrichmentRetrieveCount)
	}
}
