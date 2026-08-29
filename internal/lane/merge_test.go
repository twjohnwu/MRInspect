package lane

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func mergeTestLine(n int) *int {
	return &n
}

func mergeTestFinding(title string, severity Severity, file string, line *int, category string) Finding {
	return Finding{
		Title:     title,
		Severity:  severity,
		Rationale: "rationale for " + title,
		File:      file,
		Line:      line,
		Category:  category,
	}
}

func hasCitation(citations []Citation, want Citation) bool {
	return slices.Contains(citations, want)
}

func hasExactlyReporters(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, laneID := range want {
		if !slices.Contains(got, laneID) {
			return false
		}
	}
	return true
}

func mergedFingerprint(findings []MergedFinding) string {
	var output strings.Builder
	for _, finding := range findings {
		line := -1
		if finding.Line != nil {
			line = *finding.Line
		}
		fmt.Fprintf(
			&output,
			"%q|%q|%q|%d|%q|%q|%q|%#v|%#v\n",
			finding.Severity,
			finding.Title,
			finding.Rationale,
			line,
			finding.File,
			finding.Category,
			finding.Suggestion,
			finding.ReportedBy,
			finding.Citations,
		)
	}
	return output.String()
}

// TestMerge_DeduplicatesAcrossLanes verifies REQ-05 / S-17: findings in the
// same normalized file/category group and within three lines form one cluster.
func TestMerge_DeduplicatesAcrossLanes(t *testing.T) {
	laneA := mergeTestFinding("lane A title", SeverityMedium, "internal/auth.go", mergeTestLine(44), "security")
	laneA.Rationale = "lane A rationale"
	laneA.Citations = []Citation{{SourceID: "standard-a", Label: "A"}}
	laneB := mergeTestFinding("lane B title", SeverityMedium, "internal/auth.go", mergeTestLine(42), "security")
	laneB.Rationale = "lane B rationale"
	laneB.Citations = []Citation{{SourceID: "standard-b", Label: "B"}}

	got := Merge(
		[]string{"lane-a", "lane-b"},
		[]LaneResult{
			{LaneID: "lane-b", Findings: []Finding{laneB}},
			{LaneID: "lane-a", Findings: []Finding{laneA}},
		},
	)

	if len(got) != 1 {
		t.Fatalf("Merge returned %d findings, want one cross-lane cluster: %#v", len(got), got)
	}
	merged := got[0]
	if !hasExactlyReporters(merged.ReportedBy, "lane-a", "lane-b") {
		t.Errorf("ReportedBy = %v, want exactly lane-a and lane-b", merged.ReportedBy)
	}
	if merged.Title != laneA.Title || merged.Rationale != laneA.Rationale ||
		merged.File != laneA.File || merged.Line == nil || *merged.Line != *laneA.Line ||
		merged.Category != laneA.Category {
		t.Errorf("representative fields = %#v, want lane-a fields %#v", merged.Finding, laneA)
	}
	if len(merged.Citations) != 2 {
		t.Errorf("Citations has %d entries, want exactly both lane contributions: %#v", len(merged.Citations), merged.Citations)
	}
	for _, citation := range append(laneA.Citations, laneB.Citations...) {
		if !hasCitation(merged.Citations, citation) {
			t.Errorf("Citations = %#v, want contribution %#v", merged.Citations, citation)
		}
	}
}

// TestMerge_KeepsDistantFindingsSeparate verifies REQ-05 / S-18: grouping is
// representative-based, neither whole-file merging, transitive chaining, nor line/4 bucketing.
func TestMerge_KeepsDistantFindingsSeparate(t *testing.T) {
	t.Run("nine lines apart", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("line 10", SeverityMedium, "internal/a.go", mergeTestLine(10), "correctness")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("line 19", SeverityMedium, "internal/a.go", mergeTestLine(19), "correctness")}},
			},
		)
		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want two clusters nine lines apart: %#v", len(got), got)
		}
	})

	t.Run("representative distance is not transitive or bucketed", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b", "lane-c", "lane-d"},
			[]LaneResult{
				{LaneID: "lane-d", Findings: []Finding{mergeTestFinding("line 19", SeverityMedium, "internal/chain.go", mergeTestLine(19), "correctness")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("line 13", SeverityMedium, "internal/chain.go", mergeTestLine(13), "correctness")}},
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("line 10", SeverityMedium, "internal/chain.go", mergeTestLine(10), "correctness")}},
				{LaneID: "lane-c", Findings: []Finding{mergeTestFinding("line 16", SeverityMedium, "internal/chain.go", mergeTestLine(16), "correctness")}},
			},
		)
		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want clusters 10+13 and 16+19: %#v", len(got), got)
		}
		if got[0].Line == nil || *got[0].Line != 10 || !hasExactlyReporters(got[0].ReportedBy, "lane-a", "lane-b") {
			t.Errorf("first cluster = %#v, want representative line 10 reported by lane-a and lane-b", got[0])
		}
		if got[1].Line == nil || *got[1].Line != 16 || !hasExactlyReporters(got[1].ReportedBy, "lane-c", "lane-d") {
			t.Errorf("second cluster = %#v, want representative line 16 reported by lane-c and lane-d", got[1])
		}
	})
}

