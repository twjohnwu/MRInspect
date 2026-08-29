package lane

import (
	"fmt"
	"path"
	"strings"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane/hunk"
	"mrinspect/internal/rag"
)

const (
	findingsTableHeader    = "| # | Severity | Category | Standard | Item | File:Line |"
	findingsTableSeparator = "|---|----------|----------|----------|------|-----------|"
)

// RenderLane couples an ordered lane declaration with the resource-set names
// resolved for that declaration.
type RenderLane struct {
	Declaration          Lane
	ResolvedResourceSets []string
}

// RenderInput contains the complete state needed to render a review.
type RenderInput struct {
	Findings       []MergedFinding
	Lanes          []RenderLane
	FailedLanes    []LaneFailure
	ReceivedChunks map[string][]rag.Chunk
	Changes        []gitlab.Change
	ChangedLines   hunk.Lookup
}

// Render renders a complete review markdown document.
func Render(input RenderInput) string {
	var output strings.Builder

	output.WriteString("## MRInspect Review\n\n")
	renderScope(&output, input)
	renderFindingsTable(&output, input)
	renderSeveritySection(&output, "High", SeverityHigh, input)
	renderSeveritySection(&output, "Medium", SeverityMedium, input)
	renderSeveritySection(&output, "Low", SeverityLow, input)
	output.WriteString("### Verdict\n")
	output.WriteString(renderVerdict(input))
	output.WriteByte('\n')

	return output.String()
}

func renderScope(output *strings.Builder, input RenderInput) {
	output.WriteString("### Scope\n")
	for _, renderLane := range input.Lanes {
		laneID := neutralize(renderLane.Declaration.ID)
		resourceSets := neutralizedJoin(renderLane.ResolvedResourceSets)
		if resourceSets == "" {
			resourceSets = "none"
		}
		fmt.Fprintf(output, "- **%s** — Resource sets: %s\n", laneID, resourceSets)
	}
	if len(input.Lanes) == 0 {
		output.WriteString("- No runnable lanes.\n")
	}
	for _, failure := range input.FailedLanes {
		fmt.Fprintf(
			output,
			"- **Failed lane %s** (%s): %s\n",
			neutralize(failure.LaneID),
			neutralize(string(failure.Kind)),
			neutralize(failure.Reason),
		)
	}
	output.WriteByte('\n')
}

func renderFindingsTable(output *strings.Builder, input RenderInput) {
	output.WriteString("### Findings\n")
	output.WriteString(findingsTableHeader)
	output.WriteByte('\n')
	output.WriteString(findingsTableSeparator)
	output.WriteByte('\n')
	for index, finding := range input.Findings {
		fmt.Fprintf(
			output,
			"| %d | %s | %s | %s | %s | %s |\n",
			index+1,
			neutralize(string(finding.Severity)),
			neutralize(finding.Category),
			renderCitationSummary(finding, input.ReceivedChunks),
			neutralize(finding.Title),
			renderLocation(finding, input),
		)
	}
	output.WriteByte('\n')
}

func renderSeveritySection(output *strings.Builder, heading string, severity Severity, input RenderInput) {
	fmt.Fprintf(output, "#### %s\n", heading)
	found := false
	for index, finding := range input.Findings {
		if finding.Severity != severity {
			continue
		}
		found = true
		fmt.Fprintf(output, "**Finding %d — %s**\n", index+1, neutralize(finding.Title))
		fmt.Fprintf(output, "- **Reported by**: %s\n", neutralizedJoin(finding.ReportedBy))
		fmt.Fprintf(output, "- **Rationale**: %s\n", valueOrDash(neutralize(finding.Rationale)))
		if finding.Suggestion != "" {
			fmt.Fprintf(output, "- **Suggestion**: %s\n", neutralize(finding.Suggestion))
		}
		if len(finding.Citations) > 0 {
			fmt.Fprintf(output, "- **Citations**: %s\n", renderCitationSummary(finding, input.ReceivedChunks))
		}
		output.WriteByte('\n')
	}
	if !found {
		output.WriteString("- None.\n\n")
	}
}

func renderCitationSummary(finding MergedFinding, received map[string][]rag.Chunk) string {
	if len(finding.Citations) == 0 {
		return "—"
	}

	citations := make([]string, 0, len(finding.Citations))
	for _, citation := range finding.Citations {
		location, verified := resolveCitation(citation.SourceID, finding.ReportedBy, received)
		if !verified {
			location = valueOrDash(neutralize(citation.SourceID)) + " (unverified)"
		}
		if citation.Label != "" {
			location += " — " + neutralize(citation.Label)
		}
		citations = append(citations, location)
	}
	return strings.Join(citations, "; ")
}

func resolveCitation(sourceID string, reportedBy []string, received map[string][]rag.Chunk) (string, bool) {
	for _, laneID := range reportedBy {
		for _, chunk := range received[laneID] {
			if chunk.ID != sourceID {
				continue
			}
			location := neutralize(chunk.Source)
			if chunk.StartLine > 0 {
				location = fmt.Sprintf("%s:%d", location, chunk.StartLine)
			}
			return location, true
		}
	}
	return "", false
}

func renderLocation(finding MergedFinding, input RenderInput) string {
	if finding.File == "" && finding.Line == nil {
		return "—"
	}

	file := normalizeMergeFile(finding.File)
	if finding.Line != nil && validChangedLocation(file, *finding.Line, input) {
		return fmt.Sprintf("%s:%d", neutralize(file), *finding.Line)
	}
	if file == "" {
		return "location-unverifiable"
	}
	return neutralize(file) + " (location-unverifiable)"
}

func validChangedLocation(file string, line int, input RenderInput) bool {
	if file == "" || line <= 0 || path.IsAbs(file) || hasParentTraversal(file) {
		return false
	}

	for _, change := range input.Changes {
		if change.DeletedFile || normalizeMergeFile(change.NewPath) != file {
			continue
		}
		return input.ChangedLines.Contains(file, line)
	}
	return false
}

func hasParentTraversal(file string) bool {
	for _, segment := range strings.Split(file, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func renderVerdict(input RenderInput) string {
	if len(input.FailedLanes) > 0 || !hasRunnableLane(input.Lanes) {
		return "Incomplete"
	}
	for _, finding := range input.Findings {
		if finding.Severity == SeverityHigh {
			return "Needs changes"
		}
	}
	for _, finding := range input.Findings {
		if finding.Severity == SeverityMedium {
			return "Comments"
		}
	}
	return "Approved"
}

func hasRunnableLane(lanes []RenderLane) bool {
	for _, renderLane := range lanes {
		if renderLane.Declaration.Enabled {
			return true
		}
	}
	return false
}

func neutralizedJoin(values []string) string {
	neutralized := make([]string, len(values))
	for index, value := range values {
		neutralized[index] = neutralize(value)
	}
	return strings.Join(neutralized, ", ")
}

func valueOrDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
