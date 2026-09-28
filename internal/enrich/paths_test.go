package enrich

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/ai"
)

func TestResolve_RejectsSymlinkPathsAndTargets(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".git/config", "TOKEN=abc123\n")
	writeTestFile(t, root, ".env", "SECRET=value\n")
	writeTestFile(t, root, "internal/ai/client.go", "package ai\n")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("create docs directory: %v", err)
	}
	for link, target := range map[string]string{
		"docs/ctx":  "../.git",
		"notes.txt": ".env",
		"inner.go":  "internal/ai/client.go",
	} {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Fatalf("create symlink %s: %v", link, err)
		}
	}

	ex := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second})
	for _, path := range []string{"docs/ctx/config", "notes.txt", "inner.go"} {
		result := oneResult(t, ex, ai.ToolCall{
			ID:   path,
			Name: "read_file_ranges",
			Args: json.RawMessage(`{"path":` + quoteJSON(path) + `}`),
		})
		if result.Error != "path-rejected" {
			t.Errorf("read_file_ranges(%q) Error = %q, want %q", path, result.Error, "path-rejected")
		}
	}

	search := oneResult(t, ex, ai.ToolCall{ID: "search", Name: "repo_search", Args: json.RawMessage(`{"query":"TOKEN"}`)})
	if search.Error != "" {
		t.Errorf("repo_search Error = %q, want empty", search.Error)
	}
	if strings.Contains(search.Content, "TOKEN") {
		t.Errorf("repo_search Content = %q, want no symlinked .git match", search.Content)
	}
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
