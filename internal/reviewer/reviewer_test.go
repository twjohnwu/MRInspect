package reviewer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/diffbudget"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/testfake"
	"mrinspect/internal/validator"
)

// TestPostReview_FooterDisclosesStoreProvenance verifies REQ-09 / S-33 at PostNote.
func TestPostReview_FooterDisclosesStoreProvenance(t *testing.T) {
	tests := []struct {
		name  string
		state ReviewRAGState
		want  []string
	}{
		{
			name:  "store provenance",
			state: ReviewRAGState{StorePresent: true, Store: rag.StoreResolution{BuiltAt: "2026-08-01T12:00:00Z"}, ResourcesSHA256: "0123456789abcdef", Degraded: []string{"artifact unavailable", "baked stale"}, SkippedFiles: 3},
			want:  []string{"2026-08-01T12:00:00Z", "01234567", "Degraded entries: 2", "skipped files: 3"},
		},
		{
			name:  "store absent",
			state: ReviewRAGState{StorePresent: false, Degraded: []string{"package unavailable"}},
			want:  []string{"store", "absent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl := newReviewerFixture(t, fakeComposer{prompt: "RAG prompt"})
			r.rag.State = tt.state
			if err := r.postReview(context.Background(), "## Findings\nreview"); err != nil {
				t.Fatalf("postReview: %v", err)
			}
			note := gl.lastNote(t)
			for _, want := range tt.want {
				if !strings.Contains(strings.ToLower(note), strings.ToLower(want)) {
					t.Errorf("PostNote body missing footer detail %q: %q", want, note)
				}
			}
		})
	}
}

// TestRun_NeverIndexesOnReviewPath verifies REQ-07 / S-45.
func TestRun_NeverIndexesOnReviewPath(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "RAG prompt"})
	path := &unavailableStorePath{}
	indexer := &spyIndexer{}
	r.rag.ReviewPath = path
	r.rag.Indexer = indexer

	r.Run(context.Background())

	if path.calls != 1 {
		t.Errorf("review RAG path calls = %d, want 1", path.calls)
	}
	if indexer.calls != 0 {
		t.Errorf("index calls = %d, want 0", indexer.calls)
	}
	if len(r.rag.State.Chunks) != 0 || len(r.rag.State.Degraded) == 0 {
		t.Errorf("review RAG state = %#v, want zero chunks with Degraded", r.rag.State)
	}
	if len(gl.notes) == 0 {
		t.Error("review did not complete by posting a note")
	}
}

// TestPostReview_FooterListsEvictedSections verifies REQ-14 / S-62 at PostNote.
func TestPostReview_FooterListsEvictedSections(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "RAG prompt"})
	r.rag.State.Composition = prompt.ComposeResult{
		Evicted: []prompt.EvictedSection{
			{Name: "retrieval-tail", Mode: prompt.SectionModeRetrieval, DeclarationOrder: 4, TokenEst: 30},
			{Name: "full-tail", Mode: prompt.SectionModeFull, DeclarationOrder: 3, TokenEst: 50},
		},
		Degraded: []string{"evicted section \"retrieval-tail\"", "evicted section \"full-tail\""},
	}
	if got := r.rag.State.Composition.Evicted; len(got) != 2 || got[0].Name != "retrieval-tail" || got[0].Mode != prompt.SectionModeRetrieval || got[0].DeclarationOrder != 4 || got[0].TokenEst != 30 || got[1].Name != "full-tail" || got[1].Mode != prompt.SectionModeFull || got[1].DeclarationOrder != 3 || got[1].TokenEst != 50 {
		t.Fatalf("Evicted = %#v, want two named sections in eviction order", got)
	}
	if got := r.rag.State.Composition.Degraded; len(got) != 2 || !strings.Contains(got[0], "retrieval-tail") || !strings.Contains(got[1], "full-tail") {
		t.Fatalf("Degraded = %#v, want one named entry per evicted section", got)
	}
	if err := r.postReview(context.Background(), "## Findings\nreview"); err != nil {
		t.Fatalf("postReview: %v", err)
	}
	note := gl.lastNote(t)
	for _, name := range []string{"retrieval-tail", "full-tail"} {
		if !strings.Contains(note, name) {
			t.Errorf("PostNote footer omits evicted section %q: %q", name, note)
		}
	}
}

// TestRun_CompositionErrorIsNotSilentlyDowngraded verifies REQ-14 / S-64.
func TestRun_CompositionErrorIsNotSilentlyDowngraded(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{err: errors.New("prompt exceeds budget after section eviction: diff TokenEst=900, framing overhead=20, budget=100")})
	provider := r.ai.(*fakeProvider)

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(strings.ToLower(note), "composition") || !strings.Contains(note, "diff TokenEst=900") {
		t.Errorf("composition failure was not visibly posted: %q", note)
	}
	for _, got := range provider.prompts {
		if strings.Contains(got, "## Output Format") {
			t.Errorf("AI received legacy SelectTemplate-shaped prompt after composition error: %q", got)
		}
	}
}

// TestPostReview_FooterFlagsUnpinnedVersion verifies REQ-09 / S-68 at PostNote.
func TestPostReview_FooterFlagsUnpinnedVersion(t *testing.T) {
	tests := []struct {
		name       string
		versionEnv string
		pinned     bool
		wantMarker bool
	}{
		{name: "latest is disclosed", versionEnv: "", pinned: false, wantMarker: true},
		{name: "pinned version is not marked unpinned", versionEnv: "v2.4.1", pinned: true, wantMarker: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.versionEnv == "" {
				unsetEnv(t, "MRI_RAG_PACKAGE_VERSION")
			} else {
				t.Setenv("MRI_RAG_PACKAGE_VERSION", tt.versionEnv)
			}
			r, gl := newReviewerFixture(t, fakeComposer{prompt: "RAG prompt"})
			r.rag.State = ReviewRAGState{StorePresent: true, Store: rag.StoreResolution{Version: "v2.4.1"}, PackageVersionPinned: tt.pinned}
			if err := r.postReview(context.Background(), "## Findings\nreview"); err != nil {
				t.Fatalf("postReview: %v", err)
			}
			note := strings.ToLower(gl.lastNote(t))
			if tt.wantMarker && (!strings.Contains(note, "unpinned") || !strings.Contains(note, "v2.4.1")) {
				t.Errorf("PostNote footer must mark the actual latest version unpinned: %q", note)
			}
			if !tt.wantMarker && strings.Contains(note, "unpinned") {
				t.Errorf("PostNote footer incorrectly marks an explicitly pinned version unpinned: %q", note)
			}
		})
	}
}

