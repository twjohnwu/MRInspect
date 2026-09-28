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
	fixturesDir := filepath.Join(root, "eval", "fixtures")
	pizzaDir := filepath.Join(root, "corpus", "pizza")
	sharedDir := filepath.Join(root, "corpus", "shared")
	for _, dir := range []string{projectsDir, fixturesDir, pizzaDir, sharedDir} {
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
				Fixture: fixture.name,
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
				Fixture: fixture.name,
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
		System:      "margherita-pizza",
		FixturesDir: h.fixturesDir,
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
		names[i] = fixtures[i].Name
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
	if err := golden.ValidateAgainstPlan(plan.Triples); err != nil {
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
		if len(parts) != 19 {
			continue
		}
		switch parts[0] {
		case "fixture":
			continue
		case "mean":
			mean = parts
		default:
			if strings.Trim(parts[0], "-: ") != "" {
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
		if _, ok := wantSets[row[2]]; !ok {
			t.Errorf("report contains row for non-lane set %q", row[2])
			continue
		}
		wantSets[row[2]] = true
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

	wantTableHeader := "| fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |"
	if !strings.Contains(report, wantTableHeader) {
		t.Errorf("report missing table header %q", wantTableHeader)
	}
	rows, mean := tableRows(t, report)
	if want := len(harness.fixtures) * 2; len(rows) != want {
		t.Fatalf("report has %d data rows, want %d", len(rows), want)
	}
	for rowIndex, row := range rows {
		for _, column := range []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 17} {
			requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
		}
		for _, column := range []int{16, 18} {
			requireInteger(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
		}
	}
	for _, column := range []int{6, 9, 12, 15, 18} {
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
			for _, column := range []int{4, 5, 7, 8, 10, 11, 13, 14, 17} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[16], fmt.Sprintf("row %d distractors_off", rowIndex+1))
			if rowIndex == 2 {
				for _, column := range []int{6, 9, 12, 15, 18} {
					if row[column] != "degraded: embed-call-failed" {
						t.Errorf("row 3 ON cell = %q, want degraded: embed-call-failed", row[column])
					}
				}
				continue
			}
			for _, column := range []int{6, 9, 12, 15} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[18], fmt.Sprintf("row %d distractors_on", rowIndex+1))
		}
		for _, column := range []int{6, 9, 12, 15, 18} {
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
			for _, column := range []int{4, 5, 7, 8, 10, 11, 13, 14, 17} {
				requireNumeric(t, row[column], fmt.Sprintf("row %d column %d", rowIndex+1, column+1))
			}
			requireInteger(t, row[16], fmt.Sprintf("row %d distractors_off", rowIndex+1))
			for _, column := range []int{6, 9, 12, 15, 18} {
				if row[column] != "degraded: no-vectors" {
					t.Errorf("row %d ON cell = %q, want degraded: no-vectors", rowIndex+1, row[column])
				}
			}
		}
		for _, column := range []int{6, 9, 12, 15, 18} {
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
	const tableHeader = "| fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |"
	if got := strings.Count(report, tableHeader); got != 1 {
		t.Errorf("table header count = %d, want 1", got)
	}
	if !strings.Contains(report, `| pipe\|set |`) {
		t.Error(`report does not contain escaped set name "pipe\|set"`)
	}
	if got := headerValue(t, report, "pool"); !strings.Contains(got, "shuffle=") {
		t.Errorf("pool = %q, want it to contain shuffle=", got)
	}
	meanRows := 0
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "| mean |") {
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
		if row[2] == "pipe|set" {
			degraded = row
			break
		}
	}
	if degraded == nil {
		t.Fatal("report has no row for pipe|set")
	}
	for _, column := range []int{6, 9, 12, 15, 18} {
		if degraded[column] != "degraded: embed-call-failed" {
			t.Errorf("degraded row column %d = %q, want degraded: embed-call-failed", column+1, degraded[column])
		}
	}
	for _, column := range []int{4, 5, 7, 8, 10, 11, 13, 14, 16, 17} {
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
