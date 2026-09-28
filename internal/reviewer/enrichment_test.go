package reviewer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/enrich"
	"mrinspect/internal/testfake"
)

// validEnrichmentReview satisfies the real ValidateReviewContent shape
// (>=100 bytes, "##", "Findings", "Verdict") even though the fixtures below
// use fakeValidator, which bypasses content validation — this keeps the
// fixture text realistic for when a real validator is wired in later.
const validEnrichmentReview = "## Code Review\n\n## Findings\nNo material findings were located in the reviewed diff for this attempt.\n\n## Verdict\napproved"

// installInfoLogRecorder (reviewer_test.go) already captures at Info level,
// which is where the retry-attempt log line this suite asserts on
// ("enrichment: empty final text") is expected to land — reused here.

// writeEnrichmentRepoFixture builds the t.TempDir() repo root used by the
// enrichment round tests: a file repo_search can hit (internal/ai/client.go,
// two NewClient lines) and a denylisted .env file read_file_ranges must
// reject (REQ-03).
func writeEnrichmentRepoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "ai"), 0o755); err != nil {
		t.Fatalf("mkdir internal/ai: %v", err)
	}
	clientGo := "package ai\n\nfunc NewClient() *Client { return &Client{} }\nfunc NewClientWithOptions() *Client { return &Client{} }\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "ai", "client.go"), []byte(clientGo), 0o644); err != nil {
		t.Fatalf("write client.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=leaked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return root
}

func newEnrichmentExecutor(t *testing.T, root string) *enrich.Executor {
	t.Helper()
	exec, err := enrich.New(root, enrich.Limits{MaxCalls: 3, ResultBytes: 8192, ToolTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("enrich.New: %v", err)
	}
	return exec
}

func repoSearchCall(id string) ai.ToolCall {
	return ai.ToolCall{ID: id, Name: "repo_search", Args: []byte(`{"query":"NewClient"}`)}
}

func readEnvCall(id string) ai.ToolCall {
	return ai.ToolCall{ID: id, Name: "read_file_ranges", Args: []byte(`{"path":".env","start":1,"end":1}`)}
}

// TestEnrichment_DisabledPathUnchanged pins REQ-04 / S-02: with the
// enrichment switch off, the review path is unchanged — only Generate is
// called, GenerateTurn is never reached, and the report carries no
// enrichment trace.
func TestEnrichment_DisabledPathUnchanged(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	fake := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{Output: validEnrichmentReview}}
	r.ai = fake
	r.cfg.Enrichment.Enabled = false

	r.Run(context.Background())

	if got := len(fake.GenerateCalls()); got != 1 {
		t.Errorf("GenerateCalls() length = %d, want 1", got)
	}
	if got := len(fake.GenerateTurnCalls()); got != 0 {
		t.Errorf("GenerateTurnCalls() length = %d, want 0", got)
	}
	report := gl.lastNote(t)
	if strings.Contains(strings.ToLower(report), "enrichment") {
		t.Errorf("report contains %q with enrichment disabled: %q", "enrichment", report)
	}
}

