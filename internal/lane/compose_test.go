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
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
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
		Composer:         prompt.NewComposer(),
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

// TestCompose_PromptCarriesOutputContract verifies REQ-04: lane composition
// carries one stable structured-output contract into initial and retry prompts.
func TestCompose_PromptCarriesOutputContract(t *testing.T) {
	lane := Lane{ID: "contract-lane", Intent: "verify the lane output contract"}
	input := composeTestInput(t, lane, nil, resources.Registry{}, "OUTPUT-CONTRACT-DIFF")
	composed, err := Compose(context.Background(), input)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	if LaneOutputContract == "" {
		t.Error("LaneOutputContract is empty, want a stable lane JSON output contract")
	}
	if LaneOutputContract == "" || !strings.Contains(composed.Prompt, LaneOutputContract) {
		t.Error("composed lane prompt does not contain the non-empty LaneOutputContract")
	}
	for _, token := range []string{"laneId", "findings", "title", "severity", "rationale"} {
		if !strings.Contains(LaneOutputContract, token) {
			t.Errorf("LaneOutputContract does not name required literal token %q", token)
		}
		if !strings.Contains(composed.Prompt, token) {
			t.Errorf("composed lane prompt does not name required literal token %q", token)
		}
	}
	contractInstruction := strings.ToLower(LaneOutputContract)
	namesOne := strings.Contains(contractInstruction, "single") || strings.Contains(contractInstruction, "one")
	if !namesOne || !strings.Contains(contractInstruction, "json") || !strings.Contains(contractInstruction, "block") {
		t.Error("LaneOutputContract does not instruct the model to return a single JSON block")
	}

	provider := &testfake.FakeProvider{Responses: []testfake.ProviderResponse{
		{Output: "FIRST-CONTRACT-ATTEMPT-IS-GARBAGE"},
		{Output: `{"laneId":"contract-lane","findings":[]}`},
	}}
	got := ExecuteLane(context.Background(), input, provider, 2)
	if got.Failure != nil {
		t.Fatalf("ExecuteLane failure = %#v, want retry success", got.Failure)
	}
	calls := provider.GenerateCalls()
	if len(calls) != 2 {
		t.Fatalf("Generate call count = %d, want exactly 2", len(calls))
	}
	if LaneOutputContract == "" || !strings.Contains(calls[1].Prompt, LaneOutputContract) {
		t.Error("retry prompt does not contain the same non-empty LaneOutputContract")
	}

	// The internal/prompt golden test guards the single-mode path; this contract
	// assertion deliberately enters through lane Compose only.
}

