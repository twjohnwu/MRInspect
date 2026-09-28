package enrich

import (
	"bufio"
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

	requested := filepath.Join(root, filepath.Clean(args.Path))
	if info, err := os.Lstat(requested); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "path-rejected"
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
	relative := filepath.ToSlash(filepath.Clean(args.Path))
	output := newResultBuilder(lim.ResultBytes)
	reader := bufio.NewReader(file)
	lineNumber := 1
	var scannedBytes int64
	var line []byte
	lineTooLong := false
	for lineNumber <= end {
		if ctx.Err() != nil {
			return "", "timeout"
		}
		fragment, readErr := reader.ReadSlice('\n')
		if readErr != nil && readErr != io.EOF && readErr != bufio.ErrBufferFull {
			return "unable to read file", "tool-error"
		}
		if len(fragment) == 0 && readErr == io.EOF {
			break
		}
		scannedBytes += int64(len(fragment))
		if scannedBytes > maxScanBytes {
			if ctx.Err() != nil {
				return "", "timeout"
			}
			break
		}

		if !lineTooLong {
			part := fragment
			if len(part) > 0 && part[len(part)-1] == '\n' {
				part = part[:len(part)-1]
			}
			if len(line)+len(part) > maxLineBytes {
				line = nil
				lineTooLong = true
			} else {
				line = append(line, part...)
			}
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}

		if lineNumber >= start && !lineTooLong {
			value := string(line)
			if containsNUL(value) {
				return "", "tool-error"
			}
			if output.addLine(fmt.Sprintf("%s:%d: %s", relative, lineNumber, value)) {
				break
			}
		}
		lineNumber++
		line = nil
		lineTooLong = false
		if readErr == io.EOF {
			break
		}
	}
	content := output.content()
	if containsNUL(content) {
		return "", "tool-error"
	}
	return content, ""
}
