package retrievaleval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"

	"mrinspect/internal/config"
	"mrinspect/internal/evalrun"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

type harnessFixture struct {
	name  string
	terms string
}

type runHarness struct {
	repoRoot    string
	fixturesDir string
	goldenPath  string
	storePath   string
	reportPath  string
	corpusPaths []string
	sets        []resources.Set
	fixtures    []harnessFixture
}

var numericCell = regexp.MustCompile(`^[0-9]\.[0-9]+$`)
var integerCell = regexp.MustCompile(`^[0-9]+$`)

func newRunHarness(t *testing.T, fixtures []harnessFixture, withVectors bool) *runHarness {
	t.Helper()
	t.Setenv("MRI_RAG_EMBED_KEY", "")
	t.Setenv("AI_PROVIDER_KEY", "")

	root := t.TempDir()
	projectsDir := filepath.Join(root, "projects")
	fixturesDir := filepath.Join(root, "eval", "retrieval-fixtures", "margherita-pizza")
	pizzaDir := filepath.Join(root, "corpus", "pizza")
	sharedDir := filepath.Join(root, "corpus", "shared")
	for _, dir := range []string{projectsDir, filepath.Join(projectsDir, "margherita-pizza"), fixturesDir, pizzaDir, sharedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	writeHarnessFile(t, filepath.Join(projectsDir, "lanes.yaml"), `lanes:
  - id: spec-conformance
    enabled: true
    template: spec-conformance.tmpl.md
    intent: verify pizza specifications
    resources:
      sets: [margherita-pizza-docs]
      tags: []
    topK: 3
  - id: standards
    enabled: true
    template: standards.tmpl.md
    intent: verify shared standards
    resources:
      sets: [shared-standards]
      tags: []
    topK: 3
`)
	writeHarnessFile(t, filepath.Join(projectsDir, "resources.yaml"), `sets:
  - name: margherita-pizza-docs
    tags: [pizza]
    mode: retrieval
    paths: [corpus/pizza]
    include: ["*.md"]
  - name: shared-standards
    tags: [shared]
    mode: retrieval
    paths: [corpus/shared]
    include: ["*.md"]
`)

	pizzaPath := filepath.Join(pizzaDir, "guide.md")
	sharedPath := filepath.Join(sharedDir, "guide.md")
	writeHarnessFile(t, pizzaPath, `# Pizza Manual

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Tomato Basil Procedure

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Oven Cheese Contract

Oven cheese tomato basil standard audit policy searchable vocabulary.

## Sauce Herb Guidance

Sauce herb baking dairy specification review guidance searchable vocabulary.

## Crust Timing Note

Crust timing preparation checklist searchable vocabulary.
`)
	writeHarnessFile(t, sharedPath, `# Shared Manual

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Review Safety

Standard audit policy tomato basil oven cheese searchable vocabulary.

## Inspection Guardrails

Inspection controls governance tomato basil oven cheese searchable vocabulary.

## Delivery Checklist

Delivery checklist records searchable vocabulary.
`)

	for _, fixture := range fixtures {
		diff := fmt.Sprintf("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -0,0 +1 @@\n+%s\n", fixture.terms)
		writeHarnessFile(t, filepath.Join(fixturesDir, fixture.name), diff)
	}

	golden := Golden{}
	for _, fixture := range fixtures {
		golden.Entries = append(golden.Entries,
			Entry{
				Fixture: "margherita-pizza/" + fixture.name,
				Lane:    "spec-conformance",
				Relevant: []Target{
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Tomato Basil Procedure"},
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Oven Cheese Contract"},
				},
				Paraphrase: []Target{
					{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Sauce Herb Guidance"},
				},
				Distractors: []Distractor{
					{Target: Target{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Crust Timing Note"}, Category: "scope"},
				},
			},
			Entry{
				Fixture: "margherita-pizza/" + fixture.name,
				Lane:    "standards",
				Relevant: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Review Safety"},
				},
				Paraphrase: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Inspection Guardrails"},
				},
				Distractors: []Distractor{
					{Target: Target{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Delivery Checklist"}, Category: "scope"},
				},
			},
		)
	}
	goldenData, err := yaml.Marshal(golden)
	if err != nil {
		t.Fatalf("yaml.Marshal golden: %v", err)
	}
	goldenPath := filepath.Join(root, "eval", "retrieval-golden.yaml")
	if err := os.WriteFile(goldenPath, goldenData, 0o644); err != nil {
		t.Fatalf("WriteFile golden: %v", err)
	}

	registry, err := resources.Load(root, "")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	if len(registry.Sets) != 2 {
		t.Fatalf("resources.Load returned %d sets, want 2", len(registry.Sets))
	}

	harness := &runHarness{
		repoRoot:    root,
		fixturesDir: fixturesDir,
		goldenPath:  goldenPath,
		storePath:   filepath.Join(root, "store.sqlite"),
		reportPath:  filepath.Join(root, "eval", "RETRIEVAL.md"),
		corpusPaths: []string{pizzaPath, sharedPath},
		sets:        registry.Sets,
		fixtures:    fixtures,
	}
	harness.index(t, harness.storePath, withVectors)
	harness.validateSetup(t)
	return harness
}

func (h *runHarness) index(t *testing.T, path string, withVectors bool, sets ...resources.Set) {
	t.Helper()
	if len(sets) == 0 {
		sets = h.sets
	}
	var indexEmbedder embed.Embedder
	if withVectors {
		indexEmbedder = embed.NewFixture(4)
	}
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{
		OutputPath: path,
		Sets:       sets,
		Embedder:   indexEmbedder,
		Progress:   io.Discard,
	}); err != nil {
		t.Fatalf("sqlite.Index: %v", err)
	}
}

func (h *runHarness) options(queryEmbedder embed.Embedder) Options {
	return Options{
		RepoRoot:    h.repoRoot,
		FixturesDir: filepath.Dir(h.fixturesDir),
		GoldenPath:  h.goldenPath,
		StorePath:   h.storePath,
		ReportPath:  h.reportPath,
		Embedding: config.RAGEmbeddingConfig{
			Enabled:  true,
			Provider: "openai",
			Key:      "fixture-key",
		},
		Embedder: queryEmbedder,
	}
}

// TestRun_LoadsFixturesPerSystemDir verifies REQ-01 / S-01: retrieval
// fixtures are discovered per system and rejected before retrieval when the
// system-directory preflight is invalid.
func TestRun_LoadsFixturesPerSystemDir(t *testing.T) {
	t.Run("missing projects directory or symlink subdir", func(t *testing.T) {
		root := t.TempDir()
		fixturesDir := filepath.Join(root, "retrieval-fixtures")
		for _, dir := range []string{
			filepath.Join(fixturesDir, "alpha"),
			filepath.Join(fixturesDir, "beta"),
			filepath.Join(root, "projects", "alpha"),
		} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll %s: %v", dir, err)
			}
		}
		writeHarnessFile(t, filepath.Join(fixturesDir, "alpha", "01-a.diff"), "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+alpha\n")
		writeHarnessFile(t, filepath.Join(fixturesDir, "beta", "01-b.diff"), "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -0,0 +1 @@\n+beta\n")
		if err := os.Symlink(filepath.Join(fixturesDir, "alpha"), filepath.Join(fixturesDir, "gamma")); err != nil {
			t.Fatalf("Symlink gamma: %v", err)
		}
		writeHarnessFile(t, filepath.Join(fixturesDir, "notes.txt"), "not a system directory\n")

		reportPath := filepath.Join(root, "retrieval-report.md")
		queryEmbedder := embed.NewFixture(4)
		err := Run(context.Background(), Options{
			RepoRoot:    root,
			FixturesDir: fixturesDir,
			GoldenPath:  filepath.Join(root, "missing-golden.yaml"),
			StorePath:   filepath.Join(root, "missing-store.sqlite"),
			ReportPath:  reportPath,
			Embedder:    queryEmbedder,
		})
		if err == nil {
			t.Fatal("Run error = nil, want fixture-loading preflight error")
		}
		if !strings.Contains(err.Error(), "load retrieval fixtures:") {
			t.Errorf("Run error = %q, want it to contain %q", err, "load retrieval fixtures:")
		}
		if !strings.Contains(err.Error(), `"beta" has no projects directory`) &&
			!strings.Contains(err.Error(), `"gamma" is not a plain directory`) {
			t.Errorf("Run error = %q, want it to contain %q or %q", err,
				`"beta" has no projects directory`, `"gamma" is not a plain directory`)
		}
		if strings.Contains(err.Error(), root) {
			t.Errorf("Run error = %q, must not contain temp root %q", err, root)
		}
		if got := queryEmbedder.Calls(); got != 0 {
			t.Errorf("Embedder.Calls() = %d, want 0", got)
		}
		if _, statErr := os.Stat(reportPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("report exists after fixture-loading preflight rejection; stat error = %v", statErr)
		}
	})

	t.Run("no qualifying system directories", func(t *testing.T) {
		root := t.TempDir()
		fixturesDir := filepath.Join(root, "retrieval-fixtures")
		if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", fixturesDir, err)
		}

		reportPath := filepath.Join(root, "retrieval-report.md")
		queryEmbedder := embed.NewFixture(4)
		err := Run(context.Background(), Options{
			RepoRoot:    root,
			FixturesDir: fixturesDir,
			GoldenPath:  filepath.Join(root, "missing-golden.yaml"),
			StorePath:   filepath.Join(root, "missing-store.sqlite"),
			ReportPath:  reportPath,
			Embedder:    queryEmbedder,
		})
		if err == nil {
			t.Fatal("Run error = nil, want no-system-directories error")
		}
		if !strings.Contains(err.Error(), "no system directories found") {
			t.Errorf("Run error = %q, want it to contain %q", err, "no system directories found")
		}
		if got := queryEmbedder.Calls(); got != 0 {
			t.Errorf("Embedder.Calls() = %d, want 0", got)
		}
		if _, statErr := os.Stat(reportPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("report exists after no-system-directories rejection; stat error = %v", statErr)
		}
	})
}

