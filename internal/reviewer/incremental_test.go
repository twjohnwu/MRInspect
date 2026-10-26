package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/diff"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
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

const (
	updPriorSHA = "1111111111111111111111111111111111111111"
	updHeadSHA  = "2222222222222222222222222222222222222222"
	updBaseSHA  = "3333333333333333333333333333333333333333"
)

const updDiffBody = "@@ -1,1 +1,1 @@\n-old\n+new\n"

// diffEchoComposer puts the received diff into the single-mode prompt.
type diffEchoComposer struct{ fakeComposer }

func (diffEchoComposer) ComposeReviewPrompt(_ project.LoadedProject, diffText string, _ gitlab.MergeRequest) (string, error) {
	return "single prompt\n" + diffText, nil
}

// countingDiff counts calls to the local diff fetcher.
type countingDiff struct{ calls int }

func (d *countingDiff) Fetch(context.Context, string, string) (string, error) {
	d.calls++
	return "diff --git a/local.go b/local.go", nil
}

// updateFixture is the S-07 / S-09 setup: prior state A (base X), head B,
// Compare(A,B) holds only x.go, the MR holds x.go and y.go.
func updateFixture(t *testing.T, mode string) (*MRInspectReviewer, *fakeGitLab, *modeRoutingProvider, *countingDiff) {
	t.Helper()
	prior := FormatState(State{SHA: updPriorSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
	r, gl, provider, _ := incrFixture(t, mode, true, prior)
	r.prompt = diffEchoComposer{}
	local := &countingDiff{}
	r.diff = local
	gl.mr.SHA = updHeadSHA
	gl.mr.DiffRefs.BaseSHA = updBaseSHA
	gl.mergeBase = updPriorSHA
	gl.compare = gitlab.CompareResponse{Diffs: []gitlab.Change{
		{OldPath: "x.go", NewPath: "x.go", Diff: updDiffBody},
	}}
	gl.changes = gitlab.MRChangesResponse{SHA: updHeadSHA, Changes: []gitlab.Change{
		{OldPath: "x.go", NewPath: "x.go", Diff: updDiffBody},
		{OldPath: "y.go", NewPath: "y.go", Diff: updDiffBody},
	}}
	return r, gl, provider, local
}

// TestIncremental_Update verifies REQ-05 / S-07.
func TestIncremental_Update(t *testing.T) {
	const header = "Other files changed in this MR (not shown in this diff):"
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			r, gl, provider, local := updateFixture(t, mode)

			r.Run(context.Background())

			if provider.callCount() == 0 {
				t.Fatal("model was not called")
			}
			provider.mu.Lock()
			prompts := append([]string(nil), provider.prompts...)
			provider.mu.Unlock()
			for _, prompt := range prompts {
				if prompt == "reflection" {
					continue
				}
				if !diff.HasFileSection(prompt, "x.go") {
					t.Errorf("prompt lacks x.go file section: %q", prompt)
				}
				if diff.HasFileSection(prompt, "y.go") {
					t.Errorf("prompt has y.go file section: %q", prompt)
				}
				idx := strings.Index(prompt, header)
				if idx < 0 {
					t.Errorf("prompt lacks header %q: %q", header, prompt)
					continue
				}
				tail := prompt[idx+len(header):]
				if !strings.Contains("\n"+tail, "\ny.go") {
					t.Errorf("section after header lacks a y.go line: %q", tail)
				}
				if strings.Contains(tail, "x.go") {
					t.Errorf("section after header contains x.go: %q", tail)
				}
			}
			if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
				t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
			}
			if gl.compareCalls != 1 {
				t.Errorf("Compare calls = %d, want 1", gl.compareCalls)
			}
			if gl.lastMergeBaseProject != "1" || gl.lastCompareProject != "1" {
				t.Errorf("GetMergeBase/Compare project = %q/%q, want 1/1", gl.lastMergeBaseProject, gl.lastCompareProject)
			}
			if local.calls != 0 {
				t.Errorf("local diff fetch calls = %d, want 0", local.calls)
			}
			body := gl.lastNote(t)
			if first := strings.SplitN(body, "\n", 2)[0]; first != ReviewNoteMarker {
				t.Errorf("first line = %q, want %q", first, ReviewNoteMarker)
			}
			wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeUpdate, Round: 1})
			if !strings.Contains(body, wantState) {
				t.Errorf("body lacks state %q: %q", wantState, body)
			}
			wantHeader := "Update review " + updPriorSHA[:8] + ".." + updHeadSHA[:8]
			if !strings.Contains("\n"+body, "\n"+wantHeader) {
				t.Errorf("body lacks line %q: %q", wantHeader, body)
			}
		})
	}
}

// TestIncremental_NoStateNoteDoesNotEnableSkip verifies REQ-06 / S-09.
func TestIncremental_NoStateNoteDoesNotEnableSkip(t *testing.T) {
	r, gl, provider, _ := updateFixture(t, "single")
	bot := gl.currentUser
	gl.listedNotes = []gitlab.Note{
		{ID: 10, Author: bot, CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z",
			Body: ReviewNoteMarker + "\n" + FormatState(State{SHA: updPriorSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0}) + "\nold review"},
		{ID: 20, Author: bot, CreatedAt: "2026-10-02T00:00:00Z", UpdatedAt: "2026-10-02T00:00:00Z",
			Body: ReviewNoteMarker + "\nreview of head B without state"},
	}

	r.Run(context.Background())

	if n := provider.callCount(); n != 1 {
		t.Errorf("model calls = %d, want 1", n)
	}
	if gl.compareCalls != 1 {
		t.Errorf("Compare calls = %d, want 1", gl.compareCalls)
	}
	if gl.lastMergeBaseProject != "1" || gl.lastCompareProject != "1" {
		t.Errorf("GetMergeBase/Compare project = %q/%q, want 1/1", gl.lastMergeBaseProject, gl.lastCompareProject)
	}
	if gl.postCalls != 1 {
		t.Fatalf("PostNote calls = %d, want 1", gl.postCalls)
	}
	wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeUpdate, Round: 1})
	if body := gl.lastNote(t); !strings.Contains(body, wantState) {
		t.Errorf("body lacks state %q: %q", wantState, body)
	}
}
