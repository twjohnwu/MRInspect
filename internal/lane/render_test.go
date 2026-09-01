package lane

import (
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane/hunk"
	"mrinspect/internal/rag"
	"mrinspect/internal/validator"
)

func renderTestLine(line int) *int {
	return &line
}

func renderTestLane(id string, sets ...string) RenderLane {
	return RenderLane{
		Declaration:          Lane{ID: id, Enabled: true},
		ResolvedResourceSets: sets,
	}
}

func renderTestFinding(title string, severity Severity, laneID string) MergedFinding {
	return MergedFinding{
		Finding: Finding{
			Title:     title,
			Severity:  severity,
			Rationale: "Rationale for " + title,
			Category:  "correctness",
		},
		ReportedBy: []string{laneID},
	}
}

func renderTestInput(findings []MergedFinding) RenderInput {
	return RenderInput{
		Findings:       findings,
		Lanes:          []RenderLane{renderTestLane("code-diff")},
		ReceivedChunks: map[string][]rag.Chunk{},
		ChangedLines:   hunk.Build(nil),
	}
}

func renderTestExactLineCount(markdown, want string) int {
	count := 0
	for _, line := range strings.Split(markdown, "\n") {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}

func renderTestFourthLevelHeadingCount(markdown string) int {
	count := 0
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#### ") {
			count++
		}
	}
	return count
}

func renderTestSection(markdown, heading, nextHeading string) (string, bool) {
	start := strings.Index(markdown, heading)
	if start < 0 {
		return "", false
	}
	start += len(heading)
	if nextHeading == "" {
		return markdown[start:], true
	}
	end := strings.Index(markdown[start:], nextHeading)
	if end < 0 {
		return "", false
	}
	return markdown[start : start+end], true
}

func renderTestFindingRows(markdown string) ([]string, bool) {
	const header = "| # | Severity | Category | Standard | Item | File:Line |"
	lines := strings.Split(markdown, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, false
	}

	tableLines := make([]string, 0)
	for _, line := range lines[start:] {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			break
		}
		tableLines = append(tableLines, line)
	}
	if len(tableLines) < 2 {
		return nil, false
	}
	return tableLines[2:], true
}

func renderTestRowWithTitle(rows []string, title string) (string, bool) {
	for _, row := range rows {
		if strings.Contains(row, title) {
			return row, true
		}
	}
	return "", false
}

// TestRender_PreservesExistingStructure verifies REQ-06 / S-22: rendering
// retains the Findings table and three populated severity sections and remains
// acceptable to the existing review validator.
func TestRender_PreservesExistingStructure(t *testing.T) {
	findings := []MergedFinding{
		renderTestFinding("high title", SeverityHigh, "code-diff"),
		renderTestFinding("medium title", SeverityMedium, "code-diff"),
		renderTestFinding("low title", SeverityLow, "code-diff"),
	}
	rendered := Render(renderTestInput(findings))

	if err := validator.New(config.Config{}).ValidateReviewContent(rendered); err != nil {
		t.Errorf("ValidateReviewContent(rendered) = %v, want nil", err)
	}
	for _, heading := range []string{"#### High", "#### Medium", "#### Low"} {
		if got := renderTestExactLineCount(rendered, heading); got != 1 {
			t.Errorf("heading %q occurs on %d exact lines, want exactly 1", heading, got)
		}
	}
	if got := renderTestFourthLevelHeadingCount(rendered); got != 3 {
		t.Errorf("rendered review has %d fourth-level headings, want exactly the three severity headings", got)
	}

	high, highOK := renderTestSection(rendered, "#### High", "#### Medium")
	medium, mediumOK := renderTestSection(rendered, "#### Medium", "#### Low")
	low, lowOK := renderTestSection(rendered, "#### Low", "### Verdict")
	for _, check := range []struct {
		name    string
		section string
		found   bool
		title   string
	}{
		{name: "high", section: high, found: highOK, title: "high title"},
		{name: "medium", section: medium, found: mediumOK, title: "medium title"},
		{name: "low", section: low, found: lowOK, title: "low title"},
	} {
		if !check.found || !strings.Contains(check.section, check.title) {
			t.Errorf("%s section does not contain matching title %q", check.name, check.title)
		}
	}

	rows, foundTable := renderTestFindingRows(rendered)
	if !foundTable {
		t.Error("rendered review does not contain the existing Findings table header and separator")
	} else if len(rows) != len(findings) {
		t.Errorf("Findings table has %d data rows, want exactly %d", len(rows), len(findings))
	}
}

