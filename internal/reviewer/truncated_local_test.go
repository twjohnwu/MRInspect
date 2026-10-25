package reviewer

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/logger"
)

// textDiff is a diff fetcher returning fixed text, standing in for the local
// go-git fetcher whose output includes files GitLab marks truncated.
type textDiff string

func (d textDiff) Fetch(context.Context, string, string) (string, error) { return string(d), nil }

const localStyleDiff = "diff --git a/small/kept.go b/small/kept.go\nindex 111..222 100644\n--- a/small/kept.go\n+++ b/small/kept.go\n@@ -1,1 +1,1 @@\n-old\n+new\n" +
	"diff --git a/cut/one.go b/cut/one.go\nindex 333..444 100644\n--- a/cut/one.go\n+++ b/cut/one.go\n@@ -1,1 +1,1 @@\n-a\n+b\n" +
	"diff --git a/old/name.go b/new/name.go\nsimilarity index 90%\nrename from old/name.go\nrename to new/name.go\nindex 555..666 100644\n--- a/old/name.go\n+++ b/new/name.go\n@@ -1,1 +1,1 @@\n-x\n+y\n"

func runLocalTruncatedReview(t *testing.T, fetched string, changes []gitlab.Change) string {
	t.Helper()
	gl := &changesGitLab{fakeGitLab: &fakeGitLab{}, changes: gitlab.MRChangesResponse{Changes: changes}}
	cfg := config.Config{
		AIProvider: config.ProviderGemini,
		ReviewMode: "single",
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderGemini: {Model: "gemini-test", MaxTokens: 10},
		},
		Service:    config.ServiceConfig{Name: "test", Type: "backend"},
		Validation: config.ValidationConfig{AIRetryAttempts: 1, MaxDiffSizeKB: 300},
	}
	r := New(cfg, gl, newModeRoutingProvider(), textDiff(fetched), fakeProjects{}, fakeComposer{prompt: "single prompt"},
		fakeValidator{}, fakeErrorHandler{}, logger.New(slog.LevelError, t.TempDir()+"/metrics.json"))
	r.Run(context.Background())
	return gl.lastNote(t)
}

func TestFooter_TruncatedFileCoveredByLocalDiffNotListed(t *testing.T) {
	note := runLocalTruncatedReview(t, localStyleDiff, []gitlab.Change{
		{OldPath: "small/kept.go", NewPath: "small/kept.go", Diff: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
		{OldPath: "cut/one.go", NewPath: "cut/one.go"},
		{OldPath: "old/name.go", NewPath: "new/name.go", RenamedFile: true},
	})
	if strings.Contains(note, "Not reviewed") {
		t.Errorf("footer lists files present in the reviewed diff: %q", note)
	}
}

func TestFooter_TruncatedFileMissingFromLocalDiffStillListed(t *testing.T) {
	note := runLocalTruncatedReview(t, localStyleDiff, []gitlab.Change{
		{OldPath: "small/kept.go", NewPath: "small/kept.go", Diff: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
		{OldPath: "cut/one.go", NewPath: "cut/one.go"},
		{OldPath: "cut/absent.go", NewPath: "cut/absent.go"},
	})
	want := notReviewedPrefix + " cut/absent.go_"
	if !strings.Contains(note, want) {
		t.Errorf("want %q in %q", want, note)
	}
}
