package hunk

import (
	"reflect"
	"testing"

	"mrinspect/internal/gitlab"
)

func TestHunkMultiHunkFile(t *testing.T) {
	diff := "@@ -1,2 +4,3 @@ first\n context\n@@ -10,2 +20,2 @@ second\n"
	want := []Range{{Start: 4, End: 6}, {Start: 20, End: 21}}
	if got := Parse(diff); !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}

	lookup := Build([]gitlab.Change{{NewPath: "b/src/main.go", Diff: diff}})
	for _, line := range []int{5, 20} {
		if !lookup.Contains("./src/main.go", line) {
			t.Errorf("Contains(src/main.go, %d) = false, want true", line)
		}
	}
	if lookup.Contains("src/main.go", 10) {
		t.Error("Contains(src/main.go, 10) = true, want false between hunks")
	}
}

func TestHunkOmittedCountMeansOne(t *testing.T) {
	lookup := Build([]gitlab.Change{{NewPath: "one.go", Diff: "@@ -7 +12 @@ replacement\n"}})
	if !lookup.Contains("one.go", 12) {
		t.Error("Contains(one.go, 12) = false, want true")
	}
	if lookup.Contains("one.go", 13) {
		t.Error("Contains(one.go, 13) = true, want false")
	}
}

func TestHunkNewFile(t *testing.T) {
	lookup := Build([]gitlab.Change{{
		NewPath: "new.go", NewFile: true, Diff: "@@ -0,0 +1,4 @@\n",
	}})
	if !lookup.Contains("new.go", 4) || lookup.Contains("new.go", 5) {
		t.Error("new-file range should contain line 4 only up to its declared end")
	}
}

func TestHunkDeletedFileHasNoNewSideRanges(t *testing.T) {
	if got := Parse("@@ -1,3 +0,0 @@\n"); len(got) != 0 {
		t.Fatalf("pure-deletion Parse() = %#v, want no ranges", got)
	}

	lookup := Build([]gitlab.Change{{
		OldPath: "gone.go", NewPath: "gone.go", DeletedFile: true,
		Diff: "@@ -1,3 +1,2 @@\n",
	}})
	for _, line := range []int{0, 1, 3} {
		if lookup.Contains("gone.go", line) {
			t.Errorf("Contains(gone.go, %d) = true for deleted file", line)
		}
	}
}

func TestHunkRenamedFileUsesNewPath(t *testing.T) {
	lookup := Build([]gitlab.Change{{
		OldPath: "old/name.go", NewPath: "b/new/name.go", RenamedFile: true,
		Diff: "@@ -3 +8,2 @@\n",
	}})
	if !lookup.Contains("new/name.go", 8) {
		t.Error("renamed file is not indexed under new path")
	}
	if lookup.Contains("old/name.go", 8) {
		t.Error("renamed file is unexpectedly indexed under old path")
	}
}

func TestHunkEmptyDiffHasNoRanges(t *testing.T) {
	for _, diff := range []string{"diff --git a/a.go b/a.go\n", ""} {
		if got := Parse(diff); len(got) != 0 {
			t.Errorf("Parse(%q) = %#v, want no ranges", diff, got)
		}
	}

	lookup := Build([]gitlab.Change{{NewPath: "empty.go", Diff: ""}})
	if lookup.Contains("empty.go", 1) {
		t.Error("Contains(empty.go, 1) = true for empty diff")
	}
}

func TestHunkMalformedHeadersAreSkipped(t *testing.T) {
	diff := "@@ garbage @@\n@@ -1,2 +30,2 @@ valid\n@@ -oops +40 @@\n"
	want := []Range{{Start: 30, End: 31}}
	if got := Parse(diff); !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}
