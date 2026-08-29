package reviewer

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
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

type fakeGitLab struct{ notes []string }

func (*fakeGitLab) HealthCheck(context.Context) bool { return true }
func (*fakeGitLab) GetMergeRequest(context.Context, string, string) (gitlab.MergeRequest, error) {
	return gitlab.MergeRequest{IID: 7, Title: "RAG test", SourceBranch: "feature", TargetBranch: "main"}, nil
}
func (*fakeGitLab) GetMRChanges(context.Context, string, string) (gitlab.MRChangesResponse, error) {
	return gitlab.MRChangesResponse{}, nil
}
func (g *fakeGitLab) PostNote(_ context.Context, _, _, body string) (gitlab.Note, error) {
	g.notes = append(g.notes, body)
	return gitlab.Note{Body: body}, nil
}
func (g *fakeGitLab) lastNote(t *testing.T) string {
	t.Helper()
	if len(g.notes) == 0 {
		t.Fatal("PostNote was not called")
	}
	return g.notes[len(g.notes)-1]
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