// TestEnrichment_RoundLoopAndCallLimit pins REQ-04 / S-09: the single-mode
// round loop executes at most MaxCalls tool calls per attempt, the
// remainder are call-limited, and the degraded outcomes surface in the
// footer's RAG provenance line.
func TestEnrichment_RoundLoopAndCallLimit(t *testing.T) {
	t.Run("main case", func(t *testing.T) {
		root := writeEnrichmentRepoFixture(t)
		r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		fake := &testfake.FakeProvider{
			TurnResponses: []testfake.TurnResponse{
				{ToolCalls: []ai.ToolCall{
					repoSearchCall("call-1"),
					repoSearchCall("call-2"),
					repoSearchCall("call-3"),
					readEnvCall("call-4"),
				}},
				{Text: validEnrichmentReview},
			},
		}
		r.ai = fake
		r.cfg.Enrichment.Enabled = true
		r.cfg.Enrichment.MaxCalls = 3
		r.SetEnrichment(newEnrichmentExecutor(t, root))

		r.Run(context.Background())

		calls := fake.GenerateTurnCalls()
		if len(calls) != 2 {
			t.Fatalf("GenerateTurnCalls() length = %d, want 2", len(calls))
		}
		results := calls[1].Request.ToolResults
		if len(results) != 4 {
			t.Fatalf("turn 2 ToolResults length = %d, want 4", len(results))
		}
		for i := 0; i < 3; i++ {
			if results[i].Error != "" {
				t.Errorf("ToolResults[%d].Error = %q, want empty", i, results[i].Error)
			}
		}
		if results[3].Error != "call-limit" {
			t.Errorf("ToolResults[3].Error = %q, want call-limit", results[3].Error)
		}

		report := gl.lastNote(t)
		if !strings.Contains(report, "RAG provenance:") {
			t.Errorf("report missing RAG provenance line: %q", report)
		}
		if !strings.Contains(report, "degraded: enrichment call-limit") {
			t.Errorf("report missing degraded: enrichment call-limit: %q", report)
		}
		if !strings.Contains(report, "Degraded entries: 1") {
			t.Errorf("report missing Degraded entries: 1: %q", report)
		}
		if !strings.Contains(report, validEnrichmentReview) {
			t.Errorf("report does not contain turn 2 review text: %q", report)
		}
	})

	t.Run("sub-case A: .env read within budget", func(t *testing.T) {
		root := writeEnrichmentRepoFixture(t)
		r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		fake := &testfake.FakeProvider{
			TurnResponses: []testfake.TurnResponse{
				{ToolCalls: []ai.ToolCall{
					repoSearchCall("call-1"),
					readEnvCall("call-2"),
					repoSearchCall("call-3"),
					repoSearchCall("call-4"),
				}},
				{Text: validEnrichmentReview},
			},
		}
		r.ai = fake
		r.cfg.Enrichment.Enabled = true
		r.cfg.Enrichment.MaxCalls = 3
		r.SetEnrichment(newEnrichmentExecutor(t, root))

		r.Run(context.Background())

		calls := fake.GenerateTurnCalls()
		if len(calls) != 2 {
			t.Fatalf("GenerateTurnCalls() length = %d, want 2", len(calls))
		}
		results := calls[1].Request.ToolResults
		if len(results) != 4 {
			t.Fatalf("turn 2 ToolResults length = %d, want 4", len(results))
		}
		if results[1].Error != "path-rejected" {
			t.Errorf("ToolResults[1].Error = %q, want path-rejected", results[1].Error)
		}

		report := gl.lastNote(t)
		if !strings.Contains(report, "enrichment path-rejected") {
			t.Errorf("report missing enrichment path-rejected: %q", report)
		}
		if !strings.Contains(report, "enrichment call-limit") {
			t.Errorf("report missing enrichment call-limit: %q", report)
		}
		if !strings.Contains(report, "Degraded entries: 2") {
			t.Errorf("report missing Degraded entries: 2: %q", report)
		}
	})

	t.Run("sub-case B: no tool calls", func(t *testing.T) {
		root := writeEnrichmentRepoFixture(t)
		r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		fake := &testfake.FakeProvider{
			TurnResponses: []testfake.TurnResponse{
				{Text: validEnrichmentReview},
			},
		}
		r.ai = fake
		r.cfg.Enrichment.Enabled = true
		r.cfg.Enrichment.MaxCalls = 3
		r.SetEnrichment(newEnrichmentExecutor(t, root))

		r.Run(context.Background())

		if got := len(fake.GenerateTurnCalls()); got != 1 {
			t.Errorf("GenerateTurnCalls() length = %d, want 1", got)
		}
		report := gl.lastNote(t)
		if strings.Contains(strings.ToLower(report), "enrichment") {
			t.Errorf("report contains %q with no tool calls requested: %q", "enrichment", report)
		}
	})
}

// TestEnrichment_Turn2EmptyFallsToRetry pins REQ-04 / S-10: a turn 2 that
// returns empty final text ends the attempt with "enrichment: empty final
// text" and falls through to the existing AIRetryAttempts retry, starting
// a fresh (non-continued) turn 1 on the next attempt.
func TestEnrichment_Turn2EmptyFallsToRetry(t *testing.T) {
	root := writeEnrichmentRepoFixture(t)
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	readInfo := installInfoLogRecorder(t, r)
	fake := &testfake.FakeProvider{
		TurnResponses: []testfake.TurnResponse{
			{ToolCalls: []ai.ToolCall{repoSearchCall("call-1")}},              // attempt 1, turn 1
			{Text: "", ToolCalls: []ai.ToolCall{repoSearchCall("call-2")}},     // attempt 1, turn 2: empty text + a call (ignored)
			{Text: validEnrichmentReview},                                    // attempt 2, turn 1: valid review
		},
	}
	r.ai = fake
	r.cfg.Enrichment.Enabled = true
	r.cfg.Enrichment.MaxCalls = 3
	r.cfg.Validation.AIRetryAttempts = 2
	exec := newEnrichmentExecutor(t, root)
	r.SetEnrichment(exec)

	r.Run(context.Background())

	calls := fake.GenerateTurnCalls()
	if len(calls) != 3 {
		t.Fatalf("GenerateTurnCalls() length = %d, want 3", len(calls))
	}
	if calls[2].Request.Continuation != nil {
		t.Errorf("third request Continuation = %#v, want nil (fresh attempt)", calls[2].Request.Continuation)
	}
	if got := exec.Executed(); got != 1 {
		t.Errorf("Executor.Executed() = %d, want 1 (turn 2's call must not run)", got)
	}
	report := gl.lastNote(t)
	if !strings.Contains(report, validEnrichmentReview) {
		t.Errorf("report does not contain attempt 2 review text: %q", report)
	}
	if !strings.Contains(readInfo(), "enrichment: empty final text") {
		t.Errorf("log does not contain %q", "enrichment: empty final text")
	}
}
