package evalrun_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/logger"
)

func TestSynthesizeChanges_AddedDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "01-added-deleted.diff", []byte(
		"diff --git a/new.go b/new.go\n"+
			"new file mode 100644\n"+
			"--- /dev/null\n"+
			"+++ b/new.go\n"+
			"@@ -0,0 +1 @@\n"+
			"+package new\n"+
			"diff --git a/old.go b/old.go\n"+
			"deleted file mode 100644\n"+
			"--- a/old.go\n"+
			"+++ /dev/null\n"+
			"@@ -1 +0,0 @@\n"+
			"-package old\n"+
			"diff --git a/before.go b/after.go\n"+
			"similarity index 100%\n"+
			"rename from before.go\n"+
			"rename to after.go\n"+
			"--- a/before.go\n"+
			"+++ b/after.go\n"+
			"@@ -1 +1 @@\n"+
			"-package before\n"+
			"+package after\n"))

	fixtures, err := evalrun.LoadFixtures(dir, logger.NewWithWriter(slog.LevelWarn, "", io.Discard))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("fixture count = %d, want 1", len(fixtures))
	}
	changes := fixtures[0].Changes
	if len(changes) != 3 {
		t.Fatalf("change count = %d, want 3: %+v", len(changes), changes)
	}
	if got := changes[0]; got.OldPath != "" || got.NewPath != "new.go" || !got.NewFile || got.DeletedFile || got.RenamedFile {
		t.Errorf("added change = %+v, want empty old path, new.go, and only NewFile", got)
	}
	if got := changes[1]; got.OldPath != "old.go" || got.NewPath != "" || got.NewFile || !got.DeletedFile || got.RenamedFile {
		t.Errorf("deleted change = %+v, want old.go, empty new path, and only DeletedFile", got)
	}
	if got := changes[2]; got.OldPath != "before.go" || got.NewPath != "after.go" || got.NewFile || got.DeletedFile || !got.RenamedFile {
		t.Errorf("renamed change = %+v, want before.go -> after.go and only RenamedFile", got)
	}
}

func TestSynthesizeChanges_TrailingSection(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "01-trailing.diff", []byte(
		"diff --git a/kept.go b/kept.go\n"+
			"--- a/kept.go\n"+
			"+++ b/kept.go\n"+
			"@@ -1 +1 @@\n"+
			"-old\n"+
			"+new\n"+
			"diff --git a/binary.dat b/binary.dat\n"+
			"new file mode 100644\n"+
			"Binary files /dev/null and b/binary.dat differ\n"))

	fixtures, err := evalrun.LoadFixtures(dir, logger.NewWithWriter(slog.LevelWarn, "", io.Discard))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	changes := fixtures[0].Changes
	if len(changes) != 1 {
		t.Fatalf("change count = %d, want 1", len(changes))
	}
	if strings.Contains(changes[0].Diff, "binary.dat") {
		t.Errorf("first change absorbed trailing unmatched section: %q", changes[0].Diff)
	}
}

func TestShippedFixturesParse(t *testing.T) {
	fixturesDir, err := filepath.Abs(filepath.Join("..", "..", "eval", "fixtures"))
	if err != nil {
		t.Fatalf("resolve shipped fixtures: %v", err)
	}
	fixtures, err := evalrun.LoadFixtures(fixturesDir, logger.NewWithWriter(slog.LevelWarn, "", io.Discard))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	byName := make(map[string]evalrun.Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byName[fixture.Name] = fixture
	}

	fixture03, ok := byName["03-lane-overlays-config.diff"]
	if !ok {
		t.Fatal("fixture 03 was not loaded")
	}
	wantAdded := map[string]bool{
		"projects/fried-chicken/lanes.yaml":    false,
		"projects/margherita-pizza/lanes.yaml": false,
	}
	for _, change := range fixture03.Changes {
		if _, wanted := wantAdded[change.NewPath]; wanted && change.NewFile && change.OldPath == "" {
			wantAdded[change.NewPath] = true
		}
	}
	for path, found := range wantAdded {
		if !found {
			t.Errorf("fixture 03 missing faithful added-file change for %s", path)
		}
	}

	fixture01, ok := byName["01-echo-cut-earliest-marker.diff"]
	if !ok {
		t.Fatal("fixture 01 was not loaded")
	}
	for _, change := range fixture01.Changes {
		if change.NewPath == "src/review/MRReviewer.ts" && strings.Contains(change.Diff, "tests/reviewer-quoted-marker.test.ts") {
			t.Error("fixture 01 attached tests/reviewer-quoted-marker.test.ts content to MRReviewer.ts")
		}
	}
}
