package reviewer

import (
	"fmt"
	"strings"
)

// Section is one named, token-estimated component of a composed review
// prompt. TokenEst is supplied by the caller from the same composition that
// produced the section, never recomputed independently by this file.
type Section struct {
	Name     string
	TokenEst int
}

// BuildPromptBreakdown renders the always-on, incident-proven observability
// table: which section dominates a composed prompt. Percentages are one
// decimal place; their denominator is the sum of the listed sections.
func BuildPromptBreakdown(sections []Section) string {
	total := 0
	for _, section := range sections {
		total += section.TokenEst
	}

	var sb strings.Builder
	sb.WriteString("Prompt composition breakdown (estimated tokens per section):\n")
	sb.WriteString("| Section | Tokens | % of total |\n")
	sb.WriteString("|---------|--------|------------|\n")
	for _, section := range sections {
		var pct float64
		if total > 0 {
			pct = float64(section.TokenEst) / float64(total) * 100
		}
		fmt.Fprintf(&sb, "| %s | %d | %.1f%% |\n", section.Name, section.TokenEst, pct)
	}
	fmt.Fprintf(&sb, "| **total** | %d | 100.0%% |\n", total)
	return sb.String()
}
