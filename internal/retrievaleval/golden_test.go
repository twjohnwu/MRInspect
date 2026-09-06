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

	"mrinspect/internal/evalrun"
	"mrinspect/internal/gitlab"
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

func TestGolden_RejectsDuplicateFixtureLane(t *testing.T) {
	golden := Golden{Entries: []Entry{
		{Fixture: goldenFixture, Lane: "spec-conformance", Relevant: minimumTargets()},
		{Fixture: goldenFixture, Lane: "spec-conformance"},
		{Fixture: goldenFixture, Lane: "standards"},
	}}
	path := writeGoldenFile(t, golden)

	_, err := LoadGolden(path, []string{goldenFixture})
	if err == nil {
		t.Fatal("LoadGolden error = nil, want duplicate fixture/lane error")
	}
	for _, want := range []string{"duplicate entry for fixture", goldenFixture, "spec-conformance"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadGolden error = %q, want it to contain %q", err, want)
		}
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

func TestGolden_RequiresEveryTierPerTriple(t *testing.T) {
	repoRoot := t.TempDir()
	projectsDir := filepath.Join(repoRoot, "projects")
	for _, path := range []string{
		projectsDir,
		filepath.Join(repoRoot, "docs", "pizza"),
		filepath.Join(repoRoot, "docs", "standards"),
		filepath.Join(repoRoot, "docs", "extra"),
		filepath.Join(repoRoot, "docs", "chicken"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", path, err)
		}
	}

	lanesYAML := []byte(`lanes:
  - id: spec-conformance
    enabled: true
    template: spec-conformance.tmpl.md
    intent: check the feature specification
    resources: {sets: [margherita-pizza-docs], tags: []}
  - id: standards
    enabled: true
    template: standards.tmpl.md
    intent: check shared standards
    resources: {sets: [shared-standards, shared-extra], tags: []}
`)
	if err := os.WriteFile(filepath.Join(projectsDir, "lanes.yaml"), lanesYAML, 0o644); err != nil {
		t.Fatalf("WriteFile lanes.yaml: %v", err)
	}
	resourcesYAML := []byte(`sets:
  - name: margherita-pizza-docs
    mode: retrieval
    paths: [docs/pizza]
  - name: shared-standards
    mode: retrieval
    paths: [docs/standards]
  - name: shared-extra
    mode: retrieval
    paths: [docs/extra]
  - name: fried-chicken-docs
    mode: retrieval
    paths: [docs/chicken]
`)
	if err := os.WriteFile(filepath.Join(projectsDir, "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("WriteFile resources.yaml: %v", err)
	}

	fixtures := []evalrun.Fixture{{
		Name: goldenFixture,
		Changes: []gitlab.Change{{
			NewPath: "internal/pizza/service.go",
			Diff:    "@@ -1 +1 @@\n-old\n+new\n",
		}},
	}}
	plan, err := BuildPlan(repoRoot, "", fixtures)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Triples) != 3 {
		t.Fatalf("len(BuildPlan().Triples) = %d, want 3; triples = %+v", len(plan.Triples), plan.Triples)
	}

	newGolden := func() Golden {
		return Golden{Entries: []Entry{
			{
				Fixture: goldenFixture,
				Lane:    "spec-conformance",
				Relevant: []Target{
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > A"},
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > B"},
				},
				Paraphrase: []Target{
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > Paraphrase"},
				},
				Distractors: []Target{
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > Distractor"},
				},
			},
			{
				Fixture: goldenFixture,
				Lane:    "standards",
				Relevant: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Guide > A"},
					{Set: "shared-extra", Path: "guide.md", Heading: "Guide > A"},
				},
				Paraphrase: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Guide > Paraphrase"},
					{Set: "shared-extra", Path: "guide.md", Heading: "Guide > Paraphrase"},
				},
				Distractors: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Guide > Distractor"},
					{Set: "shared-extra", Path: "guide.md", Heading: "Guide > Distractor"},
				},
			},
		}}
	}
	loadAndValidate := func(t *testing.T, golden Golden) error {
		t.Helper()
		loaded, err := LoadGolden(writeGoldenFile(t, golden), []string{goldenFixture})
		if err != nil {
			return err
		}
		return loaded.ValidateAgainstPlan(plan.Triples)
	}

	tests := []struct {
		name   string
		mutate func(*Golden)
		want   []string
	}{
		{
			name: "empty paraphrase",
			mutate: func(golden *Golden) {
				golden.Entries[0].Paraphrase = nil
			},
			want: []string{"load golden:", goldenFixture, "spec-conformance", "paraphrase"},
		},
		{
			name: "distractor belongs to another lane",
			mutate: func(golden *Golden) {
				golden.Entries[0].Distractors = []Target{{
					Set: "fried-chicken-docs", Path: "guide.md", Heading: "Guide > Distractor",
				}}
			},
			want: []string{"load golden:", goldenFixture, "spec-conformance", "fried-chicken-docs"},
		},
		{
			name: "relevant misses one standards set",
			mutate: func(golden *Golden) {
				golden.Entries[1].Relevant = []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Guide > A"},
				}
			},
			want: []string{"load golden:", goldenFixture, "standards", "relevant", "shared-extra"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			golden := newGolden()
			test.mutate(&golden)
			err := loadAndValidate(t, golden)
			if err == nil {
				t.Fatal("LoadGolden + ValidateAgainstPlan error = nil, want validation error")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("LoadGolden + ValidateAgainstPlan error = %q, want it to contain %q", err, want)
				}
			}
		})
	}

	t.Run("valid golden", func(t *testing.T) {
		if err := loadAndValidate(t, newGolden()); err != nil {
			t.Fatalf("LoadGolden + ValidateAgainstPlan valid golden: %v", err)
		}
	})
}

