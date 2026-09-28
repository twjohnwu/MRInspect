package retrievaleval

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/intake"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
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
// file covers every valid fixture, in every system directory under
// eval/retrieval-fixtures/, and satisfies the per-set minimums.
func TestCorpus_GoldenCoversAllFixtures(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	systems, err := loadSystems(filepath.Join(root, "eval", "retrieval-fixtures"), root, nil)
	if err != nil {
		t.Fatalf("loadSystems: %v", err)
	}

	var names []string
	for _, system := range systems {
		for _, fixture := range system.fixtures {
			names = append(names, system.name+"/"+fixture.Name)
		}
	}
	if _, err := LoadGolden(filepath.Join(root, "eval", "retrieval-golden.yaml"), names); err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}
}

// TestCorpus_TierRankBands verifies REQ-02 / S-03/S-04: the checked-in
// golden targets occupy their required BM25 rank bands in the production
// corpus, across every system directory under eval/retrieval-fixtures/.
func TestCorpus_TierRankBands(t *testing.T) {
	ctx := context.Background()
	root := filepath.Clean(filepath.Join("..", ".."))
	registry, err := resources.Load(root, "margherita-pizza")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	storePath := filepath.Join(t.TempDir(), "retrieval.sqlite")
	if _, err := sqlite.Index(ctx, sqlite.IndexOptions{
		OutputPath: storePath,
		Sets:       registry.Sets,
		Embedder:   embed.NewFixture(),
		Progress:   io.Discard,
	}); err != nil {
		t.Fatalf("sqlite.Index: %v", err)
	}

	systems, err := loadSystems(filepath.Join(root, "eval", "retrieval-fixtures"), root, nil)
	if err != nil {
		t.Fatalf("loadSystems: %v", err)
	}

	var names []string
	var plan []Triple
	for _, system := range systems {
		for _, fixture := range system.fixtures {
			names = append(names, system.name+"/"+fixture.Name)
		}
		builtPlan, err := BuildPlan(root, system.name, system.fixtures)
		if err != nil {
			t.Fatalf("BuildPlan(%q): %v", system.name, err)
		}
		for _, triple := range builtPlan.Triples {
			triple.Fixture = system.name + "/" + triple.Fixture
			plan = append(plan, triple)
		}
	}

	golden, err := LoadGolden(filepath.Join(root, "eval", "retrieval-golden.yaml"), names)
	if err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}

	if err := golden.ValidateAgainstPlan(plan); err != nil {
		t.Fatalf("ValidateAgainstPlan: %v", err)
	}

	sets := distinctPlanSets(plan)
	for _, triple := range plan {
		retriever, err := sqlite.OpenRetriever(
			storePath,
			sets,
			sqlite.WithReadOnly(),
			sqlite.WithEmbeddingConfig(false, false),
		)
		if err != nil {
			t.Fatalf("sqlite.OpenRetriever: %v", err)
		}
		result, retrieveErr := retriever.Retrieve(ctx, rag.Query{
			Terms:  triple.Terms,
			SetRef: triple.Set.Name,
			Intent: triple.LaneID,
			TopK:   4 * triple.K,
		})
		closeErr := retriever.Close()
		if retrieveErr != nil {
			t.Fatalf("Retrieve %s / %s / %s: %v", triple.Fixture, triple.LaneID, triple.Set.Name, retrieveErr)
		}
		if closeErr != nil {
			t.Fatalf("Close retriever: %v", closeErr)
		}
		if len(result.Degraded) != 0 {
			t.Fatalf("Retrieve %s / %s / %s degraded: %v", triple.Fixture, triple.LaneID, triple.Set.Name, result.Degraded)
		}

		rankOf := func(target Target) int {
			for i, hit := range result.Chunks {
				if hit.ResourceSet == target.Set && hit.Source == target.Path && hit.Heading == target.Heading {
					return i + 1
				}
			}
			return 0
		}
		reportViolation := func(target Target, rank int) {
			if rank == 0 {
				t.Errorf("%s / %s / %s/%s#%s / not in top 4k", triple.Fixture, triple.LaneID, target.Set, target.Path, target.Heading)
				return
			}
			t.Errorf("%s / %s / %s/%s#%s / rank=%d", triple.Fixture, triple.LaneID, target.Set, target.Path, target.Heading, rank)
		}

		targets := scoringTargetsFor(golden, triple.Fixture, triple.LaneID, triple.Set.Name)
		for _, target := range targets.paraphrase {
			if rank := rankOf(target); rank == 0 || rank <= triple.K || rank > 4*triple.K {
				reportViolation(target, rank)
			}
		}
		for _, tier := range [][]Target{targets.original, targets.distractors} {
			for _, target := range tier {
				if rank := rankOf(target); rank == 0 || rank > triple.K {
					reportViolation(target, rank)
				}
			}
		}
	}
}