// TestMerge_SeverityTakesMaximum verifies REQ-05 / S-19: severity is the
// cluster maximum, while representative content still follows lane declaration order.
func TestMerge_SeverityTakesMaximum(t *testing.T) {
	t.Run("later lane raises severity but does not replace representative", func(t *testing.T) {
		laneA := mergeTestFinding("earlier lane title", SeverityLow, "internal/risk.go", mergeTestLine(22), "security")
		laneA.Rationale = "earlier lane rationale"
		laneB := mergeTestFinding("later lane title", SeverityHigh, "internal/risk.go", mergeTestLine(20), "security")
		laneB.Rationale = "later lane rationale"

		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-b", Findings: []Finding{laneB}},
				{LaneID: "lane-a", Findings: []Finding{laneA}},
			},
		)
		if len(got) != 1 {
			t.Fatalf("Merge returned %d findings, want one cluster: %#v", len(got), got)
		}
		if got[0].Severity != SeverityHigh {
			t.Errorf("Severity = %q, want maximum %q", got[0].Severity, SeverityHigh)
		}
		if got[0].Title != laneA.Title || got[0].Rationale != laneA.Rationale || got[0].Line == nil || *got[0].Line != 22 {
			t.Errorf("representative = %#v, want earlier-declared lane-a fields %#v", got[0].Finding, laneA)
		}
	})

	t.Run("agreement does not promote two lows", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("low A", SeverityLow, "internal/style.go", mergeTestLine(7), "style")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("low B", SeverityLow, "internal/style.go", mergeTestLine(8), "style")}},
			},
		)
		if len(got) != 1 {
			t.Fatalf("Merge returned %d findings, want one agreed cluster: %#v", len(got), got)
		}
		if got[0].Severity != SeverityLow {
			t.Errorf("Severity = %q, want %q; agreement must not promote severity", got[0].Severity, SeverityLow)
		}
	})
}

// TestMerge_OutputIsDeterministic verifies REQ-05 / S-21: repeated merges are
// byte-identical and follow the complete severity/lane/file/line/title/category order.
func TestMerge_OutputIsDeterministic(t *testing.T) {
	laneA := []Finding{
		mergeTestFinding("lane-a high", SeverityHigh, "z.go", mergeTestLine(50), "high-a"),
		mergeTestFinding("byte-file-uppercase", SeverityMedium, "A.go", mergeTestLine(1), "file"),
		mergeTestFinding("missing line", SeverityMedium, "sort.go", nil, "missing"),
		mergeTestFinding("Alpha title", SeverityMedium, "sort.go", mergeTestLine(10), "title-alpha"),
		mergeTestFinding("Same title", SeverityMedium, "sort.go", mergeTestLine(10), "category-alpha"),
		mergeTestFinding("Same title", SeverityMedium, "sort.go", mergeTestLine(10), "category-omega"),
		mergeTestFinding("Zulu title", SeverityMedium, "sort.go", mergeTestLine(10), "title-zulu"),
		mergeTestFinding("lane-a low", SeverityLow, "a.go", mergeTestLine(1), "low"),
	}
	laneB := []Finding{
		mergeTestFinding("lane-b high", SeverityHigh, "a.go", mergeTestLine(1), "high-b"),
		mergeTestFinding("duplicate alpha", SeverityMedium, "sort.go", mergeTestLine(12), "title-alpha"),
		mergeTestFinding("lane-b medium", SeverityMedium, "0.go", mergeTestLine(1), "medium-b"),
	}
	results := []LaneResult{
		{LaneID: "lane-b", Findings: laneB},
		{LaneID: "lane-a", Findings: laneA},
	}
	wantTitles := []string{
		"lane-a high",
		"lane-b high",
		"byte-file-uppercase",
		"missing line",
		"Alpha title",
		"Same title",
		"Same title",
		"Zulu title",
		"lane-b medium",
		"lane-a low",
	}

	var first string
	for run := 0; run < 5; run++ {
		got := Merge([]string{"lane-a", "lane-b"}, results)
		gotTitles := make([]string, len(got))
		for i := range got {
			gotTitles[i] = got[i].Title
		}
		if !slices.Equal(gotTitles, wantTitles) {
			t.Fatalf("run %d title order = %q, want six-key order %q", run, gotTitles, wantTitles)
		}
		if got[5].Category != "category-alpha" || got[6].Category != "category-omega" {
			t.Errorf("run %d equal-first-five category order = [%q %q], want byte order [category-alpha category-omega]", run, got[5].Category, got[6].Category)
		}

		serialized := mergedFingerprint(got)
		if run == 0 {
			first = serialized
			continue
		}
		if serialized != first {
			t.Errorf("run %d output differs byte-for-byte from run 0\nrun 0: %q\nrun %d: %q", run, first, run, serialized)
		}
	}
}

