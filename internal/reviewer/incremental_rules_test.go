package reviewer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mrinspect/internal/diff"
	"mrinspect/internal/gitlab"
)

// Rule pins for REQ-05 / REQ-06: each case fails if the named rule in
// incremental.go is deleted.

const rulesStatelessBody = ReviewNoteMarker + "\nold review without state"

// countingSourceDiff is a full-review diff fetcher that reports the pinned
// head as reviewed SHA and counts its calls.
type countingSourceDiff struct {
	sha   string
	calls int
}

func (d *countingSourceDiff) Fetch(context.Context, string, string) (string, error) {
	d.calls++
	return degradePatch, nil
}

func (d *countingSourceDiff) FetchWithSource(context.Context, string, string) (string, string, error) {
	d.calls++
	return degradePatch, d.sha, nil
}

// fullRunFixture is updateFixture without a usable prior state, with a local
// fetcher whose reviewed SHA matches the pinned head, so only the rule under
// test can make the run dirty.
func fullRunFixture(t *testing.T, mode string) (*MRInspectReviewer, *fakeGitLab, *modeRoutingProvider, func() string) {
	t.Helper()
	r, gl, provider, _ := updateFixture(t, mode)
	readWarn := installWarningLogRecorder(t, r)
	r.diff = &countingSourceDiff{sha: updHeadSHA}
	gl.listedNotes[0].Body = rulesStatelessBody
	gl.changes = gitlab.MRChangesResponse{SHA: updHeadSHA, Changes: []gitlab.Change{
		{OldPath: "full.go", NewPath: "full.go", Diff: updDiffBody},
	}}
	return r, gl, provider, readWarn
}

func rulesFullState() string {
	return FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0})
}

func rulesUpdateState() string {
	return FormatState(State{SHA: updHeadSHA, Base: updBaseSHA, Mode: ModeUpdate, Round: 1})
}

// assertPostedNote checks exactly one PostNote, no UpdateNote, and that the
// body carries wantState exactly, or no state comment when wantState is "".
func assertPostedNote(t *testing.T, gl *fakeGitLab, wantState string) string {
	t.Helper()
	if gl.postCalls != 1 || len(gl.updateCalls) != 0 {
		t.Fatalf("PostNote/UpdateNote calls = %d/%d, want 1/0", gl.postCalls, len(gl.updateCalls))
	}
	body := gl.lastNote(t)
	if wantState == "" {
		if strings.Contains(body, "mrinspect:state") {
			t.Errorf("dirty run wrote a state comment: %q", body)
		}
	} else if !strings.Contains(body, wantState) {
		t.Errorf("body lacks state %q: %q", wantState, body)
	}
	return body
}

func assertWarnContains(t *testing.T, readWarn func() string, reason string) {
	t.Helper()
	if warn := readWarn(); !strings.Contains(warn, reason) {
		t.Errorf("Warn log lacks %q: %q", reason, warn)
	}
}

func reviewPrompts(p *modeRoutingProvider) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, prompt := range p.prompts {
		if prompt != "reflection" {
			out = append(out, prompt)
		}
	}
	return out
}

