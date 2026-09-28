package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/ai"
	"mrinspect/internal/testfake"
)

func TestEnrichmentFooter_MissingLanesIncludesToolDegradation(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: t.TempDir(), ModelLimits: reviewerModelLimits()})
	r.cfg.Enrichment.Enabled = true
	r.cfg.Enrichment.MaxCalls = 3
	r.SetEnrichment(newEnrichmentExecutor(t, writeEnrichmentRepoFixture(t)))
	r.ai = &testfake.FakeProvider{
		TurnResponses: []testfake.TurnResponse{
			{ToolCalls: []ai.ToolCall{readEnvCall("call-1")}},
			{Text: validEnrichmentReview},
		},
	}

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, "Degraded entries: 1") {
		t.Errorf("single-degradation footer does not include enrichment degradation: %q", note)
	}
	if !strings.Contains(note, "degraded: enrichment path-rejected") {
		t.Errorf("single-degradation footer does not name enrichment degradation: %q", note)
	}
}
