// Package disclosure renders the diff trailer that tells the model (and the
// reader) which files were left out of a reviewed diff. It is a leaf package
// so both internal/diff and internal/diffbudget can share one format.
package disclosure

import (
	"fmt"
	"strings"

	"mrinspect/internal/gitlab"
)

// ReasonDiffUnavailable marks a file whose diff GitLab returned empty because
// of its own diff limits; it is never sent to the model.
const ReasonDiffUnavailable = "not reviewed: diff unavailable from GitLab"

// Entry is one file excluded from the diff and why.
type Entry struct {
	Path   string
	Reason string
}

// SplitTruncated separates changes whose diff GitLab withheld (see
// gitlab.Change.Truncated) from the reviewable ones.
func SplitTruncated(changes []gitlab.Change) ([]gitlab.Change, []Entry) {
	reviewable := make([]gitlab.Change, 0, len(changes))
	var unavailable []Entry
	for _, change := range changes {
		if change.Truncated() {
			path := change.NewPath
			if path == "" {
				path = change.OldPath
			}
			unavailable = append(unavailable, Entry{Path: path, Reason: ReasonDiffUnavailable})
			continue
		}
		reviewable = append(reviewable, change)
	}
	return reviewable, unavailable
}

// Render renders a deterministic, clearly-marked comment block listing the
// excluded files. It returns "" when there are none.
func Render(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}

	var trailer strings.Builder
	trailer.WriteString("\n\n<!-- mrinspect:diff-reduction -->\n")
	trailer.WriteString("Excluded from this diff:\n")
	for _, entry := range entries {
		fmt.Fprintf(&trailer, "- %s (%s)\n", entry.Path, entry.Reason)
	}
	trailer.WriteString("<!-- /mrinspect:diff-reduction -->\n")
	return trailer.String()
}
