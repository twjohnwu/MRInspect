package lane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/testfake"
)

type fanoutProviderOutcome struct {
	laneID string
	err    error
}

type fanoutPromptProvider struct {
	mu       sync.Mutex
	outcomes map[string]fanoutProviderOutcome
	calls    []testfake.ProviderCall
}

func newFanoutPromptProvider(lanes []Lane) *fanoutPromptProvider {
	outcomes := make(map[string]fanoutProviderOutcome, len(lanes))
	for _, declaration := range lanes {
		outcomes[fanoutTemplateMarker(declaration.ID)] = fanoutProviderOutcome{laneID: declaration.ID}
	}
	return &fanoutPromptProvider{outcomes: outcomes}
}

func (p *fanoutPromptProvider) Generate(ctx context.Context, prompt string, opts ai.GenerateOptions) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, testfake.ProviderCall{Context: ctx, Prompt: prompt, Options: opts})
	p.mu.Unlock()

	for marker, outcome := range p.outcomes {
		if !strings.Contains(prompt, marker) {
			continue
		}
		if outcome.err != nil {
			return "", outcome.err
		}
		return fmt.Sprintf(
			`{"laneId":%q,"findings":[{"title":%q,"severity":"medium","rationale":%q}]}`,
			outcome.laneID,
			"finding-"+outcome.laneID,
			"rationale-"+outcome.laneID,
		), nil
	}
	return "", errors.New("provider received a prompt without a known lane template marker")
}

func (*fanoutPromptProvider) Name() string { return "fanout-prompt-provider" }

func (p *fanoutPromptProvider) setError(laneID string, err error) {
	p.outcomes[fanoutTemplateMarker(laneID)] = fanoutProviderOutcome{laneID: laneID, err: err}
}

func (p *fanoutPromptProvider) generateCalls() []testfake.ProviderCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]testfake.ProviderCall(nil), p.calls...)
}

func fanoutTestLanes(ids ...string) []Lane {
	lanes := make([]Lane, len(ids))
	for i, id := range ids {
		lanes[i] = Lane{
			ID:        id,
			Enabled:   true,
			Intent:    "review " + id,
			Resources: Resources{Sets: []string{}, Tags: []string{}},
		}
	}
	return lanes
}

func fanoutTemplateMarker(laneID string) string { return "FANOUT-TEMPLATE::" + laneID }

func fanoutTestInput(t *testing.T, lanes []Lane, provider ai.Provider, diff string) FanoutInput {
	t.Helper()
	t.Setenv("MRI_PROMPT_BUDGET_FACTOR", "1")
	templateDir := t.TempDir()
	for i := range lanes {
		path := filepath.Join(templateDir, lanes[i].ID+".tmpl.md")
		if err := os.WriteFile(path, []byte(fanoutTemplateMarker(lanes[i].ID)), 0o644); err != nil {
			t.Fatalf("write lane template %q: %v", lanes[i].ID, err)
		}
		lanes[i].Template = path
	}
	return FanoutInput{
		Lanes:            lanes,
		Terms:            []string{"shared", "fanout", "terms"},
		ResourceRegistry: resources.Registry{},
		Project: project.LoadedProject{
			System:              project.SystemProject{Name: "Fanout Test System"},
			ResolvedServiceType: "backend",
		},
		Diff: diff,
		MergeRequest: gitlab.MergeRequest{
			IID:          9,
			Title:        "Fanout test MR",
			SourceBranch: "feature/fanout",
			TargetBranch: "main",
			Author:       gitlab.Author{Name: "Fanout Tester"},
		},
		Provider:    provider,
		Attempts:    1,
		GlobalModel: "global-default",
		ModelLimits: map[string]int{"global-default": 1_000_000},
	}
}

func fanoutResultByID(results []LaneResult, laneID string) (LaneResult, bool) {
	for _, result := range results {
		if result.LaneID == laneID {
			return result, true
		}
	}
	return LaneResult{}, false
}

func fanoutFailureByID(failures []LaneFailure, laneID string) (LaneFailure, bool) {
	for _, failure := range failures {
		if failure.LaneID == laneID {
			return failure, true
		}
	}
	return LaneFailure{}, false
}

