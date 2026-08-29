package diffbudget

import (
	"reflect"
	"strings"
	"testing"

	"mrinspect/internal/diff"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/rag/chunk"
)

type warningRecorder struct {
	messages []string
}

func (r *warningRecorder) Warn(message string, _ ...any) {
	r.messages = append(r.messages, message)
}

func TestReduce_PassThroughUnderBudget(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "")
	changes := []gitlab.Change{
		{OldPath: "src/one.go", NewPath: "src/one.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"},
		{OldPath: "src/two.go", NewPath: "src/two.go", Diff: "@@ -2 +2 @@\n-before\n+after\n"},
	}

	kept, dropped, err := Reduce(changes, Options{ModelBudget: 10_000, MaxDiffSizeKB: 300})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !reflect.DeepEqual(kept, changes) {
		t.Fatalf("kept = %#v, want original changes %#v", kept, changes)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped = %#v, want none", dropped)
	}
}

func TestReduce_DropsNonReviewableFirst(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "")
	realDiff := "@@ -1 +1 @@\n-old\n+new\n"
	changes := []gitlab.Change{
		{NewPath: "package-lock.json", Diff: strings.Repeat("lockfile filler line\n", 200)},
		{NewPath: "src/real.go", Diff: realDiff},
	}
	// The effective budget is floor(100*0.85) = 85 tokens, while realDiff
	// alone is far below that and the repeated lockfile is far above it.
	if got := chunk.TokenEst(realDiff); got > 85 {
		t.Fatalf("test fixture real diff TokenEst = %d, want <= 85", got)
	}

	kept, dropped, err := Reduce(changes, Options{ModelBudget: 100, MaxDiffSizeKB: 300})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(dropped) != 1 || dropped[0].Path != "package-lock.json" || dropped[0].Reason != ReasonNonReviewable {
		t.Fatalf("dropped = %#v, want package-lock.json as %q", dropped, ReasonNonReviewable)
	}
	if len(kept) != 1 || kept[0].NewPath != "src/real.go" {
		t.Fatalf("kept = %#v, want only src/real.go", kept)
	}
	if kept[0].Diff != realDiff {
		t.Fatalf("surviving diff was changed: got %q, want %q", kept[0].Diff, realDiff)
	}
}

func TestReduce_DropsLargestFirstNeverTruncates(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "")
	smallDiff := strings.Repeat("s", 40)
	mediumDiff := strings.Repeat("m", 160)
	largeDiff := strings.Repeat("l", 400)
	changes := []gitlab.Change{
		{NewPath: "src/small.go", Diff: smallDiff},
		{NewPath: "src/medium.go", Diff: mediumDiff},
		{NewPath: "src/large.go", Diff: largeDiff},
	}
	// floor(20*0.85) = 17 tokens. The 10-token small file fits only after
	// the 100-token large and 40-token medium files are dropped in that order.
	kept, dropped, err := Reduce(changes, Options{ModelBudget: 20, MaxDiffSizeKB: 300})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %#v, want two files", dropped)
	}
	if dropped[0].Path != "src/large.go" || dropped[1].Path != "src/medium.go" {
		t.Fatalf("drop order = %#v, want largest then medium", dropped)
	}
	for _, drop := range dropped {
		if drop.Reason != ReasonSizeBudget {
			t.Errorf("drop reason = %q, want %q", drop.Reason, ReasonSizeBudget)
		}
	}
	if len(kept) != 1 || kept[0].NewPath != "src/small.go" {
		t.Fatalf("kept = %#v, want only src/small.go", kept)
	}
	if kept[0].Diff != smallDiff {
		t.Fatalf("surviving diff was changed: got %q, want %q", kept[0].Diff, smallDiff)
	}
}

