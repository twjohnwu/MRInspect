package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

const v4GoSection = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"

const v4BinarySection = "diff --git a/logo.png b/logo.png\nindex 1111111..2222222 100644\nBinary files a/logo.png and b/logo.png differ\n"

// TestIncremental_DegradeAndStateWritingV4 verifies REQ-06 / S-08 (q)–(r).
func TestIncremental_DegradeAndStateWritingV4(t *testing.T) {
	aGo := gitlab.Change{OldPath: "a.go", NewPath: "a.go", Diff: updDiffBody}
	tests := []struct {
		name    string
		patch   string
		changes []gitlab.Change
	}{
		{name: "q collapsed lockfile has no hunk, nothing dropped for budget",
			patch: v4GoSection,
			changes: []gitlab.Change{
				{OldPath: "package-lock.json", NewPath: "package-lock.json", Collapsed: true},
				aGo,
			}},
		{name: "r too_large binary has a Binary files line",
			patch: v4GoSection + v4BinarySection,
			changes: []gitlab.Change{
				{OldPath: "logo.png", NewPath: "logo.png", TooLarge: true},
				aGo,
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, _, _ := updateFixture(t, "single")
			readWarn := installWarningLogRecorder(t, r)
			r.diff = v3Source{patch: tt.patch, sha: updHeadSHA}
			gl.listedNotes[0].Body = rulesStatelessBody
			gl.changes = gitlab.MRChangesResponse{SHA: updHeadSHA, Changes: tt.changes}

			r.Run(context.Background())

			if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
				t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
			}
			body := gl.lastNote(t)
			wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
			if !strings.Contains(body, wantState) {
				t.Errorf("body lacks state %q: %q", wantState, body)
			}
			if !strings.Contains(body, "Full review at") {
				t.Errorf("body lacks %q: %q", "Full review at", body)
			}
			if warns := readWarn(); strings.Contains(warns, "not sent to the model") {
				t.Errorf("unexpected unreviewed-file Warn: %s", warns)
			}
		})
	}
}