func assertFanoutFinding(t *testing.T, results []LaneResult, laneID string) {
	t.Helper()
	result, ok := fanoutResultByID(results, laneID)
	if !ok {
		t.Errorf("LaneResults do not contain successful lane %q: %#v", laneID, results)
		return
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "finding-"+laneID {
		t.Errorf("lane %q Findings = %#v, want its provider finding", laneID, result.Findings)
	}
}

// TestFanout_AllLanesInFlightConcurrently verifies REQ-03 / S-08: all enabled
// lanes reach Generate before a shared provider barrier releases any of them.
func TestFanout_AllLanesInFlightConcurrently(t *testing.T) {
	lanes := fanoutTestLanes("lane-one", "lane-two", "lane-three")
	arrived := make(chan struct{}, len(lanes))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	provider := &testfake.FakeProvider{
		DefaultResponse: testfake.ProviderResponse{Output: `{"laneId":"barrier","findings":[{"title":"barrier finding","severity":"low","rationale":"all calls arrived"}]}`},
		Barrier:         &testfake.ProviderBarrier{Arrived: arrived, Release: release},
	}
	input := fanoutTestInput(t, lanes, provider, "S08-CONCURRENT-DIFF")
	type outcome struct {
		result FanoutResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := Fanout(context.Background(), input)
		done <- outcome{result: result, err: err}
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for call := 1; call <= len(lanes); call++ {
		select {
		case <-arrived:
		case <-deadline.C:
			t.Fatalf("only %d/%d Generate calls reached the barrier; want every lane in flight concurrently", call-1, len(lanes))
		}
	}
	releaseAll()

	select {
	case got := <-done:
		if got.err != nil {
			t.Errorf("Fanout error = %v, want nil", got.err)
		}
		if len(got.result.LaneResults) != len(lanes) {
			t.Errorf("LaneResults count = %d, want %d successful lanes", len(got.result.LaneResults), len(lanes))
		}
		if len(got.result.Failures) != 0 {
			t.Errorf("Failures = %#v, want none", got.result.Failures)
		}
		for _, declaration := range lanes {
			if _, ok := fanoutResultByID(got.result.LaneResults, declaration.ID); !ok {
				t.Errorf("LaneResults do not contain completed lane %q", declaration.ID)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fanout did not complete after the provider barrier was released")
	}
}

// TestFanout_OneLaneFailureDoesNotAbortOthers verifies REQ-03 / S-10: one
// named provider failure is isolated while sibling findings still succeed.
func TestFanout_OneLaneFailureDoesNotAbortOthers(t *testing.T) {
	lanes := fanoutTestLanes("lane-one", "lane-two", "lane-three")
	provider := newFanoutPromptProvider(lanes)
	providerErr := errors.New("provider boom for lane-two")
	provider.setError("lane-two", providerErr)

	got, err := Fanout(context.Background(), fanoutTestInput(t, lanes, provider, "S10-PARTIAL-FAILURE-DIFF"))
	if err != nil {
		t.Errorf("Fanout error = %v, want nil for an isolated lane failure", err)
	}
	if len(got.LaneResults) != 2 {
		t.Errorf("LaneResults count = %d, want 2 successful siblings", len(got.LaneResults))
	}
	assertFanoutFinding(t, got.LaneResults, "lane-one")
	assertFanoutFinding(t, got.LaneResults, "lane-three")
	if len(got.Failures) != 1 {
		t.Errorf("Failures count = %d, want exactly 1: %#v", len(got.Failures), got.Failures)
	}
	failure, ok := fanoutFailureByID(got.Failures, "lane-two")
	if !ok {
		t.Errorf("Failures do not contain lane-two: %#v", got.Failures)
	} else {
		if failure.Kind == "" {
			t.Error("lane-two failure Kind is empty, want a named failure kind")
		}
		if !strings.Contains(failure.Reason, providerErr.Error()) {
			t.Errorf("lane-two failure Reason = %q, want it to name %q", failure.Reason, providerErr)
		}
	}
}

// TestFanout_PerLaneModelOverride verifies REQ-03 / S-30: only a lane with a
// model override changes GenerateOptions.Model; siblings use the global model.
func TestFanout_PerLaneModelOverride(t *testing.T) {
	lanes := fanoutTestLanes("lane-one", "lane-two", "lane-three")
	lanes[1].Model = "lane-special"
	provider := newFanoutPromptProvider(lanes)
	input := fanoutTestInput(t, lanes, provider, "S30-MODEL-OVERRIDE-DIFF")
	input.ModelLimits["lane-special"] = 1_000_000

	got, err := Fanout(context.Background(), input)
	if err != nil {
		t.Errorf("Fanout error = %v, want nil", err)
	}
	if len(got.LaneResults) != 3 || len(got.Failures) != 0 {
		t.Errorf("Fanout result = %#v, want 3 successes and no failures", got)
	}
	calls := provider.generateCalls()
	if len(calls) != 3 {
		t.Fatalf("Generate call count = %d, want 3", len(calls))
	}
	for _, call := range calls {
		matched := false
		for _, declaration := range lanes {
			if !strings.Contains(call.Prompt, fanoutTemplateMarker(declaration.ID)) {
				continue
			}
			matched = true
			wantModel := "global-default"
			if declaration.ID == "lane-two" {
				wantModel = "lane-special"
			}
			if call.Options.Model != wantModel {
				t.Errorf("lane %q GenerateOptions.Model = %q, want %q", declaration.ID, call.Options.Model, wantModel)
			}
		}
		if !matched {
			t.Errorf("Generate prompt has no identifiable lane marker: %.120q", call.Prompt)
		}
	}
}

// TestFanout_DisabledLaneNotExecuted verifies REQ-03 / S-32: a disabled lane
// is absent from provider calls, successful results, and failure results.
func TestFanout_DisabledLaneNotExecuted(t *testing.T) {
	lanes := fanoutTestLanes("lane-one", "lane-disabled", "lane-three")
	lanes[1].Enabled = false
	provider := newFanoutPromptProvider(lanes)

	got, err := Fanout(context.Background(), fanoutTestInput(t, lanes, provider, "S32-DISABLED-DIFF"))
	if err != nil {
		t.Errorf("Fanout error = %v, want nil", err)
	}
	calls := provider.generateCalls()
	if len(calls) != 2 {
		t.Errorf("Generate call count = %d, want exactly 2 enabled lanes", len(calls))
	}
	for _, call := range calls {
		if strings.Contains(call.Prompt, fanoutTemplateMarker("lane-disabled")) {
			t.Error("disabled lane reached Provider.Generate")
		}
	}
	if len(got.LaneResults) != 2 {
		t.Errorf("LaneResults count = %d, want exactly 2 enabled lanes", len(got.LaneResults))
	}
	if _, ok := fanoutResultByID(got.LaneResults, "lane-disabled"); ok {
		t.Error("disabled lane unexpectedly appears in LaneResults")
	}
	if _, ok := fanoutFailureByID(got.Failures, "lane-disabled"); ok {
		t.Error("disabled lane unexpectedly appears in Failures")
	}
}

// TestFanout_ComposeHardFailureIsolated verifies REQ-03 / S-33: an MR-data
// budget failure is lane-local, but an unknown model is a preflight error.
func TestFanout_ComposeHardFailureIsolated(t *testing.T) {
	t.Run("budget failure is isolated", func(t *testing.T) {
		lanes := fanoutTestLanes("lane-one", "lane-too-small", "lane-three")
		lanes[1].Model = "tiny-context"
		provider := newFanoutPromptProvider(lanes)
		diff := strings.Repeat("+oversized shared diff token ", 128)
		input := fanoutTestInput(t, lanes, provider, diff)
		input.ModelLimits["tiny-context"] = 1

		got, err := Fanout(context.Background(), input)
		if err != nil {
			t.Errorf("Fanout error = %v, want nil for isolated budget failure", err)
		}
		calls := provider.generateCalls()
		if len(calls) != 2 {
			t.Errorf("Generate call count = %d, want 2 passing lanes", len(calls))
		}
		for _, call := range calls {
			if strings.Contains(call.Prompt, fanoutTemplateMarker("lane-too-small")) {
				t.Error("budget-failed lane reached Provider.Generate")
			}
		}
		if len(got.LaneResults) != 2 {
			t.Errorf("LaneResults count = %d, want 2 successful siblings", len(got.LaneResults))
		}
		assertFanoutFinding(t, got.LaneResults, "lane-one")
		assertFanoutFinding(t, got.LaneResults, "lane-three")
		if len(got.Failures) != 1 {
			t.Errorf("Failures count = %d, want exactly 1: %#v", len(got.Failures), got.Failures)
		}
		failure, ok := fanoutFailureByID(got.Failures, "lane-too-small")
		if !ok {
			t.Errorf("Failures do not contain lane-too-small: %#v", got.Failures)
		} else {
			if failure.Kind != FailureKindCompose {
				t.Errorf("failure Kind = %q, want named compose failure %q", failure.Kind, FailureKindCompose)
			}
			if !strings.Contains(strings.ToLower(failure.Reason), "budget") {
				t.Errorf("failure Reason = %q, want it to name the budget/assembly failure", failure.Reason)
			}
		}
	})

	t.Run("unknown model fails whole fanout", func(t *testing.T) {
		lanes := fanoutTestLanes("lane-one", "lane-unknown", "lane-three")
		const unknownModel = "model-name-typo"
		lanes[1].Model = unknownModel
		provider := newFanoutPromptProvider(lanes)
		input := fanoutTestInput(t, lanes, provider, "S33-UNKNOWN-MODEL-DIFF")

		got, err := Fanout(context.Background(), input)
		if err == nil {
			t.Fatal("Fanout error = nil, want whole-fanout unknown-model configuration failure")
		}
		if !strings.Contains(err.Error(), unknownModel) {
			t.Errorf("Fanout error = %q, want it to name unknown model %q", err, unknownModel)
		}
		if calls := provider.generateCalls(); len(calls) != 0 {
			t.Errorf("Generate call count = %d, want 0 before unknown-model preflight failure", len(calls))
		}
		if len(got.LaneResults) != 0 || len(got.Failures) != 0 {
			t.Errorf("Fanout result = %#v, want no launched lane results on preflight failure", got)
		}
	})
}

var _ ai.Provider = (*fanoutPromptProvider)(nil)
