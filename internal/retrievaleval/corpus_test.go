package retrievaleval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/intake"
	"mrinspect/internal/rag/resources"
)

// TestCorpus_MeetsSizeAndUniqueBreadcrumbs verifies REQ-01 / S-01: the two
// production resource sets yield at least 200 unambiguous, file-unique chunks.
func TestCorpus_MeetsSizeAndUniqueBreadcrumbs(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	registry, err := resources.Load(root, "margherita-pizza")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}

	wanted := map[string]bool{
		"margherita-pizza-docs": false,
		"shared-standards":      false,
	}
	totalChunks := 0
	for _, set := range registry.Sets {
		if _, ok := wanted[set.Name]; !ok {
			continue
		}
		wanted[set.Name] = true

		walked, err := intake.Walk(intake.WalkOptions{
			Paths:   set.Paths,
			Include: set.Include,
			Exclude: set.Exclude,
		})
		if err != nil {
			t.Fatalf("walk resource set %q: %v", set.Name, err)
		}

		for _, path := range walked.Files {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %q: %v", path, err)
			}

			for lineNumber, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "#") && strings.Contains(line, " > ") {
					t.Errorf("%s:%d: heading text contains breadcrumb separator %q", path, lineNumber+1, " > ")
				}
			}

			chunks, err := chunk.Markdown(string(content))
			if err != nil {
				t.Fatalf("chunk.Markdown(%q): %v", path, err)
			}
			totalChunks += len(chunks)

			seen := make(map[string]struct{}, len(chunks))
			for _, item := range chunks {
				segments := strings.Split(item.Heading, " > ")
				if joined := strings.Join(segments, " > "); joined != item.Heading {
					t.Errorf("%s: ambiguous breadcrumb %q", path, item.Heading)
				}
				if _, duplicate := seen[item.Heading]; duplicate {
					t.Errorf("%s: duplicate breadcrumb %q", path, item.Heading)
				}
				seen[item.Heading] = struct{}{}
			}
		}
	}

	for name, found := range wanted {
		if !found {
			t.Errorf("resource set %q is missing", name)
		}
	}
	if totalChunks < 200 {
		t.Errorf("corpus produced %d chunks, want at least 200", totalChunks)
	}
}

// TestCorpus_GoldenCoversAllFixtures verifies REQ-02: the checked-in golden
// file covers every valid fixture and satisfies the per-set minimums.
func TestCorpus_GoldenCoversAllFixtures(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	fixtures, err := evalrun.LoadFixtures(filepath.Join(root, "eval", "fixtures"), nil)
	if err != nil {
		t.Fatalf("evalrun.LoadFixtures: %v", err)
	}

	names := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		names = append(names, fixture.Name)
	}
	if _, err := LoadGolden(filepath.Join(root, "eval", "retrieval-golden.yaml"), names); err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}
}
