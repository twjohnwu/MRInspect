package lane

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/testfake"
)

func loadComposeResourceRegistry(t *testing.T, declarations string) resources.Registry {
	t.Helper()
	repoRoot := t.TempDir()
	path := filepath.Join(repoRoot, "projects", "resources.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create resource fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("sets:\n"+declarations), 0o644); err != nil {
		t.Fatalf("write resource fixture: %v", err)
	}
	registry, err := resources.Load(repoRoot, "")
	if err != nil {
		t.Fatalf("load resource fixture: %v", err)
	}
	return registry
}

func composeTestInput(t *testing.T, lane Lane, terms []string, registry resources.Registry, diff string) ComposeInput {
	t.Helper()
	templatePath := filepath.Join(t.TempDir(), lane.ID+".tmpl.md")
	if err := os.WriteFile(templatePath, []byte("STATIC-PREAMBLE-FOR-"+lane.ID), 0o644); err != nil {
		t.Fatalf("write lane template: %v", err)
	}
	lane.Template = templatePath
	return ComposeInput{
		Lane:             lane,
		Terms:            terms,
		ResourceRegistry: registry,
		Project: project.LoadedProject{
			System:              project.SystemProject{Name: "Compose Test System"},
			ResolvedServiceType: "backend",
		},
		Diff: diff,
		MergeRequest: gitlab.MergeRequest{
			IID:          17,
			Title:        "Compose test MR",
			SourceBranch: "feature/compose",
			TargetBranch: "main",
			Author:       gitlab.Author{Name: "Test Author"},
		},
	}
}

// TestCompose_OneRetrievePerSet verifies REQ-02 / S-05: each retrieval-mode
// set produces exactly one query with the lane intent and shared terms.
func TestCompose_OneRetrievePerSet(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: internal-specs
    mode: retrieval
    paths: []
  - name: tech-docs
    mode: retrieval
    paths: []
`)
	terms := []string{"transaction", "service", "go"}
	lane := Lane{
		ID:        "spec-conformance",
		Intent:    "check internal specifications",
		Resources: Resources{Sets: []string{"internal-specs", "tech-docs"}},
		TopK:      7,
	}
	retriever := &testfake.FakeRetriever{}
	input := composeTestInput(t, lane, terms, registry, "S05-DIFF-CONTENT")
	input.Retriever = retriever
	input.FullLoader = &testfake.FakeFullLoader{}

	if _, err := Compose(context.Background(), input); err != nil {
		t.Fatalf("Compose: %v", err)
	}

	calls := retriever.RetrieveCalls()
	if len(calls) != 2 {
		t.Fatalf("Retrieve call count = %d, want exactly 2", len(calls))
	}
	wantSets := map[string]bool{"internal-specs": false, "tech-docs": false}
	for i, call := range calls {
		if _, exists := wantSets[call.Query.SetRef]; !exists {
			t.Errorf("Retrieve call %d SetRef = %q, want internal-specs or tech-docs", i, call.Query.SetRef)
		} else {
			wantSets[call.Query.SetRef] = true
		}
		if call.Query.Intent != lane.Intent {
			t.Errorf("Retrieve call %d Intent = %q, want %q", i, call.Query.Intent, lane.Intent)
		}
		if !slices.Equal(call.Query.Terms, terms) {
			t.Errorf("Retrieve call %d Terms = %v, want shared Terms %v", i, call.Query.Terms, terms)
		}
	}
	for setRef, seen := range wantSets {
		if !seen {
			t.Errorf("Retrieve never called with SetRef %q", setRef)
		}
	}
}

// TestCompose_EmptyResourcesSkipsRetrieval verifies REQ-02 / S-06: an empty
// resource selection needs neither store and still composes template plus diff.
func TestCompose_EmptyResourcesSkipsRetrieval(t *testing.T) {
	lane := Lane{ID: "code-diff", Intent: "review the complete diff", Resources: Resources{Sets: []string{}, Tags: []string{}}}
	const diff = "S06-EMPTY-RESOURCES-DIFF"

	t.Run("spies receive no calls", func(t *testing.T) {
		retriever := &testfake.FakeRetriever{}
		fullLoader := &testfake.FakeFullLoader{}
		input := composeTestInput(t, lane, []string{"shared", "terms"}, resources.Registry{}, diff)
		input.Retriever = retriever
		input.FullLoader = fullLoader

		result, err := Compose(context.Background(), input)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		if retriever.RetrieveCallCount() != 0 {
			t.Errorf("Retrieve call count = %d, want 0", retriever.RetrieveCallCount())
		}
		if fullLoader.LoadFullCallCount() != 0 {
			t.Errorf("LoadFull call count = %d, want 0", fullLoader.LoadFullCallCount())
		}
		if !strings.Contains(result.Prompt, diff) {
			t.Errorf("prompt does not contain diff %q", diff)
		}
		if !strings.Contains(result.Prompt, "STATIC-PREAMBLE-FOR-code-diff") {
			t.Error("prompt does not contain the lane template's static preamble")
		}
	})

	t.Run("nil stores are valid", func(t *testing.T) {
		input := composeTestInput(t, lane, []string{"shared", "terms"}, resources.Registry{}, diff)
		result, err := Compose(context.Background(), input)
		if err != nil {
			t.Fatalf("Compose with nil stores: %v", err)
		}
		if !strings.Contains(result.Prompt, diff) {
			t.Errorf("prompt composed with nil stores does not contain diff %q", diff)
		}
	})
}

// TestCompose_FullSetsUseFullLoader verifies REQ-02 / S-07: full-mode sets
// are loaded in one FullLoader call and never sent to Retriever.
func TestCompose_FullSetsUseFullLoader(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: official-standards
    mode: full
    paths: []
  - name: implementation-guides
    mode: retrieval
    paths: []
`)
	lane := Lane{
		ID:        "standards",
		Intent:    "check standards compliance",
		Resources: Resources{Sets: []string{"official-standards", "implementation-guides"}},
	}
	retriever := &testfake.FakeRetriever{}
	fullLoader := &testfake.FakeFullLoader{}
	input := composeTestInput(t, lane, []string{"shared", "terms"}, registry, "S07-DIFF-CONTENT")
	input.Retriever = retriever
	input.FullLoader = fullLoader

	if _, err := Compose(context.Background(), input); err != nil {
		t.Fatalf("Compose: %v", err)
	}

	fullCalls := fullLoader.LoadFullCalls()
	if len(fullCalls) != 1 {
		t.Fatalf("LoadFull call count = %d, want exactly 1", len(fullCalls))
	}
	if !slices.Contains(fullCalls[0].SetRefs, "official-standards") {
		t.Errorf("LoadFull SetRefs = %v, want official-standards", fullCalls[0].SetRefs)
	}
	retrieveCalls := retriever.RetrieveCalls()
	if len(retrieveCalls) != 1 {
		t.Fatalf("Retrieve call count = %d, want exactly 1", len(retrieveCalls))
	}
	if retrieveCalls[0].Query.SetRef != "implementation-guides" {
		t.Errorf("Retrieve SetRef = %q, want implementation-guides", retrieveCalls[0].Query.SetRef)
	}
	for i, call := range retrieveCalls {
		if call.Query.SetRef == "official-standards" {
			t.Errorf("Retrieve call %d incorrectly used full-mode SetRef %q", i, call.Query.SetRef)
		}
	}
}