// TestRun_AllLanesFailedIsVisible verifies REQ-03 / S-12.
func TestRun_AllLanesFailedIsVisible(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{
		{id: "spec-conformance", enabled: true},
		{id: "standards", enabled: true},
		{id: "code-diff", enabled: true},
	})
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	provider := newModeRoutingProvider()
	provider.laneErrors = map[string]error{
		"spec-conformance": errors.New("spec provider unavailable"),
		"standards":        errors.New("standards quota exhausted"),
		"code-diff":        errors.New("code diff response timed out"),
	}
	r.ai = provider
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: root, ModelLimits: reviewerModelLimits()})
	r.rag.State = ReviewRAGState{
		StorePresent: true,
		Store:        rag.StoreResolution{BuiltAt: "2026-08-20T01:02:03Z"},
		Degraded:     []string{"shared store degradation"},
	}

	r.Run(context.Background())

	note := gl.lastNote(t)
	for laneID, laneErr := range provider.laneErrors {
		if !strings.Contains(note, laneID) || !strings.Contains(note, laneErr.Error()) {
			t.Errorf("posted note does not name failed lane %q and reason %q: %q", laneID, laneErr, note)
		}
	}
	if !strings.Contains(note, "Verdict\nIncomplete") && !strings.Contains(note, "Verdict: Incomplete") {
		t.Errorf("all-lanes-failed note lacks Verdict Incomplete: %q", note)
	}
	if strings.Contains(note, "Verdict\nApproved") || strings.Contains(note, "Verdict: Approved") {
		t.Errorf("all-lanes-failed note looks like a normal zero-finding review: %q", note)
	}
	// T11 pins the minimum integration-level footer contract here. Per-lane
	// eviction union labels, lane counts, and skipped-file sums are deferred until
	// the GREEN path exposes those records to the reviewer.
	if got := strings.Count(note, "store built_at:"); got != 1 {
		t.Errorf("store provenance occurrence count = %d, want exactly 1: %q", got, note)
	}
	if !strings.Contains(note, "Degraded entries: 1") {
		t.Errorf("multi note lacks aggregated Degraded sum line: %q", note)
	}
}

// TestRun_MissingLaneConfigFallsBackToSingle verifies REQ-07 / S-27.
func TestRun_MissingLaneConfigFallsBackToSingle(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	provider := newModeRoutingProvider()
	r.ai = provider
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: t.TempDir(), ModelLimits: reviewerModelLimits()})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, singleReviewSentinel) {
		t.Errorf("missing lanes.yaml did not complete through single review path: %q", note)
	}
	lower := strings.ToLower(note)
	if !strings.Contains(lower, "degrad") || !strings.Contains(lower, "lanes") || !strings.Contains(lower, "missing") {
		t.Errorf("missing lanes.yaml degradation is not named in posted note: %q", note)
	}
	if got := provider.callCount(); got != 1 {
		t.Errorf("Generate call count = %d, want 1 single-path review call", got)
	}
}

// TestRun_SelfReflectionOnlyInSingleMode verifies REQ-07 / S-28.
func TestRun_SelfReflectionOnlyInSingleMode(t *testing.T) {
	t.Setenv("IS_SELF_REFLECTION", "true")

	t.Run("multi skips reflection", func(t *testing.T) {
		root := writeLaneFixture(t, []laneFixture{
			{id: "spec-conformance", enabled: true},
			{id: "standards", enabled: true},
			{id: "code-diff", enabled: true},
		})
		r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		r.cfg.ReviewMode = "multi"
		r.cfg.SelfReflection = true
		provider := newModeRoutingProvider()
		r.ai = provider
		r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: root, ModelLimits: reviewerModelLimits()})

		r.Run(context.Background())

		if got, want := provider.callCount(), 3; got != want {
			t.Errorf("multi Generate call count = %d, want enabled lane count %d exactly (no reflection)", got, want)
		}
	})

	t.Run("single keeps reflection", func(t *testing.T) {
		r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		r.cfg.ReviewMode = "single"
		r.cfg.SelfReflection = true
		provider := newModeRoutingProvider()
		r.ai = provider

		r.Run(context.Background())

		if got, want := provider.callCount(), 2; got != want {
			t.Errorf("single Generate call count = %d, want %d including reflection", got, want)
		}
	})
}

// TestRun_NormativeEvictionFailIsNotSwallowed verifies REQ-03 / S-34.
func TestRun_NormativeEvictionFailIsNotSwallowed(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{
		{id: "spec-conformance", enabled: true},
		{id: "normative-standards", enabled: true},
		{id: "code-diff", enabled: true},
	})
	lanesPath := filepath.Join(root, "projects", "lanes.yaml")
	lanesYAML, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("read lanes fixture: %v", err)
	}
	lanesYAML = []byte(strings.Replace(string(lanesYAML),
		"id: normative-standards\n    enabled: true\n",
		"id: normative-standards\n    enabled: true\n    model: normative-tiny\n", 1))
	lanesYAML = []byte(strings.Replace(string(lanesYAML),
		"intent: review normative-standards\n    resources:\n      sets: []\n",
		"intent: review normative-standards\n    resources:\n      sets: [binding-standards]\n", 1))
	if err := os.WriteFile(lanesPath, lanesYAML, 0o644); err != nil {
		t.Fatalf("write normative lane fixture: %v", err)
	}
	resourcesYAML := []byte("sets:\n  - name: binding-standards\n    mode: full\n    paths: []\n")
	if err := os.WriteFile(filepath.Join(root, "projects", "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("write resources fixture: %v", err)
	}
	resourceRegistry, err := resources.Load(root, "")
	if err != nil {
		t.Fatalf("load resources fixture: %v", err)
	}
	const policyError = "prompt composition rejected: normative section evicted"

	for _, policy := range []string{"fail", "warn"} {
		t.Run(policy, func(t *testing.T) {
			t.Setenv("MRI_PROMPT_BUDGET_FACTOR", "1")
			r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
			r.cfg.ReviewMode = "multi"
			r.cfg.RAGOnNormativeEviction = policy
			provider := newModeRoutingProvider()
			provider.laneResponses["spec-conformance"] = `{"laneId":"spec-conformance","findings":[{"title":"sibling-spec-result","severity":"low","rationale":"spec lane completed"}]}`
			provider.laneResponses["normative-standards"] = `{"laneId":"normative-standards","findings":[{"title":"normative-standards-result","severity":"low","rationale":"normative lane completed"}]}`
			provider.laneResponses["code-diff"] = `{"laneId":"code-diff","findings":[{"title":"sibling-code-result","severity":"low","rationale":"code lane completed"}]}`
			r.ai = provider
			largeDoc := strings.Repeat("OVERSIZED-BINDING-STANDARD-", 4_000)
			fullLoader := &testfake.FakeFullLoader{DefaultResponse: testfake.FullLoaderResponse{
				Result: rag.FullResult{Docs: []rag.FullDoc{{
					Source:      "binding-standards",
					ResourceSet: "binding-standards",
					Bytes:       []byte(largeDoc),
				}}},
			}}
			r.SetMultiLaneReviewPath(MultiLaneReviewPath{
				RepoRoot:         root,
				ResourceRegistry: resourceRegistry,
				FullLoader:       fullLoader,
				ModelLimits:      map[string]int{"gemini-test": 1_000_000, "normative-tiny": 4_000},
			})

			r.Run(context.Background())

			note := gl.lastNote(t)
			if policy == "fail" {
				if !strings.Contains(note, policyError) || !strings.Contains(strings.ToLower(note), "normative") {
					t.Errorf("strict normative eviction did not visibly fail the whole review: %q", note)
				}
				if strings.Contains(note, "sibling-spec-result") || strings.Contains(note, "sibling-code-result") {
					t.Errorf("strict failure posted normal sibling lane results: %q", note)
				}
				return
			}
			for _, want := range []string{"sibling-spec-result", "sibling-code-result", "normative-standards-result", "binding-standards", "evicted section"} {
				if !strings.Contains(note, want) {
					t.Errorf("warn-mode review missing %q: %q", want, note)
				}
			}
			if strings.Contains(note, "Incomplete") {
				t.Errorf("warn-mode normative eviction incorrectly failed the lane: %q", note)
			}
		})
	}
}

