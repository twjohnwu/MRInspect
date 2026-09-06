package retrievaleval

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestCorpus_TierRankBands verifies REQ-02 / S-03: the checked-in golden
// targets occupy their required BM25 rank bands in the production corpus.
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

	fixtures, err := evalrun.LoadFixtures(filepath.Join(root, "eval", "fixtures"), nil)
	if err != nil {
		t.Fatalf("evalrun.LoadFixtures: %v", err)
	}

	names := make([]string, len(fixtures))
	for i := range fixtures {
		names[i] = fixtures[i].Name
	}
	golden, err := LoadGolden(filepath.Join(root, "eval", "retrieval-golden.yaml"), names)
	if err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}

	plan, err := BuildPlan(root, "margherita-pizza", fixtures)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := golden.ValidateAgainstPlan(plan.Triples); err != nil {
		t.Fatalf("ValidateAgainstPlan: %v", err)
	}

	sets := distinctPlanSets(plan.Triples)
	for _, triple := range plan.Triples {
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
