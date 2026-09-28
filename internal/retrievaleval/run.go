package retrievaleval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/evalrun"
	"mrinspect/internal/logger"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/embed"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

// Options configures one retrieval-quality evaluation run.
type Options struct {
	RepoRoot    string
	FixturesDir string
	GoldenPath  string
	StorePath   string
	ReportPath  string
	Embedding   config.RAGEmbeddingConfig
	Embedder    embed.Embedder
	Progress    io.Writer
	RetryWait   func(ctx context.Context, d time.Duration) error
}

type systemFixtures struct {
	name     string
	fixtures []evalrun.Fixture
}

var validSystemName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// isPlainSystemDir reports whether path is a non-symlink directory (via
// Lstat, so a symlinked directory is rejected) whose base name is a valid
// system name.
func isPlainSystemDir(path, name string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsDir() && validSystemName.MatchString(name)
}

// isDirectory reports whether path exists and is a directory, following
// symlinks.
func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func loadSystems(fixturesDir, repoRoot string, log *logger.Logger) ([]systemFixtures, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, errors.New("load retrieval fixtures: could not read fixtures directory")
	}

	systems := make([]systemFixtures, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !isPlainSystemDir(filepath.Join(fixturesDir, name), name) {
			return nil, fmt.Errorf(
				"load retrieval fixtures: entry %q is not a plain directory with a valid system name",
				name,
			)
		}
		if !isDirectory(filepath.Join(repoRoot, "projects", name)) {
			return nil, fmt.Errorf("load retrieval fixtures: system %q has no projects directory", name)
		}

		fixtures, err := evalrun.LoadFixtures(filepath.Join(fixturesDir, name), log)
		if err != nil {
			if !strings.ContainsAny(err.Error(), `/\`) {
				return nil, fmt.Errorf("load retrieval fixtures: system %q: %w", name, err)
			}
			return nil, errors.New("load retrieval fixtures failed")
		}
		systems = append(systems, systemFixtures{name: name, fixtures: fixtures})
	}

	if len(systems) == 0 {
		return nil, errors.New("load retrieval fixtures: no system directories found")
	}
	return systems, nil
}

// Run executes the retrieval-quality evaluation harness.
func Run(ctx context.Context, opts Options) error {
	if opts.FixturesDir == "" {
		opts.FixturesDir = "eval/retrieval-fixtures"
	}
	systems, err := loadSystems(
		opts.FixturesDir,
		opts.RepoRoot,
		logger.NewWithWriter(slog.LevelError, "", io.Discard),
	)
	if err != nil {
		return err
	}

	var plan []Triple
	var warnings []string
	var fixtureNames []string
	for _, system := range systems {
		builtPlan, err := BuildPlan(opts.RepoRoot, system.name, system.fixtures)
		if err != nil {
			return errors.New("build retrieval plan failed")
		}
		warnings = append(warnings, builtPlan.Warnings...)
		for _, triple := range builtPlan.Triples {
			triple.Fixture = system.name + "/" + triple.Fixture
			plan = append(plan, triple)
		}
		for _, fixture := range system.fixtures {
			fixtureNames = append(fixtureNames, system.name+"/"+fixture.Name)
		}
	}
	if opts.Progress != nil {
		for _, warning := range warnings {
			_, _ = fmt.Fprintln(opts.Progress, warning)
		}
	}
	golden, err := LoadGolden(opts.GoldenPath, fixtureNames)
	if err != nil {
		return errors.New("load retrieval golden failed")
	}
	type fixtureLane struct {
		fixture string
		lane    string
	}
	planned := make(map[fixtureLane]struct{}, len(plan))
	for _, triple := range plan {
		planned[fixtureLane{fixture: triple.Fixture, lane: triple.LaneID}] = struct{}{}
	}
	for _, entry := range golden.Entries {
		if _, ok := planned[fixtureLane{fixture: entry.Fixture, lane: entry.Lane}]; !ok {
			return fmt.Errorf(
				"plan: golden lane %q resolved to no resource set for fixture %q (check lanes overlay)",
				entry.Lane,
				entry.Fixture,
			)
		}
	}

	registry, err := resources.Load(opts.RepoRoot, systems[0].name)
	if err != nil {
		return errors.New("load retrieval resources failed")
	}
	fingerprint, err := sqlite.ResourcesFingerprint(registry.Sets)
	if err != nil {
		return errors.New("fingerprint retrieval resources failed")
	}
	meta, err := sqlite.ReadMeta(ctx, opts.StorePath)
	if err != nil {
		return errors.New("read retrieval store metadata failed")
	}
	if fingerprint != meta.ResourcesSHA256 {
		return errors.New("store is stale; rerun mrinspect index")
	}
	if err := golden.ValidateAgainstPlan(plan); err != nil {
		return errors.New("validate retrieval golden failed")
	}
	if err := golden.ValidateAgainstStore(ctx, opts.StorePath); err != nil {
		return errors.New("validate retrieval golden against store failed")
	}

	header := Header{
		BuiltAt:      meta.BuiltAt,
		ResourcesSHA: meta.ResourcesSHA256,
		EmbedModel:   meta.EmbedModel,
		Pool:         reportPool,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateHeader(header); err != nil {
		return err
	}

	queryEmbedder, embedderErr := evaluationEmbedder(opts)
	if queryEmbedder != nil {
		queryEmbedder = embed.WithRateLimitRetry(queryEmbedder, embed.RetryOptions{
			Wait: opts.RetryWait,
			OnRetry: func(attempt int, delay time.Duration) {
				if opts.Progress != nil {
					fmt.Fprintf(opts.Progress, "embedding rate limited (HTTP 429); retrying in %ds\n", int(delay.Seconds()))
				}
			},
		})
	}
	// An injected embedder is a complete test/local dependency and does not
	// require an otherwise-unused remote API key.
	keyPresent := queryEmbedder != nil || opts.Embedding.Key != ""
	sets := distinctPlanSets(plan)
	off, err := sqlite.OpenRetriever(
		opts.StorePath,
		sets,
		sqlite.WithReadOnly(),
		sqlite.WithEmbeddingConfig(false, keyPresent),
	)
	if err != nil {
		return errors.New("open retrieval OFF store failed")
	}
	defer off.Close()

	onOptions := []sqlite.RetrieverOption{
		sqlite.WithReadOnly(),
		sqlite.WithEmbeddingConfig(true, keyPresent),
	}
	if queryEmbedder != nil {
		onOptions = append(onOptions, sqlite.WithEmbedder(queryEmbedder))
	} else if embedderErr != nil {
		onOptions = append(onOptions, sqlite.WithEmbedderError(embedderErr))
	}
	on, err := sqlite.OpenRetriever(opts.StorePath, sets, onOptions...)
	if err != nil {
		return errors.New("open retrieval ON store failed")
	}
	defer on.Close()

	rows := make([]Row, 0, len(plan))
	for _, triple := range plan {
		query := rag.Query{
			Terms:  triple.Terms,
			SetRef: triple.Set.Name,
			Intent: triple.LaneID,
			TopK:   triple.K,
		}
		offStarted := time.Now()
		offResult, err := off.Retrieve(ctx, query)
		offMs := time.Since(offStarted).Milliseconds()
		if err != nil {
			return errors.New("retrieval OFF query failed")
		}
		if len(offResult.Degraded) != 0 {
			return errors.New("retrieval OFF store degraded")
		}
		poolQuery := query
		poolQuery.TopK = 4 * triple.K
		poolResult, err := off.Retrieve(ctx, poolQuery)
		if err != nil {
			return errors.New("retrieval shuffle pool query failed")
		}
		if len(poolResult.Degraded) != 0 {
			return errors.New("retrieval shuffle pool store degraded")
		}
		onStarted := time.Now()
		onResult, err := on.Retrieve(ctx, query)
		onMs := time.Since(onStarted).Milliseconds()
		if err != nil {
			return errors.New("retrieval ON query failed")
		}

		targets := scoringTargetsFor(golden, triple.Fixture, triple.LaneID, triple.Set.Name)
		offScores := scoreRetrievedArm(offResult.Chunks, targets, triple.K)
		shufScores := scoreShuffleArm(poolResult.Chunks, targets, triple.K)
		system, bareFixture, found := strings.Cut(triple.Fixture, "/")
		if !found {
			system = ""
			bareFixture = triple.Fixture
		}
		row := Row{
			System:     system,
			Fixture:    bareFixture,
			Lane:       triple.LaneID,
			Set:        triple.Set.Name,
			K:          triple.K,
			RecallByK:  make(map[int]RecallByK),
			Categories: make(map[string]Triplet),
			OffMs:      offMs,
			OnMs:       onMs,
		}
		for index := range row.Metrics {
			integer := index == metricDistractors
			row.Metrics[index].Off = Cell{Value: offScores[index], Integer: integer}
			row.Metrics[index].Shuf = Cell{Value: shufScores[index]}
		}
		var degradation string
		if len(onResult.Degraded) != 0 {
			code, ok := parseRerankDegradation(onResult.Degraded)
			if !ok {
				return errors.New("retrieval ON store degraded")
			}
			for index := range row.Metrics {
				row.Metrics[index].On = Cell{Degraded: code, Integer: index == metricDistractors}
			}
			degradation = code
		} else {
			onScores := scoreRetrievedArm(onResult.Chunks, targets, triple.K)
			for index := range row.Metrics {
				row.Metrics[index].On = Cell{Value: onScores[index], Integer: index == metricDistractors}
			}
		}

		for _, k := range []int{1, 3, triple.K} {
			origOff, _ := Score(offResult.Chunks, targets.original, k)
			origShuf, _ := ShuffleScore(poolResult.Chunks, targets.original, k, DefaultShuffleSeeds)
			paraOff, _ := Score(offResult.Chunks, targets.paraphrase, k)
			paraShuf, _ := ShuffleScore(poolResult.Chunks, targets.paraphrase, k, DefaultShuffleSeeds)
			row.RecallByK[k] = RecallByK{
				Orig: Triplet{
					Off:  Cell{Value: origOff},
					Shuf: Cell{Value: origShuf},
					On:   onCell(degradation, false, func() float64 { v, _ := Score(onResult.Chunks, targets.original, k); return v }),
				},
				Para: Triplet{
					Off:  Cell{Value: paraOff},
					Shuf: Cell{Value: paraShuf},
					On:   onCell(degradation, false, func() float64 { v, _ := Score(onResult.Chunks, targets.paraphrase, k); return v }),
				},
			}
		}

		for category, categoryTargets := range targets.categories {
			row.Categories[category] = Triplet{
				Off:  Cell{Value: float64(Distractors(offResult.Chunks, categoryTargets, triple.K)), Integer: true},
				Shuf: Cell{Value: ShuffleDistractors(poolResult.Chunks, categoryTargets, triple.K, DefaultShuffleSeeds)},
				On:   onCell(degradation, true, func() float64 { return float64(Distractors(onResult.Chunks, categoryTargets, triple.K)) }),
			}
		}
		rows = append(rows, row)
	}

	var offTotal int64
	var onTotal int64
	onCount := 0
	for _, row := range rows {
		offTotal += row.OffMs
		if row.Metrics[metricOrigRecall].On.Degraded == "" {
			onTotal += row.OnMs
			onCount++
		}
	}
	offMean := int64(0)
	if len(rows) != 0 {
		offMean = int64(math.Round(float64(offTotal) / float64(len(rows))))
	}
	if onCount == 0 {
		header.RetrieveMs = fmt.Sprintf("off_mean=%d on_mean=- (n=0)", offMean)
	} else {
		onMean := int64(math.Round(float64(onTotal) / float64(onCount)))
		header.RetrieveMs = fmt.Sprintf("off_mean=%d on_mean=%d (n=%d)", offMean, onMean, onCount)
	}
	topK := 0
	for _, triple := range plan {
		if triple.K > topK {
			topK = triple.K
		}
	}
	if err := writeReport(opts.ReportPath, header, rows, topK, golden.CategoryCounts()); err != nil {
		return errors.New("write retrieval report failed")
	}
	return nil
}

func distinctPlanSets(plan []Triple) []resources.Set {
	sets := make([]resources.Set, 0)
	seen := make(map[string]struct{})
	for _, triple := range plan {
		if _, exists := seen[triple.Set.Name]; exists {
			continue
		}
		seen[triple.Set.Name] = struct{}{}
		sets = append(sets, triple.Set)
	}
	return sets
}

func evaluationEmbedder(opts Options) (embed.Embedder, error) {
	if opts.Embedder != nil {
		return opts.Embedder, nil
	}
	if !opts.Embedding.Enabled {
		return nil, nil
	}
	return embed.New(opts.Embedding.Provider, opts.Embedding.Key)
}

type scoringTargets struct {
	original    []Target
	paraphrase  []Target
	distractors []Target
	categories  map[string][]Target
}

func scoringTargetsFor(golden Golden, fixture, lane, set string) scoringTargets {
	for _, entry := range golden.Entries {
		if entry.Fixture != fixture || entry.Lane != lane {
			continue
		}
		categories := make(map[string][]Target)
		for _, distractor := range entry.Distractors {
			if _, exists := categories[distractor.Category]; !exists {
				categories[distractor.Category] = nil
			}
			if distractor.Set == set {
				categories[distractor.Category] = append(categories[distractor.Category], distractor.Target)
			}
		}
		return scoringTargets{
			original:    targetsInSet(entry.Relevant, set),
			paraphrase:  targetsInSet(entry.Paraphrase, set),
			distractors: targetsInSet(distractorTargets(entry.Distractors), set),
			categories:  categories,
		}
	}
	return scoringTargets{}
}

func targetsInSet(candidates []Target, set string) []Target {
	targets := make([]Target, 0, len(candidates))
	for _, target := range candidates {
		if target.Set == set {
			targets = append(targets, target)
		}
	}
	return targets
}

type armScores [metricCount]float64

func scoreRetrievedArm(hits []rag.Chunk, targets scoringTargets, k int) armScores {
	var scores armScores
	scores[metricOrigRecall], scores[metricOrigMRR] = Score(hits, targets.original, k)
	scores[metricParaRecall], scores[metricParaMRR] = Score(hits, targets.paraphrase, k)
	scores[metricDistractors] = float64(Distractors(hits, targets.distractors, k))
	return scores
}

func scoreShuffleArm(pool []rag.Chunk, targets scoringTargets, k int) armScores {
	var scores armScores
	scores[metricOrigRecall], scores[metricOrigMRR] = ShuffleScore(pool, targets.original, k, DefaultShuffleSeeds)
	scores[metricParaRecall], scores[metricParaMRR] = ShuffleScore(pool, targets.paraphrase, k, DefaultShuffleSeeds)
	scores[metricDistractors] = ShuffleDistractors(pool, targets.distractors, k, DefaultShuffleSeeds)
	return scores
}

// onCell renders an ON-arm cell that degrades uniformly with the row's ON
// arm (a rerank failure, an embed-call failure, missing vectors): if
// degradation is non-empty, compute is never called and the cell just
// carries the same degradation code as the row's main On cells; otherwise
// compute produces the cell's value. Shared by the main table, "## Mean by
// k", and "## Distractors by category" On cells, which all follow this same
// degrade-or-compute shape.
func onCell(degradation string, integer bool, compute func() float64) Cell {
	if degradation != "" {
		return Cell{Degraded: degradation, Integer: integer}
	}
	return Cell{Value: compute(), Integer: integer}
}

func parseRerankDegradation(reasons []string) (string, bool) {
	const prefix = "rerank degraded: "
	var code string
	for _, reason := range reasons {
		if !strings.HasPrefix(reason, prefix) {
			return "", false
		}
		remainder := strings.TrimPrefix(reason, prefix)
		end := strings.Index(remainder, " (")
		if end <= 0 {
			return "", false
		}
		parsed := remainder[:end]
		if code != "" && parsed != code {
			return "", false
		}
		code = parsed
	}
	return code, code != ""
}

func writeReport(path string, header Header, rows []Row, topK int, categoryCounts map[string]int) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err = Render(temporary, header, rows, topK, categoryCounts); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
