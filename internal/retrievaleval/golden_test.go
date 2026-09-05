package retrievaleval

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

const goldenFixture = "01-cut-earliest-marker.diff"

func writeGoldenFile(t *testing.T, golden Golden) string {
	t.Helper()

	content, err := yaml.Marshal(golden)
	if err != nil {
		t.Fatalf("yaml.Marshal golden: %v", err)
	}
	path := filepath.Join(t.TempDir(), "retrieval-golden.yaml")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile golden: %v", err)
	}
	return path
}

func minimumTargets() []Target {
	return []Target{
		{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > A"},
		{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > B"},
		{Set: "shared-standards", Path: "guide.md", Heading: "Guide > A"},
	}
}

// TestGolden_RejectsIncompleteCoverage verifies REQ-02 / S-02: every fixture
// has both retrieval lanes and the required per-set target coverage, while an
// empty fixture inventory is rejected before evaluation.
func TestGolden_RejectsIncompleteCoverage(t *testing.T) {
	tests := []struct {
		name     string
		fixtures []string
		golden   Golden
		want     []string
	}{
		{
			name:     "missing standards entry",
			fixtures: []string{goldenFixture},
			golden: Golden{Entries: []Entry{
				{Fixture: goldenFixture, Lane: "spec-conformance", Relevant: minimumTargets()},
			}},
			want: []string{goldenFixture, "standards"},
		},
		{
			name:     "only one pizza target",
			fixtures: []string{goldenFixture},
			golden: Golden{Entries: []Entry{
				{
					Fixture: goldenFixture,
					Lane:    "spec-conformance",
					Relevant: []Target{
						{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > A"},
						{Set: "shared-standards", Path: "guide.md", Heading: "Guide > A"},
					},
				},
				{Fixture: goldenFixture, Lane: "standards"},
			}},
			want: []string{goldenFixture, "margherita-pizza-docs"},
		},
		{
			name:     "empty fixtures",
			fixtures: nil,
			golden: Golden{Entries: []Entry{
				{Fixture: goldenFixture, Lane: "spec-conformance", Relevant: minimumTargets()},
				{Fixture: goldenFixture, Lane: "standards"},
			}},
			want: []string{"no fixtures"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeGoldenFile(t, test.golden)
			_, err := LoadGolden(path, test.fixtures)
			if err == nil {
				t.Fatal("LoadGolden error = nil, want coverage error")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("LoadGolden error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func indexGoldenStore(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	sets := make([]resources.Set, 0, 2)
	for _, name := range []string{"margherita-pizza-docs", "shared-standards"} {
		docs := filepath.Join(root, name)
		if err := os.MkdirAll(docs, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		source := []byte("# Guide\n\n## A\ntext\n\n## B\ntext\n")
		if err := os.WriteFile(filepath.Join(docs, "guide.md"), source, 0o644); err != nil {
			t.Fatalf("WriteFile %s/guide.md: %v", name, err)
		}
		sets = append(sets, resources.Set{
			Name:  name,
			Mode:  resources.ModeRetrieval,
			Paths: []string{docs},
		})
	}

	storePath := filepath.Join(root, "store.sqlite")
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{
		OutputPath: storePath,
		Sets:       sets,
		Embedder:   embed.NewFixture(4),
		Progress:   io.Discard,
	}); err != nil {
		t.Fatalf("sqlite.Index: %v", err)
	}
	return storePath
}

func unknownGoldenTargets() ([]Target, []string) {
	targets := make([]Target, 0, 60)
	refs := make([]string, 0, 60)
	for index := range 60 {
		set := "margherita-pizza-docs"
		if index >= 40 {
			set = "shared-standards"
		}
		target := Target{
			Set:     set,
			Path:    fmt.Sprintf("missing-%02d.md", index),
			Heading: fmt.Sprintf("Missing %02d", index),
		}
		targets = append(targets, target)
		refs = append(refs, fmt.Sprintf("%s/%s#%s", target.Set, target.Path, target.Heading))
	}
	sort.Strings(refs)
	return targets, refs
}

func listedTargetLines(message string, refs []string) []string {
	var listed []string
	for _, line := range strings.Split(message, "\n") {
		for _, ref := range refs {
			if strings.Contains(line, ref) {
				listed = append(listed, ref)
				break
			}
		}
	}
	return listed
}

// TestGolden_RejectsUnknownEntriesBounded verifies REQ-02 / S-03: store
// identities use set, relative path, and breadcrumb; missing identities are
// sorted and bounded to 50 lines, and oversized golden files fail before YAML
// parsing.
func TestGolden_RejectsUnknownEntriesBounded(t *testing.T) {
	storePath := indexGoldenStore(t)

	t.Run("valid targets", func(t *testing.T) {
		golden := Golden{Entries: []Entry{
			{Fixture: goldenFixture, Lane: "spec-conformance", Relevant: minimumTargets()},
			{Fixture: goldenFixture, Lane: "standards"},
		}}
		if err := golden.ValidateAgainstStore(context.Background(), storePath); err != nil {
			t.Fatalf("ValidateAgainstStore valid targets: %v", err)
		}
	})

	t.Run("sixty missing targets", func(t *testing.T) {
		targets, refs := unknownGoldenTargets()
		golden := Golden{Entries: []Entry{
			{Fixture: goldenFixture, Lane: "spec-conformance", Relevant: targets[:30]},
			{Fixture: goldenFixture, Lane: "standards", Relevant: targets[30:]},
		}}

		err := golden.ValidateAgainstStore(context.Background(), storePath)
		if err == nil {
			t.Fatal("ValidateAgainstStore error = nil, want missing-target error")
		}
		if !strings.Contains(err.Error(), "and 10 more") {
			t.Errorf("ValidateAgainstStore error missing truncation count: %q", err)
		}
		listed := listedTargetLines(err.Error(), refs)
		if want := refs[:50]; !reflect.DeepEqual(listed, want) {
			t.Errorf("listed target lines = %q, want sorted first 50 %q", listed, want)
		}
		for _, omitted := range refs[50:] {
			if strings.Contains(err.Error(), omitted) {
				t.Errorf("ValidateAgainstStore error lists omitted target %q", omitted)
			}
		}
	})

	t.Run("two MiB golden", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "retrieval-golden.yaml")
		if err := os.WriteFile(path, bytes.Repeat([]byte{0xff}, 2<<20), 0o644); err != nil {
			t.Fatalf("WriteFile oversized golden: %v", err)
		}
		_, err := LoadGolden(path, []string{goldenFixture})
		if err == nil || !strings.Contains(err.Error(), "golden exceeds 1 MiB") {
			t.Fatalf("LoadGolden oversized error = %v, want it to contain %q", err, "golden exceeds 1 MiB")
		}
	})
}