func TestCompose_BudgetEviction(t *testing.T) {
	t.Run("non-normative sections evict under tiny budget", func(t *testing.T) {
		t.Setenv("MRI_RAG_ON_NORMATIVE_EVICTION", "warn")
		registry := loadComposeResourceRegistry(t, `  - name: oversized-reference
    mode: retrieval
    paths: []
`)
		const diff = "BUDGET-EVICTION-DIFF-MUST-REMAIN"
		largeChunk := strings.Repeat("OVERSIZED-RETRIEVAL-CONTENT-", 4_000)
		retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
			Result: rag.Result{Chunks: []rag.Chunk{{
				ID:          "oversized-reference",
				Text:        largeChunk,
				Source:      "oversized-reference",
				ResourceSet: "oversized-reference",
				TokenEst:    chunk.TokenEst(largeChunk),
			}}},
		}}
		declaration := Lane{ID: "budgeted-retrieval", Intent: "review retrieved material", Resources: Resources{Sets: []string{"oversized-reference"}}}
		input := composeTestInput(t, declaration, []string{"budget"}, registry, diff)
		input.Retriever = retriever
		input.Budget = 4_000

		result, err := Compose(context.Background(), input)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		if !slices.ContainsFunc(result.Degraded, func(entry string) bool {
			return strings.Contains(entry, "evicted section") && strings.Contains(entry, "oversized-reference")
		}) {
			t.Errorf("Degraded = %v, want named oversized-reference eviction", result.Degraded)
		}
		if strings.Contains(result.Prompt, largeChunk) {
			t.Error("prompt contains retrieval chunk that should have been wholly evicted")
		}
		if !strings.Contains(result.Prompt, diff) {
			t.Errorf("prompt lost non-evictable diff %q", diff)
		}
	})

	t.Run("normative eviction is a hard error", func(t *testing.T) {
		t.Setenv("MRI_RAG_ON_NORMATIVE_EVICTION", "fail")
		registry := loadComposeResourceRegistry(t, `  - name: binding-standards
    mode: full
    paths: []
`)
		largeDoc := strings.Repeat("OVERSIZED-BINDING-STANDARD-", 4_000)
		fullLoader := &testfake.FakeFullLoader{DefaultResponse: testfake.FullLoaderResponse{
			Result: rag.FullResult{Docs: []rag.FullDoc{{
				Source:      "binding-standards",
				ResourceSet: "binding-standards",
				Bytes:       []byte(largeDoc),
				TokenEst:    chunk.TokenEst(largeDoc),
			}}},
		}}
		declaration := Lane{ID: "budgeted-normative", Intent: "review binding standards", Resources: Resources{Sets: []string{"binding-standards"}}}
		input := composeTestInput(t, declaration, []string{"budget"}, registry, "NORMATIVE-EVICTION-DIFF")
		input.FullLoader = fullLoader
		input.Budget = 4_000

		_, err := Compose(context.Background(), input)
		if err == nil || !strings.Contains(err.Error(), "normative section evicted") {
			t.Fatalf("Compose error = %v, want normative section evicted", err)
		}
	})

	t.Run("zero budget disables budgeting", func(t *testing.T) {
		t.Setenv("MRI_RAG_ON_NORMATIVE_EVICTION", "fail")
		registry := loadComposeResourceRegistry(t, `  - name: zero-budget-full
    mode: full
    paths: []
  - name: zero-budget-retrieval
    mode: retrieval
    paths: []
`)
		const (
			diff          = "ZERO-BUDGET-DIFF"
			fullText      = "ZERO-BUDGET-FULL-CONTENT"
			retrievalText = "ZERO-BUDGET-RETRIEVAL-CONTENT"
		)
		retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
			Result: rag.Result{Chunks: []rag.Chunk{{ID: "zero-budget-retrieval", Text: retrievalText, Source: "zero-budget-retrieval", ResourceSet: "zero-budget-retrieval", TokenEst: chunk.TokenEst(retrievalText)}}},
		}}
		fullLoader := &testfake.FakeFullLoader{DefaultResponse: testfake.FullLoaderResponse{
			Result: rag.FullResult{Docs: []rag.FullDoc{{Source: "zero-budget-full", ResourceSet: "zero-budget-full", Bytes: []byte(fullText), TokenEst: chunk.TokenEst(fullText)}}},
		}}
		declaration := Lane{ID: "unbudgeted", Intent: "preserve legacy composition", Resources: Resources{Sets: []string{"zero-budget-full", "zero-budget-retrieval"}}}
		input := composeTestInput(t, declaration, []string{"budget"}, registry, diff)
		input.Retriever = retriever
		input.FullLoader = fullLoader
		input.Budget = 0

		result, err := Compose(context.Background(), input)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		for _, want := range []string{diff, fullText, retrievalText} {
			if !strings.Contains(result.Prompt, want) {
				t.Errorf("zero-budget prompt missing %q", want)
			}
		}
		if slices.ContainsFunc(result.Degraded, func(entry string) bool { return strings.Contains(entry, "evicted section") }) {
			t.Errorf("zero-budget composition unexpectedly reported eviction: %v", result.Degraded)
		}
	})
}

func TestCompose_UnknownSelectorIsNamedDegradation(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: existing-guides
    tags: [known]
    mode: retrieval
    paths: []
`)
	lane := Lane{
		ID:        "unknown-selectors",
		Intent:    "review available material",
		Resources: Resources{Sets: []string{"existing-guides", "missing-set"}, Tags: []string{"missing-tag"}},
	}
	retriever := &testfake.FakeRetriever{}
	input := composeTestInput(t, lane, []string{"shared", "terms"}, registry, "UNKNOWN-SELECTOR-DIFF")
	input.Retriever = retriever

	result, err := Compose(context.Background(), input)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if retriever.RetrieveCallCount() != 1 {
		t.Fatalf("Retrieve call count = %d, want exactly 1", retriever.RetrieveCallCount())
	}
	if len(result.Degraded) != 2 {
		t.Errorf("Degraded = %v, want exactly one entry per unknown selector", result.Degraded)
	}
	for _, selector := range []string{"missing-set", "missing-tag"} {
		if !slices.ContainsFunc(result.Degraded, func(entry string) bool {
			return strings.Contains(entry, "unknown resource selector") && strings.Contains(entry, selector)
		}) {
			t.Errorf("Degraded = %v, want unknown resource selector naming %q", result.Degraded, selector)
		}
	}
}

func TestCompose_ChunkSourceIDsReachPrompt(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: cited-guides
    mode: retrieval
    paths: []
`)
	retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{
			{ID: "chunk-42", Source: "docs/x.md", StartLine: 7, Text: "BODY-SENTINEL"},
			{ID: "chunk-zero", Source: "docs/zero.md", Text: "ZERO-LINE-SENTINEL"},
		}},
	}}

	for _, test := range []struct {
		name   string
		budget int
	}{
		{name: "unbudgeted"},
		{name: "budgeted", budget: 100_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			declaration := Lane{ID: "cited-lane", Intent: "review cited material", Resources: Resources{Sets: []string{"cited-guides"}}}
			input := composeTestInput(t, declaration, []string{"citations"}, registry, "CITATION-DIFF")
			input.Retriever = retriever
			input.Budget = test.budget

			result, err := Compose(context.Background(), input)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}

			for _, want := range []struct {
				header string
				body   string
			}{
				{header: "[sourceId: chunk-42 | source: docs/x.md:7]", body: "BODY-SENTINEL"},
				{header: "[sourceId: chunk-zero | source: docs/zero.md]", body: "ZERO-LINE-SENTINEL"},
			} {
				headerAt := strings.Index(result.Prompt, want.header)
				if headerAt < 0 {
					t.Fatalf("prompt missing source header %q", want.header)
				}
				bodyAt := strings.Index(result.Prompt, want.body)
				if bodyAt < 0 || headerAt >= bodyAt {
					t.Fatalf("source header %q is not before body %q", want.header, want.body)
				}
				openAt := strings.LastIndex(result.Prompt[:headerAt], "<<<RESOURCE:")
				if openAt < 0 {
					t.Fatalf("source header %q has no preceding resource boundary", want.header)
				}
				openEnd := strings.Index(result.Prompt[openAt:], ">>>")
				if openEnd < 0 {
					t.Fatalf("source header %q has no complete resource opening marker", want.header)
				}
				nonce := strings.TrimSuffix(strings.TrimPrefix(result.Prompt[openAt:openAt+openEnd+3], "<<<RESOURCE:"), ">>>")
				closeAt := strings.Index(result.Prompt[headerAt:], "<<<END:"+nonce+">>>")
				if nonce == "" || closeAt < 0 || headerAt+closeAt <= bodyAt {
					t.Fatalf("source header %q and body %q are not enclosed by one matching nonce block", want.header, want.body)
				}
			}
			if strings.Contains(result.Prompt, "source: docs/zero.md:0") {
				t.Error("StartLine == 0 source header unexpectedly contains :0")
			}
		})
	}
}

