package reviewer

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/diffbudget"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/logger"
)

// TestFetchDiff_BudgetRebuildExcludesAndDisclosesTruncated covers the rebuild
// path: when the budget drops files, the reviewed diff is rebuilt from API
// changes, and a GitLab-truncated (empty-diff) file must not survive as a
// header-only entry.
func TestFetchDiff_BudgetRebuildExcludesAndDisclosesTruncated(t *testing.T) {
	root := writeLaneFixture(t, []laneFixture{{id: "standards", enabled: true}})
	bigDiff := "@@ -1,50 +1,50 @@\n" + strings.Repeat("-old filler line\n+new filler line\n", 30)
	gl := &changesGitLab{
		fakeGitLab: &fakeGitLab{},
		changes: gitlab.MRChangesResponse{Overflow: true, Changes: []gitlab.Change{
			{OldPath: "big/dropped.go", NewPath: "big/dropped.go", Diff: bigDiff},
			{OldPath: "small/kept.go", NewPath: "small/kept.go", Diff: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
			{OldPath: "cut/truncated.go", NewPath: "cut/truncated.go"},
			{OldPath: "old/name.go", NewPath: "new/name.go", RenamedFile: true},
		}},
	}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		ReviewMode: "multi",
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service:    config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{AIRetryAttempts: 1, MaxDiffSizeKB: 300},
	}
	r := New(cfg, gl, newModeRoutingProvider(), fakeDiff{}, fakeProjects{}, fakeComposer{prompt: "p"},
		fakeValidator{}, fakeErrorHandler{}, logger.New(slog.LevelError, t.TempDir()+"/metrics.json"))
	r.SetMultiLaneReviewPath(MultiLaneReviewPath{
		RepoRoot:    root,
		ModelLimits: map[string]int{"gemini-test": 250},
	})

	fetched, err := r.fetchDiff(context.Background())
	if err != nil {
		t.Fatalf("fetchDiff() error: %v", err)
	}
	if len(fetched.dropped) == 0 {
		t.Fatalf("fixture did not trigger a budget drop; rebuild path not exercised")
	}
	if strings.Contains(fetched.diff, "+++ b/cut/truncated.go") || strings.Contains(fetched.diff, "--- a/cut/truncated.go") {
		t.Errorf("truncated file header leaked into reviewed diff:\n%s", fetched.diff)
	}
	if !strings.Contains(fetched.diff, "cut/truncated.go ("+diffbudget.ReasonDiffUnavailable+")") {
		t.Errorf("truncated file not disclosed in trailer:\n%s", fetched.diff)
	}
	if !strings.Contains(fetched.diff, "+++ b/new/name.go") {
		t.Errorf("pure rename wrongly excluded:\n%s", fetched.diff)
	}
	if strings.Contains(fetched.diff, "new/name.go ("+diffbudget.ReasonDiffUnavailable+")") {
		t.Errorf("pure rename wrongly flagged:\n%s", fetched.diff)
	}
}
