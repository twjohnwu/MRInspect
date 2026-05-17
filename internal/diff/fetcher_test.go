package diff

import (
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
)

func TestConvertChangesToDiff(t *testing.T) {
	tests := []struct {
		name     string
		changes  []gitlab.Change
		empty    bool
		contains []string
	}{
		{
			name: "new file",
			changes: []gitlab.Change{
				{NewPath: "cmd/main.go", NewFile: true, Diff: "@@ -0,0 +1 @@\n+package main\n"},
			},
			contains: []string{"--- /dev/null", "+++ b/cmd/main.go"},
		},
		{
			name: "deleted file",
			changes: []gitlab.Change{
				{OldPath: "cmd/old.go", NewPath: "cmd/old.go", DeletedFile: true, Diff: "@@ -1 +0,0 @@\n-package main\n"},
			},
			contains: []string{"--- a/cmd/old.go", "+++ /dev/null"},
		},
		{
			name: "renamed file",
			changes: []gitlab.Change{
				{OldPath: "cmd/old.go", NewPath: "cmd/new.go", RenamedFile: true},
			},
			contains: []string{"--- a/cmd/old.go", "+++ b/cmd/new.go"},
		},
		{
			name: "modified file",
			changes: []gitlab.Change{
				{OldPath: "pkg/foo.go", NewPath: "pkg/foo.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"},
			},
			contains: []string{"--- a/pkg/foo.go", "+++ b/pkg/foo.go"},
		},
		{
			name:    "empty changes slice",
			changes: []gitlab.Change{},
			empty:   true,
		},
		{
			name: "diff without trailing newline gets one appended",
			changes: []gitlab.Change{
				{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n+fix"},
			},
			contains: []string{"+fix\n"},
		},
		{
			name: "multiple files are all included",
			changes: []gitlab.Change{
				{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n+a\n"},
				{OldPath: "b.go", NewPath: "b.go", Diff: "@@ -1 +1 @@\n+b\n"},
			},
			contains: []string{"a.go", "b.go", "+a", "+b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertChangesToDiff(tt.changes)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.empty {
				if got != "" {
					t.Fatalf("expected empty string, got %q", got)
				}
				return
			}
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q\ngot:\n%s", s, got)
				}
			}
		})
	}
}