// TestRun_NoEnabledLanesIsNotAnEmptyReview verifies REQ-07 / S-37.
func TestRun_NoEnabledLanesIsNotAnEmptyReview(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{
		{id: "spec-conformance", enabled: false},
		{id: "standards", enabled: false},
		{id: "code-diff", enabled: false},
	})
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	provider := newModeRoutingProvider()
	r.ai = provider
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: root, ModelLimits: reviewerModelLimits()})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, singleReviewSentinel) || provider.callCount() == 0 {
		t.Errorf("no-enabled-lane case did not complete a single review: %q", note)
	}
	lower := strings.ToLower(note)
	if !strings.Contains(lower, "no runnable lane") || !strings.Contains(lower, "degrad") || !strings.Contains(lower, "single") {
		t.Errorf("posted note does not visibly name no-runnable-lane degradation to single: %q", note)
	}
}

// TestPostReview_UpdatesExistingNote verifies REQ-06 / S-41.
func TestPostReview_UpdatesExistingNote(t *testing.T) {
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	bot := gitlab.Author{ID: 101, Username: "mrinspect-bot"}
	decoyAuthor := gitlab.Author{ID: 202, Username: "decoy-user"}
	gl.currentUser = bot
	gl.listedNotes = []gitlab.Note{
		{ID: 41, Body: ReviewNoteMarker + " old bot review", Author: bot},
		{ID: 42, Body: ReviewNoteMarker + " decoy review", Author: decoyAuthor},
	}
	decoyBefore := gl.listedNotes[1]

	if err := r.postReview(context.Background(), "## Findings\nreplacement review\n## Verdict"); err != nil {
		t.Fatalf("postReview: %v", err)
	}

	if len(gl.updateCalls) != 1 || gl.updateCalls[0].noteID != 41 {
		t.Errorf("UpdateNote calls = %#v, want exactly bot note ID 41", gl.updateCalls)
	} else if !strings.Contains(gl.updateCalls[0].body, ReviewNoteMarker) {
		t.Errorf("updated body lacks exported stable marker %q: %q", ReviewNoteMarker, gl.updateCalls[0].body)
	}
	if len(gl.notes) != 0 {
		t.Errorf("PostNote call count = %d, want 0 on rerun", len(gl.notes))
	}
	if got := gl.noteByID(42); got != decoyBefore {
		t.Errorf("decoy note changed: got %#v, want %#v", got, decoyBefore)
	}
	if got := gl.markedNoteCountByAuthor(bot.ID); got != 1 {
		t.Errorf("marked notes by bot author = %d, want 1 after rerun", got)
	}
}

const (
	laneTemplatePrefix   = "REVIEWER-LANE-TEMPLATE::"
	singleReviewSentinel = "SINGLE-REVIEW-COMPLETE"
)

type laneFixture struct {
	id      string
	enabled bool
}

func writeLaneFixture(t *testing.T, declarations []laneFixture) string {
	t.Helper()
	root := t.TempDir()
	projectsDir := filepath.Join(root, "projects")
	templateDir := filepath.Join(projectsDir, "_lanes")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("create lane fixture directory: %v", err)
	}

	var registry strings.Builder
	registry.WriteString("lanes:\n")
	for _, declaration := range declarations {
		templatePath := filepath.Join(templateDir, declaration.id+".tmpl.md")
		if err := os.WriteFile(templatePath, []byte(laneTemplatePrefix+declaration.id), 0o644); err != nil {
			t.Fatalf("write template for %q: %v", declaration.id, err)
		}
		fmt.Fprintf(&registry, "  - id: %s\n    enabled: %t\n    template: %q\n    intent: review %s\n    resources:\n      sets: []\n      tags: []\n", declaration.id, declaration.enabled, templatePath, declaration.id)
	}
	if err := os.WriteFile(filepath.Join(projectsDir, "lanes.yaml"), []byte(registry.String()), 0o644); err != nil {
		t.Fatalf("write lanes.yaml: %v", err)
	}
	return root
}

func reviewerModelLimits() map[string]int {
	return map[string]int{"gemini-test": 1_000_000}
}

type modeRoutingProvider struct {
	mu             sync.Mutex
	prompts        []string
	laneErrors     map[string]error
	laneResponses  map[string]string
	laneResponders map[string]func(string) string
}

func newModeRoutingProvider() *modeRoutingProvider {
	return &modeRoutingProvider{
		laneErrors:     make(map[string]error),
		laneResponses:  make(map[string]string),
		laneResponders: make(map[string]func(string) string),
	}
}

func (p *modeRoutingProvider) Generate(_ context.Context, reviewPrompt string, _ ai.GenerateOptions) (string, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, reviewPrompt)
	p.mu.Unlock()

	for laneID, laneErr := range p.laneErrors {
		if strings.Contains(reviewPrompt, laneTemplatePrefix+laneID) {
			return "", laneErr
		}
	}
	for laneID, respond := range p.laneResponders {
		if strings.Contains(reviewPrompt, laneTemplatePrefix+laneID) {
			return respond(reviewPrompt), nil
		}
	}
	for laneID, response := range p.laneResponses {
		if strings.Contains(reviewPrompt, laneTemplatePrefix+laneID) {
			return response, nil
		}
	}
	for _, laneID := range []string{"spec-conformance", "standards", "code-diff", "normative-standards"} {
		if strings.Contains(reviewPrompt, laneTemplatePrefix+laneID) {
			return fmt.Sprintf(`{"laneId":%q,"findings":[{"title":%q,"severity":"low","rationale":"lane completed"}]}`, laneID, "finding-"+laneID), nil
		}
	}
	if reviewPrompt == "reflection" {
		return "REVIEW VALIDATED", nil
	}
	return "## Code Review\n## Findings\n" + singleReviewSentinel + "\n## Verdict\nApproved", nil
}

func (*modeRoutingProvider) Name() string { return "mode-routing-fake" }

func (p *modeRoutingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prompts)
}

type fakeNoteUpdate struct {
	noteID int
	body   string
}

type fakeGitLab struct {
	notes       []string
	listedNotes []gitlab.Note
	currentUser gitlab.Author
	currentErr  error
	listErr     error
	updateCalls []fakeNoteUpdate
}