func TestRender_EmptyFindingsPlaceholderRow(t *testing.T) {
	rendered := Render(renderTestInput(nil))

	for _, row := range []string{
		"| # | Severity | Category | Standard | Item | File:Line |",
		"|---|----------|----------|----------|------|-----------|",
		"| - | - | - | - | No findings reported | - |",
	} {
		if got := renderTestExactLineCount(rendered, row); got != 1 {
			t.Errorf("row %q occurs on %d exact lines, want exactly 1", row, got)
		}
	}
}

// TestRender_ShowsLaneAndCitation verifies REQ-06 / S-23: each finding shows
// its reporting lane and a verified chunk location, omitting a zero line.
func TestRender_ShowsLaneAndCitation(t *testing.T) {
	withLine := renderTestFinding("cited standard", SeverityHigh, "standards")
	withLine.Citations = []Citation{{SourceID: "chunk-with-line"}}
	withoutLine := renderTestFinding("cited file only", SeverityLow, "standards")
	withoutLine.Citations = []Citation{{SourceID: "chunk-without-line"}}
	input := renderTestInput([]MergedFinding{withLine, withoutLine})
	input.Lanes = []RenderLane{renderTestLane("standards", "official-standards")}
	input.ReceivedChunks = map[string][]rag.Chunk{
		"standards": {
			{ID: "chunk-with-line", Source: "standards/security.md", StartLine: 17},
			{ID: "chunk-without-line", Source: "standards/overview.md", StartLine: 0},
		},
	}
	rendered := Render(input)

	high, highOK := renderTestSection(rendered, "#### High", "#### Medium")
	if !highOK || !strings.Contains(high, withLine.Title) ||
		!strings.Contains(high, "standards") || !strings.Contains(high, "standards/security.md:17") {
		t.Errorf("High finding entry does not contain title, lane, and verified citation: %q", high)
	}
	low, lowOK := renderTestSection(rendered, "#### Low", "### Verdict")
	if !lowOK || !strings.Contains(low, withoutLine.Title) || !strings.Contains(low, "standards/overview.md") {
		t.Errorf("Low finding entry does not contain title and zero-line citation file: %q", low)
	}
	if strings.Contains(rendered, "standards/overview.md:0") {
		t.Error("zero-line citation rendered the forbidden precise location standards/overview.md:0")
	}
}

// TestRender_FlagsUnverifiedCitation verifies REQ-06 / S-24: an unmatched
// source ID remains visible with its finding and is explicitly unverified.
func TestRender_FlagsUnverifiedCitation(t *testing.T) {
	finding := renderTestFinding("unmatched citation finding", SeverityMedium, "standards")
	finding.Citations = []Citation{{SourceID: "missing-source"}}
	input := renderTestInput([]MergedFinding{finding})
	input.Lanes = []RenderLane{renderTestLane("standards", "official-standards")}
	input.ReceivedChunks = map[string][]rag.Chunk{
		"standards": {{ID: "received-source", Source: "standards/received.md", StartLine: 4}},
	}
	rendered := Render(input)

	medium, ok := renderTestSection(rendered, "#### Medium", "#### Low")
	if !ok || !strings.Contains(medium, finding.Title) ||
		!strings.Contains(medium, "missing-source") || !strings.Contains(medium, "unverified") {
		t.Errorf("Medium finding entry does not retain and flag unmatched citation: %q", medium)
	}
}

