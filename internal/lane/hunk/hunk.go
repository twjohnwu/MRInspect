package hunk

import (
	"regexp"
	"strconv"
	"strings"

	"mrinspect/internal/gitlab"
)

var headerPattern = regexp.MustCompile(`^@@ -[0-9]+(,[0-9]+)? \+([0-9]+)(,([0-9]+))? @@`)

// Range is an inclusive range of line numbers on the new side of a diff.
type Range struct {
	Start int
	End   int
}

// Lookup stores new-side hunk ranges by normalized file path.
type Lookup struct {
	rangesByFile map[string][]Range
}

// Parse extracts new-side ranges from unified-diff hunk headers.
func Parse(diff string) []Range {
	var ranges []Range
	for _, line := range strings.Split(diff, "\n") {
		matches := headerPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		start, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}

		count := 1
		if matches[4] != "" {
			count, err = strconv.Atoi(matches[4])
			if err != nil {
				continue
			}
		}
		if count == 0 {
			continue
		}

		maxInt := int(^uint(0) >> 1)
		if start > maxInt-(count-1) {
			continue
		}
		ranges = append(ranges, Range{Start: start, End: start + count - 1})
	}
	return ranges
}

// Build creates a per-file lookup from GitLab changes.
func Build(changes []gitlab.Change) Lookup {
	lookup := Lookup{rangesByFile: make(map[string][]Range, len(changes))}
	for _, change := range changes {
		if change.DeletedFile {
			continue
		}
		path := normalizeFile(change.NewPath)
		lookup.rangesByFile[path] = append(lookup.rangesByFile[path], Parse(change.Diff)...)
	}
	return lookup
}

// Contains reports whether line belongs to a new-side hunk for file.
func (lookup Lookup) Contains(file string, line int) bool {
	for _, lineRange := range lookup.rangesByFile[normalizeFile(file)] {
		if line >= lineRange.Start && line <= lineRange.End {
			return true
		}
	}
	return false
}

func normalizeFile(file string) string {
	file = strings.TrimPrefix(file, "./")
	if strings.HasPrefix(file, "a/") || strings.HasPrefix(file, "b/") {
		file = file[2:]
	}
	return file
}