func (*fakeGitLab) HealthCheck(context.Context) bool { return true }
func (g *fakeGitLab) CurrentUser(context.Context) (gitlab.Author, error) {
	return g.currentUser, g.currentErr
}
func (*fakeGitLab) GetMergeRequest(context.Context, string, string) (gitlab.MergeRequest, error) {
	return gitlab.MergeRequest{IID: 7, Title: "RAG test", SourceBranch: "feature", TargetBranch: "main"}, nil
}
func (*fakeGitLab) GetMRChanges(context.Context, string, string) (gitlab.MRChangesResponse, error) {
	return gitlab.MRChangesResponse{}, nil
}
func (g *fakeGitLab) ListNotes(context.Context, string, string) ([]gitlab.Note, error) {
	return append([]gitlab.Note(nil), g.listedNotes...), g.listErr
}
func (g *fakeGitLab) PostNote(_ context.Context, _, _, body string) (gitlab.Note, error) {
	g.notes = append(g.notes, body)
	note := gitlab.Note{ID: 1000 + len(g.notes), Body: body, Author: g.currentUser}
	g.listedNotes = append(g.listedNotes, note)
	return note, nil
}
func (g *fakeGitLab) UpdateNote(_ context.Context, _, _ string, noteID int, body string) (gitlab.Note, error) {
	g.updateCalls = append(g.updateCalls, fakeNoteUpdate{noteID: noteID, body: body})
	for index := range g.listedNotes {
		if g.listedNotes[index].ID == noteID {
			g.listedNotes[index].Body = body
			return g.listedNotes[index], nil
		}
	}
	return gitlab.Note{}, nil
}
func (g *fakeGitLab) lastNote(t *testing.T) string {
	t.Helper()
	if len(g.notes) == 0 {
		t.Fatal("PostNote was not called")
	}
	return g.notes[len(g.notes)-1]
}

func (g *fakeGitLab) noteByID(noteID int) gitlab.Note {
	for _, note := range g.listedNotes {
		if note.ID == noteID {
			return note
		}
	}
	return gitlab.Note{}
}

func (g *fakeGitLab) markedNoteCountByAuthor(authorID int) int {
	count := 0
	for _, note := range g.listedNotes {
		if note.Author.ID == authorID && strings.Contains(note.Body, ReviewNoteMarker) {
			count++
		}
	}
	return count
}

type fakeProvider struct{ prompts []string }

func (p *fakeProvider) Generate(_ context.Context, prompt string, _ ai.GenerateOptions) (string, error) {
	p.prompts = append(p.prompts, prompt)
	return "## Code Review\n## Findings\n## Verdict", nil
}
func (*fakeProvider) Name() string { return "fake" }

type fakeDiff struct{}

func (fakeDiff) Fetch(context.Context, string, string) (string, error) {
	return "diff --git a/a.go b/a.go", nil
}

type fakeProjects struct{}

func (fakeProjects) IsAvailable() bool { return true }
func (fakeProjects) LoadProfile(string, string) (project.LoadedProject, error) {
	return project.LoadedProject{}, nil
}

type fakeComposer struct {
	prompt string
	err    error
}

func (c fakeComposer) ComposeReviewPrompt(project.LoadedProject, string, gitlab.MergeRequest) (string, error) {
	return c.prompt, c.err
}
func (fakeComposer) ComposeSelfReflectionPrompt(project.LoadedProject, string) string {
	return "reflection"
}

type fakeValidator struct{}

func (fakeValidator) ValidateEnvironment() error                     { return nil }
func (fakeValidator) ValidateMergeRequest(gitlab.MergeRequest) error { return nil }
func (fakeValidator) ValidateDiff(string) (validator.DiffValidationResult, error) {
	return validator.DiffValidationResult{}, nil
}
func (fakeValidator) ValidateReviewContent(string) error { return nil }
func (fakeValidator) SanitizeInput(input string) string  { return input }
func (fakeValidator) GetProjectID() string               { return "1" }
func (fakeValidator) GetMRIID() string                   { return "7" }
func (fakeValidator) GetSourceBranch() string            { return "feature" }
func (fakeValidator) GetTargetBranch() string            { return "main" }

type fakeErrorHandler struct{}

func (fakeErrorHandler) Categorize(error) mrerrors.Category       { return mrerrors.CategoryUnknown }
func (fakeErrorHandler) ShouldPost(error, mrerrors.Category) bool { return true }
func (fakeErrorHandler) GenerateMessage(err error, _ string, _ mrerrors.Category) string {
	return err.Error()
}

type unavailableStorePath struct{ calls int }

func (p *unavailableStorePath) RetrieveForReview(context.Context, string) (ReviewRAGState, error) {
	p.calls++
	return ReviewRAGState{Degraded: []string{"package unavailable"}}, nil
}

type spyIndexer struct{ calls int }

func (s *spyIndexer) Index(context.Context) error {
	s.calls++
	return nil
}

func newReviewerFixture(t *testing.T, composer fakeComposer) (*MRInspectReviewer, *fakeGitLab) {
	t.Helper()
	gl := &fakeGitLab{}
	provider := &fakeProvider{}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service:    config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{AIRetryAttempts: 1},
	}
	return New(cfg, gl, provider, fakeDiff{}, fakeProjects{}, composer, fakeValidator{}, fakeErrorHandler{}, logger.New(slog.LevelError, t.TempDir()+"/metrics.json")), gl
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func TestRun_CitationsVerifiedAgainstReceivedChunks(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})

	lanesPath := filepath.Join(root, "projects", "lanes.yaml")
	lanesYAML, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("read lanes fixture: %v", err)
	}
	lanesYAML = []byte(strings.Replace(string(lanesYAML), "sets: []", "sets: [official-standards]", 1))
	if err := os.WriteFile(lanesPath, lanesYAML, 0o644); err != nil {
		t.Fatalf("write lanes fixture resources: %v", err)
	}

	resourcesYAML := []byte("sets:\n  - name: official-standards\n    mode: retrieval\n    paths: []\n")
	if err := os.WriteFile(filepath.Join(root, "projects", "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("write resources fixture: %v", err)
	}
	resourceRegistry, err := resources.Load(root, "")
	if err != nil {
		t.Fatalf("load resources fixture: %v", err)
	}

	retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{{
			ID: "std-chunk-7", Source: "standards/rule.md", StartLine: 17,
		}}},
	}}
	provider := newModeRoutingProvider()
	provider.laneResponders["standards"] = func(receivedPrompt string) string {
		const headerPrefix = "[sourceId: "
		headerAt := strings.Index(receivedPrompt, headerPrefix)
		if headerAt < 0 {
			return `{"laneId":"standards","findings":[{"title":"known citation","severity":"low","rationale":"source ID was absent from the prompt","citations":[{"sourceId":"prompt-had-no-source-id"}]},{"title":"unknown citation","severity":"low","rationale":"does not match a received standard","citations":[{"sourceId":"missing-source"}]}]}`
		}
		sourceIDStart := headerAt + len(headerPrefix)
		sourceIDEnd := strings.Index(receivedPrompt[sourceIDStart:], " | source:")
		if sourceIDEnd < 0 {
			return `{"laneId":"standards","findings":[]}`
		}
		sourceID := receivedPrompt[sourceIDStart : sourceIDStart+sourceIDEnd]
		return fmt.Sprintf(`{"laneId":"standards","findings":[{"title":"known citation","severity":"low","rationale":"matches the received standard","citations":[{"sourceId":%q}]},{"title":"unknown citation","severity":"low","rationale":"does not match a received standard","citations":[{"sourceId":"missing-source"}]}]}`, sourceID)
	}
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	r.ai = provider
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:         root,
		ResourceRegistry: resourceRegistry,
		Retriever:        retriever,
		ModelLimits:      reviewerModelLimits(),
	})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, "standards/rule.md:17") {
		t.Errorf("posted note missing verified citation coordinate standards/rule.md:17: %q", note)
	}
	if strings.Contains(note, "std-chunk-7 (unverified)") {
		t.Errorf("posted note marks the received citation unverified: %q", note)
	}
	if !strings.Contains(note, "missing-source (unverified)") {
		t.Errorf("posted note does not preserve the unknown citation as unverified: %q", note)
	}
}

