package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

// TestIncremental_EmptyChangesSHAWithholdsState verifies REQ-06 / S-08 (m):
// on a full review a successful GetMRChanges whose sha is empty differs from
// the pinned head, so no state line is recorded.
func TestIncremental_EmptyChangesSHAWithholdsState(t *testing.T) {
	r, gl, _, _ := updateFixture(t, "single")
	readWarn := installWarningLogRecorder(t, r)
	r.diff = v3Source{patch: v4GoSection, sha: updHeadSHA}
	gl.listedNotes[0].Body = rulesStatelessBody
	gl.changes = gitlab.MRChangesResponse{
		SHA:     "",
		Changes: []gitlab.Change{{OldPath: "a.go", NewPath: "a.go", Diff: updDiffBody}},
	}

	r.Run(context.Background())

	if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
		t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
	}
	if body := gl.lastNote(t); strings.Contains(body, "mrinspect:state") {
		t.Errorf("body carries a state line despite empty changes sha: %q", body)
	}
	if warns := readWarn(); !strings.Contains(warns, "MR file list is not at the pinned head") {
		t.Errorf("Warn lacks %q: %s", "MR file list is not at the pinned head", warns)
	}
}
