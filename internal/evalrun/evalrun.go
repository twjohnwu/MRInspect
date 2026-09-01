package evalrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/ragwire"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/validator"
)

var ErrNoValidFixtures = errors.New("no valid fixtures")

const (
	maxFixtureSize      = 1 << 20
	fixtureHeaderPrefix = "# mrinspect-fixture:"
)

type Fixture struct {
	Name    string
	Diff    []byte
	Changes []gitlab.Change
}

type runOptions struct {
	progressWriter io.Writer
}

// RunOption customizes eval orchestration behavior.
type RunOption func(*runOptions)

// WithProgressWriter directs live eval progress to writer.
func WithProgressWriter(writer io.Writer) RunOption {
	return func(options *runOptions) {
		if writer != nil {
			options.progressWriter = writer
		}
	}
}

func newRunOptions(options ...RunOption) runOptions {
	runOptions := runOptions{progressWriter: os.Stderr}
	for _, option := range options {
		option(&runOptions)
	}
	return runOptions
}

// ReviewerFactory constructs the independently configured reviewer for a mode-run.
type ReviewerFactory func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error)

// ModeResult records one mode-run outcome or its isolated failure.
type ModeResult struct {
	Mode    reviewer.EvalMode
	Outcome reviewer.EvalOutcome
	Err     error
}

// Report is the complete input needed to render one qualitative eval report.
type Report struct {
	GeneratedAt time.Time
	Provider    string
	Model       string
	Fixtures    []FixtureReport
}

// FixtureReport groups all mode-runs belonging to one fixture.
type FixtureReport struct {
	Fixture Fixture
	Modes   []ModeReport
}

// ModeReport carries one result together with its writer-captured prompt logs
// and independently collected token metrics.
type ModeReport struct {
	Result          ModeResult
	PromptBreakdown string
	Metrics         logger.Metrics
}

// UsageSummary is the known token lower bound for one eval invocation.
type UsageSummary struct {
	TotalTokens       int64
	UsageUnknownCalls int
}