func (h *runHarness) validateSetup(t *testing.T) {
	t.Helper()
	fixtures, err := evalrun.LoadFixtures(h.fixturesDir, nil)
	if err != nil {
		t.Fatalf("evalrun.LoadFixtures: %v", err)
	}
	if len(fixtures) != len(h.fixtures) {
		t.Fatalf("evalrun.LoadFixtures returned %d fixtures, want %d", len(fixtures), len(h.fixtures))
	}
	names := make([]string, len(fixtures))
	for i := range fixtures {
		names[i] = "margherita-pizza/" + fixtures[i].Name
	}
	golden, err := LoadGolden(h.goldenPath, names)
	if err != nil {
		t.Fatalf("LoadGolden harness data: %v", err)
	}
	if err := golden.ValidateAgainstStore(context.Background(), h.storePath); err != nil {
		t.Fatalf("ValidateAgainstStore harness data: %v", err)
	}
	plan, err := BuildPlan(h.repoRoot, "margherita-pizza", fixtures)
	if err != nil {
		t.Fatalf("BuildPlan harness data: %v", err)
	}
	prefixedTriples := make([]Triple, len(plan.Triples))
	copy(prefixedTriples, plan.Triples)
	for i, triple := range prefixedTriples {
		prefixedTriples[i].Fixture = "margherita-pizza/" + triple.Fixture
	}
	if err := golden.ValidateAgainstPlan(prefixedTriples); err != nil {
		t.Fatalf("ValidateAgainstPlan harness data: %v", err)
	}
	if want := len(fixtures) * 2; len(plan.Triples) != want {
		t.Fatalf("BuildPlan harness data returned %d triples, want %d", len(plan.Triples), want)
	}
}

func writeHarnessFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return sha256.Sum256(content)
}

func readReport(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile report: %v", err)
	}
	return string(content)
}

// tableRows parses the main 20-column table (REQ-04 prepends a leading
// system column: system, fixture, lane, set, k, then 15 metric cells). The
// mean row's system cell is blank and its fixture cell carries the "mean"
// literal (system has nothing to average).
func tableRows(t *testing.T, report string) (data [][]string, mean []string) {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(strings.TrimSpace(line), "|") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "|")
		trimmed = strings.TrimSuffix(trimmed, "|")
		const escapedPipe = "\x00"
		parts := strings.Split(strings.ReplaceAll(trimmed, `\|`, escapedPipe), "|")
		for i := range parts {
			parts[i] = strings.ReplaceAll(strings.TrimSpace(parts[i]), escapedPipe, "|")
		}
		if len(parts) != 20 {
			continue
		}
		switch {
		case parts[0] == "system":
			continue // header row
		case parts[0] == "" && parts[1] == "mean":
			mean = parts
		default:
			separator := true
			for _, cell := range parts {
				if strings.Trim(cell, "-: ") != "" {
					separator = false
					break
				}
			}
			if !separator {
				data = append(data, parts)
			}
		}
	}
	if mean == nil {
		t.Fatal("report has no | mean | row")
	}
	return data, mean
}

func requireNumeric(t *testing.T, value, location string) {
	t.Helper()
	if !numericCell.MatchString(value) {
		t.Errorf("%s = %q, want numeric cell matching %s", location, value, numericCell)
	}
}

func requireInteger(t *testing.T, value, location string) {
	t.Helper()
	if !integerCell.MatchString(value) {
		t.Errorf("%s = %q, want integer cell matching %s", location, value, integerCell)
	}
}

func headerValue(t *testing.T, report, name string) string {
	t.Helper()
	prefix := name + ": "
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("report missing header line %q", prefix+"…")
	return ""
}

