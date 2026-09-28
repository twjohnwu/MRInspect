package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"mrinspect/internal/ai"
)

const maxRangeLine = 10_000_000

type readRangeTool struct{}

type readRangeArgs struct {
	Path  string `json:"path"`
	Start *int   `json:"start"`
	End   *int   `json:"end"`
}

func (*readRangeTool) Name() string { return "read_file_ranges" }

func (*readRangeTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:        "read_file_ranges",
		Description: "Read a bounded line range from one repository text file.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"start":{"type":"integer"},"end":{"type":"integer"}},"required":["path"]}`),
	}
}

func (*readRangeTool) Run(ctx context.Context, root string, raw json.RawMessage, lim Limits) (string, string) {
	var args readRangeArgs
	if detail := decodeToolArgs(raw, &args); detail != "" {
		return detail, "tool-error"
	}
	if args.Path == "" || len(args.Path) > 200 {
		return "invalid path", "tool-error"
	}
	start, end := 1, maxRangeLine
	if args.Start != nil {
		start = *args.Start
	}
	if args.End != nil {
		end = *args.End
	}
	if start < 1 || end < 1 || start > end || start > maxRangeLine || end > maxRangeLine {
		return "invalid range", "tool-error"
	}

	resolved, code := resolve(root, args.Path)
	if code != "" {
		return "", code
	}
	if ctx.Err() != nil {
		return "", "timeout"
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "unable to read file", "tool-error"
	}
	defer file.Close()
	if isBinary(file) {
		return "", "path-rejected"
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "unable to read file", "tool-error"
	}

	lines := splitFileLines(string(data))
	if len(lines) == 0 || start > len(lines) {
		return "", ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	relative := filepath.ToSlash(filepath.Clean(args.Path))
	output := newResultBuilder(lim.ResultBytes)
	for lineNumber := start; lineNumber <= end; lineNumber++ {
		line := lines[lineNumber-1]
		if len(line) > maxLineBytes {
			continue
		}
		if containsNUL(line) {
			return "", "tool-error"
		}
		if output.addLine(fmt.Sprintf("%s:%d: %s", relative, lineNumber, line)) {
			break
		}
	}
	content := output.content()
	if containsNUL(content) {
		return "", "tool-error"
	}
	return content, ""
}