func TestGolden_RejectsDuplicatesOverlapAndMissingTargets(t *testing.T) {
	validEntry := func() Entry {
		return Entry{
			Fixture: goldenFixture,
			Lane:    "spec-conformance",
			Relevant: []Target{
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > A"},
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > B"},
			},
			Paraphrase: []Target{
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > Paraphrase"},
			},
			Distractors: []Target{
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Guide > Distractor"},
			},
		}
	}
	standardsEntry := Entry{
		Fixture: goldenFixture,
		Lane:    "standards",
		Relevant: []Target{
			{Set: "shared-standards", Path: "guide.md", Heading: "Guide > A"},
		},
		Paraphrase: []Target{
			{Set: "shared-standards", Path: "guide.md", Heading: "Guide > B"},
		},
		Distractors: []Target{
			{Set: "shared-standards", Path: "guide.md", Heading: "Guide > Distractor"},
		},
	}
	triples := []Triple{
		{Fixture: goldenFixture, LaneID: "spec-conformance", Set: resources.Set{Name: "margherita-pizza-docs"}},
		{Fixture: goldenFixture, LaneID: "standards", Set: resources.Set{Name: "shared-standards"}},
	}
	loadAndValidate := func(t *testing.T, golden Golden) error {
		t.Helper()
		loaded, err := LoadGolden(writeGoldenFile(t, golden), []string{goldenFixture})
		if err != nil {
			return err
		}
		return loaded.ValidateAgainstPlan(triples)
	}

	t.Run("duplicate within distractors", func(t *testing.T) {
		entry := validEntry()
		duplicate := entry.Distractors[0]
		entry.Distractors = append(entry.Distractors, duplicate)
		err := loadAndValidate(t, Golden{Entries: []Entry{entry, standardsEntry}})
		if err == nil {
			t.Fatal("LoadGolden + ValidateAgainstPlan error = nil, want duplicate-target error")
		}
		for _, want := range []string{"load golden:", targetReference(duplicate)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("LoadGolden + ValidateAgainstPlan error = %q, want it to contain %q", err, want)
			}
		}
	})

	t.Run("overlap between relevant and distractors", func(t *testing.T) {
		entry := validEntry()
		overlap := entry.Relevant[0]
		entry.Distractors = []Target{overlap}
		err := loadAndValidate(t, Golden{Entries: []Entry{entry, standardsEntry}})
		if err == nil {
			t.Fatal("LoadGolden + ValidateAgainstPlan error = nil, want overlapping-target error")
		}
		for _, want := range []string{"load golden:", targetReference(overlap)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("LoadGolden + ValidateAgainstPlan error = %q, want it to contain %q", err, want)
			}
		}
	})

	t.Run("missing paraphrase target", func(t *testing.T) {
		missing := Target{
			Set: "margherita-pizza-docs", Path: "missing.md", Heading: "Missing paraphrase",
		}
		golden := Golden{Entries: []Entry{{
			Fixture:    goldenFixture,
			Lane:       "spec-conformance",
			Paraphrase: []Target{missing},
		}}}
		err := golden.ValidateAgainstStore(context.Background(), indexGoldenStore(t))
		if err == nil {
			t.Fatal("ValidateAgainstStore error = nil, want missing-target error")
		}
		ref := targetReference(missing)
		if listed := listedTargetLines(err.Error(), []string{ref}); !reflect.DeepEqual(listed, []string{ref}) {
			t.Errorf("listed target lines = %q, want %q; error = %q", listed, []string{ref}, err)
		}
	})
}
