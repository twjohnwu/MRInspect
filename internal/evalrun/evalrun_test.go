package evalrun_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/logger"
)

// TestS02_FixtureLoading verifies REQ-02 / S-02 fixture loading, filtering, ordering, and change synthesis.
func TestS02_FixtureLoading(t *testing.T) {
	dir := t.TempDir()
	first := []byte("# mrinspect-fixture: source=abc123 kind=logic\n" +
		"--- a/internal/service/first.go\n" +
		"+++ b/internal/service/first.go\n" +
		"@@ -10,2 +10,2 @@ func calculate() int {\n" +
		"-\treturn subtotal\n" +
		"+\treturn subtotal + tax\n")
	second := []byte("# mrinspect-fixture: source=abc123 kind=logic\n" +
		"--- a/config/review.yaml\n" +
		"+++ b/config/review.yaml\n" +
		"@@ -3,2 +3,2 @@ review:\n" +
		"-  enabled: false\n" +
		"+  enabled: true\n")

	writeFixture(t, dir, "01-first.diff", first)
	writeFixture(t, dir, "02-second.diff", second)
	if err := os.Symlink(filepath.Join(dir, "01-first.diff"), filepath.Join(dir, "03-linked.diff")); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}
	tooLarge := append([]byte("# mrinspect-fixture: source=abc123 kind=logic\n"), bytes.Repeat([]byte("x"), 1<<20)...)
	writeFixture(t, dir, "04-too-large.diff", tooLarge)
	writeFixture(t, dir, "05-empty.diff", nil)

	log, readWarnings := captureWarnings(t)
	fixtures, err := evalrun.LoadFixtures(dir, log)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("fixture count = %d, want 2", len(fixtures))
	}

	want := []struct {
		name    string
		diff    []byte
		oldPath string
		newPath string
	}{
		{name: "01-first.diff", diff: first, oldPath: "internal/service/first.go", newPath: "internal/service/first.go"},
		{name: "02-second.diff", diff: second, oldPath: "config/review.yaml", newPath: "config/review.yaml"},
	}
	for i, expected := range want {
		fixture := fixtures[i]
		if fixture.Name != expected.name {
			t.Errorf("fixture[%d].Name = %q, want %q", i, fixture.Name, expected.name)
		}
		if !bytes.Equal(fixture.Diff, expected.diff) {
			t.Errorf("fixture[%d].Diff differs from original file bytes", i)
		}
		if len(fixture.Changes) != 1 {
			t.Errorf("fixture[%d] change count = %d, want 1", i, len(fixture.Changes))
			continue
		}
		if fixture.Changes[0].OldPath != expected.oldPath || fixture.Changes[0].NewPath != expected.newPath {
			t.Errorf("fixture[%d] paths = (%q, %q), want (%q, %q)", i,
				fixture.Changes[0].OldPath, fixture.Changes[0].NewPath, expected.oldPath, expected.newPath)
		}
	}

	warnings := readWarnings()
	for _, skipped := range []string{"03-linked.diff", "04-too-large.diff", "05-empty.diff"} {
		if !strings.Contains(warnings, skipped) {
			t.Errorf("warnings do not name skipped fixture %q: %s", skipped, warnings)
		}
	}
	if got := strings.Count(warnings, `"level":"WARN"`); got != 3 {
		t.Errorf("warning count = %d, want 3; logs: %s", got, warnings)
	}
}

// TestS05_EmptyFixturesGuard verifies REQ-02 / S-05 rejects zero valid fixtures without replacing an existing report.
func TestS05_EmptyFixturesGuard(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "empty directory", setup: func(*testing.T, string) {}},
		{name: "only invalid fixtures", setup: func(t *testing.T, dir string) {
			writeFixture(t, dir, "01-empty.diff", nil)
			writeFixture(t, dir, "02-too-large.diff", bytes.Repeat([]byte("x"), (1<<20)+1))
			validTarget := filepath.Join(t.TempDir(), "valid.diff")
			writeFixture(t, filepath.Dir(validTarget), filepath.Base(validTarget), []byte(
				"# mrinspect-fixture: source=abc123 kind=logic\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"))
			if err := os.Symlink(validTarget, filepath.Join(dir, "03-linked.diff")); err != nil {
				t.Fatalf("create symlink fixture: %v", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturesDir := t.TempDir()
			tt.setup(t, fixturesDir)
			log := logger.New(slog.LevelWarn, "")

			fixtures, err := evalrun.LoadFixtures(fixturesDir, log)
			if !errors.Is(err, evalrun.ErrNoValidFixtures) {
				t.Errorf("LoadFixtures error = %v, want ErrNoValidFixtures", err)
			}
			if len(fixtures) != 0 {
				t.Errorf("LoadFixtures returned %d fixtures, want 0", len(fixtures))
			}

			reportPath := filepath.Join(t.TempDir(), "REPORT.md")
			oldReport := []byte("# Existing report\n\nkeep this exact content\n")
			if err := os.WriteFile(reportPath, oldReport, 0o644); err != nil {
				t.Fatalf("write existing report: %v", err)
			}
			if err := evalrun.Run(fixturesDir, reportPath, log); !errors.Is(err, evalrun.ErrNoValidFixtures) {
				t.Errorf("Run error = %v, want ErrNoValidFixtures", err)
			}
			after, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("read report after guarded run: %v", err)
			}
			if !bytes.Equal(after, oldReport) {
				t.Errorf("guarded run changed existing report: got %q, want %q", after, oldReport)
			}
		})
	}
}

func writeFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func captureWarnings(t *testing.T) (*logger.Logger, func() string) {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "evalrun-warnings-*.jsonl")
	if err != nil {
		t.Fatalf("create warning sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	stdout := os.Stdout
	os.Stdout = sink
	log := logger.New(slog.LevelWarn, "")
	os.Stdout = stdout

	return log, func() string {
		t.Helper()
		if err := sink.Sync(); err != nil {
			t.Fatalf("sync warning sink: %v", err)
		}
		data, err := os.ReadFile(sink.Name())
		if err != nil {
			t.Fatalf("read warning sink: %v", err)
		}
		return string(data)
	}
}
