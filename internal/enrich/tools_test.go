package enrich

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/ai"
)

// REQ-03 / S-06
func TestRepoSearch_ScopeExcludeTruncate(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/ai/client.go", "func NewClient() {}\nvar factory = NewClient\n")
	writeTestFile(t, root, "internals-secret/x.go", "NewClient\n")
	writeTestFile(t, root, "docs/x.md", "use newclient here\n")
	writeTestFile(t, root, ".env", "NewClient\n")
	writeTestFile(t, root, ".git/config", "NewClient\n")
	writeTestFile(t, root, "secrets/credentials.yaml", "NewClient\n")
	writeTestFile(t, root, ".docker/config.json", "NewClient\n")
	writeTestFile(t, root, "bin/blob", "\x00NewClient\n")
	writeTestFile(t, root, "big.txt", strings.Repeat("NewClient\n", 2000))

	ex := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second})
	calls := []ai.ToolCall{
		{ID: "c1", Name: "repo_search", Args: json.RawMessage(`{"query":"newclient","paths":["internal","docs"]}`)},
		{ID: "c2", Name: "repo_search", Args: json.RawMessage(`{"query":"NewClient","paths":["big.txt"]}`)},
		{ID: "c3", Name: "repo_search", Args: json.RawMessage(`{"query":"NewClient"}`)},
	}

	results := make([]ai.ToolResult, 0, len(calls))
	for _, call := range calls {
		result := ex.Execute(context.Background(), []ai.ToolCall{call}, 10)
		if len(result) != 1 {
			t.Fatalf("Execute(%s) returned %d results, want 1", call.ID, len(result))
		}
		results = append(results, result[0])
	}

	if results[0].Error != "" {
		t.Errorf("scoped repo_search Error = %q, want empty", results[0].Error)
	}
	gotLines := nonEmptyLines(results[0].Content)
	wantLines := []string{
		"docs/x.md:1: use newclient here",
		"internal/ai/client.go:1: func NewClient() {}",
		"internal/ai/client.go:2: var factory = NewClient",
	}
	sort.Strings(gotLines)
	sort.Strings(wantLines)
	if !reflect.DeepEqual(gotLines, wantLines) {
		t.Errorf("scoped repo_search lines = %#v, want %#v", gotLines, wantLines)
	}
	for _, line := range gotLines {
		if strings.HasPrefix(line, "internals-secret/") {
			t.Errorf("scoped repo_search unexpectedly matched segment-prefix sibling: %q", line)
		}
	}

	if results[1].Error != "" {
		t.Errorf("truncated repo_search Error = %q, want empty", results[1].Error)
	}
	if len(results[1].Content) > 512 {
		t.Errorf("truncated repo_search returned %d bytes, want <= 512", len(results[1].Content))
	}
	truncatedLines := nonEmptyLines(results[1].Content)
	if len(truncatedLines) == 0 || truncatedLines[len(truncatedLines)-1] != "[truncated at 512 bytes]" {
		t.Errorf("truncated repo_search last line = %q, want %q", lastLine(truncatedLines), "[truncated at 512 bytes]")
	}

	if results[2].Error != "" {
		t.Errorf("whole-repo repo_search Error = %q, want empty", results[2].Error)
	}
	for _, forbidden := range []string{".env", ".git", "credentials.yaml", ".docker/config.json", "bin/blob"} {
		if strings.Contains(results[2].Content, forbidden) {
			t.Errorf("whole-repo repo_search content contains denied path %q", forbidden)
		}
	}
	for i, result := range results {
		if strings.Contains(result.Content, root) {
			t.Errorf("call %d content leaks absolute repo root %q", i+1, root)
		}
	}
}

