package ragcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

// TestDispatch_BareArgsEntersReview verifies REQ-05 / S-18: bare mrinspect
// selects the existing review path rather than a subcommand branch.
func TestDispatch_BareArgsEntersReview(t *testing.T) {
	path, rest := dispatchForTest(t, nil)
	if path != PathReview {
		t.Errorf("Dispatch(nil) path = %v, want review path", path)
	}
	if len(rest) != 0 {
		t.Errorf("Dispatch(nil) rest = %q, want no arguments", rest)
	}
}

// TestIndex_ExitCodesAreDistinct verifies REQ-05 / S-20: each named index
// outcome has a distinct returned exit code and explanatory message.
func TestIndex_ExitCodesAreDistinct(t *testing.T) {
	validSet := fixtureSet(t, "guide.md", "# Guide\n\nindex this resource\n")
	failedSet := fixtureSet(t, "broken.yaml", "paths: [not valid yaml\n")

	tests := []struct {
		name    string
		opts    Options
		want    int
		message string
	}{
		{
			name:    "normal index",
			opts:    Options{OutputPath: filepath.Join(t.TempDir(), "store.sqlite"), Loader: staticLoader{sets: []resources.Set{validSet}}, Indexer: sqliteTestIndexer{}},
			want:    0,
			message: "indexed successfully",
		},
		{
			name:    "no resource sets resolved",
			opts:    Options{OutputPath: filepath.Join(t.TempDir(), "store.sqlite"), Loader: staticLoader{}, Indexer: sqliteTestIndexer{}},
			want:    2,
			message: "no resource sets resolved",
		},
		{
			name:    "backend does not support indexing",
			opts:    Options{OutputPath: filepath.Join(t.TempDir(), "store.sqlite"), Loader: staticLoader{sets: []resources.Set{validSet}}, Indexer: unsupportedIndexer{}},
			want:    5,
			message: "backend does not support indexing",
		},
		{
			name:    "some files failed",
			opts:    Options{OutputPath: filepath.Join(t.TempDir(), "store.sqlite"), Loader: staticLoader{sets: []resources.Set{failedSet}}, Indexer: sqliteTestIndexer{}},
			want:    3,
			message: "some files failed",
		},
	}

	seen := make(map[int]string, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode, stats, err := runIndexForTest(t, tt.opts)
			if err != nil {
				t.Errorf("RunIndex error = %v, want outcome %q", err, tt.message)
			}
			if exitCode != tt.want {
				t.Errorf("RunIndex exit code = %d, want %d for %s", exitCode, tt.want, tt.message)
			}
			if !strings.Contains(stats.Message, tt.message) {
				t.Errorf("RunIndex message = %q, want it to name %q", stats.Message, tt.message)
			}
			if previous, duplicate := seen[exitCode]; duplicate {
				t.Errorf("exit code %d duplicates %q; S-20 requires distinct causes", exitCode, previous)
			}
			seen[exitCode] = tt.message
		})
	}
}

// TestIndex_DryRunWritesNothing verifies REQ-05 / S-21: dry-run reports
// successful statistics but must not create the requested output store.
func TestIndex_DryRunWritesNothing(t *testing.T) {
	output := filepath.Join(t.TempDir(), "store.sqlite")
	set := fixtureSet(t, "guide.md", "# Guide\n\ndry run resource\n")
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("dry-run output %q stat error = %v, want absent output", output, err)
	}
	var printed bytes.Buffer

	exitCode, stats, err := runIndexForTest(t, Options{
		OutputPath: output,
		DryRun:     true,
		Output:     &printed,
		Loader:     staticLoader{sets: []resources.Set{set}},
		Indexer:    sqliteTestIndexer{},
	})
	if err != nil {
		t.Errorf("RunIndex --dry-run error = %v, want dry-run statistics", err)
	}
	if exitCode != 0 {
		t.Errorf("RunIndex --dry-run exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stats.Message, "dry-run") {
		t.Errorf("RunIndex --dry-run message = %q, want dry-run statistics", stats.Message)
	}
	if !strings.Contains(printed.String(), "dry-run statistics") {
		t.Errorf("RunIndex --dry-run output = %q, want printed dry-run statistics", printed.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Errorf("RunIndex --dry-run created output %q; stat error = %v, want not exist", output, err)
	}
}

type staticLoader struct {
	sets []resources.Set
	err  error
}

func (l staticLoader) Load(context.Context) ([]resources.Set, error) { return l.sets, l.err }

type sqliteTestIndexer struct{}

func (sqliteTestIndexer) SupportsIndexing() bool { return true }

func (sqliteTestIndexer) Index(ctx context.Context, output string, sets []resources.Set) (IndexStats, error) {
	stats, err := sqlite.Index(ctx, sqlite.IndexOptions{OutputPath: output, Sets: sets})
	return IndexStats{FilesFailed: len(stats.Failures)}, err
}

type unsupportedIndexer struct{}

func (unsupportedIndexer) SupportsIndexing() bool { return false }

func (unsupportedIndexer) Index(context.Context, string, []resources.Set) (IndexStats, error) {
	return IndexStats{}, fmt.Errorf("indexing is unsupported")
}

func fixtureSet(t *testing.T, file, content string) resources.Set {
	t.Helper()
	docs := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("MkdirAll docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, file), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}
	return resources.Set{Name: "test-set", Mode: resources.ModeRetrieval, Paths: []string{docs}}
}

// The RED stubs intentionally panic. Recovering converts that temporary state
// into ordinary assertion failures so every S-20 case is reported independently.
func dispatchForTest(t *testing.T, args []string) (path Path, rest []string) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("Dispatch is still RED: %v", recovered)
		}
	}()
	return Dispatch(args)
}

func runIndexForTest(t *testing.T, opts Options) (exitCode int, stats IndexStats, err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			exitCode = -1
			err = fmt.Errorf("RunIndex is still RED: %v", recovered)
		}
	}()
	return RunIndex(context.Background(), opts)
}

// TestIndex_ReportsRealFileCount verifies REQ-03 / T28: a real index reports
// the number of indexed files both to callers and in printed statistics.
func TestIndex_ReportsRealFileCount(t *testing.T) {
	set := fixtureSet(t, "guide.md", "# Guide\n\nindex this resource\n")
	if err := os.WriteFile(filepath.Join(set.Paths[0], "reference.md"), []byte("# Reference\n\nindex this too\n"), 0o644); err != nil {
		t.Fatalf("WriteFile reference.md: %v", err)
	}
	var printed bytes.Buffer

	exitCode, stats, err := runIndexForTest(t, Options{
		OutputPath: filepath.Join(t.TempDir(), "store.sqlite"),
		Output:     &printed,
		Loader:     staticLoader{sets: []resources.Set{set}},
		Indexer:    sqliteIndexer{},
	})
	if err != nil {
		t.Fatalf("RunIndex: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("RunIndex exit code = %d, want 0", exitCode)
	}
	if stats.FilesIndexed != 2 {
		t.Errorf("IndexStats.FilesIndexed = %d, want 2", stats.FilesIndexed)
	}
	if !strings.Contains(printed.String(), "files indexed=2") {
		t.Errorf("printed statistics = %q, want files indexed=2", printed.String())
	}
}