func TestReduce_InvalidShareFallsBack(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "abc")
	diff := strings.Repeat("x", 360)
	// ASCII TokenEst is ceil(len/4), so this is 90 tokens: it fits a full
	// 100-token model budget but exceeds the 85-token fallback share.
	if got := chunk.TokenEst(diff); got != 90 {
		t.Fatalf("test fixture TokenEst = %d, want 90", got)
	}

	kept, dropped, err := Reduce([]gitlab.Change{{NewPath: "src/borderline.go", Diff: diff}}, Options{
		ModelBudget:   100,
		MaxDiffSizeKB: 300,
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(kept) != 0 {
		t.Fatalf("kept = %#v, want file dropped under 0.85 fallback", kept)
	}
	if len(dropped) != 1 || dropped[0].Path != "src/borderline.go" || dropped[0].Reason != ReasonSizeBudget {
		t.Fatalf("dropped = %#v, want borderline file dropped for %q", dropped, ReasonSizeBudget)
	}
}

func TestReduce_ShareAboveOneFallsBackAndWarns(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "85")
	diffText := strings.Repeat("x", 360)
	log := &warningRecorder{}

	kept, dropped, err := Reduce([]gitlab.Change{{NewPath: "src/borderline.go", Diff: diffText}}, Options{
		ModelBudget:   100,
		MaxDiffSizeKB: 300,
		Logger:        log,
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(kept) != 0 || len(dropped) != 1 {
		t.Fatalf("kept = %#v, dropped = %#v; want file dropped under 0.85 fallback", kept, dropped)
	}
	wantWarning := `invalid MRI_DIFF_PROMPT_SHARE "85", using default 0.85`
	if len(log.messages) != 1 || log.messages[0] != wantWarning {
		t.Fatalf("warnings = %#v, want [%q]", log.messages, wantWarning)
	}
}

func TestReduce_RenderedInitialDiffTriggersTokenReduction(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "1")
	changes := []gitlab.Change{
		{OldPath: "a.go", NewPath: "a.go", Diff: "x"},
		{OldPath: "b.go", NewPath: "b.go", Diff: "y"},
	}
	initialDiff := strings.Repeat("local-render-overhead ", 20)
	render := func(kept []gitlab.Change, dropped []DroppedFile) (string, error) {
		rendered, err := diff.ConvertChangesToDiff(kept)
		return rendered + Trailer(dropped), err
	}

	kept, dropped, err := Reduce(changes, Options{
		ModelBudget:   100,
		MaxDiffSizeKB: 300,
		InitialDiff:   initialDiff,
		Render:        render,
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(dropped) == 0 {
		t.Fatal("Reduce dropped no files even though the rendered initial diff exceeds the token budget")
	}
	finalDiff, err := render(kept, dropped)
	if err != nil {
		t.Fatalf("render final diff: %v", err)
	}
	if got := chunk.TokenEst(finalDiff); got > 100 {
		t.Fatalf("final rendered TokenEst = %d, want <= 100; diff = %q", got, finalDiff)
	}
}

func TestReduce_PostReductionRenderedDiffFitsKBBackstop(t *testing.T) {
	t.Setenv("MRI_DIFF_PROMPT_SHARE", "1")
	changes := []gitlab.Change{
		{OldPath: strings.Repeat("a", 300) + ".go", NewPath: strings.Repeat("a", 300) + ".go", Diff: "x"},
		{OldPath: "b.go", NewPath: "b.go", Diff: "y"},
	}
	render := func(kept []gitlab.Change, dropped []DroppedFile) (string, error) {
		rendered, err := diff.ConvertChangesToDiff(kept)
		return rendered + Trailer(dropped), err
	}
	initialDiff, err := render(changes, nil)
	if err != nil {
		t.Fatalf("render initial diff: %v", err)
	}
	const maxKB = 0.55
	if float64(len(initialDiff))/1024 <= maxKB {
		t.Fatalf("fixture initial size = %.3f KB, want > %.3f KB", float64(len(initialDiff))/1024, maxKB)
	}

	kept, dropped, err := Reduce(changes, Options{
		ModelBudget:   10_000,
		MaxDiffSizeKB: maxKB,
		InitialDiff:   initialDiff,
		Render:        render,
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(dropped) == 0 {
		t.Fatal("Reduce dropped no files even though rendered headers exceed the KB backstop")
	}
	finalDiff, err := render(kept, dropped)
	if err != nil {
		t.Fatalf("render final diff: %v", err)
	}
	if gotKB := float64(len(finalDiff)) / 1024; gotKB > maxKB {
		t.Fatalf("final rendered size = %.3f KB, want <= %.3f KB; diff = %q", gotKB, maxKB, finalDiff)
	}
}