// TestCompose_ZeroTopKNeverReachesRetriever verifies that a lane built
// directly with TopK 0 (bypassing Load's default) still reaches the
// Retriever with a positive TopK: Compose substitutes DefaultLaneTopK as a
// backstop so hand-constructed lanes can never trigger the sqlite
// retriever's "TopK <= 0 => empty result" guard.
func TestCompose_ZeroTopKNeverReachesRetriever(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: zero-topk-guides
    mode: retrieval
    paths: []
`)
	lane := Lane{
		ID:        "zero-topk",
		Intent:    "review material despite an unset topK",
		Resources: Resources{Sets: []string{"zero-topk-guides"}},
		TopK:      0,
	}
	retriever := &testfake.FakeRetriever{}
	input := composeTestInput(t, lane, []string{"shared", "terms"}, registry, "ZERO-TOPK-DIFF")
	input.Retriever = retriever

	if _, err := Compose(context.Background(), input); err != nil {
		t.Fatalf("Compose: %v", err)
	}

	calls := retriever.RetrieveCalls()
	if len(calls) != 1 {
		t.Fatalf("Retrieve call count = %d, want exactly 1", len(calls))
	}
	if calls[0].Query.TopK < 1 {
		t.Errorf("Retrieve call TopK = %d, want >= 1 (never 0 or negative)", calls[0].Query.TopK)
	}
}

func TestCompose_BudgetCountsFullPromptOverhead(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: overhead-guides
    mode: retrieval
    paths: []
`)
	declaration := Lane{
		ID:        "budget-overhead",
		Intent:    "review retrieved overhead material",
		Resources: Resources{Sets: []string{"overhead-guides"}},
	}
	resourceFreeLane := declaration
	resourceFreeLane.Resources = Resources{}
	resourceFreeInput := composeTestInput(t, resourceFreeLane, []string{"overhead"}, registry, "OVERHEAD-BUDGET-DIFF")
	if err := os.WriteFile(resourceFreeInput.Lane.Template, []byte(strings.Repeat("LARGE-LANE-PREAMBLE-", 100)), 0o644); err != nil {
		t.Fatalf("write large lane template: %v", err)
	}
	resourceFreeInput.Budget = 1_000_000
	resourceFree, err := Compose(context.Background(), resourceFreeInput)
	if err != nil {
		t.Fatalf("Compose resource-free prompt: %v", err)
	}
	overheadTokens := chunk.TokenEst(resourceFree.Prompt)

	const chunkText = "RETRIEVAL-CHUNK-THAT-FITS-WITHOUT-FULL-PROMPT-OVERHEAD"
	retrieved := rag.Chunk{
		ID:          "overhead-chunk",
		Text:        chunkText,
		Source:      "overhead.md",
		ResourceSet: "overhead-guides",
	}
	chunkTokens := chunk.TokenEst(chunksWithSourceHeaders([]rag.Chunk{retrieved})[0].Text)
	input := resourceFreeInput
	input.Lane = declaration
	input.Lane.Template = resourceFreeInput.Lane.Template
	input.Retriever = &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{retrieved}},
	}}
	input.Budget = overheadTokens + chunkTokens - 1

	result, err := Compose(context.Background(), input)
	if err != nil {
		t.Fatalf("Compose budgeted prompt: %v", err)
	}
	if !slices.ContainsFunc(result.Degraded, func(entry string) bool {
		return strings.Contains(entry, "evicted section") && strings.Contains(entry, retrieved.ID)
	}) {
		t.Errorf("Degraded = %v, want named %q eviction", result.Degraded, retrieved.ID)
	}
	if strings.Contains(result.Prompt, chunkText) {
		t.Error("prompt contains retrieval chunk after full-prompt overhead forced its eviction")
	}
}
