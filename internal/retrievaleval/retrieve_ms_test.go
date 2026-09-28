package retrievaleval

import (
	"testing"
	"time"
)

// TestRetrieveMs_MeanBeforeRound verifies REQ-04: retrieve_ms samples are
// averaged as durations first, and math.Round is applied once to the mean —
// not per-sample before averaging (which would compound truncation error).
func TestRetrieveMs_MeanBeforeRound(t *testing.T) {
	off := []time.Duration{4700 * time.Microsecond, 4700 * time.Microsecond, 4700 * time.Microsecond}
	got := retrieveMsLine(off, off)
	want := "off_mean=5 on_mean=5 (n=3)"
	if got != want {
		t.Errorf("retrieveMsLine(3x4.7ms) = %q, want %q", got, want)
	}

	subMs := []time.Duration{400 * time.Microsecond}
	got = retrieveMsLine(subMs, subMs)
	want = "off_mean=0 on_mean=0 (n=1)"
	if got != want {
		t.Errorf("retrieveMsLine(0.4ms) = %q, want %q", got, want)
	}

	got = retrieveMsLine(off, nil)
	want = "off_mean=5 on_mean=- (n=0)"
	if got != want {
		t.Errorf("retrieveMsLine(all-degraded) = %q, want %q", got, want)
	}
}

// TestScoringTargets_CategoryOnlyForMatchingSet verifies REQ-04: a
// distractor category is only reported for a triple's set if at least one
// distractor of that category actually belongs to that set — a category
// that only has distractors in a different set must not appear at all
// (an empty "0/0" category row would misrepresent coverage).
func TestScoringTargets_CategoryOnlyForMatchingSet(t *testing.T) {
	golden := Golden{Entries: []Entry{
		{
			Fixture: "sys/01-x.diff",
			Lane:    "spec-conformance",
			Distractors: []Distractor{
				{Target: Target{Set: "set-a", Path: "guide.md", Heading: "A"}, Category: "scope"},
			},
		},
	}}

	targets := scoringTargetsFor(golden, "sys/01-x.diff", "spec-conformance", "set-b")
	if _, ok := targets.categories["scope"]; ok {
		t.Errorf("categories = %v, want no %q key (distractor is set-a, triple set is set-b)", targets.categories, "scope")
	}

	targets = scoringTargetsFor(golden, "sys/01-x.diff", "spec-conformance", "set-a")
	if _, ok := targets.categories["scope"]; !ok {
		t.Errorf("categories = %v, want %q key present (distractor is set-a, triple set is set-a)", targets.categories, "scope")
	}
}