// TestCorpus_GoldenScale verifies REQ-02 / S-03: the retrieval fixture and
// golden-entry counts, per-fixture lane coverage, per-category distractor
// minimums, fixture-copy byte-identity, K uniformity across triples, and
// the per-system resources-overlay freshness invariant required by the
// two-system golden set. Every violation is reported individually so one
// shortfall never masks another.
func TestCorpus_GoldenScale(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))

	wantFixtureCounts := []struct {
		system string
		want   int
	}{
		{"margherita-pizza", 14},
		{"fried-chicken", 10},
	}

	var allFixtureIDs []string
	var plan []Triple
	for _, want := range wantFixtureCounts {
		fixtures, err := evalrun.LoadFixtures(filepath.Join(root, "eval", "retrieval-fixtures", want.system), nil)
		if err != nil && !errors.Is(err, evalrun.ErrNoValidFixtures) {
			t.Fatalf("evalrun.LoadFixtures(%q): %v", want.system, err)
		}
		if len(fixtures) != want.want {
			t.Errorf("system %q has %d fixtures, want %d", want.system, len(fixtures), want.want)
		}
		for _, fixture := range fixtures {
			allFixtureIDs = append(allFixtureIDs, want.system+"/"+fixture.Name)
		}

		builtPlan, err := BuildPlan(root, want.system, fixtures)
		if err != nil {
			t.Fatalf("BuildPlan(%q): %v", want.system, err)
		}
		plan = append(plan, builtPlan.Triples...)
	}

	if len(plan) > 0 {
		wantK := plan[0].K
		for _, triple := range plan {
			if triple.K != wantK {
				t.Errorf("triple %s/%s/%s has K=%d, want %d (all triples must share K)", triple.Fixture, triple.LaneID, triple.Set.Name, triple.K, wantK)
			}
		}
	}

	content, err := os.ReadFile(filepath.Join(root, "eval", "retrieval-golden.yaml"))
	if err != nil {
		t.Fatalf("read retrieval-golden.yaml: %v", err)
	}
	var golden Golden
	if err := yaml.Unmarshal(content, &golden); err != nil {
		t.Fatalf("parse retrieval-golden.yaml: %v", err)
	}

	if len(golden.Entries) != 48 {
		t.Errorf("golden has %d entries, want 48", len(golden.Entries))
	}

	lanesByFixture := make(map[string]map[string]bool, len(golden.Entries))
	for _, entry := range golden.Entries {
		if lanesByFixture[entry.Fixture] == nil {
			lanesByFixture[entry.Fixture] = make(map[string]bool, len(requiredLanes))
		}
		lanesByFixture[entry.Fixture][entry.Lane] = true
	}
	for _, id := range allFixtureIDs {
		for _, lane := range requiredLanes {
			if !lanesByFixture[id][lane] {
				t.Errorf("golden missing entry for fixture %q lane %q", id, lane)
			}
		}
	}

	categories := make([]string, 0, len(validCategories))
	for category := range validCategories {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	counts := golden.CategoryCounts()
	for _, category := range categories {
		if counts[category] < 6 {
			t.Errorf("category %q has %d distractor targets, want at least 6", category, counts[category])
		}
	}

	diffs, err := filepath.Glob(filepath.Join(root, "eval", "fixtures", "*.diff"))
	if err != nil {
		t.Fatalf("glob eval/fixtures/*.diff: %v", err)
	}
	if len(diffs) == 0 {
		t.Errorf("eval/fixtures/*.diff matched no files")
	}
	for _, path := range diffs {
		name := filepath.Base(path)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		copyPath := filepath.Join(root, "eval", "retrieval-fixtures", "margherita-pizza", name)
		copied, err := os.ReadFile(copyPath)
		if err != nil {
			t.Errorf("read copy %s: %v", copyPath, err)
			continue
		}
		if !bytes.Equal(original, copied) {
			t.Errorf("%s is not byte-identical to its copy at %s", path, copyPath)
		}
	}

	overlays, err := filepath.Glob(filepath.Join(root, "projects", "*", "resources.yaml"))
	if err != nil {
		t.Fatalf("glob projects/*/resources.yaml: %v", err)
	}
	for _, overlay := range overlays {
		t.Errorf("per-system resources overlay must not exist: %s", overlay)
	}
}