// TestIncrementalRules_FullRunDirty pins the full-run dirty sources of
// assessRun / planIncremental (REQ-06).
func TestIncrementalRules_FullRunDirty(t *testing.T) {
	t.Run("1 GetMRChanges error in single mode", func(t *testing.T) {
		r, gl, _, readWarn := fullRunFixture(t, "single")
		gl.changesErr = errors.New("changes boom")

		r.Run(context.Background())

		assertPostedNote(t, gl, "")
		assertWarnContains(t, readWarn, "MR file list unavailable: changes boom")
	})

	t.Run("2 degraded to single", func(t *testing.T) {
		cases := map[string]func(t *testing.T) string{
			"lane config missing": func(t *testing.T) string { return t.TempDir() },
			"no enabled lane": func(t *testing.T) string {
				return writeLaneFixture(t, []laneFixture{{id: "standards", enabled: false}})
			},
		}
		for name, root := range cases {
			t.Run(name, func(t *testing.T) {
				r, gl, provider, readWarn := fullRunFixture(t, "multi")
				r.SetMultiLaneReviewPath(MultiLaneReviewPath{RepoRoot: root(t), ModelLimits: reviewerModelLimits()})

				r.Run(context.Background())

				if got := provider.callCount(); got != 1 {
					t.Errorf("Generate calls = %d, want 1 single-path call", got)
				}
				body := assertPostedNote(t, gl, "")
				if !strings.Contains(body, "Full review at") {
					t.Errorf("body lacks %q: %q", "Full review at", body)
				}
				assertWarnContains(t, readWarn, "multi-lane review degraded to single")
			})
		}
	})

	t.Run("3 pinned head malformed", func(t *testing.T) {
		for name, head := range map[string]string{
			"39 hex":    updHeadSHA[:39],
			"uppercase": strings.ToUpper("abcdefabcdefabcdefabcdefabcdefabcdefabcd"),
			"non hex":   strings.Repeat("g", 40),
		} {
			t.Run(name, func(t *testing.T) {
				r, gl, _, readWarn := fullRunFixture(t, "single")
				gl.mr.SHA = head

				r.Run(context.Background())

				assertPostedNote(t, gl, "")
				assertWarnContains(t, readWarn, "head or base sha missing or malformed in merge request")
			})
		}
	})
}

// updatePrompts runs an update fixture and returns the single review prompt.
func runUpdateSingle(t *testing.T, gl *fakeGitLab, r *MRInspectReviewer, provider *modeRoutingProvider) string {
	t.Helper()
	r.Run(context.Background())
	prompts := reviewPrompts(provider)
	if len(prompts) != 1 {
		t.Fatalf("review prompts = %d, want 1", len(prompts))
	}
	if gl.compareCalls != 1 {
		t.Errorf("Compare calls = %d, want 1", gl.compareCalls)
	}
	return prompts[0]
}

// TestIncrementalRules_UpdateRunDirty pins the update-run dirty sources of
// fetchUpdateDiff (REQ-05 / REQ-06).
func TestIncrementalRules_UpdateRunDirty(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*fakeGitLab)
		reason string
	}{
		{"4 GetMRChanges error", func(gl *fakeGitLab) { gl.changesErr = errors.New("changes boom") },
			"MR file list unavailable: changes boom"},
		{"5 GetMRChanges overflow", func(gl *fakeGitLab) { gl.changes.Overflow = true },
			"MR file list overflow"},
		{"6 GetMRChanges SHA differs from head", func(gl *fakeGitLab) {
			gl.changes.SHA = "4444444444444444444444444444444444444444"
		}, "MR file list is not at the pinned head"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, provider, _ := updateFixture(t, "single")
			readWarn := installWarningLogRecorder(t, r)
			tt.setup(gl)

			prompt := runUpdateSingle(t, gl, r, provider)

			if !diff.HasFileSection(prompt, "x.go") {
				t.Errorf("update review prompt lacks x.go section: %q", prompt)
			}
			if strings.Contains(prompt, otherFilesHeader) {
				t.Errorf("prompt has an other-files section despite untrusted file list: %q", prompt)
			}
			body := assertPostedNote(t, gl, "")
			if want := "Update review " + updPriorSHA[:8] + ".." + updHeadSHA[:8]; !strings.Contains(body, want) {
				t.Errorf("body lacks %q: %q", want, body)
			}
			assertWarnContains(t, readWarn, tt.reason)
		})
	}
}

func manyChanges(n int) []gitlab.Change {
	changes := []gitlab.Change{{OldPath: "x.go", NewPath: "x.go", Diff: updDiffBody}}
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("f%03d.go", i)
		changes = append(changes, gitlab.Change{OldPath: p, NewPath: p, Diff: updDiffBody})
	}
	return changes
}