// setRefRetriever is a rag.Retriever test double that dispatches its
// programmed response by rag.Query.SetRef, so different lanes (which
// declare different resource sets) can be given different retrieval
// outcomes within one concurrent fanout run.
type setRefRetriever struct {
	responses map[string]rag.Result
}

func (r *setRefRetriever) Name() string { return "set-ref-fake" }

func (r *setRefRetriever) Retrieve(_ context.Context, query rag.Query) (rag.Result, error) {
	return r.responses[query.SetRef], nil
}

func (r *setRefRetriever) Close() error { return nil }

// TestRun_ScopeReflectsActualContribution verifies the Scope section
// annotates each lane's actual retrieval contribution instead of only
// listing its declared resource sets: a lane that received a chunk shows
// how many, and a lane that declared a retrieval set but received nothing
// is named as such rather than implied to have been consulted.
func TestRun_ScopeReflectsActualContribution(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{
		{id: "standards", enabled: true},
		{id: "spec-conformance", enabled: true},
	})

	lanesPath := filepath.Join(root, "projects", "lanes.yaml")
	lanesYAML, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("read lanes fixture: %v", err)
	}
	rewritten := strings.Replace(
		string(lanesYAML),
		"intent: review standards\n    resources:\n      sets: []",
		"intent: review standards\n    resources:\n      sets: [official-standards]",
		1,
	)
	rewritten = strings.Replace(
		rewritten,
		"intent: review spec-conformance\n    resources:\n      sets: []",
		"intent: review spec-conformance\n    resources:\n      sets: [product-specs]",
		1,
	)
	if err := os.WriteFile(lanesPath, []byte(rewritten), 0o644); err != nil {
		t.Fatalf("write lanes fixture resources: %v", err)
	}

	resourcesYAML := []byte("sets:\n  - name: official-standards\n    mode: retrieval\n    paths: []\n  - name: product-specs\n    mode: retrieval\n    paths: []\n")
	if err := os.WriteFile(filepath.Join(root, "projects", "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("write resources fixture: %v", err)
	}
	resourceRegistry, err := resources.Load(root, "")
	if err != nil {
		t.Fatalf("load resources fixture: %v", err)
	}

	retriever := &setRefRetriever{responses: map[string]rag.Result{
		"official-standards": {Chunks: []rag.Chunk{{ID: "std-1", Source: "standards/rule.md", StartLine: 5}}},
		"product-specs":      {},
	}}

	provider := newModeRoutingProvider()
	provider.laneResponders["standards"] = func(string) string {
		return `{"laneId":"standards","findings":[]}`
	}
	provider.laneResponders["spec-conformance"] = func(string) string {
		return `{"laneId":"spec-conformance","findings":[]}`
	}

	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	r.ai = provider
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:         root,
		ResourceRegistry: resourceRegistry,
		Retriever:        retriever,
		ModelLimits:      reviewerModelLimits(),
	})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, "1 chunk retrieved") {
		t.Errorf("posted note does not report standards lane's retrieved chunk count: %q", note)
	}
	if !strings.Contains(note, "no content retrieved") {
		t.Errorf("posted note does not name the spec-conformance lane as having retrieved no content: %q", note)
	}
}

func TestPostReview_ListingErrorFallsBackWithLog(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeGitLab, error)
		wantLog   string
	}{
		{
			name: "CurrentUser error",
			configure: func(gl *fakeGitLab, err error) {
				gl.currentErr = err
			},
			wantLog: "current user lookup failed",
		},
		{
			name: "ListNotes error",
			configure: func(gl *fakeGitLab, err error) {
				gl.listErr = err
			},
			wantLog: "note listing failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
			readWarnings := installWarningLogRecorder(t, r)
			listingErr := errors.New(tt.wantLog)
			tt.configure(gl, listingErr)

			if err := r.postReview(context.Background(), "## Findings\nreview"); err != nil {
				t.Fatalf("postReview: %v", err)
			}
			if len(gl.notes) != 1 {
				t.Errorf("PostNote call count = %d, want 1 fallback call", len(gl.notes))
			}
			logs := readWarnings()
			if !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, listingErr.Error()) {
				t.Errorf("warning log = %q, want WARN naming error %q", logs, listingErr)
			}
		})
	}
}

func installWarningLogRecorder(t *testing.T, r *MRInspectReviewer) func() string {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "reviewer-warnings-*.jsonl")
	if err != nil {
		t.Fatalf("create warning log sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	stdout := os.Stdout
	os.Stdout = sink
	r.log = logger.New(slog.LevelWarn, filepath.Join(t.TempDir(), "metrics.json"))
	os.Stdout = stdout

	return func() string {
		t.Helper()
		if err := sink.Sync(); err != nil {
			t.Fatalf("sync warning log sink: %v", err)
		}
		data, err := os.ReadFile(sink.Name())
		if err != nil {
			t.Fatalf("read warning log sink: %v", err)
		}
		return string(data)
	}
}

// TestCleanResponse_EarliestMarkerWins guards against the marker-hijack bug:
// cleanResponse must cut at the EARLIEST marker occurrence in the response,
// not at the first marker in list-priority order.
func TestCleanResponse_EarliestMarkerWins(t *testing.T) {
	r := &MRInspectReviewer{}

	t.Run("tail-quoted high-priority marker hijack", func(t *testing.T) {
		response := "Some preamble text before the real heading.\n\n" +
			"## Review\nThis is the real review body with actual findings.\n" +
			"More real review content here.\n\n" +
			"```diff\n+ ## Code Review\n+ some quoted diff line from the MR\n```\n"
		got := r.cleanResponse(response)
		if !strings.Contains(got, "This is the real review body with actual findings.") {
			t.Fatalf("cleanResponse dropped the real review body; got: %q", got)
		}
		if strings.HasPrefix(got, "```diff") {
			t.Fatalf("cleanResponse cut at the tail-quoted marker instead of the earliest real one; got: %q", got)
		}
	})

	t.Run("earliest position beats list priority", func(t *testing.T) {
		response := "noise\n### MR Info\nreal content\nmore noise\n## Code Review\nlater section"
		got := r.cleanResponse(response)
		if !strings.HasPrefix(got, "### MR Info") {
			t.Fatalf("cleanResponse must cut at the earliest marker position (### MR Info), got: %q", got)
		}
	})
}

// sequencedProvider returns one response per call, in order, holding the
// last response steady once exhausted. It records every prompt it received.
type sequencedProvider struct {
	responses []string
	prompts   []string
	calls     int
}

func (p *sequencedProvider) Generate(_ context.Context, prompt string, _ ai.GenerateOptions) (string, error) {
	p.prompts = append(p.prompts, prompt)
	idx := p.calls
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	p.calls++
	return p.responses[idx], nil
}
func (*sequencedProvider) Name() string { return "sequenced-fake" }

// configurableValidator lets a test control ValidateReviewContent while
// keeping every other validator method a no-op, matching fakeValidator.
type configurableValidator struct {
	validateReviewContent func(string) error
}

func (configurableValidator) ValidateEnvironment() error                     { return nil }
func (configurableValidator) ValidateMergeRequest(gitlab.MergeRequest) error { return nil }
func (configurableValidator) ValidateDiff(string) (validator.DiffValidationResult, error) {
	return validator.DiffValidationResult{}, nil
}
func (v configurableValidator) ValidateReviewContent(content string) error {
	if v.validateReviewContent == nil {
		return nil
	}
	return v.validateReviewContent(content)
}
func (configurableValidator) SanitizeInput(input string) string { return input }
func (configurableValidator) GetProjectID() string              { return "1" }
func (configurableValidator) GetMRIID() string                  { return "7" }
func (configurableValidator) GetSourceBranch() string           { return "feature" }
func (configurableValidator) GetTargetBranch() string           { return "main" }

// newForensicsFixture builds a reviewer with a configurable provider and
// validator plus a WARN-level log recorder, for the validation-forensics
// and self-reflection-guard tests below.
func newForensicsFixture(t *testing.T, provider ai.Provider, v configurableValidator, retryAttempts int, composer fakeComposer) (*MRInspectReviewer, func() string) {
	t.Helper()
	gl := &fakeGitLab{}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service:    config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{AIRetryAttempts: retryAttempts},
	}
	r := New(cfg, gl, provider, fakeDiff{}, fakeProjects{}, composer, v, fakeErrorHandler{}, logger.New(slog.LevelError, t.TempDir()+"/metrics.json"))
	readWarnings := installWarningLogRecorder(t, r)
	return r, readWarnings
}

