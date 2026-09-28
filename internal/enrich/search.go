package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"mrinspect/internal/ai"
	"mrinspect/internal/rag/intake"
)

type searchTool struct{}

type searchArgs struct {
	Query string   `json:"query"`
	Paths []string `json:"paths"`
}

func (*searchTool) Name() string { return "repo_search" }

func (*searchTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:        "repo_search",
		Description: "Search repository text files for a fixed string.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"required":["query"]}`),
	}
}

func (*searchTool) Run(ctx context.Context, root string, raw json.RawMessage, lim Limits) (string, string) {
	if !utf8.Valid(raw) {
		return "invalid query", "tool-error"
	}
	var args searchArgs
	if detail := decodeToolArgs(raw, &args); detail != "" {
		return detail, "tool-error"
	}
	if args.Query == "" || len(args.Query) > 200 || !utf8.ValidString(args.Query) {
		return "invalid query", "tool-error"
	}
	if len(args.Paths) > 5 {
		return "invalid paths", "tool-error"
	}
	for _, path := range args.Paths {
		if len(path) > 200 {
			return "invalid paths", "tool-error"
		}
	}
	if ctx.Err() != nil {
		return "", "timeout"
	}

	query := strings.ToLower(args.Query)
	output := newResultBuilder(lim.ResultBytes)
	files := 0
	var scannedBytes int64
	stop := fmt.Errorf("stop walk")
	timedOut := false
	containsNULMatch := false

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx.Err() != nil {
			timedOut = true
			return stop
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if hasRestrictedSegment(relative) {
				return filepath.SkipDir
			}
			return nil
		}

		files++
		if files > maxFiles {
			return stop
		}
		if !inSearchScope(relative, args.Paths) || deniedSearchPath(relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > int64(maxScanBytes)-scannedBytes {
			return stop
		}
		scannedBytes += info.Size()

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		if isBinary(file) {
			_ = file.Close()
			return nil
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			return nil
		}

		for lineIndex, line := range splitFileLines(string(data)) {
			if len(line) > maxLineBytes || !strings.Contains(strings.ToLower(line), query) {
				continue
			}
			if containsNUL(line) {
				containsNULMatch = true
				return stop
			}
			if output.addLine(fmt.Sprintf("%s:%d: %s", relative, lineIndex+1, line)) {
				return stop
			}
		}
		return nil
	})
	if timedOut || ctx.Err() != nil {
		return "", "timeout"
	}
	if containsNULMatch {
		return "", "tool-error"
	}
	if err != nil && err != stop {
		return "", "tool-error"
	}
	content := output.content()
	if containsNUL(content) {
		return "", "tool-error"
	}
	return content, ""
}

func inSearchScope(relative string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, prefix := range paths {
		if segmentPrefix(relative, prefix) {
			return true
		}
	}
	return false
}

func deniedSearchPath(relative string) bool {
	if hasRestrictedSegment(relative) {
		return true
	}
	base := filepath.Base(relative)
	lowerBase := strings.ToLower(base)
	return intake.IsDenylisted(base) || strings.Contains(lowerBase, "credential") || strings.Contains(lowerBase, "secret")
}

func hasRestrictedSegment(relative string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == ".git" || segment == ".docker" {
			return true
		}
	}
	return false
}

func splitFileLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