// sectionLines returns the lines after the other-files header.
func sectionLines(t *testing.T, prompt string) []string {
	t.Helper()
	idx := strings.Index(prompt, otherFilesHeader)
	if idx < 0 {
		t.Fatalf("prompt lacks header %q", otherFilesHeader)
	}
	var lines []string
	for _, l := range strings.Split(prompt[idx+len(otherFilesHeader):], "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestIncrementalRules_OtherFilesCap pins the 200-path cap and its dirty
// mark (REQ-05 / REQ-06).
func TestIncrementalRules_OtherFilesCap(t *testing.T) {
	t.Run("201 other files", func(t *testing.T) {
		r, gl, provider, _ := updateFixture(t, "single")
		readWarn := installWarningLogRecorder(t, r)
		gl.changes.Changes = manyChanges(201)

		prompt := runUpdateSingle(t, gl, r, provider)

		lines := sectionLines(t, prompt)
		if len(lines) != maxOtherFiles+1 {
			t.Fatalf("section lines = %d, want %d paths plus a more line", len(lines), maxOtherFiles)
		}
		for i := 0; i < maxOtherFiles; i++ {
			if want := fmt.Sprintf("f%03d.go", i); lines[i] != want {
				t.Fatalf("line %d = %q, want %q", i, lines[i], want)
			}
		}
		if last := lines[maxOtherFiles]; last != "... and 1 more" {
			t.Errorf("last line = %q, want %q", last, "... and 1 more")
		}
		assertPostedNote(t, gl, "")
		assertWarnContains(t, readWarn, "file list truncated")
	})

	t.Run("200 other files", func(t *testing.T) {
		r, gl, provider, _ := updateFixture(t, "single")
		gl.changes.Changes = manyChanges(200)

		prompt := runUpdateSingle(t, gl, r, provider)

		lines := sectionLines(t, prompt)
		if len(lines) != maxOtherFiles {
			t.Fatalf("section lines = %d, want %d", len(lines), maxOtherFiles)
		}
		if strings.Contains(prompt, "more") {
			t.Errorf("prompt has a more line at exactly the cap: %q", prompt)
		}
		assertPostedNote(t, gl, rulesUpdateState())
	})
}

// TestIncrementalRules_CompareFallback pins every compare fallback to a
// clean full round-0 review through the local diff fetch (REQ-05).
func TestIncrementalRules_CompareFallback(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*fakeGitLab)
		reason string
	}{
		{"compare error", func(gl *fakeGitLab) { gl.compareErr = errors.New("compare boom") }, "incremental: compare failed, full review"},
		{"compare timeout", func(gl *fakeGitLab) { gl.compare = gitlab.CompareResponse{CompareTimeout: true} }, "incremental: compare timed out, full review"},
		{"collapsed diff", func(gl *fakeGitLab) {
			gl.compare = gitlab.CompareResponse{Diffs: []gitlab.Change{
				{OldPath: "x.go", NewPath: "x.go", Diff: updDiffBody, Collapsed: true},
			}}
		}, "incremental: compare diff incomplete, full review"},
		{"truncated diff", func(gl *fakeGitLab) {
			gl.compare = gitlab.CompareResponse{Diffs: []gitlab.Change{{OldPath: "x.go", NewPath: "x.go"}}}
		}, "incremental: compare diff incomplete, full review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, gl, provider, readWarn := fullRunFixture(t, "single")
			gl.listedNotes[0].Body = ReviewNoteMarker + "\n" +
				FormatState(State{SHA: updPriorSHA, Base: updBaseSHA, Mode: ModeFull, Round: 0}) + "\nold review"
			local := r.diff.(*countingSourceDiff)
			tt.setup(gl)

			prompt := runUpdateSingle(t, gl, r, provider)

			if local.calls == 0 {
				t.Error("local diff fetch was not used")
			}
			if !diff.HasFileSection(prompt, "full.go") {
				t.Errorf("prompt lacks the full MR diff (full.go section): %q", prompt)
			}
			body := assertPostedNote(t, gl, rulesFullState())
			if want := "Full review at " + updHeadSHA[:8]; !strings.Contains(body, want) {
				t.Errorf("body lacks %q: %q", want, body)
			}
			if strings.Contains(body, "Update review") {
				t.Errorf("body has an update header: %q", body)
			}
			assertWarnContains(t, readWarn, tt.reason)
		})
	}
}