// parseMarkdownTable finds the first markdown table in report with exactly
// wantColumns pipe-delimited cells — immediately under the "## "+heading
// section marker, or (heading == "") the first such table in the document —
// and returns its header row plus every following data/mean row (the
// "---"-only separator row is skipped). Unlike tableRows, it does not
// assume a fixed 19-column main table: REQ-04 grows the main table to 20
// columns and adds two more tables ("## Mean by k", "## Distractors by
// category") with their own column counts.
func parseMarkdownTable(report, heading string, wantColumns int) (header []string, rows [][]string) {
	section := report
	if heading != "" {
		marker := "## " + heading
		start := strings.Index(report, marker)
		if start < 0 {
			return nil, nil
		}
		section = report[start:]
		if next := strings.Index(section[len(marker):], "\n## "); next >= 0 {
			section = section[:len(marker)+next]
		}
	}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "|")
		trimmed = strings.TrimSuffix(trimmed, "|")
		const escapedPipe = "\x00"
		parts := strings.Split(strings.ReplaceAll(trimmed, `\|`, escapedPipe), "|")
		for i := range parts {
			parts[i] = strings.ReplaceAll(strings.TrimSpace(parts[i]), escapedPipe, "|")
		}
		if len(parts) != wantColumns {
			continue
		}
		separator := true
		for _, cell := range parts {
			if strings.Trim(cell, "-: ") != "" {
				separator = false
				break
			}
		}
		if separator {
			continue
		}
		if header == nil {
			header = parts
			continue
		}
		rows = append(rows, parts)
	}
	return header, rows
}

// colByName returns row's cell under the header column named name, failing
// the test if the column is missing from header or the row is too short.
func colByName(t *testing.T, header, row []string, name string) string {
	t.Helper()
	for i, cell := range header {
		if cell == name {
			if i >= len(row) {
				t.Fatalf("row %v has no cell for column %q at index %d", row, name, i)
			}
			return row[i]
		}
	}
	t.Fatalf("header %v has no column %q", header, name)
	return ""
}

// assertPipeCountsMatchHeader verifies every table row in report has the
// same "|" count as the header row of the table it belongs to. An escaped
// "\|" inside a cell value (e.g. a set name containing a literal "|") is
// not a column delimiter, so it is counted the same escape-aware way
// tableRows and parseMarkdownTable already do: replaced with a placeholder
// before counting.
func assertPipeCountsMatchHeader(t *testing.T, report string) {
	t.Helper()
	const escapedPipe = "\x00"
	var headerPipes int
	inTable := false
	for _, line := range strings.Split(report, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			continue
		}
		pipes := strings.Count(strings.ReplaceAll(trimmed, `\|`, escapedPipe), "|")
		if !inTable {
			headerPipes = pipes
			inTable = true
			continue
		}
		if strings.Trim(trimmed, "|-: ") == "" {
			continue // markdown separator row
		}
		if pipes != headerPipes {
			t.Errorf("table row %q has %d \"|\", want %d matching its header", line, pipes, headerPipes)
		}
	}
}

// TestRun_RefusesStaleStore verifies REQ-03 / S-04: corpus drift rejects the
// store with remediation guidance and leaves a pre-existing report unchanged.
func TestRun_RefusesStaleStore(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-a.diff", terms: "tomato basil oven"},
		{name: "02-b.diff", terms: "standard audit policy"},
	}, true)
	writeHarnessFile(t, harness.reportPath, "pre-existing report\n")
	before := fileDigest(t, harness.reportPath)

	file, err := os.OpenFile(harness.corpusPaths[0], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile corpus for one-byte append: %v", err)
	}
	if _, err := file.Write([]byte("x")); err != nil {
		file.Close()
		t.Fatalf("append one byte to corpus: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close changed corpus: %v", err)
	}

	err = Run(context.Background(), harness.options(embed.NewFixture(4)))
	if err == nil {
		t.Fatal("Run error = nil, want stale-store error")
	}
	for _, want := range []string{"stale", "rerun mrinspect index"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run error = %q, want it to contain %q", err, want)
		}
	}
	if after := fileDigest(t, harness.reportPath); after != before {
		t.Errorf("report sha256 changed after stale-store rejection: before %x, after %x", before, after)
	}
}

func TestRun_RefusesWhenGoldenLaneHasNoTriples(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-x.diff", terms: "tomato basil oven"},
	}, true)
	writeHarnessFile(t, filepath.Join(harness.repoRoot, "projects", "lanes.yaml"), `lanes:
  - id: spec-conformance
    enabled: true
    template: spec-conformance.tmpl.md
    intent: verify pizza specifications
    resources:
      sets: []
      tags: [docs]
    topK: 3
  - id: standards
    enabled: true
    template: standards.tmpl.md
    intent: verify shared standards
    resources:
      sets: [shared-standards]
      tags: []
    topK: 3
`)

	err := Run(context.Background(), harness.options(embed.NewFixture(4)))
	if err == nil {
		t.Error("Run error = nil, want missing golden lane error")
	} else {
		for _, want := range []string{"spec-conformance", "no resource set"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Run error = %q, want it to contain %q", err, want)
			}
		}
	}
	if _, statErr := os.Stat(harness.reportPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("report exists after missing golden lane rejection; stat error = %v", statErr)
	}
}

func TestRun_FreshnessCoversAllRegistrySets(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-a.diff", terms: "tomato basil oven"},
	}, true)
	friedChickenDir := filepath.Join(harness.repoRoot, "corpus", "fried-chicken")
	if err := os.MkdirAll(friedChickenDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", friedChickenDir, err)
	}
	friedChickenPath := filepath.Join(friedChickenDir, "guide.md")
	writeHarnessFile(t, friedChickenPath, `# Fried Chicken Manual

Crispy coating and frying temperature guidance.
`)
	writeHarnessFile(t, filepath.Join(harness.repoRoot, "projects", "resources.yaml"), `sets:
  - name: margherita-pizza-docs
    tags: [pizza]
    mode: retrieval
    paths: [corpus/pizza]
    include: ["*.md"]
  - name: shared-standards
    tags: [shared]
    mode: retrieval
    paths: [corpus/shared]
    include: ["*.md"]
  - name: fried-chicken-docs
    tags: [fried-chicken]
    mode: retrieval
    paths: [corpus/fried-chicken]
    include: ["*.md"]
`)

	registry, err := resources.Load(harness.repoRoot, "margherita-pizza")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	if len(registry.Sets) != 3 {
		t.Fatalf("resources.Load returned %d sets, want 3", len(registry.Sets))
	}
	harness.index(t, harness.storePath, true, registry.Sets...)

	if err := Run(context.Background(), harness.options(embed.NewFixture(4))); err != nil {
		t.Fatalf("Run with all registry sets indexed: %v", err)
	}
	rows, _ := tableRows(t, readReport(t, harness.reportPath))
	if len(rows) != 2 {
		t.Fatalf("report has %d data rows, want 2", len(rows))
	}
	wantSets := map[string]bool{
		"margherita-pizza-docs": false,
		"shared-standards":      false,
	}
	for _, row := range rows {
		if _, ok := wantSets[row[3]]; !ok {
			t.Errorf("report contains row for non-lane set %q", row[3])
			continue
		}
		wantSets[row[3]] = true
	}
	for set, found := range wantSets {
		if !found {
			t.Errorf("report missing row for lane-resolved set %q", set)
		}
	}

	writeHarnessFile(t, friedChickenPath, `# Fried Chicken Manual

Crispy coating, frying temperature guidance, and a changed brining rule.
`)
	err = Run(context.Background(), harness.options(embed.NewFixture(4)))
	if err == nil {
		t.Fatal("Run error = nil after unindexed third-set change, want stale-store error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("Run error = %q, want it to contain %q", err, "stale")
	}
}