// TestMerge_NormalizesFilePaths verifies REQ-05 / S-44 plus the exact-group
// category rule and no-line exception: prefixes normalize, case does not fold.
func TestMerge_NormalizesFilePaths(t *testing.T) {
	t.Run("leading dot and diff prefixes merge", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b", "lane-c", "lane-d"},
			[]LaneResult{
				{LaneID: "lane-d", Findings: []Finding{mergeTestFinding("a prefix", SeverityMedium, "a/internal/foo.go", mergeTestLine(12), "correctness")}},
				{LaneID: "lane-c", Findings: []Finding{mergeTestFinding("b prefix", SeverityMedium, "b/internal/foo.go", mergeTestLine(12), "correctness")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("dot prefix", SeverityMedium, "./internal/foo.go", mergeTestLine(12), "correctness")}},
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("plain", SeverityMedium, "internal/foo.go", mergeTestLine(12), "correctness")}},
			},
		)
		if len(got) != 1 {
			t.Fatalf("Merge returned %d findings, want all normalized prefixes in one cluster: %#v", len(got), got)
		}
		if got[0].File != "internal/foo.go" || !hasExactlyReporters(got[0].ReportedBy, "lane-a", "lane-b", "lane-c", "lane-d") {
			t.Errorf("merged finding = %#v, want lane-a file and all four reporters", got[0])
		}
	})

	t.Run("case remains distinct", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("upper", SeverityMedium, "Internal/Foo.go", mergeTestLine(12), "correctness")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("lower", SeverityMedium, "internal/foo.go", mergeTestLine(12), "correctness")}},
			},
		)
		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want case-sensitive paths kept separate: %#v", len(got), got)
		}
	})

	t.Run("filled and missing categories remain distinct", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("filled", SeverityMedium, "internal/foo.go", mergeTestLine(12), "x")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("missing", SeverityMedium, "internal/foo.go", mergeTestLine(12), "")}},
			},
		)
		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want category x and missing category kept separate: %#v", len(got), got)
		}
	})

	t.Run("two missing categories merge", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("missing A", SeverityMedium, "internal/foo.go", mergeTestLine(12), "")}},
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("missing B", SeverityMedium, "internal/foo.go", mergeTestLine(13), "")}},
			},
		)
		if len(got) != 1 || !hasExactlyReporters(got[0].ReportedBy, "lane-a", "lane-b") {
			t.Fatalf("Merge result = %#v, want one missing-category cluster reported by both lanes", got)
		}
	})

	t.Run("findings without lines never merge", func(t *testing.T) {
		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-b", Findings: []Finding{mergeTestFinding("no line B", SeverityMedium, "internal/foo.go", nil, "correctness")}},
				{LaneID: "lane-a", Findings: []Finding{mergeTestFinding("no line A", SeverityMedium, "internal/foo.go", nil, "correctness")}},
			},
		)
		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want each no-line finding in its own cluster: %#v", len(got), got)
		}
		if !hasExactlyReporters(got[0].ReportedBy, "lane-a") || !hasExactlyReporters(got[1].ReportedBy, "lane-b") {
			t.Errorf("no-line reporters = [%v %v], want separate lane-a and lane-b facts", got[0].ReportedBy, got[1].ReportedBy)
		}
	})
}

