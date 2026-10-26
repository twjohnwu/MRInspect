package diff

import (
	"testing"

	"mrinspect/internal/gitlab"
)

// TestHasFileHunk pins HasFileHunk: only a section with an "@@" hunk counts,
// header-only sections (empty diff, pure rename, binary) do not.
// Known pre-existing limitation, deliberately not covered: a content line
// that looks like a header ("++ b/<path>" after a hunk) is parsed as a
// section start by the section scanner.
func TestHasFileHunk(t *testing.T) {
	converted, err := ConvertChangesToDiff([]gitlab.Change{
		{OldPath: "n.go", NewPath: "n.go", NewFile: true, Diff: "@@ -0,0 +1 @@\n+a\n"},
		{OldPath: "d.go", NewPath: "d.go", DeletedFile: true, Diff: "@@ -1 +0,0 @@\n-a\n"},
		{OldPath: "m.go", NewPath: "m.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
		{OldPath: "o.go", NewPath: "r.go", RenamedFile: true},
		{OldPath: "e.go", NewPath: "e.go"},
		{OldPath: "sp ace.go", NewPath: "sp ace.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
		{OldPath: "go.go", NewPath: "go.go"},
		{OldPath: "xgo.go", NewPath: "xgo.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
	})
	if err != nil {
		t.Fatalf("ConvertChangesToDiff: %v", err)
	}
	local := "diff --git a/add.go b/add.go\nnew file mode 100644\n--- /dev/null\n+++ b/add.go\n@@ -0,0 +1 @@\n+a\n" +
		"diff --git a/del.go b/del.go\ndeleted file mode 100644\n--- a/del.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-a\n" +
		"diff --git a/ren old.go b/ren new.go\nsimilarity index 100%\nrename from ren old.go\nrename to ren new.go\n" +
		"diff --git a/img.png b/img.png\nindex 1..2 100644\nBinary files a/img.png and b/img.png differ\n" +
		"diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"

	tests := []struct {
		name, text, path string
		want             bool
	}{
		{"converted new file", converted, "n.go", true},
		{"converted deleted file", converted, "d.go", true},
		{"converted modified file", converted, "m.go", true},
		{"converted rename, empty diff, new path", converted, "r.go", false},
		{"converted rename, empty diff, old path", converted, "o.go", false},
		{"converted empty diff", converted, "e.go", false},
		{"converted path with spaces", converted, "sp ace.go", true},
		{"converted suffix collision: go.go has no hunk", converted, "go.go", false},
		{"converted suffix collision: xgo.go has a hunk", converted, "xgo.go", true},
		{"local add (--- /dev/null)", local, "add.go", true},
		{"local delete (+++ /dev/null)", local, "del.go", true},
		{"local rename header only, path with spaces", local, "ren new.go", false},
		{"local binary, no hunk", local, "img.png", false},
		{"local modified", local, "x.go", true},
		{"local suffix of a path is not the path", local, ".go", false},
		{"absent path", local, "nope.go", false},
		{"empty path", local, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasFileHunk(tt.text, tt.path); got != tt.want {
				t.Errorf("HasFileHunk(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