// TestRun_WritesReportAndSanitizesHeader verifies REQ-03 / S-07: the report
// has paired numeric metrics, bounded metadata, and rejects unsafe metadata.
func TestRun_WritesReportAndSanitizesHeader(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-a.diff", terms: "tomato basil oven"},
		{name: "02-b.diff", terms: "standard audit policy"},
	}, true)
	const sentinel = "SENTINEL-KEY-8f3a"
	t.Setenv("MRI_RAG_EMBED_KEY", sentinel)
	opts := harness.options(embed.NewFixture(4))
	opts.Embedding.Key = sentinel

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := readReport(t, harness.reportPath)
	if _, err := time.Parse(time.RFC3339, headerValue(t, report, "built_at")); err != nil {
		t.Errorf("built_at is not RFC3339: %v", err)
	}
	if got := headerValue(t, report, "resources_sha256"); !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(got) {
		t.Errorf("resources_sha256 = %q, want first 8 lowercase hex characters", got)
	}
	if got := headerValue(t, report, "embed_model"); got != embed.FixtureModel {
		t.Errorf("embed_model = %q, want %q", got, embed.FixtureModel)
	}
	if got := headerValue(t, report, "pool"); got != "off=TopK+1 on=4xTopK shuffle=4xTopK×20" {
		t.Errorf("pool = %q, want %q", got, "off=TopK+1 on=4xTopK shuffle=4xTopK×20")
	}
	if _, err := time.Parse(time.RFC3339, headerValue(t, report, "generated_at")); err != nil {
		t.Errorf("generated_at is not RFC3339: %v", err)
	}

	wantTableHeader := "| system | fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |"
	if !strings.Contains(report, wantTableHeader) {
		t.Errorf("report missing table header %q", wantTableHeader)
	}
	rows, mean := tableRows(t, report)
	if want := len(harness.fixtures) * 2; len(rows) != want {
		t.Fatalf("report has %d data rows, want %d", len(rows), want)
	}
	for rowIndex, row := range rows {
		for _, column := range []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18} {
			requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
		}
		for _, column := range []int{17, 19} {
			requireInteger(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
		}
	}
	for _, column := range []int{7, 10, 13, 16, 19} {
		if !strings.HasSuffix(mean[column], ")") || !strings.Contains(mean[column], " (n=") {
			t.Errorf("mean ON cell %q does not end with (n=N)", mean[column])
		}
	}
	for _, forbidden := range []string{sentinel, harness.storePath, "http"} {
		if strings.Contains(strings.ToLower(report), strings.ToLower(forbidden)) {
			t.Errorf("report contains forbidden value %q", forbidden)
		}
	}

	if err := os.Remove(harness.reportPath); err != nil {
		t.Fatalf("Remove first report: %v", err)
	}
	db, err := sql.Open("sqlite", harness.storePath)
	if err != nil {
		t.Fatalf("sql.Open store: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET embed_model = ? WHERE id = 1`, "fixture\npoison"); err != nil {
		db.Close()
		t.Fatalf("corrupt embed_model: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corrupted store: %v", err)
	}
	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run error = nil, want unsafe embed_model error")
	}
	if _, err := os.Stat(harness.reportPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("report exists after unsafe embed_model rejection; stat error = %v", err)
	}
}

func TestRun_WritesPlanWarningsToProgress(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{{name: "01-pizza.diff", terms: "tomato basil"}}, true)
	lanesPath := filepath.Join(harness.repoRoot, "projects", "lanes.yaml")
	lanesData, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("ReadFile lanes.yaml: %v", err)
	}
	updated := strings.Replace(string(lanesData), "tags: []", "tags: [missing]", 1)
	writeHarnessFile(t, lanesPath, updated)

	var progress bytes.Buffer
	opts := harness.options(embed.NewFixture(4))
	opts.Progress = &progress
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := "warning: lane \"spec-conformance\" unknown resource selector: missing\n"
	if got := progress.String(); got != want {
		t.Errorf("progress = %q, want %q", got, want)
	}
	if report := readReport(t, harness.reportPath); strings.Contains(report, "unknown resource selector") {
		t.Error("report contains unknown resource selector warning")
	}
}

// TestRun_DegradationPolicy verifies REQ-03 / S-08: rerank failures degrade
// individual ON cells, while store-level failures reject the entire report.
func TestRun_DegradationPolicy(t *testing.T) {
	fixtures := []harnessFixture{
		{name: "01-a.diff", terms: "tomato basil oven"},
		{name: "02-b.diff", terms: "standard audit policy"},
	}

	t.Run("embed call failure is row-local", func(t *testing.T) {
		harness := newRunHarness(t, fixtures, true)
		queryEmbedder := embed.NewFixture(4)
		queryEmbedder.ErrAt = 3
		if err := Run(context.Background(), harness.options(queryEmbedder)); err != nil {
			t.Fatalf("Run: %v", err)
		}
		rows, mean := tableRows(t, readReport(t, harness.reportPath))
		if len(rows) != 4 {
			t.Fatalf("report has %d rows, want 4", len(rows))
		}
		for rowIndex, row := range rows {
			for _, column := range []int{5, 6, 8, 9, 11, 12, 14, 15, 18} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[17], fmt.Sprintf("row %d distractors_off", rowIndex+1))
			if rowIndex == 2 {
				for _, column := range []int{7, 10, 13, 16, 19} {
					if row[column] != "degraded: embed-call-failed" {
						t.Errorf("row 3 ON cell = %q, want degraded: embed-call-failed", row[column])
					}
				}
				continue
			}
			for _, column := range []int{7, 10, 13, 16} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[19], fmt.Sprintf("row %d distractors_on", rowIndex+1))
		}
		for _, column := range []int{7, 10, 13, 16, 19} {
			if !strings.HasSuffix(mean[column], "(n=3)") {
				t.Errorf("mean ON cell = %q, want suffix %q", mean[column], "(n=3)")
			}
		}
	})

	t.Run("no vectors degrades every ON cell", func(t *testing.T) {
		harness := newRunHarness(t, fixtures, false)
		if err := Run(context.Background(), harness.options(embed.NewFixture(4))); err != nil {
			t.Fatalf("Run: %v", err)
		}
		rows, mean := tableRows(t, readReport(t, harness.reportPath))
		if len(rows) != 4 {
			t.Fatalf("report has %d rows, want 4", len(rows))
		}
		for rowIndex, row := range rows {
			for _, column := range []int{5, 6, 8, 9, 11, 12, 14, 15, 18} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[17], fmt.Sprintf("row %d distractors_off", rowIndex+1))
			for _, column := range []int{7, 10, 13, 16, 19} {
				if row[column] != "degraded: no-vectors" {
					t.Errorf("row %d ON cell = %q, want degraded: no-vectors", rowIndex+1, row[column])
				}
			}
		}
		for _, column := range []int{7, 10, 13, 16, 19} {
			if !strings.HasSuffix(mean[column], "(n=0)") {
				t.Errorf("mean ON cell = %q, want suffix %q", mean[column], "(n=0)")
			}
		}
	})

	t.Run("missing indexed set rejects whole run", func(t *testing.T) {
		harness := newRunHarness(t, fixtures, true)
		harness.index(t, harness.storePath, true, harness.sets[0])
		writeHarnessFile(t, harness.reportPath, "pre-existing report\n")
		before := fileDigest(t, harness.reportPath)
		if err := Run(context.Background(), harness.options(embed.NewFixture(4))); err == nil {
			t.Fatal("Run error = nil, want missing-set store error")
		}
		if after := fileDigest(t, harness.reportPath); after != before {
			t.Errorf("report sha256 changed after missing-set rejection: before %x, after %x", before, after)
		}
	})
}

