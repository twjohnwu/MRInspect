package retrievaleval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	System      string
	FixturesDir string
	GoldenPath  string
	StorePath   string
	ReportPath  string
	Embedding   config.RAGEmbeddingConfig
	Embedder    embed.Embedder
}

// Run executes the retrieval-quality evaluation harness.
func Run(ctx context.Context, opts Options) error {
	fixtures, err := evalrun.LoadFixtures(
		opts.FixturesDir,
		logger.NewWithWriter(slog.LevelError, "", io.Discard),
	)
	if err != nil {
		return errors.New("load retrieval fixtures failed")
	}

	fixtureNames := make([]string, len(fixtures))
	for i := range fixtures {
		fixtureNames[i] = fixtures[i].Name
	}
	golden, err := LoadGolden(opts.GoldenPath, fixtureNames)
	if err != nil {
		return errors.New("load retrieval golden failed")
	}
	plan, err := BuildPlan(opts.RepoRoot, opts.System, fixtures)
	if err != nil {
		return errors.New("build retrieval plan failed")
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
				"plan: golden lane %q resolved to no resource set for fixture %q (check lanes overlay for system %q)",
				entry.Lane,
				entry.Fixture,
				opts.System,
			)
		}
	}

	registry, err := resources.Load(opts.RepoRoot, opts.System)
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
		offResult, err := off.Retrieve(ctx, query)
		if err != nil {
			return errors.New("retrieval OFF query failed")
		}
		if len(offResult.Degraded) != 0 {
			return errors.New("retrieval OFF store degraded")
		}
		onResult, err := on.Retrieve(ctx, query)
		if err != nil {
			return errors.New("retrieval ON query failed")
		}

		relevant := relevantTargets(golden, triple.Fixture, triple.LaneID, triple.Set.Name)
		recallOff, mrrOff := Score(offResult.Chunks, relevant, triple.K)
		row := Row{
			Fixture:   triple.Fixture,
			Lane:      triple.LaneID,
			Set:       triple.Set.Name,
			K:         triple.K,
			RecallOff: Cell{Value: recallOff},
			MRROff:    Cell{Value: mrrOff},
		}
		if len(onResult.Degraded) != 0 {
			code, ok := parseRerankDegradation(onResult.Degraded)
			if !ok {
				return errors.New("retrieval ON store degraded")
			}
			row.RecallOn.Degraded = code
			row.MRROn.Degraded = code
		} else {
			row.RecallOn.Value, row.MRROn.Value = Score(onResult.Chunks, relevant, triple.K)
		}
		rows = append(rows, row)
	}

	if err := writeReport(opts.ReportPath, header, rows); err != nil {
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

func relevantTargets(golden Golden, fixture, lane, set string) []Target {
	var targets []Target
	for _, entry := range golden.Entries {
		if entry.Fixture != fixture || entry.Lane != lane {
			continue
		}
		for _, target := range entry.Relevant {
			if target.Set == set {
				targets = append(targets, target)
			}
		}
	}
	return targets
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

func writeReport(path string, header Header, rows []Row) (err error) {
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

	if err = Render(temporary, header, rows); err != nil {
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
