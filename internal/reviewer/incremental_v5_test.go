package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

// TestIncremental_DegradeAndStateWritingV5 verifies REQ-06 / S-08 (s): an empty
// new file (new_file=true, empty diff, neither too_large nor collapsed) does not
// count as unreviewed; the same file with too_large=true still blocks state.
func TestIncremental_DegradeAndStateWritingV5(t *testing.T) {
	aGo := gitlab.Change{OldPath: "a.go", NewPath: "a.go", Diff: updDiffBody}
	tests := []struct {
		name      string
		emptyNew  gitlab.Change
		wantState bool
	}{
		{name: "s empty new file is not unreviewed",
			emptyNew:  gitlab.Change{OldPath: "pkg/__init__.py", NewPath: "pkg/__init__.py", NewFile: true},
			wantState: true},
		{name: "control too_large empty new file blocks state",
			emptyNew:  gitlab.Change{OldPath: "pkg/__init__.py", NewPath: "pkg/__init__.py", NewFile: true, TooLarge: true},
			wantState: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, _, _ := updateFixture(t, "single")
			readWarn := installWarningLogRecorder(t, r)
			r.diff = v3Source{patch: v4GoSection, sha: updHeadSHA}
			gl.listedNotes[0].Body = rulesStatelessBody
			gl.changes = gitlab.MRChangesResponse{SHA: updHeadSHA, Changes: []gitlab.Change{aGo, tt.emptyNew}}

			r.Run(context.Background())

			if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
				t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
			}
			body := gl.lastNote(t)
			wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
			warns := readWarn()
			if tt.wantState {
				if !strings.Contains(body, wantState) {
					t.Errorf("body lacks state %q: %q", wantState, body)
				}
				if !strings.Contains(body, "Full review at") {
					t.Errorf("body lacks %q: %q", "Full review at", body)
				}
				if strings.Contains(warns, "not sent to the model") {
					t.Errorf("unexpected unreviewed-file Warn: %s", warns)
				}
				return
			}
			if strings.Contains(body, "<!-- mrinspect:state") || strings.Contains(body, wantState) {
				t.Errorf("body unexpectedly carries state: %q", body)
			}
			if !strings.Contains(warns, "not sent to the model") || !strings.Contains(warns, "pkg/__init__.py") {
				t.Errorf("Warn lacks %q naming pkg/__init__.py: %s", "not sent to the model", warns)
			}
		})
	}
}