// TestCompose_RetrievedContentReachesPrompt verifies REQ-02 / S-35: a
// retrieved chunk reaches only its selecting lane and remains nonce-delimited.
func TestCompose_RetrievedContentReachesPrompt(t *testing.T) {
	const sentinel = "ZX9-UNIQUE-NORMATIVE-CLAUSE-77"
	registry := loadComposeResourceRegistry(t, `  - name: normative-guides
    mode: retrieval
    paths: []
`)
	retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{{
			ID:          "normative-clause",
			Text:        "The selected reference says " + sentinel,
			Source:      "normative.md",
			ResourceSet: "normative-guides",
		}}},
	}}
	terms := []string{"shared", "terms"}
	laneOne := Lane{
		ID:        "lane-one",
		Intent:    "review normative requirements",
		Resources: Resources{Sets: []string{"normative-guides"}},
	}
	inputOne := composeTestInput(t, laneOne, terms, registry, "S35-LANE-ONE-DIFF")
	inputOne.Retriever = retriever
	resultOne, err := Compose(context.Background(), inputOne)
	if err != nil {
		t.Fatalf("Compose lane L1: %v", err)
	}

	sentinelAt := strings.Index(resultOne.Prompt, sentinel)
	if sentinelAt < 0 {
		t.Fatalf("lane L1 prompt does not contain retrieved sentinel %q", sentinel)
	}
	openAt := strings.LastIndex(resultOne.Prompt[:sentinelAt], "<<<RESOURCE:")
	if openAt < 0 {
		t.Fatal("retrieved sentinel has no preceding resource boundary")
	}
	openEndOffset := strings.Index(resultOne.Prompt[openAt:], ">>>")
	if openEndOffset < 0 || openAt+openEndOffset+3 > sentinelAt {
		t.Fatal("retrieved sentinel is not after a complete resource opening marker")
	}
	openMarker := resultOne.Prompt[openAt : openAt+openEndOffset+3]
	nonce := strings.TrimSuffix(strings.TrimPrefix(openMarker, "<<<RESOURCE:"), ">>>")
	closeAtOffset := strings.Index(resultOne.Prompt[sentinelAt:], "<<<END:"+nonce+">>>")
	if nonce == "" || closeAtOffset < 0 {
		t.Fatalf("retrieved sentinel is not enclosed by a matching nonce boundary; nonce=%q", nonce)
	}

	laneTwo := Lane{ID: "lane-two", Intent: "review only the diff", Resources: Resources{Sets: []string{}, Tags: []string{}}}
	inputTwo := composeTestInput(t, laneTwo, terms, registry, "S35-LANE-TWO-DIFF")
	inputTwo.Retriever = retriever
	resultTwo, err := Compose(context.Background(), inputTwo)
	if err != nil {
		t.Fatalf("Compose lane L2: %v", err)
	}
	if strings.Contains(resultTwo.Prompt, sentinel) {
		t.Errorf("lane L2 prompt unexpectedly contains L1 resource sentinel %q", sentinel)
	}
}