// TestIncrementalRules_MultiUpdate pins that a multi-mode update run reviews
// the delta through the lanes, not through a single-mode fallback (REQ-05).
func TestIncrementalRules_MultiUpdate(t *testing.T) {
	r, gl, provider, _ := updateFixture(t, "multi")

	r.Run(context.Background())

	prompts := reviewPrompts(provider)
	if len(prompts) != 2 {
		t.Fatalf("review prompts = %d, want exactly 2 lane prompts", len(prompts))
	}
	for _, laneID := range []string{"standards", "code-diff"} {
		found := false
		for _, prompt := range prompts {
			if !strings.Contains(prompt, laneTemplatePrefix+laneID) {
				continue
			}
			found = true
			if !diff.HasFileSection(prompt, "x.go") || diff.HasFileSection(prompt, "y.go") {
				t.Errorf("lane %s prompt does not carry only the delta: %q", laneID, prompt)
			}
			if lines := sectionLines(t, prompt); len(lines) == 0 || lines[0] != "y.go" || strings.Contains(strings.Join(lines[:1], ""), "x.go") {
				t.Errorf("lane %s other-files section starts %q, want y.go", laneID, lines)
			}
		}
		if !found {
			t.Errorf("no prompt for lane %s", laneID)
		}
	}
	assertPostedNote(t, gl, rulesUpdateState())
}

// TestOtherFilesSection_Rules pins dedupe, sort, control-char strip, delta
// exclusion, the empty result and the cap boundary.
func TestOtherFilesSection_Rules(t *testing.T) {
	t.Run("dedupe sort strip and delta drop", func(t *testing.T) {
		mr := []string{"b.go", "a.go", "b.go", "c\n\x1b[31m.go", "d.go", "e.go", "\n\x1b"}
		delta := []string{"d.go", "e\x1b.go"}

		section, truncated := otherFilesSection(mr, delta)

		want := strings.Join([]string{otherFilesHeader, "a.go", "b.go", "c[31m.go"}, "\n")
		if section != want {
			t.Errorf("section = %q, want %q", section, want)
		}
		if truncated {
			t.Error("truncated = true, want false")
		}
	})

	t.Run("nothing remains", func(t *testing.T) {
		for name, tc := range map[string]struct{ mr, delta []string }{
			"nil mr":         {nil, []string{"x.go"}},
			"all in delta":   {[]string{"x.go", "y.go"}, []string{"y.go", "x.go"}},
			"only empty":     {[]string{"", "\n"}, nil},
			"only duplicate": {[]string{"x.go", "x.go"}, []string{"x.go"}},
		} {
			section, truncated := otherFilesSection(tc.mr, tc.delta)
			if section != "" || truncated {
				t.Errorf("%s: got (%q, %v), want (\"\", false)", name, section, truncated)
			}
		}
	})

	t.Run("cap boundary", func(t *testing.T) {
		paths := func(n int) []string {
			var out []string
			for i := 0; i < n; i++ {
				out = append(out, fmt.Sprintf("f%03d.go", i))
			}
			return out
		}
		section, truncated := otherFilesSection(paths(200), nil)
		if truncated || strings.Contains(section, "more") || len(strings.Split(section, "\n")) != 201 {
			t.Errorf("200 paths: truncated=%v section lines=%d", truncated, len(strings.Split(section, "\n")))
		}
		section, truncated = otherFilesSection(paths(203), nil)
		lines := strings.Split(section, "\n")
		if !truncated || len(lines) != 202 || lines[201] != "... and 3 more" || lines[200] != "f199.go" {
			t.Errorf("203 paths: truncated=%v lines=%d tail=%q", truncated, len(lines), lines[len(lines)-1])
		}
	})
}
