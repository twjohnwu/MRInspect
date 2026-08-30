package evalrun

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/logger"
)

var ErrNoValidFixtures = errors.New("no valid fixtures")

const (
	maxFixtureSize      = 1 << 20
	fixtureHeaderPrefix = "# mrinspect-fixture:"
)

type Fixture struct {
	Name    string
	Diff    []byte
	Changes []gitlab.Change
}

func LoadFixtures(dir string, log *logger.Logger) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if isFixtureName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	fixtures := make([]Fixture, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			warnSkippedFixture(log, name, "inspect fixture", err)
			continue
		}
		if !info.Mode().IsRegular() {
			warnSkippedFixture(log, name, "fixture is not a regular file", nil)
			continue
		}
		if info.Size() > maxFixtureSize {
			warnSkippedFixture(log, name, "fixture exceeds 1 MiB", nil)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			warnSkippedFixture(log, name, "read fixture", err)
			continue
		}
		if len(data) == 0 {
			warnSkippedFixture(log, name, "fixture is empty", nil)
			continue
		}
		if len(data) > maxFixtureSize {
			warnSkippedFixture(log, name, "fixture exceeds 1 MiB", nil)
			continue
		}
		if !utf8.Valid(data) {
			warnSkippedFixture(log, name, "fixture is not valid UTF-8", nil)
			continue
		}

		fixtures = append(fixtures, Fixture{
			Name:    name,
			Diff:    data,
			Changes: synthesizeChanges(data),
		})
	}

	if len(fixtures) == 0 {
		return fixtures, ErrNoValidFixtures
	}
	return fixtures, nil
}

func Run(fixturesDir, reportPath string, log *logger.Logger) error {
	if _, err := LoadFixtures(fixturesDir, log); err != nil {
		return err
	}
	return errors.New("not implemented")
}

func isFixtureName(name string) bool {
	if len(name) < len("00-a.diff") || name[2] != '-' || !strings.HasSuffix(name, ".diff") {
		return false
	}
	return name[0] >= '0' && name[0] <= '9' && name[1] >= '0' && name[1] <= '9'
}

func warnSkippedFixture(log *logger.Logger, name, reason string, err error) {
	if log == nil {
		return
	}
	if err != nil {
		log.Warn("skipping fixture", "fixture", name, "reason", reason, "error", err)
		return
	}
	log.Warn("skipping fixture", "fixture", name, "reason", reason)
}

type diffFile struct {
	oldPath   string
	newPath   string
	headerAt  int
	diffStart int
}

func synthesizeChanges(data []byte) []gitlab.Change {
	lines := splitLines(data)
	firstDiffLine := 0
	if len(lines) > 0 && bytes.HasPrefix(lines[0].text, []byte(fixtureHeaderPrefix)) {
		firstDiffLine = 1
	}
	files := make([]diffFile, 0)
	for i := firstDiffLine; i+1 < len(lines); i++ {
		oldPath, ok := diffPath(lines[i].text, "--- a/")
		if !ok {
			continue
		}
		newPath, ok := diffPath(lines[i+1].text, "+++ b/")
		if !ok {
			continue
		}
		files = append(files, diffFile{
			oldPath:   oldPath,
			newPath:   newPath,
			headerAt:  lines[i].start,
			diffStart: lines[i+1].end,
		})
		i++
	}

	changes := make([]gitlab.Change, 0, len(files))
	for i, file := range files {
		diffEnd := len(data)
		if i+1 < len(files) {
			diffEnd = files[i+1].headerAt
			for _, line := range lines {
				if line.start < file.diffStart || line.start >= diffEnd {
					continue
				}
				if bytes.HasPrefix(line.text, []byte("diff --git ")) {
					diffEnd = line.start
					break
				}
			}
		}
		changes = append(changes, gitlab.Change{
			OldPath: file.oldPath,
			NewPath: file.newPath,
			Diff:    string(data[file.diffStart:diffEnd]),
		})
	}
	return changes
}

type diffLine struct {
	text       []byte
	start, end int
}

func splitLines(data []byte) []diffLine {
	lines := make([]diffLine, 0, bytes.Count(data, []byte{'\n'})+1)
	for start := 0; start < len(data); {
		relativeEnd := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if relativeEnd >= 0 {
			end = start + relativeEnd + 1
		}
		textEnd := end
		if textEnd > start && data[textEnd-1] == '\n' {
			textEnd--
		}
		if textEnd > start && data[textEnd-1] == '\r' {
			textEnd--
		}
		lines = append(lines, diffLine{text: data[start:textEnd], start: start, end: end})
		start = end
	}
	return lines
}

func diffPath(line []byte, prefix string) (string, bool) {
	if !bytes.HasPrefix(line, []byte(prefix)) || len(line) == len(prefix) {
		return "", false
	}
	return string(line[len(prefix):]), true
}
