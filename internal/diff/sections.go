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

// HasFileHunk reports whether diffText carries at least one hunk ("@@" line)
// for path. A header-only section, as ConvertChangesToDiff writes for an empty
// diff, does not count. Section headers are recognised as in HasFileSection.
func HasFileHunk(diffText, path string) bool {
	if path == "" {
		return false
	}
	inFile := false
	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inFile = strings.HasPrefix(line, "diff --git a/"+path+" ") || strings.HasSuffix(line, " b/"+path)
		case strings.HasPrefix(line, "--- a/") || line == "--- /dev/null":
			inFile = line == "--- a/"+path
		case line == "+++ b/"+path:
			inFile = true
		case inFile && strings.HasPrefix(line, "@@"):
			return true
		}
	}
	return false
}
