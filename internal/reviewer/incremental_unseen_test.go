package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

// TestIncremental_UnseenContentIsolated verifies REQ-06 / S-08 (2) in
// isolation from the size-budget rule: a too_large / collapsed / truncated
// file whose content is not in the sent diff blocks the state line, and a
// hunk in the sent diff clears it. No case has a size-budget drop; (i) uses a
// non-reviewable lockfile drop only to force the diff rebuild from the MR
// changes.
func TestIncremental_UnseenContentIsolated(t *testing.T) {
	fullGo := gitlab.Change{OldPath: "full.go", NewPath: "full.go", Diff: updDiffBody}
	lock := gitlab.Change{OldPath: "package-lock.json", NewPath: "package-lock.json", Diff: v3BigDiff}
	collapsedPatch := degradePatch + "diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n" + updDiffBody
	tests := []struct {
		name      string
		patch     string
		changes   []gitlab.Change
		wantState bool
		wantWarn  string
	}{
		{name: "i rename too_large, empty diff, header-only section",
			patch: degradePatch,
			changes: []gitlab.Change{lock, fullGo,
				{OldPath: "old/name.go", NewPath: "new/name.go", RenamedFile: true, TooLarge: true}},
			wantWarn: "not sent to the model: new/name.go"},
		{name: "ii collapsed file absent from the fetcher diff",
			patch: degradePatch,
			changes: []gitlab.Change{fullGo,
				{OldPath: "c.go", NewPath: "c.go", Collapsed: true, Diff: updDiffBody}},
			wantWarn: "not sent to the model: c.go"},
		{name: "iii control: collapsed file has a hunk in the fetcher diff",
			patch: collapsedPatch,
			changes: []gitlab.Change{fullGo,
				{OldPath: "c.go", NewPath: "c.go", Collapsed: true, Diff: updDiffBody}},
			wantState: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, _, _ := updateFixture(t, "single")
			readWarn := installWarningLogRecorder(t, r)
			r.SetMultiLaneReviewPath(MultiLaneReviewPath{
				RepoRoot:    t.TempDir(),
				ModelLimits: map[string]int{"gemini-test": 250},
			})
			r.cfg.Validation.MaxDiffSizeKB = 300
			r.diff = v3Source{patch: tt.patch, sha: updHeadSHA}
			gl.listedNotes[0].Body = rulesStatelessBody
			gl.changes = gitlab.MRChangesResponse{SHA: updHeadSHA, Changes: tt.changes}

			r.Run(context.Background())

			if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
				t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
			}
			body := gl.lastNote(t)
			if tt.wantState {
				wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
				if !strings.Contains(body, wantState) {
					t.Errorf("body lacks state %q: %q", wantState, body)
				}
			} else if strings.Contains(body, "mrinspect:state") {
				t.Errorf("dirty run wrote a state comment: %q", body)
			}
			warns := readWarn()
			if tt.wantWarn != "" && !strings.Contains(warns, tt.wantWarn) {
				t.Errorf("Warn output lacks %q: %s", tt.wantWarn, warns)
			}
			if tt.wantState && strings.Contains(warns, "not sent to the model") {
				t.Errorf("control run warned about unsent content: %s", warns)
			}
		})
	}
}
