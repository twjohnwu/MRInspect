// Package intake implements the shared collection gatekeeper used by every
// rag backend's walker (REQ-03, REQ-11): filename denylist, symlink-cycle
// avoidance, depth cap, and max file size, each observable via a named skip
// reason.
package intake

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// WalkOptions configures Walk. MaxDepth and MaxFileSizeKB are injectable
// options (not package constants) so callers — and tests — can exercise the
// limits without building fixtures at real-world scale. A zero value for
// either means "no limit", matching Go's usual zero-value convention.
type WalkOptions struct {
	Paths         []string
	Include       []string
	Exclude       []string
	MaxDepth      int
	MaxFileSizeKB int
}

// SkipReason names why a path was excluded from a WalkResult, so callers can
// report each skip class individually (REQ-11) instead of one generic
// failure.
type SkipReason string

const (
	SkipReasonDenylist        SkipReason = "denylist"
	SkipReasonSymlinkCycle    SkipReason = "symlink-not-followed"
	SkipReasonDepthLimit      SkipReason = "depth-limit-exceeded"
	SkipReasonFileTooLarge    SkipReason = "file-too-large"
	SkipReasonUnsupportedType SkipReason = "unsupported-file-type"
	// SkipReasonUnreadable covers entries WalkDir could not stat/read (e.g.
	// a permission error): the walk must complete rather than abort, so
	// these are recorded as a named skip instead of a fatal error.
	SkipReasonUnreadable SkipReason = "unreadable"
)

// SkippedFile records one path excluded from a walk, with its reason.
type SkippedFile struct {
	Path   string
	Reason SkipReason
}

// WalkResult is the outcome of Walk: the files selected for indexing, and
// every path skipped with a named reason (REQ-03, REQ-11).
type WalkResult struct {
	Files        []string
	Skipped      []SkippedFile
	FilesSkipped int
}

// Walk recursively traverses opts.Paths, applying the filename denylist,
// include/exclude patterns, symlink-cycle avoidance, the depth cap, and the
// max file size limit (REQ-03, REQ-11). It always completes: an unreadable
// entry is recorded as a skip, never returned as a fatal error.
func Walk(opts WalkOptions) (WalkResult, error) {
	include, err := compileGlobs(opts.Include)
	if err != nil {
		return WalkResult{}, fmt.Errorf("Walk: compile include patterns: %w", err)
	}
	exclude, err := compileGlobs(opts.Exclude)
	if err != nil {
		return WalkResult{}, fmt.Errorf("Walk: compile exclude patterns: %w", err)
	}

	var result WalkResult
	for _, root := range opts.Paths {
		w := &walker{
			root:      root,
			include:   include,
			exclude:   exclude,
			maxDepth:  opts.MaxDepth,
			maxSizeKB: opts.MaxFileSizeKB,
			result:    &result,
		}
		if err := filepath.WalkDir(root, w.visit); err != nil {
			return WalkResult{}, fmt.Errorf("Walk: %s: %w", root, err)
		}
	}
	return result, nil
}

// walker holds the per-root state for one filepath.WalkDir pass. Grouping
// this into a struct (rather than closures over Walk's locals) keeps visit
// a plain method instead of a deeply nested inline func.
type walker struct {
	root      string
	include   []*regexp.Regexp
	exclude   []*regexp.Regexp
	maxDepth  int
	maxSizeKB int
	result    *WalkResult
}

// visit is filepath.WalkDir's callback for one root. It never returns an
// error that would abort the walk — unreadable entries are recorded and
// skipped instead, per REQ-11's "always completes" requirement.
func (w *walker) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		w.skip(path, SkipReasonUnreadable)
		if d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}

	if path == w.root {
		return nil // the root itself is never a candidate file
	}

	// Symlinks are never followed, whether or not they form a cycle:
	// filepath.WalkDir already does not descend into them, so recording the
	// skip here is enough to make the refusal observable.
	if d.Type()&fs.ModeSymlink != 0 {
		w.skip(path, SkipReasonSymlinkCycle)
		return nil
	}

	if d.IsDir() {
		return nil // always recurse into real directories
	}

	relPath := w.relPath(path)
	if !w.included(relPath) || w.excluded(relPath) {
		return nil // not selected by Include/Exclude — not an error
	}

	if isDenylisted(filepath.Base(path)) {
		w.skip(path, SkipReasonDenylist)
		return nil
	}

	if w.maxDepth > 0 && depthOf(relPath) > w.maxDepth {
		w.skip(path, SkipReasonDepthLimit)
		return nil
	}

	info, err := d.Info()
	if err != nil {
		w.skip(path, SkipReasonUnreadable)
		return nil
	}

	if !info.Mode().IsRegular() {
		w.skip(path, SkipReasonUnsupportedType)
		return nil
	}

	if w.maxSizeKB > 0 && info.Size() > int64(w.maxSizeKB)*1024 {
		w.skip(path, SkipReasonFileTooLarge)
		return nil
	}

	w.result.Files = append(w.result.Files, path)
	return nil
}

func (w *walker) relPath(path string) string {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}

func (w *walker) included(relPath string) bool {
	if len(w.include) == 0 {
		return true // no Include patterns means "everything is a candidate"
	}
	return matchesAny(w.include, relPath)
}

func (w *walker) excluded(relPath string) bool {
	return matchesAny(w.exclude, relPath)
}

func (w *walker) skip(path string, reason SkipReason) {
	w.result.Skipped = append(w.result.Skipped, SkippedFile{Path: path, Reason: reason})
	w.result.FilesSkipped++
}

// depthOf counts relPath's path segments (a top-level file is depth 1).
func depthOf(relPath string) int {
	return strings.Count(relPath, "/") + 1
}

// compileGlobs compiles each pattern via globToRegexp.
func compileGlobs(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := globToRegexp(pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", pattern, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func matchesAny(patterns []*regexp.Regexp, relPath string) bool {
	for _, re := range patterns {
		if re.MatchString(relPath) {
			return true
		}
	}
	return false
}

// globToRegexp translates a doublestar-style glob into an anchored regexp
// matched against a forward-slash relative path: "**/" matches zero or more
// leading path segments (so "**/*" also matches a root-level file), a bare
// "**" matches anything including "/", "*" matches within one segment, and
// "?" matches one non-separator rune.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch c := runes[i]; {
		case c == '*' && i+1 < len(runes) && runes[i+1] == '*':
			i++ // consume the second '*'
			if i+1 < len(runes) && runes[i+1] == '/' {
				b.WriteString("(?:.*/)?")
				i++ // consume the following '/' too
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