func TestRun_RendersThreeArmTable(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-neutral.diff", terms: "tomato basil standard audit"},
	}, true)

	for _, path := range []string{
		filepath.Join(harness.repoRoot, "projects", "lanes.yaml"),
		filepath.Join(harness.repoRoot, "projects", "resources.yaml"),
		harness.goldenPath,
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		writeHarnessFile(t, path, strings.ReplaceAll(string(content), "shared-standards", "pipe|set"))
	}
	registry, err := resources.Load(harness.repoRoot, "margherita-pizza")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	harness.sets = registry.Sets
	harness.index(t, harness.storePath, true)

	queryEmbedder := embed.NewFixture(4)
	queryEmbedder.ErrAt = 2
	if err := Run(context.Background(), harness.options(queryEmbedder)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := readReport(t, harness.reportPath)
	const tableHeader = "| system | fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |"
	if got := strings.Count(report, tableHeader); got != 1 {
		t.Errorf("table header count = %d, want 1", got)
	}
	if !strings.Contains(report, `| pipe\|set |`) {
		t.Error(`report does not contain escaped set name "pipe\|set"`)
	}
	if got := headerValue(t, report, "pool"); !strings.Contains(got, "shuffle=") {
		t.Errorf("pool = %q, want it to contain shuffle=", got)
	}
	// The mean row's system cell is blank (only the fixture cell carries
	// the "mean" literal), so it now reads "|  | mean | ..." rather than
	// "| mean | ...".
	meanLinePattern := regexp.MustCompile(`^\|\s*\|\s*mean\s*\|`)
	meanRows := 0
	for _, line := range strings.Split(report, "\n") {
		if meanLinePattern.MatchString(line) {
			meanRows++
			if !strings.Contains(line, "(n=1)") {
				t.Errorf("mean row = %q, want it to contain (n=1)", line)
			}
		}
	}
	if meanRows != 1 {
		t.Errorf("mean row count = %d, want 1", meanRows)
	}

	rows, _ := tableRows(t, report)
	if len(rows) != 2 {
		t.Fatalf("report has %d data rows, want 2", len(rows))
	}
	var degraded []string
	for _, row := range rows {
		if row[3] == "pipe|set" {
			degraded = row
			break
		}
	}
	if degraded == nil {
		t.Fatal("report has no row for pipe|set")
	}
	for _, column := range []int{7, 10, 13, 16, 19} {
		if degraded[column] != "degraded: embed-call-failed" {
			t.Errorf("degraded row column %d = %q, want degraded: embed-call-failed", column+1, degraded[column])
		}
	}
	for _, column := range []int{5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if _, err := strconv.ParseFloat(degraded[column], 64); err != nil {
			t.Errorf("degraded row column %d = %q, want a number", column+1, degraded[column])
		}
	}
	if forbidden := regexp.MustCompile(`(?i)better|worse|improve|good|bad|較好|較差|改善|好|差`); forbidden.MatchString(report) {
		t.Errorf("report contains conclusion word matched by %s", forbidden)
	}
}

// TestRun_EmbedsOncePerRerankedTriple verifies REQ-03 / S-07: ON embeds once
// for each triple and never embeds without vectors.
func TestRun_EmbedsOncePerRerankedTriple(t *testing.T) {
	fixtures := []harnessFixture{
		{name: "01-hit.diff", terms: "tomato basil standard audit"},
	}
	harness := newRunHarness(t, fixtures, true)
	queryEmbedder := embed.NewFixture(4)
	if err := Run(context.Background(), harness.options(queryEmbedder)); err != nil {
		t.Fatalf("Run with vectors: %v", err)
	}
	const triples = 2
	if got := queryEmbedder.Calls(); got != triples {
		t.Errorf("Embedder.Calls() = %d, want %d triples", got, triples)
	}

	noVectorPath := filepath.Join(harness.repoRoot, "store-no-vectors.sqlite")
	harness.index(t, noVectorPath, false)
	noVectorEmbedder := embed.NewFixture(4)
	opts := harness.options(noVectorEmbedder)
	opts.StorePath = noVectorPath
	opts.ReportPath = filepath.Join(harness.repoRoot, "eval", "RETRIEVAL-no-vectors.md")
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run without vectors: %v", err)
	}
	if got := noVectorEmbedder.Calls(); got != 0 {
		t.Errorf("Embedder.Calls() without vectors = %d, want 0", got)
	}
}