// REQ-03 / S-07
func TestReadFileRanges_ClampAndReject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/ai/client.go", "alpha\nbeta\ngamma\n")
	writeTestFile(t, root, ".env", "SECRET=value\n")
	writeTestFile(t, root, "bin/blob", "\x00binary\n")
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(root, "link.go")); err != nil {
		t.Fatalf("create outside-root symlink: %v", err)
	}

	ex := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second})
	calls := []ai.ToolCall{
		{ID: "c1", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"internal/ai/client.go","start":1,"end":999}`)},
		{ID: "c2", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"/etc/hosts"}`)},
		{ID: "c3", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"../x"}`)},
		{ID: "c4", Name: "read_file_ranges", Args: json.RawMessage(`{"path":".env"}`)},
		{ID: "c5", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"link.go"}`)},
		{ID: "c6", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"bin/blob"}`)},
	}

	results := make([]ai.ToolResult, 0, len(calls))
	for _, call := range calls {
		result := ex.Execute(context.Background(), []ai.ToolCall{call}, 10)
		if len(result) != 1 {
			t.Fatalf("Execute(%s) returned %d results, want 1", call.ID, len(result))
		}
		results = append(results, result[0])
	}

	if results[0].Error != "" {
		t.Errorf("clamped read Error = %q, want empty", results[0].Error)
	}
	want := "internal/ai/client.go:1: alpha\ninternal/ai/client.go:2: beta\ninternal/ai/client.go:3: gamma"
	if results[0].Content != want {
		t.Errorf("clamped read Content = %q, want %q", results[0].Content, want)
	}
	for i, result := range results[1:] {
		if result.Error != "path-rejected" {
			t.Errorf("rejected read call %d Error = %q, want %q", i+2, result.Error, "path-rejected")
		}
		for _, forbidden := range []string{"/etc", "..", root} {
			if strings.Contains(result.Content, forbidden) {
				t.Errorf("rejected read call %d content contains %q", i+2, forbidden)
			}
		}
	}
}

// REQ-03 / S-08
func TestTools_TimeoutInvalidBudget(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "small.txt", "x\n")

	timeoutExecutor := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: time.Nanosecond})
	timeout := oneResult(t, timeoutExecutor, ai.ToolCall{ID: "c1", Name: "repo_search", Args: json.RawMessage(`{"query":"x"}`)})
	if timeout.Error != "timeout" {
		t.Errorf("timed-out call Error = %q, want %q", timeout.Error, "timeout")
	}
	if timeout.Content != "" {
		t.Errorf("timed-out call Content = %q, want empty", timeout.Content)
	}

	ex := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second})
	invalidCalls := []struct {
		call       ai.ToolCall
		wantDetail string
	}{
		{ai.ToolCall{ID: "c2", Name: "unknown_tool", Args: json.RawMessage(`{}`)}, "unknown tool"},
		{ai.ToolCall{ID: "c3", Name: "repo_search", Args: json.RawMessage(`{}`)}, "query"},
		{ai.ToolCall{ID: "c4", Name: "repo_search", Args: json.RawMessage(`{"query":"x","extra":1}`)}, "extra"},
		{ai.ToolCall{ID: "c5", Name: "repo_search", Args: oversizedSearchArgs()}, "too large"},
		{ai.ToolCall{ID: "c6", Name: "read_file_ranges", Args: json.RawMessage(`{"path":"a","start":5,"end":2}`)}, "range"},
	}
	for _, tc := range invalidCalls {
		result := oneResult(t, ex, tc.call)
		if result.Error != "tool-error" {
			t.Errorf("invalid call %s Error = %q, want %q", tc.call.ID, result.Error, "tool-error")
		}
		if !strings.Contains(result.Content, tc.wantDetail) {
			t.Errorf("invalid call %s Content = %q, want substring %q", tc.call.ID, result.Content, tc.wantDetail)
		}
	}

	budgetExecutor := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second}, WithTotalBudget(2))
	for i := 1; i <= 3; i++ {
		result := oneResult(t, budgetExecutor, ai.ToolCall{ID: "budget", Name: "repo_search", Args: json.RawMessage(`{"query":"x"}`)})
		if i < 3 && result.Error != "" {
			t.Errorf("budget call %d Error = %q, want empty", i, result.Error)
		}
		if i == 3 && result.Error != "call-limit" {
			t.Errorf("budget call 3 Error = %q, want %q", result.Error, "call-limit")
		}
	}
	if budgetExecutor.Executed() != 2 {
		t.Errorf("Executed() = %d, want 2", budgetExecutor.Executed())
	}
}

func writeTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory for %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func newTestExecutor(t *testing.T, root string, lim Limits, opts ...ExecutorOption) *Executor {
	t.Helper()
	ex, err := New(root, lim, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ex
}

func oneResult(t *testing.T, ex *Executor, call ai.ToolCall) ai.ToolResult {
	t.Helper()
	results := ex.Execute(context.Background(), []ai.ToolCall{call}, 10)
	if len(results) != 1 {
		t.Fatalf("Execute(%s) returned %d results, want 1", call.ID, len(results))
	}
	return results[0]
}

func nonEmptyLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func lastLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func oversizedSearchArgs() json.RawMessage {
	return json.RawMessage(`{"query":"` + strings.Repeat("a", ai.MaxArgsBytes) + `"}`)
}