// TestRender_ListsFailedLanes verifies REQ-06 / S-25: Scope preserves ordered
// lane/resource coverage, exposes a named lane failure, and forces Incomplete.
func TestRender_ListsFailedLanes(t *testing.T) {
	input := renderTestInput([]MergedFinding{renderTestFinding("minor note", SeverityLow, "code-diff")})
	input.Lanes = []RenderLane{
		renderTestLane("spec-conformance", "product-specs"),
		renderTestLane("standards", "official-standards"),
		renderTestLane("code-diff", "changed-code"),
	}
	input.FailedLanes = []LaneFailure{{
		LaneID: "standards",
		Kind:   FailureKindParse,
		Reason: "response was not valid JSON",
	}}
	rendered := Render(input)

	scope, ok := renderTestSection(rendered, "### Scope", "### Findings")
	if !ok {
		t.Error("rendered review does not contain a Scope section before Findings")
	} else {
		for _, want := range []string{
			"spec-conformance", "product-specs",
			"standards", "official-standards",
			"code-diff", "changed-code",
			"response was not valid JSON",
		} {
			if !strings.Contains(scope, want) {
				t.Errorf("Scope section does not contain %q", want)
			}
		}
	}
	if !strings.Contains(rendered, "### Verdict\nIncomplete") {
		t.Error("failed lane did not produce exact Incomplete verdict")
	}
}

// TestRender_NeutralizesModelMarkdown verifies REQ-06 / S-38: every
// model-originated string is neutralized before entering tables or sections.
func TestRender_NeutralizesModelMarkdown(t *testing.T) {
	injectedTitle := renderTestFinding("x |\n#### High\n偽造的發現", SeverityHigh, "standards")
	injectedTitle.Rationale = "理由第一行 |\n理由第二行"
	injectedCitation := renderTestFinding("citation injection guard", SeverityMedium, "standards")
	injectedCitation.Citations = []Citation{{SourceID: "a |\n#### High\n偽造"}}
	input := renderTestInput([]MergedFinding{injectedTitle, injectedCitation})
	input.Lanes = []RenderLane{renderTestLane("standards", "official-standards")}
	input.ReceivedChunks = map[string][]rag.Chunk{"standards": {}}
	rendered := Render(input)

	if got := strings.Count(rendered, "#### High"); got != 1 {
		t.Errorf("rendered output contains %d occurrences of %q, want exactly 1", got, "#### High")
	}
	rows, foundTable := renderTestFindingRows(rendered)
	if !foundTable {
		t.Error("rendered review does not contain the Findings table")
	} else {
		if len(rows) != 2 {
			t.Errorf("Findings table has %d data rows, want exactly 2", len(rows))
		}
		matchingRows := make([]string, 0, 1)
		for _, row := range rows {
			if strings.Contains(row, "x") && strings.Contains(row, "偽造的發現") {
				matchingRows = append(matchingRows, row)
			}
		}
		if len(matchingRows) != 1 {
			t.Errorf("injected title occupies %d Findings rows, want exactly 1", len(matchingRows))
		} else if !strings.Contains(matchingRows[0], `\|`) {
			t.Errorf("injected title row does not escape its table delimiter: %q", matchingRows[0])
		}
	}
	if !strings.Contains(rendered, `理由第一行 \| 理由第二行`) {
		t.Error("rationale newline/table delimiter was not neutralized into one safe line")
	}
	if !strings.Contains(rendered, `a \|`) || !strings.Contains(rendered, "unverified") {
		t.Error("unmatched sourceId was not both neutralized and rendered as unverified")
	}
}