// TestRun_RetriesRateLimitedEmbedding verifies REQ-05 / S-09: eval-side
// embedding calls retry HTTP 429 with the same decorator the indexer uses,
// so a transient rate limit does not degrade the ON cell. Every fixture
// resolves both required lanes (run.go rejects a lane that resolves to no
// resource set), so this harness's one fixture yields two triples; the
// first triple's embed request is rate-limited twice then succeeds, and
// the second triple's embed request succeeds on its first attempt.
func TestRun_RetriesRateLimitedEmbedding(t *testing.T) {
	fixtures := []harnessFixture{{name: "01-retry.diff", terms: "standard audit policy"}}
	harness := newRunHarness(t, fixtures, true)

	queryEmbedder := embed.NewFixture(4)
	queryEmbedder.FailOn = func(call int, _ []string) error {
		if call <= 2 {
			return &embed.StatusError{Code: 429}
		}
		return nil
	}
	var waits []time.Duration
	opts := harness.options(queryEmbedder)
	opts.RetryWait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	var progress bytes.Buffer
	opts.Progress = &progress

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows, _ := tableRows(t, readReport(t, harness.reportPath))
	if len(rows) != 2 {
		t.Fatalf("report has %d data rows, want 2", len(rows))
	}
	for rowIndex, row := range rows {
		for _, column := range []int{7, 10, 13, 16, 19} {
			if row[column] == "degraded: embed-call-failed" {
				t.Errorf("row %d ON cell column %d = %q, want a non-degraded value", rowIndex+1, column+1, row[column])
			}
		}
	}

	const wantCalls = 3 /* retried triple */ + 1 /* second triple, first attempt */
	if got := queryEmbedder.Calls(); got != wantCalls {
		t.Errorf("Embedder.Calls() = %d, want %d", got, wantCalls)
	}
	wantWaits := []time.Duration{20 * time.Second, 40 * time.Second}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Errorf("retry waits = %v, want %v", waits, wantWaits)
	}
	if got := strings.Count(progress.String(), "rate limited"); got != 2 {
		t.Errorf("progress rate-limited line count = %d, want 2; progress = %q", got, progress.String())
	}
}