const validReviewBody = "## Code Review\n## Findings\nnothing found\n## Verdict\napproved"

func reviewSectionsPresent(content string) error {
	if strings.Contains(content, "## Findings") && strings.Contains(content, "## Verdict") {
		return nil
	}
	return errors.New("missing required sections")
}

// TestGenerateReview_FailedValidationForensics pins the opt-in dump policy
// while retaining metadata-only observability when full dumps are disabled.
func TestGenerateReview_FailedValidationForensics(t *testing.T) {
	prompt := "TOP-SECRET-PROMPT-CONTENT"
	badResponse := "Some preamble.\n# Bad Heading\n## Code Review\nTOP-SECRET-RESPONSE-CONTENT"
	v := configurableValidator{validateReviewContent: reviewSectionsPresent}
	retiredDumpEnv := "MRI_REVIEW_DUMP_" + "DISABLED"

	t.Run("default logs hashes but no bodies", func(t *testing.T) {
		unsetEnv(t, "MRI_REVIEW_DUMP_ENABLED")
		unsetEnv(t, retiredDumpEnv)
		provider := &sequencedProvider{responses: []string{badResponse, validReviewBody}}
		r, readWarnings := newForensicsFixture(t, provider, v, 2, fakeComposer{prompt: prompt})
		if _, err := r.generateReview(context.Background(), "diff", gitlab.MergeRequest{}); err != nil {
			t.Fatalf("generateReview: %v", err)
		}
		logs := readWarnings()
		for _, secret := range []string{prompt, "TOP-SECRET-RESPONSE-CONTENT"} {
			if strings.Contains(logs, secret) {
				t.Errorf("default warning log leaked %q: %q", secret, logs)
			}
		}
		for _, field := range []string{
			fmt.Sprintf(`"promptSHA":"%s"`, expectedSHA256Prefix(prompt)),
			fmt.Sprintf(`"responseSHA":"%s"`, expectedSHA256Prefix(badResponse)),
			`"error":"missing required sections"`,
			`"headings":["# Bad Heading","## Code Review"]`,
			fmt.Sprintf(`"responseLenBeforeClean":%d`, len(badResponse)),
			fmt.Sprintf(`"responseLenAfterClean":%d`, len(r.cleanResponse(badResponse))),
		} {
			if !strings.Contains(logs, field) {
				t.Errorf("default warning log missing %s: %q", field, logs)
			}
		}
	})

	t.Run("enabled true logs prompt and response dumps", func(t *testing.T) {
		unsetEnv(t, retiredDumpEnv)
		provider := &sequencedProvider{responses: []string{badResponse, validReviewBody}}
		r, readWarnings := newForensicsFixture(t, provider, v, 2, fakeComposer{prompt: prompt})
		r.cfg.ReviewDumpEnabled = true
		if _, err := r.generateReview(context.Background(), "diff", gitlab.MergeRequest{}); err != nil {
			t.Fatalf("generateReview: %v", err)
		}
		logs := readWarnings()
		for _, want := range []string{
			"======== Prompt (attempt 1) Start ========",
			"======== Response (attempt 1) Start ========",
			prompt,
			"TOP-SECRET-RESPONSE-CONTENT",
		} {
			if !strings.Contains(logs, want) {
				t.Errorf("MRI_REVIEW_DUMP_ENABLED=true log missing %q: %q", want, logs)
			}
		}
	})

	t.Run("old disabled variable alone has no effect", func(t *testing.T) {
		unsetEnv(t, "MRI_REVIEW_DUMP_ENABLED")
		t.Setenv(retiredDumpEnv, "true")
		provider := &sequencedProvider{responses: []string{badResponse, validReviewBody}}
		r, readWarnings := newForensicsFixture(t, provider, v, 2, fakeComposer{prompt: prompt})
		if _, err := r.generateReview(context.Background(), "diff", gitlab.MergeRequest{}); err != nil {
			t.Fatalf("generateReview: %v", err)
		}
		logs := readWarnings()
		if strings.Contains(logs, "======== Prompt") || strings.Contains(logs, "======== Response") ||
			strings.Contains(logs, prompt) || strings.Contains(logs, "TOP-SECRET-RESPONSE-CONTENT") {
			t.Errorf("old disabled variable must not enable or expose dumps: %q", logs)
		}
	})
}

func expectedSHA256Prefix(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", digest)[:12]
}

// TestSelfReflect_InvalidReflectionKeepsOriginal verifies that a reflection
// result which fails ValidateReviewContent never replaces the original
// review, while a valid updated reflection still is adopted (control).
func TestSelfReflect_InvalidReflectionKeepsOriginal(t *testing.T) {
	original := validReviewBody
	v := configurableValidator{validateReviewContent: reviewSectionsPresent}

	t.Run("garbage reflection keeps the original review", func(t *testing.T) {
		unsetEnv(t, "MRI_REVIEW_DUMP_ENABLED")
		invalidReflection := "TOP-SECRET-INVALID-REFLECTION"
		provider := &sequencedProvider{responses: []string{invalidReflection}}
		r, readWarnings := newForensicsFixture(t, provider, v, 1, fakeComposer{prompt: "compose prompt"})

		got := r.selfReflect(context.Background(), original)
		if got != original {
			t.Errorf("selfReflect() = %q, want original review kept on invalid reflection", got)
		}
		logs := readWarnings()
		if !strings.Contains(logs, `"level":"WARN"`) {
			t.Errorf("expected a WARN log on invalid reflection: %q", logs)
		}
		if strings.Contains(logs, invalidReflection) || strings.Contains(logs, "======== Response (self-reflection)") {
			t.Errorf("default self-reflection warning leaked response content: %q", logs)
		}
	})

	t.Run("enabled true dumps invalid reflection", func(t *testing.T) {
		invalidReflection := "TOP-SECRET-INVALID-REFLECTION"
		provider := &sequencedProvider{responses: []string{invalidReflection}}
		r, readWarnings := newForensicsFixture(t, provider, v, 1, fakeComposer{prompt: "compose prompt"})
		r.cfg.ReviewDumpEnabled = true

		if got := r.selfReflect(context.Background(), original); got != original {
			t.Errorf("selfReflect() = %q, want original review", got)
		}
		logs := readWarnings()
		if !strings.Contains(logs, "======== Response (self-reflection) Start ========") || !strings.Contains(logs, invalidReflection) {
			t.Errorf("enabled self-reflection dump missing marker or response: %q", logs)
		}
	})

	t.Run("valid reflection is adopted", func(t *testing.T) {
		updated := "## Code Review\n## Findings\nnew finding\n## Verdict\nchanges requested"
		preambled := "Sure — here's the improved review:\n\n" + updated
		provider := &sequencedProvider{responses: []string{preambled}}
		r, _ := newForensicsFixture(t, provider, v, 1, fakeComposer{prompt: "compose prompt"})

		got := r.selfReflect(context.Background(), original)
		if got != updated {
			t.Errorf("selfReflect() = %q, want the valid reflection adopted (%q)", got, updated)
		}
	})
}