// TestRender_ValidatesFileAgainstDiff verifies REQ-06 / S-39: only normalized
// new-side files and lines inside a new-side hunk render as precise locations.
func TestRender_ValidatesFileAgainstDiff(t *testing.T) {
	changes := []gitlab.Change{{
		OldPath: "internal/service.go",
		NewPath: "internal/service.go",
		Diff:    "@@ -8,3 +9,3 @@ func changed() {\n-old\n+new\n context\n",
	}}
	findings := []MergedFinding{
		renderTestFinding("file outside diff", SeverityHigh, "code-diff"),
		renderTestFinding("traversal file", SeverityHigh, "code-diff"),
		renderTestFinding("line outside hunk", SeverityMedium, "code-diff"),
		renderTestFinding("line inside hunk", SeverityLow, "code-diff"),
	}
	findings[0].File, findings[0].Line = ".gitlab-ci.yml", renderTestLine(10)
	findings[1].File, findings[1].Line = "../secrets.go", renderTestLine(10)
	findings[2].File, findings[2].Line = "internal/service.go", renderTestLine(999999)
	findings[3].File, findings[3].Line = "internal/service.go", renderTestLine(10)
	input := renderTestInput(findings)
	input.Changes = changes
	input.ChangedLines = hunk.Build(changes)
	rendered := Render(input)

	rows, foundTable := renderTestFindingRows(rendered)
	if !foundTable {
		t.Fatal("rendered review does not contain the Findings table")
	}
	for _, check := range []struct {
		title      string
		coordinate string
	}{
		{title: "file outside diff", coordinate: ".gitlab-ci.yml:10"},
		{title: "traversal file", coordinate: "../secrets.go:10"},
		{title: "line outside hunk", coordinate: "internal/service.go:999999"},
	} {
		row, found := renderTestRowWithTitle(rows, check.title)
		if !found {
			t.Errorf("unverifiable finding %q was dropped from the Findings table", check.title)
			continue
		}
		if !strings.Contains(row, "location-unverifiable") {
			t.Errorf("finding %q is not marked location-unverifiable: %q", check.title, row)
		}
		if strings.Contains(row, check.coordinate) {
			t.Errorf("finding %q renders unconfirmed precise coordinate %q", check.title, check.coordinate)
		}
	}
	validRow, found := renderTestRowWithTitle(rows, "line inside hunk")
	if !found {
		t.Error("valid in-hunk finding was dropped from the Findings table")
	} else {
		if !strings.Contains(validRow, "internal/service.go:10") {
			t.Errorf("valid in-hunk finding does not render its precise coordinate: %q", validRow)
		}
		if strings.Contains(validRow, "location-unverifiable") {
			t.Errorf("valid in-hunk finding is incorrectly marked location-unverifiable: %q", validRow)
		}
	}
}

func TestRender_NeutralizesBackslash(t *testing.T) {
	findings := []MergedFinding{
		renderTestFinding("backslash-title-a\\|b", SeverityHigh, "code-diff"),
		renderTestFinding("control-title-a|b", SeverityHigh, "code-diff"),
	}
	rows, ok := renderTestFindingRows(Render(renderTestInput(findings)))
	if !ok {
		t.Fatal("rendered review does not contain the Findings table")
	}
	backslashRow, found := renderTestRowWithTitle(rows, "backslash-title")
	if !found {
		t.Fatal("backslash finding is missing from the Findings table")
	}
	controlRow, found := renderTestRowWithTitle(rows, "control-title")
	if !found {
		t.Fatal("control finding is missing from the Findings table")
	}
	if !strings.Contains(backslashRow, `backslash-title-a\\\|b`) {
		t.Errorf("backslash and pipe are not independently escaped: %q", backslashRow)
	}
	if got, want := strings.Count(backslashRow, "|"), strings.Count(controlRow, "|"); got != want {
		t.Errorf("backslash row has %d raw pipe delimiters, want control row count %d", got, want)
	}
}

func TestRender_CitationVerifiedOnlyAgainstProvidingLane(t *testing.T) {
	finding := renderTestFinding("lane-scoped citations", SeverityMedium, "lane-a")
	finding.ReportedBy = []string{"lane-a", "lane-b"}
	finding.Citations = []Citation{
		{SourceID: "b-chunk-1", Label: "provided by lane A"},
		{SourceID: "b-chunk-1", Label: "provided by lane B"},
	}
	finding.CitationLanes = []string{"lane-a", "lane-b"}
	input := renderTestInput([]MergedFinding{finding})
	input.Lanes = []RenderLane{renderTestLane("lane-a"), renderTestLane("lane-b")}
	input.ReceivedChunks = map[string][]rag.Chunk{
		"lane-a": {},
		"lane-b": {{ID: "b-chunk-1", Source: "standards/lane-b.md", StartLine: 23}},
	}

	rendered := Render(input)

	if !strings.Contains(rendered, "b-chunk-1 (unverified) — provided by lane A") {
		t.Errorf("lane-a citation matched another lane's chunk: %q", rendered)
	}
	if !strings.Contains(rendered, "standards/lane-b.md:23 — provided by lane B") {
		t.Errorf("lane-b citation did not verify against its own chunk: %q", rendered)
	}
}
