package reviewer

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane"
	"mrinspect/internal/logger"
)

const notReviewedPrefix = "_Not reviewed (diff unavailable from GitLab):"

func truncatedFooterChanges(withTruncated bool) gitlab.MRChangesResponse {
	changes := []gitlab.Change{
		{OldPath: "small/kept.go", NewPath: "small/kept.go", Diff: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
	}
	if withTruncated {
		changes = append(changes,
			gitlab.Change{OldPath: "cut/one.go", NewPath: "cut/one.go"},
			gitlab.Change{OldPath: "cut/two.go", NewPath: "cut/two.go"},
		)
	}
	return gitlab.MRChangesResponse{Changes: changes}
}

func runTruncatedFooterReview(t *testing.T, mode string, withTruncated bool) string {
	t.Helper()
	gl := &changesGitLab{fakeGitLab: &fakeGitLab{}, changes: truncatedFooterChanges(withTruncated)}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		ReviewMode: mode,
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service:    config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{AIRetryAttempts: 1, MaxDiffSizeKB: 300},
	}
	r := New(cfg, gl, newModeRoutingProvider(), fakeDiff{}, fakeProjects{}, fakeComposer{prompt: "single prompt"},
		fakeValidator{}, fakeErrorHandler{}, logger.New(slog.LevelError, t.TempDir()+"/metrics.json"))
	if mode == "multi" {
		root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})
		r.SetMultiLaneReviewPath(MultiLaneReviewPath{
			RepoRoot:    root,
			ModelLimits: map[string]int{"gemini-test": 1_000_000},
			Fanout: func(ctx context.Context, input lane.FanoutInput) (lane.FanoutResult, error) {
				input.ModelLimits = map[string]int{"gemini-test": 1_000_000}
				return lane.Fanout(ctx, input)
			},
		})
	}
	r.Run(context.Background())
	return gl.lastNote(t)
}

func TestFooter_TruncatedFilesListedInSingleAndMultiMode(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			note := runTruncatedFooterReview(t, mode, true)
			want := notReviewedPrefix + " cut/one.go, cut/two.go_"
			if !strings.Contains(note, want) {
				t.Errorf("note does not list truncated files, want %q in %q", want, note)
			}
			if strings.Contains(note, "_Dropped for diff size budget:") {
				t.Errorf("truncated files leaked into size-budget line: %q", note)
			}
		})
	}
}

func TestFooter_NoTruncatedFilesLeavesFooterUnchanged(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			note := runTruncatedFooterReview(t, mode, false)
			if strings.Contains(note, "Not reviewed") {
				t.Errorf("unexpected not-reviewed line without truncated files: %q", note)
			}
		})
	}

	r, _ := newReviewerFixture(t, fakeComposer{prompt: "p"})
	got := r.ragFooterWithAggregation(footerAggregation{droppedFiles: []string{"a.go", "b.go"}})
	want := "\n\n_Dropped for diff size budget: a.go, b.go_"
	if got != want {
		t.Errorf("footer changed without truncated files:\n got %q\nwant %q", got, want)
	}
	if got := r.ragFooterWithAggregation(footerAggregation{}); got != "" {
		t.Errorf("empty footer = %q, want empty", got)
	}
}