type erroringChangesGitLab struct {
	*fakeGitLab
	err error
}

func (g *erroringChangesGitLab) GetMRChanges(context.Context, string, string) (gitlab.MRChangesResponse, error) {
	return gitlab.MRChangesResponse{}, g.err
}

func TestRun_GetMRChangesFailureDependsOnReviewMode(t *testing.T) {
	changesErr := errors.New("changes API unavailable")

	t.Run("single mode continues with local diff", func(t *testing.T) {
		r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		r.cfg.ReviewMode = "single"
		gl := &erroringChangesGitLab{fakeGitLab: &fakeGitLab{}, err: changesErr}
		r.gitlab = gl

		r.Run(context.Background())

		note := gl.lastNote(t)
		if !strings.Contains(note, ReviewNoteMarker) || !strings.Contains(note, "## Code Review") {
			t.Errorf("single-mode review did not complete with the local diff: %q", note)
		}
	})

	t.Run("multi mode reports the changes API failure", func(t *testing.T) {
		r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
		r.cfg.ReviewMode = "multi"
		gl := &erroringChangesGitLab{fakeGitLab: &fakeGitLab{}, err: changesErr}
		r.gitlab = gl

		r.Run(context.Background())

		note := gl.lastNote(t)
		if !strings.Contains(note, "fetchDiff") || !strings.Contains(note, changesErr.Error()) {
			t.Errorf("multi-mode changes failure was not visibly posted: %q", note)
		}
		if strings.Contains(note, ReviewNoteMarker) {
			t.Errorf("multi-mode changes failure posted a normal review: %q", note)
		}
	})
}

func TestSplitDiffTrailer_UsesFinalMarker(t *testing.T) {
	embedded := "diff --git a/internal/diffbudget/diffbudget.go b/internal/diffbudget/diffbudget.go\n" +
		"@@ -1 +1 @@\n" +
		"+embedded source text\n\n<!-- mrinspect:diff-reduction -->\n" +
		"+this is still part of the reviewed diff\n"
	realTrailer := diffbudget.Trailer([]diffbudget.DroppedFile{{Path: "large.go", Reason: diffbudget.ReasonSizeBudget}})

	diffText, trailerText := splitDiffTrailer(embedded + realTrailer)

	if diffText != embedded {
		t.Errorf("diff text split at embedded marker:\n got %q\nwant %q", diffText, embedded)
	}
	if trailerText != realTrailer {
		t.Errorf("trailer = %q, want final appended trailer %q", trailerText, realTrailer)
	}
}

type changesGitLab struct {
	*fakeGitLab
	changes gitlab.MRChangesResponse
}

func (g *changesGitLab) GetMRChanges(context.Context, string, string) (gitlab.MRChangesResponse, error) {
	return g.changes, nil
}

func TestRun_DroppedFilesDisclosedInFooter(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})
	provider := newModeRoutingProvider()
	provider.laneResponders["standards"] = func(string) string {
		return `{"laneId":"standards","findings":[{"title":"dropped-file-finding","severity":"low","rationale":"the dropped file needs attention","file":"big/dropped.go","line":10}]}`
	}

	bigDiff := "@@ -1,50 +1,50 @@\n" + strings.Repeat("-old filler line\n+new filler line\n", 30)
	smallDiff := "@@ -1,1 +1,1 @@\n-old\n+new\n"
	gl := &changesGitLab{
		fakeGitLab: &fakeGitLab{},
		changes: gitlab.MRChangesResponse{Changes: []gitlab.Change{
			{NewPath: "big/dropped.go", Diff: bigDiff},
			{NewPath: "small/kept.go", Diff: smallDiff},
		}},
	}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		ReviewMode: "multi",
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service: config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{
			AIRetryAttempts: 1,
			MaxDiffSizeKB:   300,
		},
	}
	r := New(
		cfg,
		gl,
		provider,
		fakeDiff{},
		fakeProjects{},
		fakeComposer{prompt: "single prompt"},
		fakeValidator{},
		fakeErrorHandler{},
		logger.New(slog.LevelError, t.TempDir()+"/metrics.json"),
	)
	// PromptBudgetForModel first applies floor(250*0.8) = 200; Reduce then
	// applies the default 0.85 share, leaving about 170 tokens. The several-
	// hundred-token big diff exceeds that while the rendered tiny diff plus
	// its required reduction trailer survives.
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:    root,
		ModelLimits: map[string]int{"gemini-test": 250},
		Fanout: func(ctx context.Context, input lane.FanoutInput) (lane.FanoutResult, error) {
			input.ModelLimits = map[string]int{"gemini-test": 1_000_000}
			return lane.Fanout(ctx, input)
		},
	})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if !strings.Contains(note, "_Dropped for diff size budget: big/dropped.go_") {
		t.Errorf("posted note does not disclose the dropped file: %q", note)
	}
	provider.mu.Lock()
	prompts := append([]string(nil), provider.prompts...)
	provider.mu.Unlock()
	trailerSeen := false
	for _, receivedPrompt := range prompts {
		if strings.Contains(receivedPrompt, "<!-- mrinspect:diff-reduction -->") {
			trailerSeen = true
			break
		}
	}
	if !trailerSeen {
		t.Errorf("AI prompts do not contain the diff-reduction trailer: %#v", prompts)
	}
	if !strings.Contains(note, "big/dropped.go (location-unverifiable)") {
		t.Errorf("finding against dropped file is not location-unverifiable: %q", note)
	}
}

func TestRun_UnderCapDropsNothingInFooter(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})
	provider := newModeRoutingProvider()
	gl := &changesGitLab{
		fakeGitLab: &fakeGitLab{},
		changes: gitlab.MRChangesResponse{Changes: []gitlab.Change{
			{NewPath: "src/one.go", Diff: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
			{NewPath: "src/two.go", Diff: "@@ -2,1 +2,1 @@\n-before\n+after\n"},
		}},
	}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		ReviewMode: "multi",
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service: config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{
			AIRetryAttempts: 1,
			MaxDiffSizeKB:   300,
		},
	}
	r := New(
		cfg,
		gl,
		provider,
		fakeDiff{},
		fakeProjects{},
		fakeComposer{prompt: "single prompt"},
		fakeValidator{},
		fakeErrorHandler{},
		logger.New(slog.LevelError, t.TempDir()+"/metrics.json"),
	)
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:    root,
		ModelLimits: map[string]int{"gemini-test": 1_000_000},
	})

	r.Run(context.Background())

	note := gl.lastNote(t)
	if strings.Contains(note, "_Dropped for diff size budget:") {
		t.Errorf("under-cap review unexpectedly disclosed dropped files: %q", note)
	}
}