// TestMerge_SameLaneFindingsStaySeparate verifies REQ-05: dedup clusters
// members from DIFFERENT lanes; two findings from the SAME lane within the
// line-distance window must never collapse into one cluster.
func TestMerge_SameLaneFindingsStaySeparate(t *testing.T) {
	t.Run("two same-lane findings never merge", func(t *testing.T) {
		low := mergeTestFinding("trivial title", SeverityLow, "internal/a.go", mergeTestLine(10), "")
		high := mergeTestFinding("serious title", SeverityHigh, "internal/a.go", mergeTestLine(12), "")

		got := Merge(
			[]string{"code-diff"},
			[]LaneResult{
				{LaneID: "code-diff", Findings: []Finding{low, high}},
			},
		)

		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want same-lane findings kept in separate clusters: %#v", len(got), got)
		}
		gotTitles := []string{got[0].Title, got[1].Title}
		if !slices.Contains(gotTitles, low.Title) || !slices.Contains(gotTitles, high.Title) {
			t.Fatalf("titles = %v, want both %q and %q to survive", gotTitles, low.Title, high.Title)
		}
		for _, finding := range got {
			if finding.Title == low.Title && finding.Severity != SeverityLow {
				t.Errorf("low finding severity = %q, want unmodified %q (no cross-contamination from same-lane peer)", finding.Severity, SeverityLow)
			}
			if finding.Title == high.Title && finding.Severity != SeverityHigh {
				t.Errorf("high finding severity = %q, want unmodified %q", finding.Severity, SeverityHigh)
			}
		}
	})

	t.Run("mixed lanes: same-lane pair splits, other lane joins one of them", func(t *testing.T) {
		laneA1 := mergeTestFinding("lane-a first", SeverityMedium, "internal/b.go", mergeTestLine(10), "")
		laneB := mergeTestFinding("lane-b only", SeverityMedium, "internal/b.go", mergeTestLine(11), "")
		laneA2 := mergeTestFinding("lane-a second", SeverityMedium, "internal/b.go", mergeTestLine(12), "")

		got := Merge(
			[]string{"lane-a", "lane-b"},
			[]LaneResult{
				{LaneID: "lane-a", Findings: []Finding{laneA1, laneA2}},
				{LaneID: "lane-b", Findings: []Finding{laneB}},
			},
		)

		if len(got) != 2 {
			t.Fatalf("Merge returned %d findings, want two clusters (same-lane pair split apart): %#v", len(got), got)
		}
		gotTitles := []string{got[0].Title, got[1].Title}
		if !slices.Contains(gotTitles, laneA1.Title) || !slices.Contains(gotTitles, laneA2.Title) {
			t.Fatalf("titles = %v, want both lane-a titles %q and %q to survive", gotTitles, laneA1.Title, laneA2.Title)
		}
	})
}

func TestMerge_CitationsKeepProvenance(t *testing.T) {
	laneA := mergeTestFinding("lane A", SeverityMedium, "internal/auth.go", mergeTestLine(40), "security")
	laneA.Citations = []Citation{{SourceID: "source-a", Label: "A"}}
	laneB := mergeTestFinding("lane B", SeverityMedium, "internal/auth.go", mergeTestLine(42), "security")
	laneB.Citations = []Citation{{SourceID: "source-b", Label: "B"}}

	got := Merge(
		[]string{"lane-a", "lane-b"},
		[]LaneResult{
			{LaneID: "lane-b", Findings: []Finding{laneB}},
			{LaneID: "lane-a", Findings: []Finding{laneA}},
		},
	)

	if len(got) != 1 {
		t.Fatalf("Merge returned %d findings, want one cluster: %#v", len(got), got)
	}
	want := []struct {
		citation Citation
		laneID   string
	}{
		{citation: laneA.Citations[0], laneID: "lane-a"},
		{citation: laneB.Citations[0], laneID: "lane-b"},
	}
	if len(got[0].Citations) != len(want) || len(got[0].CitationLanes) != len(want) {
		t.Fatalf("merged citation provenance = (%#v, %v), want %d paired entries", got[0].Citations, got[0].CitationLanes, len(want))
	}
	for index := range want {
		if got[0].Citations[index] != want[index].citation || got[0].CitationLanes[index] != want[index].laneID {
			t.Errorf("citation %d provenance = (%#v, %q), want (%#v, %q)", index, got[0].Citations[index], got[0].CitationLanes[index], want[index].citation, want[index].laneID)
		}
	}
}
