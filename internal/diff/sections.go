package diff

import "strings"

// HasFileSection reports whether diffText carries a diff section for any of
// the given paths. It recognises the headers written by both the local go-git
// patch ("diff --git a/<old> b/<new>") and ConvertChangesToDiff ("--- a/<old>"
// / "+++ b/<new>", with /dev/null for the absent side).
func HasFileSection(diffText string, paths ...string) bool {
	for _, line := range strings.Split(diffText, "\n") {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if line == "--- a/"+p || line == "+++ b/"+p ||
				(strings.HasPrefix(line, "diff --git ") &&
					(strings.HasPrefix(line, "diff --git a/"+p+" ") || strings.HasSuffix(line, " b/"+p))) {
				return true
			}
		}
	}
	return false
}