// installInfoLogRecorder is installWarningLogRecorder's INFO-level twin: the
// prompt-composition breakdown is logged at Info, not Warn, so these tests
// need a lower-level sink than the existing WARN recorder provides.
func installInfoLogRecorder(t *testing.T, r *MRInspectReviewer) func() string {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "reviewer-info-*.jsonl")
	if err != nil {
		t.Fatalf("create info log sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	stdout := os.Stdout
	os.Stdout = sink
	r.log = logger.New(slog.LevelInfo, filepath.Join(t.TempDir(), "metrics.json"))
	os.Stdout = stdout

	return func() string {
		t.Helper()
		if err := sink.Sync(); err != nil {
			t.Fatalf("sync info log sink: %v", err)
		}
		data, err := os.ReadFile(sink.Name())
		if err != nil {
			t.Fatalf("read info log sink: %v", err)
		}
		return string(data)
	}
}

// breakdownLogRecord is the subset of one JSON log line needed by the
// prompt-breakdown tests below.
type breakdownLogRecord struct {
	Msg  string `json:"msg"`
	Lane string `json:"lane"`
}

// breakdownRecords decodes every JSON log line whose msg contains label.
func breakdownRecords(t *testing.T, logs, label string) []breakdownLogRecord {
	t.Helper()
	var records []breakdownLogRecord
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, label) {
			continue
		}
		var record breakdownLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// sumBreakdownPercentages sums every row percentage in a breakdown table
// message EXCEPT the "**total**" row, which restates the same 100%.
func sumBreakdownPercentages(t *testing.T, table string) float64 {
	t.Helper()
	re := regexp.MustCompile(`\| ([0-9]+\.[0-9])% \|`)
	sum := 0.0
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "**total**") {
			continue
		}
		match := re.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			t.Fatalf("parse breakdown percentage %q: %v", match[1], err)
		}
		sum += value
	}
	return sum
}

// TestPromptBreakdown_SingleMode verifies the always-on, incident-proven
// prompt-composition breakdown is logged once the single-mode review prompt
// is composed: a diff row, a total row, and percentages that sum to ~100.
func TestPromptBreakdown_SingleMode(t *testing.T) {
	r, _ := newReviewerFixture(t, fakeComposer{prompt: "BASE-PROMPT-METADATA-AND-INSTRUCTIONS"})
	readLogs := installInfoLogRecorder(t, r)

	const diffText = "diff --git a/service.go b/service.go\n+added line\n"
	if _, err := r.generateReview(context.Background(), diffText, gitlab.MergeRequest{}); err != nil {
		t.Fatalf("generateReview: %v", err)
	}

	records := breakdownRecords(t, readLogs(), "Prompt composition breakdown")
	if len(records) != 1 {
		t.Fatalf("prompt composition breakdown log count = %d, want exactly 1: logs=%s", len(records), readLogs())
	}
	table := records[0].Msg
	if !strings.Contains(table, "| diff |") {
		t.Errorf("breakdown table has no diff row: %q", table)
	}
	if !strings.Contains(table, "| **total** |") {
		t.Errorf("breakdown table has no total row: %q", table)
	}
	if sum := sumBreakdownPercentages(t, table); sum < 99.0 || sum > 101.0 {
		t.Errorf("breakdown percentages sum to %.2f, want ~100: %q", sum, table)
	}
}

// TestPromptBreakdown_MultiPerLane verifies one breakdown table is logged
// per enabled lane, naming its lane ID in the log fields and aggregating a
// retrieval resource set's chunks into a single named row.
func TestPromptBreakdown_MultiPerLane(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})

	lanesPath := filepath.Join(root, "projects", "lanes.yaml")
	lanesYAML, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("read lanes fixture: %v", err)
	}
	lanesYAML = []byte(strings.Replace(string(lanesYAML), "sets: []", "sets: [official-standards]", 1))
	if err := os.WriteFile(lanesPath, lanesYAML, 0o644); err != nil {
		t.Fatalf("write lanes fixture resources: %v", err)
	}

	resourcesYAML := []byte("sets:\n  - name: official-standards\n    mode: retrieval\n    paths: []\n")
	if err := os.WriteFile(filepath.Join(root, "projects", "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("write resources fixture: %v", err)
	}
	resourceRegistry, err := resources.Load(root, "")
	if err != nil {
		t.Fatalf("load resources fixture: %v", err)
	}

	retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{
			{ID: "std-1", Source: "standards/one.md", ResourceSet: "official-standards", Text: "standard one content"},
			{ID: "std-2", Source: "standards/two.md", ResourceSet: "official-standards", Text: "standard two content"},
		}},
	}}
	provider := newModeRoutingProvider()
	provider.laneResponders["standards"] = func(string) string {
		return `{"laneId":"standards","findings":[]}`
	}
	r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.ReviewMode = "multi"
	r.ai = provider
	readLogs := installInfoLogRecorder(t, r)
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:         root,
		ResourceRegistry: resourceRegistry,
		Retriever:        retriever,
		ModelLimits:      reviewerModelLimits(),
	})

	r.Run(context.Background())

	records := breakdownRecords(t, readLogs(), "Prompt composition breakdown")
	if len(records) != 1 {
		t.Fatalf("prompt composition breakdown log count = %d, want exactly 1 (one enabled lane): logs=%s", len(records), readLogs())
	}
	if records[0].Lane != "standards" {
		t.Errorf("breakdown log lane field = %q, want %q", records[0].Lane, "standards")
	}
	if !strings.Contains(records[0].Msg, "official-standards") {
		t.Errorf("lane breakdown missing aggregated resource set row %q: %q", "official-standards", records[0].Msg)
	}
	if !strings.Contains(records[0].Msg, "| diff |") {
		t.Errorf("lane breakdown missing diff row: %q", records[0].Msg)
	}
}

// TestPromptBreakdown_SelfReflect verifies a small breakdown is logged
// before the self-reflection AI call.
func TestPromptBreakdown_SelfReflect(t *testing.T) {
	r, _ := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	r.cfg.SelfReflection = true
	provider := newModeRoutingProvider()
	r.ai = provider
	readLogs := installInfoLogRecorder(t, r)

	r.Run(context.Background())

	records := breakdownRecords(t, readLogs(), "Self-reflection prompt breakdown")
	if len(records) != 1 {
		t.Fatalf("self-reflection breakdown log count = %d, want exactly 1: logs=%s", len(records), readLogs())
	}
	if !strings.Contains(records[0].Msg, "original review") {
		t.Errorf("self-reflection breakdown missing original review row: %q", records[0].Msg)
	}
	if !strings.Contains(records[0].Msg, "reflection instructions") {
		t.Errorf("self-reflection breakdown missing reflection instructions row: %q", records[0].Msg)
	}
}
