package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

const (
	incrHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	incrBaseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// incrFixture builds a reviewer for mode ("single" or "multi") whose fake
// GitLab holds one unedited bot marker note carrying stateLine.
func incrFixture(t *testing.T, mode string, enabled bool, stateLine string) (*MRInspectReviewer, *fakeGitLab, *modeRoutingProvider, func() string) {
	t.Helper()
	r, gl := newReviewerFixture(t, fakeComposer{prompt: "single prompt"})
	readInfo := installInfoLogRecorder(t, r)
	provider := newModeRoutingProvider()
	r.ai = provider
	r.cfg.ReviewMode = mode
	if mode == "multi" {
		root := writeLaneFixture(t, []laneFixture{
			{id: "standards", enabled: true},
			{id: "code-diff", enabled: true},
		})
		r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: root, ModelLimits: reviewerModelLimits()})
	}
	r.cfg.Incremental.Enabled = enabled
	r.cfg.Incremental.FullEvery = 5

	bot := gitlab.Author{ID: 101, Username: "mrinspect-bot"}
	gl.currentUser = bot
	gl.mr = gitlab.MergeRequest{
		IID: 7, Title: "RAG test", SourceBranch: "feature", TargetBranch: "main",
		SHA: incrHeadSHA, DiffRefs: gitlab.DiffRefs{BaseSHA: incrBaseSHA},
	}
	gl.listedNotes = []gitlab.Note{{
		ID: 41, Author: bot,
		Body:      ReviewNoteMarker + "\n" + stateLine + "\nold review",
		CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z",
	}}
	return r, gl, provider, readInfo
}

func incrState() string {
	return FormatState(State{SHA: incrHeadSHA, Base: incrBaseSHA, Mode: ModeFull, Round: 0})
}

// incrDisabledGolden is the note body the pre-incremental implementation
// writes for the single-mode fixture in TestIncremental_DisabledPathUnchanged.
const incrDisabledGolden = `<!-- mrinspect:review -->
## Code Review
## Findings
SINGLE-REVIEW-COMPLETE
## Verdict
Approved`

// TestIncremental_DisabledPathUnchanged verifies REQ-02 / S-02.
func TestIncremental_DisabledPathUnchanged(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			r, gl, provider, _ := incrFixture(t, mode, false, incrState())

			r.Run(context.Background())

			if provider.callCount() == 0 {
				t.Error("model was not called, want at least one call with incremental disabled")
			}
			if len(gl.updateCalls) != 1 {
				t.Errorf("UpdateNote calls = %d, want 1", len(gl.updateCalls))
			}
			if gl.postCalls != 0 {
				t.Errorf("PostNote calls = %d, want 0", gl.postCalls)
			}
			if gl.mergeBaseCalls != 0 || gl.compareCalls != 0 {
				t.Errorf("GetMergeBase/Compare calls = %d/%d, want 0/0", gl.mergeBaseCalls, gl.compareCalls)
			}
			if mode != "single" || len(gl.updateCalls) != 1 {
				return
			}
			body := gl.updateCalls[0].body
			if strings.Contains(body, "mrinspect:state") {
				t.Errorf("updated body contains a state comment: %q", body)
			}
			if body != incrDisabledGolden {
				t.Errorf("updated body = %q, want golden %q", body, incrDisabledGolden)
			}
		})
	}
}

// TestIncremental_SkipSameHead verifies REQ-04 / S-05.
func TestIncremental_SkipSameHead(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			r, gl, provider, readInfo := incrFixture(t, mode, true, incrState())

			r.Run(context.Background())

			if n := provider.callCount(); n != 0 {
				t.Errorf("model calls = %d, want 0", n)
			}
			if gl.postCalls != 0 || len(gl.updateCalls) != 0 {
				t.Errorf("PostNote/UpdateNote calls = %d/%d, want 0/0", gl.postCalls, len(gl.updateCalls))
			}
			if gl.changesCalls != 0 || gl.mergeBaseCalls != 0 || gl.compareCalls != 0 {
				t.Errorf("GetMRChanges/GetMergeBase/Compare calls = %d/%d/%d, want 0/0/0",
					gl.changesCalls, gl.mergeBaseCalls, gl.compareCalls)
			}
			logs := readInfo()
			if !strings.Contains(logs, "incremental: head unchanged") {
				t.Errorf("log lacks Info line %q", "incremental: head unchanged")
			}
			completed := false
			for _, line := range strings.Split(logs, "\n") {
				if strings.Contains(line, "MRInspect completed") && strings.Contains(line, `"success":true`) {
					completed = true
				}
			}
			if !completed {
				t.Errorf("log lacks %q line with success=true", "MRInspect completed")
			}
		})
	}
}