// WriteReport atomically renders report to path.
func WriteReport(path string, report Report) error {
	var rendered strings.Builder
	rendered.WriteString("# MRInspect Review Quality Evaluation\n\n")
	fmt.Fprintf(&rendered, "Generated: %s\n\n", report.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&rendered, "Provider: `%s`\n\n", report.Provider)
	fmt.Fprintf(&rendered, "Model: `%s`\n\n", report.Model)
	rendered.WriteString("Fixtures: ")
	fixtureNames := make([]string, 0, len(report.Fixtures))
	for _, fixtureReport := range report.Fixtures {
		fixtureNames = append(fixtureNames, "`"+fixtureReport.Fixture.Name+"`")
	}
	rendered.WriteString(strings.Join(fixtureNames, ", "))
	rendered.WriteString("\n\n")

	for _, fixtureReport := range report.Fixtures {
		fmt.Fprintf(&rendered, "## %s\n\n", fixtureReport.Fixture.Name)
		var subtotal int64
		unknownCalls := 0
		for _, modeReport := range fixtureReport.Modes {
			fmt.Fprintf(&rendered, "### %s\n\n", modeReport.Result.Mode)
			if modeReport.Result.Err != nil {
				fullErr := rootCause(modeReport.Result.Err)
				leafErr := innermostCause(modeReport.Result.Err)
				fmt.Fprintf(&rendered, "Mode failed: %s\n", leafErr)
				if fullErr.Error() != leafErr.Error() {
					fmt.Fprintf(&rendered, "Failure context: %s\n", fullErr)
				}
				rendered.WriteString("\n")
			} else {
				rendered.WriteString(strings.TrimSpace(modeReport.Result.Outcome.ReviewText))
				rendered.WriteString("\n\n")
				if modeReport.Result.Mode == reviewer.EvalModeReflect {
					switch {
					case !modeReport.Result.Outcome.ReflectApplied:
						rendered.WriteString("> reflection not applied (degraded)\n\n")
					case modeReport.Result.Outcome.ReflectChanged:
						rendered.WriteString("> reflection applied, review rewritten\n\n")
					default:
						rendered.WriteString("> reflection applied, review unchanged (validated)\n\n")
					}
				}
				for _, breakdown := range promptBreakdowns(modeReport.PromptBreakdown) {
					rendered.WriteString(breakdown)
					rendered.WriteString("\n\n")
				}
			}

			modeTokens, modeUnknown := summarizeMetrics(modeReport.Metrics)
			subtotal += modeTokens
			unknownCalls += modeUnknown
		}

		if unknownCalls > 0 {
			fmt.Fprintf(&rendered, "Token subtotal: ≥%d\n\n", subtotal)
		} else {
			fmt.Fprintf(&rendered, "Token subtotal: %d\n\n", subtotal)
		}
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(rendered.String()), 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write report temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}

// SummarizeBudget logs usage and the optional MRI_DAILY_TOKEN_BUDGET comparison.
func SummarizeBudget(log *logger.Logger, usage UsageSummary) error {
	knownTokens := usage.TotalTokens
	if knownTokens < 0 {
		knownTokens = 0
	}
	usageText := strconv.FormatInt(knownTokens, 10)
	if usage.UsageUnknownCalls > 0 {
		usageText = "≥" + usageText
	}

	rawBudget, configured := os.LookupEnv("MRI_DAILY_TOKEN_BUDGET")
	trimmedBudget := strings.TrimSpace(rawBudget)
	if !configured || trimmedBudget == "" || trimmedBudget == "0" {
		if log != nil {
			log.Info("eval token usage", "usage", usageText, "usageUnknownCalls", usage.UsageUnknownCalls)
		}
		return nil
	}

	budget, err := strconv.ParseUint(trimmedBudget, 10, 64)
	if err != nil || budget == 0 {
		if log != nil {
			log.Warn("invalid MRI_DAILY_TOKEN_BUDGET; budget comparison disabled", "value", rawBudget)
			log.Info("eval token usage", "usage", usageText, "usageUnknownCalls", usage.UsageUnknownCalls)
		}
		return nil
	}

	percent := uint64(float64(knownTokens) / float64(budget) * 100)
	line := fmt.Sprintf("%s / %d (%d%%)", usageText, budget, percent)
	if log != nil {
		if uint64(knownTokens) > budget {
			log.Warn("eval token budget exceeded", "summary", line, "usage", usageText, "budget", budget, "percent", fmt.Sprintf("%d%%", percent))
		} else {
			log.Info("eval token budget", "summary", line, "usage", usageText, "budget", budget, "percent", fmt.Sprintf("%d%%", percent))
		}
	}
	return nil
}

// CIGuard refuses accidental eval execution in CI unless explicitly opted in.
func CIGuard() error {
	if os.Getenv("CI") == "true" && os.Getenv("MRI_EVAL_ALLOW_CI") != "true" {
		return errors.New("evaluation is disabled in CI; set MRI_EVAL_ALLOW_CI=true to opt in")
	}
	return nil
}

func rootCause(err error) error {
	return err
}

func innermostCause(err error) error {
	for {
		cause := errors.Unwrap(err)
		if cause == nil {
			return err
		}
		err = cause
	}
}

func promptBreakdowns(captured string) []string {
	decoder := json.NewDecoder(strings.NewReader(captured))
	var breakdowns []string
	for {
		var record struct {
			Message string `json:"msg"`
		}
		if err := decoder.Decode(&record); err != nil {
			break
		}
		if strings.Contains(record.Message, "| Section | Tokens | % of total |") {
			breakdowns = append(breakdowns, strings.TrimSpace(record.Message))
		}
	}
	if len(breakdowns) > 0 {
		return breakdowns
	}
	if strings.Contains(captured, "| Section | Tokens | % of total |") {
		return []string{strings.TrimSpace(captured)}
	}
	return nil
}

func summarizeMetrics(metrics logger.Metrics) (int64, int) {
	var total int64
	for _, call := range metrics.APICalls {
		if call.Usage != nil {
			total += call.Usage.InputTokens + call.Usage.OutputTokens
		}
	}
	return total, metrics.UsageUnknownCalls
}

// RunModes executes each requested mode with a freshly constructed reviewer.
// A mode-local construction or review failure is recorded without stopping
// subsequent modes.
func RunModes(ctx context.Context, fixture Fixture, modes []reviewer.EvalMode, factory ReviewerFactory) []ModeResult {
	results := make([]ModeResult, 0, len(modes))
	input := reviewer.EvalInput{
		Diff:    string(fixture.Diff),
		Changes: fixture.Changes,
		Title:   fixture.Name,
	}
	for _, mode := range modes {
		result := ModeResult{Mode: mode}
		r, err := factory(mode)
		if err != nil {
			result.Err = err
			results = append(results, result)
			continue
		}
		if r == nil {
			result.Err = errors.New("reviewer factory returned nil reviewer")
			results = append(results, result)
			continue
		}
		result.Outcome, result.Err = r.RunForEval(ctx, mode, input)
		results = append(results, result)
	}
	return results
}

func LoadFixtures(dir string, log *logger.Logger) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if isFixtureName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	fixtures := make([]Fixture, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			warnSkippedFixture(log, name, "inspect fixture", err)
			continue
		}
		if !info.Mode().IsRegular() {
			warnSkippedFixture(log, name, "fixture is not a regular file", nil)
			continue
		}
		if info.Size() > maxFixtureSize {
			warnSkippedFixture(log, name, "fixture exceeds 1 MiB", nil)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			warnSkippedFixture(log, name, "read fixture", err)
			continue
		}
		if len(data) == 0 {
			warnSkippedFixture(log, name, "fixture is empty", nil)
			continue
		}
		if len(data) > maxFixtureSize {
			warnSkippedFixture(log, name, "fixture exceeds 1 MiB", nil)
			continue
		}
		if !utf8.Valid(data) {
			warnSkippedFixture(log, name, "fixture is not valid UTF-8", nil)
			continue
		}

		fixtures = append(fixtures, Fixture{
			Name:    name,
			Diff:    data,
			Changes: synthesizeChanges(data),
		})
	}

	if len(fixtures) == 0 {
		return fixtures, ErrNoValidFixtures
	}
	return fixtures, nil
}

