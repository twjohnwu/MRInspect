package reviewer

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/diff"
	"mrinspect/internal/gitlab"
)

// v3Source is a full-review diff fetcher with a configurable patch and
// reviewed SHA (IDiffSourceFetcher).
type v3Source struct{ patch, sha string }

func (d v3Source) Fetch(context.Context, string, string) (string, error) { return d.patch, nil }
func (d v3Source) FetchWithSource(context.Context, string, string) (string, string, error) {
	return d.patch, d.sha, nil
}

// v3BigDiff is far above the ~170-token budget the fixture's model limit
// leaves for a diff.
var v3BigDiff = "@@ -1,50 +1,50 @@\n" + strings.Repeat("-old filler line\n+new filler line\n", 60)

const v3CutPatch = degradePatch + "diff --git a/cut.go b/cut.go\n--- a/cut.go\n+++ b/cut.go\n@@ -1,1 +1,1 @@\n-a\n+b\n"

// TestIncremental_DegradeAndStateWritingV3 verifies REQ-06 / S-08 (j)–(p).
func TestIncremental_DegradeAndStateWritingV3(t *testing.T) {
	const otherSHA = "4444444444444444444444444444444444444444"
	fullGo := gitlab.Change{OldPath: "full.go", NewPath: "full.go", Diff: updDiffBody}
	tests := []struct {
		name       string
		update     bool // keep the prior state so the run starts as an update
		patch      string
		changesSHA string
		changes    []gitlab.Change
		compare    []gitlab.Change
		wantState  bool
		wantWarn   string // substring of the Warn output; empty = none required
		wantPrompt []string
		wantAbsent []string // paths with no file section in the model prompt
	}{
		{name: "j full review, budget drops a file",
			patch: degradePatch, changesSHA: updHeadSHA,
			changes:    []gitlab.Change{{OldPath: "big.go", NewPath: "big.go", Diff: v3BigDiff}, fullGo},
			wantWarn:   "not sent to the model",
			wantPrompt: []string{"full.go"}, wantAbsent: []string{"big.go"}},
		{name: "k fetcher diff lacks a truncated file section",
			patch: degradePatch, changesSHA: updHeadSHA,
			changes:    []gitlab.Change{fullGo, {OldPath: "cut.go", NewPath: "cut.go"}},
			wantWarn:   "not sent to the model",
			wantPrompt: []string{"full.go"}, wantAbsent: []string{"cut.go"}},
		{name: "l update diff over budget, full MR diff within budget",
			update: true, patch: degradePatch, changesSHA: updHeadSHA,
			changes: []gitlab.Change{fullGo},
			compare: []gitlab.Change{
				{OldPath: "big.go", NewPath: "big.go", Diff: v3BigDiff},
				{OldPath: "x.go", NewPath: "x.go", Diff: updDiffBody},
			},
			wantState: true, wantWarn: "exceeds the size budget",
			wantPrompt: []string{"full.go"}, wantAbsent: []string{"x.go", "big.go"}},
		{name: "m changes sha differs from pinned head",
			patch: degradePatch, changesSHA: otherSHA,
			changes:    []gitlab.Change{fullGo},
			wantWarn:   "MR file list is not at the pinned head",
			wantPrompt: []string{"full.go"}},
		{name: "n budget drop, rename with too_large and empty diff",
			patch: degradePatch, changesSHA: updHeadSHA,
			changes: []gitlab.Change{
				{OldPath: "big.go", NewPath: "big.go", Diff: v3BigDiff},
				{OldPath: "old/name.go", NewPath: "new/name.go", RenamedFile: true, TooLarge: true},
				fullGo,
			},
			wantWarn:   "not sent to the model",
			wantPrompt: []string{"full.go"}, wantAbsent: []string{"big.go"}},
		{name: "o budget drops only a non-reviewable lockfile",
			patch: degradePatch, changesSHA: updHeadSHA,
			changes:   []gitlab.Change{{OldPath: "package-lock.json", NewPath: "package-lock.json", Diff: v3BigDiff}, fullGo},
			wantState: true, wantPrompt: []string{"full.go"}, wantAbsent: []string{"package-lock.json"}},
		{name: "p fetcher diff carries the truncated file section, nothing dropped",
			patch: v3CutPatch, changesSHA: updHeadSHA,
			changes:   []gitlab.Change{fullGo, {OldPath: "cut.go", NewPath: "cut.go"}},
			wantState: true, wantPrompt: []string{"full.go", "cut.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, provider, _ := updateFixture(t, "single")
			readWarn := installWarningLogRecorder(t, r)
			r.SetMultiLaneReviewPath(MultiLaneReviewPath{
				RepoRoot:    t.TempDir(),
				ModelLimits: map[string]int{"gemini-test": 250},
			})
			r.cfg.Validation.MaxDiffSizeKB = 300
			r.diff = v3Source{patch: tt.patch, sha: updHeadSHA}
			if !tt.update {
				gl.listedNotes[0].Body = rulesStatelessBody
			}
			gl.changes = gitlab.MRChangesResponse{SHA: tt.changesSHA, Changes: tt.changes}
			if tt.compare != nil {
				gl.compare = gitlab.CompareResponse{Diffs: tt.compare}
			}

			r.Run(context.Background())

			if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
				t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
			}
			provider.mu.Lock()
			prompts := append([]string(nil), provider.prompts...)
			provider.mu.Unlock()
			reviewed := 0
			for _, prompt := range prompts {
				if prompt == "reflection" {
					continue
				}
				reviewed++
				for _, p := range tt.wantPrompt {
					if !diff.HasFileSection(prompt, p) {
						t.Errorf("prompt lacks %s file section: %q", p, prompt)
					}
				}
				for _, p := range tt.wantAbsent {
					if diff.HasFileSection(prompt, p) {
						t.Errorf("prompt has %s file section, want none: %q", p, prompt)
					}
				}
			}
			if reviewed == 0 {
				t.Error("model received no review prompt")
			}
			body := gl.lastNote(t)
			if tt.wantState {
				wantState := FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
				if !strings.Contains(body, wantState) {
					t.Errorf("body lacks state %q: %q", wantState, body)
				}
				if !strings.Contains(body, "Full review at") {
					t.Errorf("body lacks %q: %q", "Full review at", body)
				}
				if strings.Contains(body, "Update review") {
					t.Errorf("body has an update header: %q", body)
				}
			} else if strings.Contains(body, "mrinspect:state") {
				t.Errorf("dirty run wrote a state comment: %q", body)
			}
			warns := readWarn()
			if tt.wantWarn != "" && !strings.Contains(warns, tt.wantWarn) {
				t.Errorf("Warn output lacks %q: %s", tt.wantWarn, warns)
			}
		})
	}
}
