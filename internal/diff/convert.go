package diff

import (
	"fmt"
	"strings"

	"mrinspect/internal/gitlab"
)

// ConvertChangesToDiff is a pure function that converts a GitLab changes list
// into a unified diff string.
func ConvertChangesToDiff(changes []gitlab.Change) (string, error) {
	if len(changes) == 0 {
		return "", nil
	}
	var sb strings.Builder
	for _, c := range changes {
		oldPath := c.OldPath
		newPath := c.NewPath
		if oldPath == "" {
			oldPath = newPath
		}

		switch {
		case c.NewFile:
			sb.WriteString(fmt.Sprintf("--- /dev/null\n+++ b/%s\n", newPath))
		case c.DeletedFile:
			sb.WriteString(fmt.Sprintf("--- a/%s\n+++ /dev/null\n", oldPath))
		case c.RenamedFile:
			sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", oldPath, newPath))
		default:
			sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", oldPath, newPath))
		}

		if c.Diff != "" {
			sb.WriteString(c.Diff)
			if !strings.HasSuffix(c.Diff, "\n") {
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String(), nil
}