func Run(fixturesDir, reportPath string, log *logger.Logger, options ...RunOption) error {
	fixtures, err := LoadFixtures(fixturesDir, log)
	if err != nil {
		return err
	}
	cfg, err := config.LoadForEval()
	if err != nil {
		return err
	}
	return runLoaded(context.Background(), fixtures, reportPath, cfg, log, newRunOptions(options...))
}

// RunWithConfig executes the offline eval workflow with command-loaded config.
func RunWithConfig(ctx context.Context, fixturesDir, reportPath string, cfg config.Config, log *logger.Logger, options ...RunOption) error {
	fixtures, err := LoadFixtures(fixturesDir, log)
	if err != nil {
		return err
	}
	return runLoaded(ctx, fixtures, reportPath, cfg, log, newRunOptions(options...))
}

func runLoaded(ctx context.Context, fixtures []Fixture, reportPath string, cfg config.Config, log *logger.Logger, options runOptions) error {
	ragwire.RegisterBuiltinBackends()
	repoRoot := "."
	resourceRegistry, err := resources.Load(repoRoot, "")
	if err != nil && log != nil {
		log.Warn("failed to load RAG resource sets", "error", err)
	}
	modelLimits, err := prompt.ModelLimitsFromEnv(cfg.ModelLimits)
	if err != nil {
		return fmt.Errorf("model limits configuration: %w", err)
	}

	modes := []reviewer.EvalMode{
		reviewer.EvalModeSingle,
		reviewer.EvalModeMulti,
		reviewer.EvalModeReflect,
	}
	fixtureReports := make([]FixtureReport, 0, len(fixtures))
	usage := UsageSummary{}
	wallStarted := time.Now()
	okModes := 0
	failedModes := 0
	defer func() {
		fmt.Fprintf(options.progressWriter, "Totals: %d modes ok, %d failed (%.1fs wall)\n", okModes, failedModes, time.Since(wallStarted).Seconds())
	}()
	metricsBase, persistModeMetrics := os.LookupEnv("AI_REVIEW_METRICS_FILE")
	persistModeMetrics = persistModeMetrics && strings.TrimSpace(metricsBase) != ""

	for fixtureIndex, fixture := range fixtures {
		fmt.Fprintf(options.progressWriter, "[%d/%d] %s\n", fixtureIndex+1, len(fixtures), fixture.Name)
		var runLogs []*logger.Logger
		var promptLogs []*bytes.Buffer
		var closers []io.Closer
		factory := func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			modeCfg := cfg
			modeCfg.SelfReflection = mode == reviewer.EvalModeReflect
			promptLog := &bytes.Buffer{}
			metricsFile := ""
			if persistModeMetrics {
				metricsFile = fmt.Sprintf("%s.%s.%s.json", metricsBase, fixture.Name, mode)
			}
			runLog := logger.NewWithWriter(slog.LevelDebug, metricsFile, promptLog)
			promptLogs = append(promptLogs, promptLog)
			runLogs = append(runLogs, runLog)

			provider, err := ai.NewProvider(modeCfg, runLog)
			if err != nil {
				return nil, err
			}
			v := validator.New(modeCfg)
			gitlabClient := gitlab.NewClient(modeCfg, runLog)
			promptComposer := prompt.NewComposer()
			r := reviewer.New(
				modeCfg,
				gitlabClient,
				provider,
				nil,
				project.NewLoader(modeCfg.Projects),
				promptComposer,
				v,
				mrerrors.NewHandler(modeCfg, runLog),
				runLog,
			)
			productionRAG := ragwire.NewProductionReviewDependencies(ragwire.ReviewPathConfig{
				ResolverConfig: rag.DefaultResolverConfig(),
				ResourceSets:   resourceRegistry.Sets,
			})
			closers = append(closers, productionRAG.Retriever)
			r.SetRAGReviewPath(productionRAG.ReviewPath)
			r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{
				RepoRoot:         repoRoot,
				ResourceRegistry: resourceRegistry,
				Retriever:        productionRAG.Retriever,
				FullLoader:       productionRAG.FullLoader,
				ModelLimits:      modelLimits,
			})
			return r, nil
		}

		modeReports := make([]ModeReport, 0, len(modes))
		for _, mode := range modes {
			fmt.Fprintf(options.progressWriter, "[%d/%d] %s %s ...\n", fixtureIndex+1, len(fixtures), fixture.Name, mode)
			modeStarted := time.Now()
			results := RunModes(ctx, fixture, []reviewer.EvalMode{mode}, factory)
			result := results[0]
			status := "ok"
			if result.Err != nil {
				status = "failed"
				failedModes++
			} else {
				okModes++
				if result.Outcome.Degraded || (mode == reviewer.EvalModeReflect && !result.Outcome.ReflectApplied) {
					status = "degraded"
				}
			}
			fmt.Fprintf(options.progressWriter, "[%d/%d] %s %s %s (%.1fs)\n", fixtureIndex+1, len(fixtures), fixture.Name, mode, status, time.Since(modeStarted).Seconds())
			runLog := runLogs[len(runLogs)-1]
			promptLog := promptLogs[len(promptLogs)-1]
			if persistModeMetrics {
				if err := runLog.SaveMetrics(); err != nil && log != nil {
					log.Warn("failed to save eval mode metrics", "fixture", fixture.Name, "mode", mode, "error", err)
				}
			}
			metrics := runLog.MetricsSnapshot()
			modeReports = append(modeReports, ModeReport{
				Result:          result,
				PromptBreakdown: promptLog.String(),
				Metrics:         metrics,
			})
			tokens, unknown := summarizeMetrics(metrics)
			usage.TotalTokens += tokens
			usage.UsageUnknownCalls += unknown
		}
		for _, closer := range closers {
			if err := closer.Close(); err != nil && log != nil {
				log.Warn("failed to clean up resolved RAG store", "error", err)
			}
		}
		fixtureReports = append(fixtureReports, FixtureReport{Fixture: fixture, Modes: modeReports})
	}

	providerName := string(cfg.AIProvider)
	model := cfg.Providers[cfg.AIProvider].Model
	if err := WriteReport(reportPath, Report{
		GeneratedAt: time.Now().UTC(),
		Provider:    providerName,
		Model:       model,
		Fixtures:    fixtureReports,
	}); err != nil {
		return err
	}
	return SummarizeBudget(log, usage)
}

