package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type transcriptEntry struct {
	Timestamp string `json:"ts"`
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	Attempt   int    `json:"attempt"`
	Prompt    string `json:"prompt"`
	Response  string `json:"response"`
	Error     string `json:"error,omitempty"`

	// Turn, Continuation, ToolCalls, and ToolResults are populated per-turn
	// by GenerateTurn (REQ-05); they carry no tool result content or raw
	// args, only metadata. Left at zero value until that wiring lands.
	Turn         int                    `json:"turn,omitempty"`
	Continuation string                 `json:"continuation,omitempty"`
	ToolCalls    []transcriptToolCall   `json:"tool_calls,omitempty"`
	ToolResults  []transcriptToolResult `json:"tool_results,omitempty"`
}

// transcriptToolCall records one requested tool call's metadata only: never
// the raw args (REQ-05: "不記錄...原始 args").
type transcriptToolCall struct {
	Name      string `json:"name"`
	ArgsBytes int    `json:"args_bytes"`
	Valid     bool   `json:"valid"`
}

// transcriptToolResult records one tool result's metadata only: never its
// content (REQ-05: "不記錄 tool result 內容").
type transcriptToolResult struct {
	Name      string `json:"name"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated"`
}

// truncatedMarkerPattern matches the "[truncated at N bytes]" line the
// enrich package's tool-output truncation appends (internal/enrich/tool.go);
// its presence, not the content, is what the transcript records (REQ-05).
var truncatedMarkerPattern = regexp.MustCompile(`\[truncated at \d+ bytes\]$`)

// transcriptToolCallsFrom converts a turn result's tool calls into their
// transcript-safe metadata: name, arg length, and JSON validity only.
func transcriptToolCallsFrom(calls []ToolCall) []transcriptToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]transcriptToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, transcriptToolCall{
			Name:      call.Name,
			ArgsBytes: len(call.Args),
			Valid:     json.Valid(call.Args),
		})
	}
	return out
}

// transcriptToolResultsFrom converts the tool results fed into a turn into
// their transcript-safe metadata: name, error code, and whether the content
// was truncated. It never records Content itself.
func transcriptToolResultsFrom(results []ToolResult) []transcriptToolResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]transcriptToolResult, 0, len(results))
	for _, result := range results {
		out = append(out, transcriptToolResult{
			Name:      result.Name,
			Error:     result.Error,
			Truncated: truncatedMarkerPattern.MatchString(strings.TrimRight(result.Content, "\n")),
		})
	}
	return out
}

type transcriptLogger struct {
	mu       sync.Mutex
	file     *os.File
	disabled bool
	warnOnce sync.Once
}

var processTranscript = &transcriptLogger{}

func (l *transcriptLogger) append(logDir string, entry transcriptEntry) {
	if logDir == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled {
		return
	}

	if l.file == nil {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			l.disable(err)
			return
		}
		name := fmt.Sprintf("ai-log-%s-%d.jsonl", time.Now().Format("20060102-150405"), os.Getpid())
		file, err := os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			l.disable(err)
			return
		}
		l.file = file
	}

	line, err := json.Marshal(entry)
	if err == nil {
		line = append(line, '\n')
		_, err = l.file.Write(line)
	}
	if err != nil {
		l.disable(err)
	}
}

func (l *transcriptLogger) disable(err error) {
	l.disabled = true
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(os.Stderr, "WARN: AI transcript logging disabled: %v\n", err)
	})
}