// TestRun_RendersSystemColumnAndRetrieveMs verifies REQ-04 / S-06: the main
// table gains a leading system column (fixture printed bare, without the
// system/ prefix) and the header gains a retrieve_ms line reporting
// OFF/ON Retrieve timings.
func TestRun_RendersSystemColumnAndRetrieveMs(t *testing.T) {
	root := t.TempDir()
	projectsDir := filepath.Join(root, "projects")
	pizzaDir := filepath.Join(root, "corpus", "pizza")
	sharedDir := filepath.Join(root, "corpus", "shared")
	fixturesRoot := filepath.Join(root, "eval", "retrieval-fixtures")
	systems := []string{"alpha-sys", "beta-sys"}
	dirs := []string{projectsDir, pizzaDir, sharedDir, fixturesRoot}
	for _, system := range systems {
		dirs = append(dirs, filepath.Join(projectsDir, system), filepath.Join(fixturesRoot, system))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	const pipeSet = "margherita|docs"
	writeHarnessFile(t, filepath.Join(projectsDir, "lanes.yaml"), fmt.Sprintf(`lanes:
  - id: spec-conformance
    enabled: true
    template: spec-conformance.tmpl.md
    intent: verify pizza specifications
    resources:
      sets: [%q]
      tags: []
    topK: 3
  - id: standards
    enabled: true
    template: standards.tmpl.md
    intent: verify shared standards
    resources:
      sets: [shared-standards]
      tags: []
    topK: 3
`, pipeSet))
	writeHarnessFile(t, filepath.Join(projectsDir, "resources.yaml"), fmt.Sprintf(`sets:
  - name: %q
    tags: [pizza]
    mode: retrieval
    paths: [corpus/pizza]
    include: ["*.md"]
  - name: shared-standards
    tags: [shared]
    mode: retrieval
    paths: [corpus/shared]
    include: ["*.md"]
`, pipeSet))
	writeHarnessFile(t, filepath.Join(pizzaDir, "guide.md"), `# Pizza Manual

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Tomato Basil Procedure

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Oven Cheese Contract

Oven cheese tomato basil standard audit policy searchable vocabulary.

## Sauce Herb Guidance

Sauce herb baking dairy specification review guidance searchable vocabulary.

## Crust Timing Note

Crust timing preparation checklist searchable vocabulary.
`)
	writeHarnessFile(t, filepath.Join(sharedDir, "guide.md"), `# Shared Manual

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Review Safety

Standard audit policy tomato basil oven cheese searchable vocabulary.

## Inspection Guardrails

Inspection controls governance tomato basil oven cheese searchable vocabulary.

## Delivery Checklist

Delivery checklist records searchable vocabulary.
`)

	var golden Golden
	for _, system := range systems {
		writeHarnessFile(t, filepath.Join(fixturesRoot, system, "01-x.diff"),
			"diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -0,0 +1 @@\n+tomato basil standard audit\n")
		golden.Entries = append(golden.Entries,
			Entry{
				Fixture: system + "/01-x.diff",
				Lane:    "spec-conformance",
				Relevant: []Target{
					{Set: pipeSet, Path: "guide.md", Heading: "Pizza Manual > Tomato Basil Procedure"},
					{Set: pipeSet, Path: "guide.md", Heading: "Pizza Manual > Oven Cheese Contract"},
				},
				Paraphrase: []Target{
					{Set: pipeSet, Path: "guide.md", Heading: "Pizza Manual > Sauce Herb Guidance"},
				},
				Distractors: []Distractor{
					{Target: Target{Set: pipeSet, Path: "guide.md", Heading: "Pizza Manual > Crust Timing Note"}, Category: "scope"},
				},
			},
			Entry{
				Fixture: system + "/01-x.diff",
				Lane:    "standards",
				Relevant: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Review Safety"},
				},
				Paraphrase: []Target{
					{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Inspection Guardrails"},
				},
				Distractors: []Distractor{
					{Target: Target{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Delivery Checklist"}, Category: "scope"},
				},
			},
		)
	}
	goldenData, err := yaml.Marshal(golden)
	if err != nil {
		t.Fatalf("yaml.Marshal golden: %v", err)
	}
	goldenPath := filepath.Join(root, "eval", "retrieval-golden.yaml")
	if err := os.WriteFile(goldenPath, goldenData, 0o644); err != nil {
		t.Fatalf("WriteFile golden: %v", err)
	}

	registry, err := resources.Load(root, systems[0])
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	storePath := filepath.Join(root, "store.sqlite")
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{
		OutputPath: storePath,
		Sets:       registry.Sets,
		Embedder:   embed.NewFixture(4),
		Progress:   io.Discard,
	}); err != nil {
		t.Fatalf("sqlite.Index: %v", err)
	}

	// Two systems x two required lanes (the harness invariant: every
	// fixture resolves both lanes) = 4 triples processed in system-name,
	// then lanes.yaml-declaration order: alpha-sys/spec-conformance,
	// alpha-sys/standards, beta-sys/spec-conformance, beta-sys/standards.
	// ErrAt=2 fails exactly the second ON embed call (alpha-sys/standards),
	// degrading exactly that one triple, so retrieve_ms's on_mean covers
	// the remaining n=3 non-degraded triples.
	queryEmbedder := embed.NewFixture(4)
	queryEmbedder.ErrAt = 2
	reportPath := filepath.Join(root, "eval", "RETRIEVAL.md")
	opts := Options{
		RepoRoot:    root,
		FixturesDir: fixturesRoot,
		GoldenPath:  goldenPath,
		StorePath:   storePath,
		ReportPath:  reportPath,
		Embedding: config.RAGEmbeddingConfig{
			Enabled:  true,
			Provider: "openai",
			Key:      "fixture-key",
		},
		Embedder: queryEmbedder,
	}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := readReport(t, reportPath)

	const wantHeader = "| system | fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |"
	if got := strings.Count(report, wantHeader); got != 1 {
		t.Errorf("20-column table header count = %d, want 1", got)
	}

	header, allRows := parseMarkdownTable(report, "", 20)
	// parseMarkdownTable returns every 20-cell row, including the trailing
	// mean row (system blank, fixture "mean"); split it out before
	// counting/checking the four per-triple data rows.
	var rows [][]string
	meanRowCount := 0
	for _, row := range allRows {
		if colByName(t, header, row, "fixture") == "mean" {
			meanRowCount++
			continue
		}
		rows = append(rows, row)
	}
	if meanRowCount != 1 {
		t.Errorf("main table has %d mean rows, want 1", meanRowCount)
	}
	if len(rows) != 4 {
		t.Fatalf("main table has %d data rows, want 4", len(rows))
	}
	for _, row := range rows {
		system := colByName(t, header, row, "system")
		if system != "alpha-sys" && system != "beta-sys" {
			t.Errorf("row system cell = %q, want alpha-sys or beta-sys", system)
		}
		if fixture := colByName(t, header, row, "fixture"); strings.Contains(fixture, "/") {
			t.Errorf("row fixture cell = %q, want a bare filename without a system/ prefix", fixture)
		}
	}

	var degradedRow []string
	for _, row := range rows {
		if colByName(t, header, row, "system") == "alpha-sys" && colByName(t, header, row, "lane") == "standards" {
			degradedRow = row
		}
	}
	if degradedRow == nil {
		t.Fatal("main table has no row for system=alpha-sys lane=standards")
	}
	for _, column := range []string{"orig_recall_on", "orig_mrr_on", "para_recall_on", "para_mrr_on", "distractors_on"} {
		if got := colByName(t, header, degradedRow, column); got != "degraded: embed-call-failed" {
			t.Errorf("degraded row column %q = %q, want %q", column, got, "degraded: embed-call-failed")
		}
	}

	if !strings.Contains(report, `margherita\|docs`) {
		t.Errorf("report does not contain escaped set name for %q", pipeSet)
	}

	retrieveMs := headerValue(t, report, "retrieve_ms")
	if !regexp.MustCompile(`^off_mean=\d+ on_mean=\d+ \(n=3\)$`).MatchString(retrieveMs) {
		t.Errorf("retrieve_ms header = %q, want off_mean=<int> on_mean=<int> (n=3)", retrieveMs)
	}

	if forbidden := regexp.MustCompile(`(?i)better|worse|improve|good|bad|較好|較差|改善|好|差`); forbidden.MatchString(report) {
		t.Errorf("report contains conclusion word matched by %s", forbidden)
	}

	assertPipeCountsMatchHeader(t, report)
}

// TestRun_RendersMeanByK verifies REQ-04 / S-07: the report gains a
// "## Mean by k" table with rows for k=1, k=3, and the lane's own TopK
// (8 here), whose k=<TopK> row reproduces the main table's mean row for
// the six original/paraphrase recall cells (same hits/shuffle
// permutations already computed for the main table; no extra Retrieve).
func TestRun_RendersMeanByK(t *testing.T) {
	harness := newRunHarness(t, []harnessFixture{
		{name: "01-a.diff", terms: "tomato basil oven"},
		{name: "02-b.diff", terms: "standard audit policy"},
	}, true)
	lanesPath := filepath.Join(harness.repoRoot, "projects", "lanes.yaml")
	lanesData, err := os.ReadFile(lanesPath)
	if err != nil {
		t.Fatalf("ReadFile lanes.yaml: %v", err)
	}
	writeHarnessFile(t, lanesPath, strings.ReplaceAll(string(lanesData), "topK: 3", "topK: 8"))

	if err := Run(context.Background(), harness.options(embed.NewFixture(4))); err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := readReport(t, harness.reportPath)

	mainHeader, mainRows := parseMarkdownTable(report, "", 20)
	var mainMeanRow []string
	for _, row := range mainRows {
		if colByName(t, mainHeader, row, "fixture") == "mean" || colByName(t, mainHeader, row, "system") == "mean" {
			mainMeanRow = row
			break
		}
	}
	if mainMeanRow == nil {
		t.Fatal("main table has no mean row")
	}

	meanByKHeader, meanByKRows := parseMarkdownTable(report, "Mean by k", 7)
	if len(meanByKRows) != 3 {
		t.Fatalf("## Mean by k has %d rows, want 3 (k=1, k=3, k=8)", len(meanByKRows))
	}
	wantK := []string{"k=1", "k=3", "k=8"}
	var k8Row []string
	for i, row := range meanByKRows {
		if got := colByName(t, meanByKHeader, row, "k"); got != wantK[i] {
			t.Errorf("## Mean by k row %d k cell = %q, want %q", i, got, wantK[i])
		} else if got == "k=8" {
			k8Row = row
		}
	}
	if k8Row == nil {
		t.Fatal("## Mean by k has no k=8 row")
	}

	for _, column := range []string{
		"orig_recall_off", "orig_recall_shuf", "orig_recall_on",
		"para_recall_off", "para_recall_shuf", "para_recall_on",
	} {
		got := colByName(t, meanByKHeader, k8Row, column)
		want := colByName(t, mainHeader, mainMeanRow, column)
		if got != want {
			t.Errorf("## Mean by k k=8 row column %q = %q, want main table mean %q", column, got, want)
		}
	}
}

// TestRun_RendersDistractorsByCategory verifies REQ-04 / S-08: the report
// gains a "## Distractors by category" table with one row per fixed-order
// golden distractor category, aggregating OFF/shuffle/ON hit counts by
// category across the triples that declare that category.
func TestRun_RendersDistractorsByCategory(t *testing.T) {
	root := t.TempDir()
	projectsDir := filepath.Join(root, "projects")
	pizzaDir := filepath.Join(root, "corpus", "pizza")
	sharedDir := filepath.Join(root, "corpus", "shared")
	fixturesRoot := filepath.Join(root, "eval", "retrieval-fixtures")
	fixturesDir := filepath.Join(fixturesRoot, "margherita-pizza")
	for _, dir := range []string{projectsDir, filepath.Join(projectsDir, "margherita-pizza"), pizzaDir, sharedDir, fixturesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	writeHarnessFile(t, filepath.Join(projectsDir, "lanes.yaml"), `lanes:
  - id: spec-conformance
    enabled: true
    template: spec-conformance.tmpl.md
    intent: verify pizza specifications
    resources:
      sets: [margherita-pizza-docs]
      tags: []
    topK: 3
  - id: standards
    enabled: true
    template: standards.tmpl.md
    intent: verify shared standards
    resources:
      sets: [shared-standards]
      tags: []
    topK: 3
`)
	writeHarnessFile(t, filepath.Join(projectsDir, "resources.yaml"), `sets:
  - name: margherita-pizza-docs
    tags: [pizza]
    mode: retrieval
    paths: [corpus/pizza]
    include: ["*.md"]
  - name: shared-standards
    tags: [shared]
    mode: retrieval
    paths: [corpus/shared]
    include: ["*.md"]
`)
	// Corpus tuned (empirically verified via a real BuildPlan+Retrieve run,
	// not guessed) so OFF top-3 retrieves exactly one of the two "scope"
	// distractors (Crust Timing Note, not Extra Topping Note) for
	// spec-conformance, and the one "lexical" distractor (Delivery
	// Checklist) for standards.
	writeHarnessFile(t, filepath.Join(pizzaDir, "guide.md"), `# Pizza Manual

Overview of kitchen documentation structure and revision history.

## Tomato Basil Procedure

Tomato basil oven cheese standard audit policy searchable vocabulary.

## Oven Cheese Contract

Oven cheese tomato basil standard audit policy searchable vocabulary.

## Sauce Herb Guidance

Sauce herb baking dairy specification review guidance searchable vocabulary.

## Crust Timing Note

Crust timing preparation checklist searchable vocabulary crust.

## Extra Topping Note

Extra topping variety inventory searchable vocabulary.
`)
	writeHarnessFile(t, filepath.Join(sharedDir, "guide.md"), `# Shared Manual

Overview of shared program governance and revision history.

## Review Safety

Standard audit policy tomato basil oven cheese searchable vocabulary.

## Inspection Guardrails

Inspection controls governance tomato basil oven cheese searchable vocabulary.

## Delivery Checklist

Delivery checklist records searchable vocabulary delivery checklist.
`)

	const terms = "tomato basil oven cheese crust standard audit policy delivery checklist searchable vocabulary"
	writeHarnessFile(t, filepath.Join(fixturesDir, "01-x.diff"),
		fmt.Sprintf("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -0,0 +1 @@\n+%s\n", terms))

	golden := Golden{Entries: []Entry{
		{
			Fixture: "margherita-pizza/01-x.diff",
			Lane:    "spec-conformance",
			Relevant: []Target{
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Tomato Basil Procedure"},
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Oven Cheese Contract"},
			},
			Paraphrase: []Target{
				{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Sauce Herb Guidance"},
			},
			Distractors: []Distractor{
				{Target: Target{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Crust Timing Note"}, Category: "scope"},
				{Target: Target{Set: "margherita-pizza-docs", Path: "guide.md", Heading: "Pizza Manual > Extra Topping Note"}, Category: "scope"},
			},
		},
		{
			Fixture: "margherita-pizza/01-x.diff",
			Lane:    "standards",
			Relevant: []Target{
				{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Review Safety"},
			},
			Paraphrase: []Target{
				{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Inspection Guardrails"},
			},
			Distractors: []Distractor{
				{Target: Target{Set: "shared-standards", Path: "guide.md", Heading: "Shared Manual > Delivery Checklist"}, Category: "lexical"},
			},
		},
	}}
	goldenData, err := yaml.Marshal(golden)
	if err != nil {
		t.Fatalf("yaml.Marshal golden: %v", err)
	}
	goldenPath := filepath.Join(root, "eval", "retrieval-golden.yaml")
	if err := os.WriteFile(goldenPath, goldenData, 0o644); err != nil {
		t.Fatalf("WriteFile golden: %v", err)
	}

	registry, err := resources.Load(root, "margherita-pizza")
	if err != nil {
		t.Fatalf("resources.Load: %v", err)
	}
	storePath := filepath.Join(root, "store.sqlite")
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{
		OutputPath: storePath,
		Sets:       registry.Sets,
		Embedder:   embed.NewFixture(4),
		Progress:   io.Discard,
	}); err != nil {
		t.Fatalf("sqlite.Index: %v", err)
	}

	reportPath := filepath.Join(root, "eval", "RETRIEVAL.md")
	opts := Options{
		RepoRoot:    root,
		FixturesDir: fixturesRoot,
		GoldenPath:  goldenPath,
		StorePath:   storePath,
		ReportPath:  reportPath,
		Embedding: config.RAGEmbeddingConfig{
			Enabled:  true,
			Provider: "openai",
			Key:      "fixture-key",
		},
		Embedder: embed.NewFixture(4),
	}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := readReport(t, reportPath)

	header, rows := parseMarkdownTable(report, "Distractors by category", 5)
	if len(rows) != 5 {
		t.Fatalf("## Distractors by category has %d rows, want 5", len(rows))
	}
	wantCategories := []string{"scope", "version", "responsibility", "lexical", "neighbor"}
	byCategory := make(map[string][]string, len(rows))
	for i, row := range rows {
		category := colByName(t, header, row, "category")
		if category != wantCategories[i] {
			t.Errorf("## Distractors by category row %d category = %q, want %q (fixed order)", i, category, wantCategories[i])
		}
		byCategory[category] = row
	}

	scopeRow := byCategory["scope"]
	if scopeRow == nil {
		t.Fatal("## Distractors by category has no scope row")
	}
	if got := colByName(t, header, scopeRow, "n"); got != "2" {
		t.Errorf("scope row n = %q, want 2 (two golden scope distractors: Crust Timing Note, Extra Topping Note)", got)
	}
	if got := colByName(t, header, scopeRow, "distractors_off"); got != "1.00 (n=1)" {
		t.Errorf("scope row distractors_off = %q, want %q (1 triple declares scope distractors; OFF top-3 hits exactly Crust Timing Note, not Extra Topping Note)", got, "1.00 (n=1)")
	}

	lexicalRow := byCategory["lexical"]
	if lexicalRow == nil {
		t.Fatal("## Distractors by category has no lexical row")
	}
	if got := colByName(t, header, lexicalRow, "n"); got != "1" {
		t.Errorf("lexical row n = %q, want 1 (one golden lexical distractor: Delivery Checklist)", got)
	}
	if got := colByName(t, header, lexicalRow, "distractors_off"); got != "1.00 (n=1)" {
		t.Errorf("lexical row distractors_off = %q, want %q (1 triple declares a lexical distractor; OFF top-3 hits it)", got, "1.00 (n=1)")
	}

	for _, category := range []string{"version", "responsibility", "neighbor"} {
		row := byCategory[category]
		if row == nil {
			t.Fatalf("## Distractors by category has no %s row", category)
		}
		for _, column := range []string{"distractors_off", "distractors_shuf", "distractors_on"} {
			if got := colByName(t, header, row, column); got != "- (n=0)" {
				t.Errorf("%s row %s = %q, want %q (no golden distractors of this category)", category, column, got, "- (n=0)")
			}
		}
	}
}