func isFixtureName(name string) bool {
	if len(name) < len("00-a.diff") || name[2] != '-' || !strings.HasSuffix(name, ".diff") {
		return false
	}
	return name[0] >= '0' && name[0] <= '9' && name[1] >= '0' && name[1] <= '9'
}

func warnSkippedFixture(log *logger.Logger, name, reason string, err error) {
	if log == nil {
		return
	}
	if err != nil {
		log.Warn("skipping fixture", "fixture", name, "reason", reason, "error", err)
		return
	}
	log.Warn("skipping fixture", "fixture", name, "reason", reason)
}

type diffFile struct {
	oldPath   string
	newPath   string
	diffStart int
}

func synthesizeChanges(data []byte) []gitlab.Change {
	lines := splitLines(data)
	firstDiffLine := 0
	if len(lines) > 0 && bytes.HasPrefix(lines[0].text, []byte(fixtureHeaderPrefix)) {
		firstDiffLine = 1
	}
	files := make([]diffFile, 0)
	for i := firstDiffLine; i+1 < len(lines); i++ {
		oldPath, ok := diffPath(lines[i].text, "--- ", "a/")
		if !ok {
			continue
		}
		newPath, ok := diffPath(lines[i+1].text, "+++ ", "b/")
		if !ok {
			continue
		}
		files = append(files, diffFile{
			oldPath:   oldPath,
			newPath:   newPath,
			diffStart: lines[i+1].end,
		})
		i++
	}

	changes := make([]gitlab.Change, 0, len(files))
	for _, file := range files {
		diffEnd := len(data)
		for _, line := range lines {
			if line.start < file.diffStart {
				continue
			}
			if bytes.HasPrefix(line.text, []byte("diff --git ")) || bytes.HasPrefix(line.text, []byte("--- ")) {
				diffEnd = line.start
				break
			}
		}
		newFile := file.oldPath == "" && file.newPath != ""
		deletedFile := file.oldPath != "" && file.newPath == ""
		changes = append(changes, gitlab.Change{
			OldPath:     file.oldPath,
			NewPath:     file.newPath,
			Diff:        string(data[file.diffStart:diffEnd]),
			NewFile:     newFile,
			DeletedFile: deletedFile,
			RenamedFile: !newFile && !deletedFile && file.oldPath != file.newPath,
		})
	}
	return changes
}

type diffLine struct {
	text       []byte
	start, end int
}

func splitLines(data []byte) []diffLine {
	lines := make([]diffLine, 0, bytes.Count(data, []byte{'\n'})+1)
	for start := 0; start < len(data); {
		relativeEnd := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if relativeEnd >= 0 {
			end = start + relativeEnd + 1
		}
		textEnd := end
		if textEnd > start && data[textEnd-1] == '\n' {
			textEnd--
		}
		if textEnd > start && data[textEnd-1] == '\r' {
			textEnd--
		}
		lines = append(lines, diffLine{text: data[start:textEnd], start: start, end: end})
		start = end
	}
	return lines
}

func diffPath(line []byte, headerPrefix, pathPrefix string) (string, bool) {
	if !bytes.HasPrefix(line, []byte(headerPrefix)) || len(line) == len(headerPrefix) {
		return "", false
	}
	path := line[len(headerPrefix):]
	if bytes.Equal(path, []byte("/dev/null")) {
		return "", true
	}
	if !bytes.HasPrefix(path, []byte(pathPrefix)) || len(path) == len(pathPrefix) {
		return "", false
	}
	return string(path[len(pathPrefix):]), true
}
