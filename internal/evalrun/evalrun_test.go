package evalrun_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/config"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/evalrun"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/testfake"
	"mrinspect/internal/validator"
)

// TestS02_FixtureLoading verifies REQ-02 / S-02 fixture loading, filtering, ordering, and change synthesis.
func TestS02_FixtureLoading(t *testing.T) {
	dir := t.TempDir()
	first := []byte("# mrinspect-fixture: source=abc123 kind=logic\n" +
		"--- a/internal/service/first.go\n" +
		"+++ b/internal/service/first.go\n" +
		"@@ -10,2 +10,2 @@ func calculate() int {\n" +
		"-\treturn subtotal\n" +
		"+\treturn subtotal + tax\n")
	second := []byte("# mrinspect-fixture: source=abc123 kind=logic\n" +
		"--- a/config/review.yaml\n" +
		"+++ b/config/review.yaml\n" +
		"@@ -3,2 +3,2 @@ review:\n" +
		"-  enabled: false\n" +
		"+  enabled: true\n")

	writeFixture(t, dir, "01-first.diff", first)
	writeFixture(t, dir, "02-second.diff", second)
	if err := os.Symlink(filepath.Join(dir, "01-first.diff"), filepath.Join(dir, "03-linked.diff")); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}
	tooLarge := append([]byte("# mrinspect-fixture: source=abc123 kind=logic\n"), bytes.Repeat([]byte("x"), 1<<20)...)
	writeFixture(t, dir, "04-too-large.diff", tooLarge)
	writeFixture(t, dir, "05-empty.diff", nil)

	log, readWarnings := captureWarnings(t)
	fixtures, err := evalrun.LoadFixtures(dir, log)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("fixture count = %d, want 2", len(fixtures))
	}

	want := []struct {
		name    string
		diff    []byte
		oldPath string
		newPath string
	}{
		{name: "01-first.diff", diff: first, oldPath: "internal/service/first.go", newPath: "internal/service/first.go"},
		{name: "02-second.diff", diff: second, oldPath: "config/review.yaml", newPath: "config/review.yaml"},
	}
	for i, expected := range want {
		fixture := fixtures[i]
		if fixture.Name != expected.name {
			t.Errorf("fixture[%d].Name = %q, want %q", i, fixture.Name, expected.name)
		}
		if !bytes.Equal(fixture.Diff, expected.diff) {
			t.Errorf("fixture[%d].Diff differs from original file bytes", i)
		}
		if len(fixture.Changes) != 1 {
			t.Errorf("fixture[%d] change count = %d, want 1", i, len(fixture.Changes))
			continue
		}
		if fixture.Changes[0].OldPath != expected.oldPath || fixture.Changes[0].NewPath != expected.newPath {
			t.Errorf("fixture[%d] paths = (%q, %q), want (%q, %q)", i,
				fixture.Changes[0].OldPath, fixture.Changes[0].NewPath, expected.oldPath, expected.newPath)
		}
	}

	warnings := readWarnings()
	for _, skipped := range []string{"03-linked.diff", "04-too-large.diff", "05-empty.diff"} {
		if !strings.Contains(warnings, skipped) {
			t.Errorf("warnings do not name skipped fixture %q: %s", skipped, warnings)
		}
	}
	if got := strings.Count(warnings, `"level":"WARN"`); got != 3 {
		t.Errorf("warning count = %d, want 3; logs: %s", got, warnings)
	}
}

// TestS05_EmptyFixturesGuard verifies REQ-02 / S-05 rejects zero valid fixtures without replacing an existing report.
func TestS05_EmptyFixturesGuard(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "empty directory", setup: func(*testing.T, string) {}},
		{name: "only invalid fixtures", setup: func(t *testing.T, dir string) {
			writeFixture(t, dir, "01-empty.diff", nil)
			writeFixture(t, dir, "02-too-large.diff", bytes.Repeat([]byte("x"), (1<<20)+1))
			validTarget := filepath.Join(t.TempDir(), "valid.diff")
			writeFixture(t, filepath.Dir(validTarget), filepath.Base(validTarget), []byte(
				"# mrinspect-fixture: source=abc123 kind=logic\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"))
			if err := os.Symlink(validTarget, filepath.Join(dir, "03-linked.diff")); err != nil {
				t.Fatalf("create symlink fixture: %v", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturesDir := t.TempDir()
			tt.setup(t, fixturesDir)
			log := logger.New(slog.LevelWarn, "")

			fixtures, err := evalrun.LoadFixtures(fixturesDir, log)
			if !errors.Is(err, evalrun.ErrNoValidFixtures) {
				t.Errorf("LoadFixtures error = %v, want ErrNoValidFixtures", err)
			}
			if len(fixtures) != 0 {
				t.Errorf("LoadFixtures returned %d fixtures, want 0", len(fixtures))
			}

			reportPath := filepath.Join(t.TempDir(), "REPORT.md")
			oldReport := []byte("# Existing report\n\nkeep this exact content\n")
			if err := os.WriteFile(reportPath, oldReport, 0o644); err != nil {
				t.Fatalf("write existing report: %v", err)
			}
			if err := evalrun.Run(fixturesDir, reportPath, log); !errors.Is(err, evalrun.ErrNoValidFixtures) {
				t.Errorf("Run error = %v, want ErrNoValidFixtures", err)
			}
			after, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("read report after guarded run: %v", err)
			}
			if !bytes.Equal(after, oldReport) {
				t.Errorf("guarded run changed existing report: got %q, want %q", after, oldReport)
			}
		})
	}
}

func writeFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func captureWarnings(t *testing.T) (*logger.Logger, func() string) {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "evalrun-warnings-*.jsonl")
	if err != nil {
		t.Fatalf("create warning sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	stdout := os.Stdout
	os.Stdout = sink
	log := logger.New(slog.LevelWarn, "")
	os.Stdout = stdout

	return log, func() string {
		t.Helper()
		if err := sink.Sync(); err != nil {
			t.Fatalf("sync warning sink: %v", err)
		}
		data, err := os.ReadFile(sink.Name())
		if err != nil {
			t.Fatalf("read warning sink: %v", err)
		}
		return string(data)
	}
}

// TestS03_OfflineIsolation verifies REQ-01 / S-03 keeps every GitLab method unused and returns review text through the eval result seam.
func TestS03_OfflineIsolation(t *testing.T) {
	configureEvalTestEnv(t)
	fixture := loadEvalFixture(t)
	gitlabClient := &testfake.FakeGitLabClient{}
	provider := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{
		Output: evalReviewText("single"),
	}}

	results := evalrun.RunModes(
		context.Background(),
		fixture,
		[]reviewer.EvalMode{reviewer.EvalModeSingle},
		func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			cfg, err := config.LoadForEval()
			if err != nil {
				return nil, err
			}
			return newEvalReviewer(cfg, gitlabClient, provider, false, ""), nil
		},
	)

	counters := map[string]int{
		"HealthCheck":     gitlabClient.HealthCheckCallCount(),
		"CurrentUser":     gitlabClient.CurrentUserCallCount(),
		"GetMergeRequest": gitlabClient.GetMergeRequestCallCount(),
		"GetMRChanges":    gitlabClient.GetMRChangesCallCount(),
		"ListNotes":       gitlabClient.ListNotesCallCount(),
		"PostNote":        gitlabClient.PostNoteCallCount(),
		"UpdateNote":      gitlabClient.UpdateNoteCallCount(),
	}
	for method, got := range counters {
		if got != 0 {
			t.Errorf("fake GitLab %s call count = %d, want 0", method, got)
		}
	}

	if len(results) != 1 {
		t.Fatalf("RunModes returned %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("single mode-run error = %v", results[0].Err)
	}
	if results[0].Mode != reviewer.EvalModeSingle || results[0].Outcome.Mode != reviewer.EvalModeSingle {
		t.Errorf("single result mode labels = (%q, %q), want %q", results[0].Mode, results[0].Outcome.Mode, reviewer.EvalModeSingle)
	}
	if strings.TrimSpace(results[0].Outcome.ReviewText) == "" {
		t.Fatal("single mode-run returned empty review text through the result seam")
	}
}

// TestS04_ThreeModes verifies REQ-01 / S-04 runs single, multi, and reflect in order without env or failure cross-contamination.
func TestS04_ThreeModes(t *testing.T) {
	modeBefore := snapshotEnv("MRI_REVIEW_MODE")
	reflectionBefore := snapshotEnv("IS_SELF_REFLECTION")
	defer func() {
		assertEnvSnapshot(t, "MRI_REVIEW_MODE", modeBefore)
		assertEnvSnapshot(t, "IS_SELF_REFLECTION", reflectionBefore)
	}()

	configureEvalTestEnv(t)
	fixture := loadEvalFixture(t)
	modes := []reviewer.EvalMode{
		reviewer.EvalModeSingle,
		reviewer.EvalModeMulti,
		reviewer.EvalModeReflect,
	}
	brokenLanesRoot := t.TempDir()
	gitlabClient := &testfake.FakeGitLabClient{}

	created := 0
	results := evalrun.RunModes(context.Background(), fixture, modes,
		func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			created++
			cfg, err := config.LoadForEval()
			if err != nil {
				return nil, err
			}
			cfg.SelfReflection = mode == reviewer.EvalModeReflect
			provider := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{
				Output: evalReviewText(string(mode)),
			}}
			if mode == reviewer.EvalModeReflect {
				provider.Responses = []testfake.ProviderResponse{
					{Output: evalReviewText(string(mode))},
					{Output: "REVIEW VALIDATED"},
				}
			}
			return newEvalReviewer(cfg, gitlabClient, provider, mode == reviewer.EvalModeMulti, brokenLanesRoot), nil
		})

	if len(results) != len(modes) {
		t.Fatalf("RunModes returned %d results, want %d", len(results), len(modes))
	}
	if created != len(modes) {
		t.Errorf("reviewer factory calls = %d, want %d independent reviewers", created, len(modes))
	}
	for i, mode := range modes {
		if results[i].Mode != mode || results[i].Outcome.Mode != mode {
			t.Errorf("result[%d] mode labels = (%q, %q), want %q", i, results[i].Mode, results[i].Outcome.Mode, mode)
		}
		if results[i].Err != nil {
			t.Errorf("result[%d] (%s) unexpected error = %v", i, mode, results[i].Err)
		}
		if !strings.Contains(results[i].Outcome.ReviewText, string(mode)) {
			t.Errorf("result[%d] review is not distinguishable as %q: %q", i, mode, results[i].Outcome.ReviewText)
		}
	}
	if results[1].Outcome.Degraded != true {
		t.Error("multi result Degraded = false with deliberately broken lanes config, want true")
	}
	if results[0].Outcome.Degraded || results[2].Outcome.Degraded {
		t.Errorf("broken multi lanes contaminated other modes: single=%t reflect=%t", results[0].Outcome.Degraded, results[2].Outcome.Degraded)
	}
	assertEnvSnapshot(t, "MRI_REVIEW_MODE", modeBefore)
	assertEnvSnapshot(t, "IS_SELF_REFLECTION", reflectionBefore)

	injected := errors.New("injected multi provider failure")
	failureResults := evalrun.RunModes(context.Background(), fixture, modes,
		func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			cfg, err := config.LoadForEval()
			if err != nil {
				return nil, err
			}
			cfg.SelfReflection = mode == reviewer.EvalModeReflect
			provider := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{
				Output: evalReviewText(string(mode)),
			}}
			if mode == reviewer.EvalModeMulti {
				provider.DefaultResponse = testfake.ProviderResponse{Err: injected}
			}
			if mode == reviewer.EvalModeReflect {
				provider.Responses = []testfake.ProviderResponse{
					{Output: evalReviewText(string(mode))},
					{Output: "REVIEW VALIDATED"},
				}
			}
			return newEvalReviewer(cfg, gitlabClient, provider, mode == reviewer.EvalModeMulti, brokenLanesRoot), nil
		})
	if len(failureResults) != len(modes) {
		t.Fatalf("failure RunModes returned %d results, want %d", len(failureResults), len(modes))
	}
	for i, mode := range modes {
		if mode == reviewer.EvalModeMulti {
			if failureResults[i].Err == nil || !strings.Contains(failureResults[i].Err.Error(), injected.Error()) {
				t.Errorf("multi failure record error = %v, want injected provider failure", failureResults[i].Err)
			}
			continue
		}
		if failureResults[i].Err != nil {
			t.Errorf("provider failure leaked into %s result: %v", mode, failureResults[i].Err)
		}
	}
}

type envSnapshot struct {
	value string
	set   bool
}

func snapshotEnv(key string) envSnapshot {
	value, set := os.LookupEnv(key)
	return envSnapshot{value: value, set: set}
}

func assertEnvSnapshot(t *testing.T, key string, want envSnapshot) {
	t.Helper()
	gotValue, gotSet := os.LookupEnv(key)
	if gotSet != want.set || gotValue != want.value {
		t.Errorf("%s changed: got (value=%q, set=%t), want (value=%q, set=%t)", key, gotValue, gotSet, want.value, want.set)
	}
}

func configureEvalTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AI_PROVIDER_KEY", "eval-test-key")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("CI_PROJECT_ID", "")
	t.Setenv("CI_MERGE_REQUEST_IID", "")
	projectsDir, err := filepath.Abs(filepath.Join("..", "..", "projects"))
	if err != nil {
		t.Fatalf("resolve projects directory: %v", err)
	}
	t.Setenv("PROJECTS_DIR", projectsDir)
}

func loadEvalFixture(t *testing.T) evalrun.Fixture {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "01-offline.diff", []byte(
		"# mrinspect-fixture: source=abc123 kind=logic\n"+
			"--- a/internal/service.go\n"+
			"+++ b/internal/service.go\n"+
			"@@ -1 +1 @@\n"+
			"-return oldValue\n"+
			"+return newValue\n"))
	fixtures, err := evalrun.LoadFixtures(dir, logger.NewWithWriter(slog.LevelWarn, "", io.Discard))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("LoadFixtures returned %d fixtures, want 1", len(fixtures))
	}
	return fixtures[0]
}

func newEvalReviewer(cfg config.Config, gitlabClient *testfake.FakeGitLabClient, provider *testfake.FakeProvider, brokenMulti bool, lanesRoot string) *reviewer.MRInspectReviewer {
	log := logger.NewWithWriter(slog.LevelDebug, "", io.Discard)
	r := reviewer.New(
		cfg,
		gitlabClient,
		provider,
		nil,
		project.NewLoader(cfg.Projects),
		prompt.NewComposer(),
		validator.New(cfg),
		mrerrors.NewHandler(cfg, log),
		log,
	)
	if brokenMulti {
		r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{RepoRoot: lanesRoot})
	}
	return r
}

func evalReviewText(mode string) string {
	return fmt.Sprintf("## Code Review: %s\n### Findings\nNo blocking issue in this fixture. %s\n### Verdict\nNeeds Minor Changes\n", mode, strings.Repeat("mode-specific evidence. ", 6))
}

// TestS06_ReportGeneration verifies REQ-03 / S-06 renders complete partial-failure reports and publishes them atomically.
func TestS06_ReportGeneration(t *testing.T) {
	configureEvalTestEnv(t)
	modes := []reviewer.EvalMode{
		reviewer.EvalModeSingle,
		reviewer.EvalModeMulti,
		reviewer.EvalModeReflect,
	}
	fixtures := []evalrun.Fixture{
		{
			Name: "01-alpha.diff",
			Diff: []byte("--- a/alpha.go\n+++ b/alpha.go\n@@ -1 +1 @@\n-old\n+new\n"),
		},
		{
			Name: "02-beta.diff",
			Diff: []byte("--- a/beta.go\n+++ b/beta.go\n@@ -1 +1 @@\n-old\n+new\n"),
		},
	}
	injected := errors.New("injected fixture/mode failure")
	fixtureReports := make([]evalrun.FixtureReport, 0, len(fixtures))

	for fixtureIndex, fixture := range fixtures {
		fixturePath := []string{"alpha.go", "beta.go"}[fixtureIndex]
		fixture.Changes = []gitlab.Change{{
			OldPath: fixturePath,
			NewPath: fixturePath,
			Diff:    string(fixture.Diff),
		}}
		var runLogs []*logger.Logger
		var promptLogs []*bytes.Buffer
		results := evalrun.RunModes(context.Background(), fixture, modes,
			func(mode reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
				cfg, err := config.LoadForEval()
				if err != nil {
					return nil, err
				}
				cfg.SelfReflection = mode == reviewer.EvalModeReflect
				promptLog := &bytes.Buffer{}
				runLog := logger.NewWithWriter(slog.LevelDebug, "", promptLog)
				promptLogs = append(promptLogs, promptLog)
				runLogs = append(runLogs, runLog)
				provider := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{
					Output: evalReviewText(fmt.Sprintf("review-%s-%s", fixture.Name, mode)),
				}}
				brokenMulti := fixtureIndex == 1 && mode == reviewer.EvalModeMulti
				if brokenMulti {
					provider.DefaultResponse = testfake.ProviderResponse{Err: injected}
				}
				r := reviewer.New(
					cfg,
					&testfake.FakeGitLabClient{},
					provider,
					nil,
					project.NewLoader(cfg.Projects),
					prompt.NewComposer(),
					validator.New(cfg),
					mrerrors.NewHandler(cfg, runLog),
					runLog,
				)
				if brokenMulti {
					r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{RepoRoot: t.TempDir()})
				}
				return r, nil
			})

		modeReports := make([]evalrun.ModeReport, 0, len(results))
		for i, result := range results {
			if result.Err != nil {
				runLogs[i].LogAIAPICall("fake", "generate", 1, false, result.Err, nil)
			} else {
				runLogs[i].LogAIAPICall("fake", "generate", 1, true, nil, &logger.TokenUsage{
					InputTokens:  100,
					OutputTokens: 50,
				})
			}
			modeReports = append(modeReports, evalrun.ModeReport{
				Result:          result,
				PromptBreakdown: promptLogs[i].String(),
				Metrics:         runLogs[i].MetricsSnapshot(),
			})
		}
		fixtureReports = append(fixtureReports, evalrun.FixtureReport{Fixture: fixture, Modes: modeReports})
	}

	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	err := evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC),
		Provider:    "fake-provider",
		Model:       "fake-model",
		Fixtures:    fixtureReports,
	})
	if err != nil {
		t.Errorf("WriteReport: %v", err)
	}
	reportBytes, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Errorf("read completed report: %v", readErr)
	}
	report := string(reportBytes)
	for _, want := range []string{
		"# MRInspect Review Quality Evaluation",
		"2026-08-30T10:00:00Z",
		"fake-provider",
		"fake-model",
		"Fixtures: `01-alpha.diff`, `02-beta.diff`",
		"## 01-alpha.diff",
		"## 02-beta.diff",
		"review-01-alpha.diff-single",
		"review-01-alpha.diff-multi",
		"review-01-alpha.diff-reflect",
		"review-02-beta.diff-single",
		"review-02-beta.diff-reflect",
		"Mode failed: injected fixture/mode failure",
		"Token subtotal: 450",
		"Token subtotal: ≥300",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q", want)
		}
	}
	if got := strings.Count(report, "| Section | Tokens | % of total |"); got != 7 {
		t.Errorf("prompt-breakdown table count = %d, want 5 base calls plus 2 reflection calls", got)
	}
	for _, mode := range modes {
		if got := strings.Count(report, "### "+string(mode)+"\n"); got != len(fixtures) {
			t.Errorf("%s mode section count = %d, want %d", mode, got, len(fixtures))
		}
	}
	if _, statErr := os.Stat(reportPath + ".tmp"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("atomic report temp file remains after WriteReport: %v", statErr)
	}
	if report != "" && (!strings.HasSuffix(report, "\n") || !strings.Contains(report, "## 02-beta.diff")) {
		t.Error("published report appears incomplete")
	}
}

func TestWriteReport_RendersAllPromptBreakdownsInLogOrder(t *testing.T) {
	base := "Prompt composition breakdown (estimated tokens per section):\n" +
		"| Section | Tokens | % of total |\n" +
		"|---|---:|---:|\n" +
		"| diff | 10 | 100.0% |"
	reflection := "Self-reflection prompt breakdown (estimated tokens per section):\n" +
		"| Section | Tokens | % of total |\n" +
		"|---|---:|---:|\n" +
		"| original review | 20 | 100.0% |"
	captured := fmt.Sprintf("{\"msg\":%q}\n{\"msg\":\"unrelated\"}\n{\"msg\":%q}\n", base, reflection)

	report := writePromptBreakdownReport(t, reviewer.EvalModeReflect, captured)
	baseIndex := strings.Index(report, base)
	reflectionIndex := strings.Index(report, reflection)
	if baseIndex < 0 || reflectionIndex < 0 {
		t.Fatalf("report did not render both prompt breakdowns:\n%s", report)
	}
	if baseIndex >= reflectionIndex {
		t.Errorf("prompt breakdown order = reflection before base, want captured log order:\n%s", report)
	}
}

func TestWriteReport_OnePromptBreakdownOutputUnchanged(t *testing.T) {
	breakdown := "Prompt composition breakdown (estimated tokens per section):\n" +
		"| Section | Tokens | % of total |\n" +
		"|---|---:|---:|\n" +
		"| diff | 10 | 100.0% |"
	captured := fmt.Sprintf("{\"msg\":%q}\n", breakdown)

	got := writePromptBreakdownReport(t, reviewer.EvalModeSingle, captured)
	want := "# MRInspect Review Quality Evaluation\n\n" +
		"Generated: 2026-08-30T14:00:00Z\n\n" +
		"Provider: `fake`\n\n" +
		"Model: `fake-model`\n\n" +
		"Fixtures: `01-breakdown.diff`\n\n" +
		"## 01-breakdown.diff\n\n" +
		"### single\n\n" +
		"review body\n\n" +
		breakdown + "\n\n" +
		"Token subtotal: 0\n\n"
	if got != want {
		t.Errorf("single-breakdown report changed:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func writePromptBreakdownReport(t *testing.T, mode reviewer.EvalMode, captured string) string {
	t.Helper()
	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	err := evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 30, 14, 0, 0, 0, time.UTC),
		Provider:    "fake",
		Model:       "fake-model",
		Fixtures: []evalrun.FixtureReport{{
			Fixture: evalrun.Fixture{Name: "01-breakdown.diff"},
			Modes: []evalrun.ModeReport{{
				Result: evalrun.ModeResult{
					Mode: mode,
					Outcome: reviewer.EvalOutcome{
						ReviewText: "review body",
						Mode:       mode,
					},
				},
				PromptBreakdown: captured,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(data)
}

func TestWriteReport_ReflectionNotes(t *testing.T) {
	tests := []struct {
		name    string
		mode    reviewer.EvalMode
		outcome reviewer.EvalOutcome
		want    string
	}{
		{
			name: "reflect not applied",
			mode: reviewer.EvalModeReflect,
			outcome: reviewer.EvalOutcome{
				ReflectApplied: false,
				ReflectChanged: false,
			},
			want: "> reflection not applied (degraded)",
		},
		{
			name: "reflect applied unchanged",
			mode: reviewer.EvalModeReflect,
			outcome: reviewer.EvalOutcome{
				ReflectApplied: true,
				ReflectChanged: false,
			},
			want: "> reflection applied, review unchanged (validated)",
		},
		{
			name: "reflect applied rewritten",
			mode: reviewer.EvalModeReflect,
			outcome: reviewer.EvalOutcome{
				ReflectApplied: true,
				ReflectChanged: true,
			},
			want: "> reflection applied, review rewritten",
		},
		{
			name: "single has no reflection note",
			mode: reviewer.EvalModeSingle,
			outcome: reviewer.EvalOutcome{
				ReflectApplied: true,
				ReflectChanged: true,
			},
		},
		{
			name: "multi has no reflection note",
			mode: reviewer.EvalModeMulti,
			outcome: reviewer.EvalOutcome{
				ReflectApplied: true,
				ReflectChanged: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "REPORT.md")
			tt.outcome.ReviewText = "review body"
			tt.outcome.Mode = tt.mode
			err := evalrun.WriteReport(reportPath, evalrun.Report{
				GeneratedAt: time.Date(2026, time.August, 30, 15, 0, 0, 0, time.UTC),
				Provider:    "fake",
				Model:       "fake-model",
				Fixtures: []evalrun.FixtureReport{{
					Fixture: evalrun.Fixture{Name: "01-reflect.diff"},
					Modes: []evalrun.ModeReport{{Result: evalrun.ModeResult{
						Mode:    tt.mode,
						Outcome: tt.outcome,
					}}},
				}},
			})
			if err != nil {
				t.Fatalf("WriteReport: %v", err)
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("read report: %v", err)
			}
			report := string(data)
			if tt.want != "" && !strings.Contains(report, tt.want) {
				t.Errorf("report missing reflection note %q:\n%s", tt.want, report)
			}
			if tt.want == "" && strings.Contains(report, "> reflection ") {
				t.Errorf("non-reflect report contains reflection note:\n%s", report)
			}
		})
	}
}

// TestS08_BudgetWarning verifies REQ-05 / S-08 parses the budget defensively and reports an honest lower bound without returning an error.
func TestS08_BudgetWarning(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		wantParseWarn bool
		wantBudget    bool
	}{
		{name: "unset", value: ""},
		{name: "zero", value: "0"},
		{name: "malformed", value: "abc", wantParseWarn: true},
		{name: "negative", value: "-5", wantParseWarn: true},
		{name: "trimmed budget", value: " 1000 ", wantBudget: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MRI_DAILY_TOKEN_BUDGET", tt.value)
			if tt.name == "unset" {
				if err := os.Unsetenv("MRI_DAILY_TOKEN_BUDGET"); err != nil {
					t.Fatalf("unset MRI_DAILY_TOKEN_BUDGET: %v", err)
				}
			}
			var logs bytes.Buffer
			log := logger.NewWithWriter(slog.LevelDebug, "", &logs)
			err := evalrun.SummarizeBudget(log, evalrun.UsageSummary{
				TotalTokens:       1500,
				UsageUnknownCalls: 1,
			})
			if err != nil {
				t.Errorf("SummarizeBudget returned error and would affect exit behavior: %v", err)
			}
			output := logs.String()
			if !strings.Contains(output, "≥1500") {
				t.Errorf("usage output does not preserve lower-bound marker: %s", output)
			}
			warned := strings.Contains(output, `"level":"WARN"`)
			if tt.wantBudget {
				if !warned || !strings.Contains(output, "1000") || !strings.Contains(output, "150%") {
					t.Errorf("over-budget Warn does not contain ≥1500 / 1000 (150%%): %s", output)
				}
				return
			}
			if strings.Contains(output, "150%") || strings.Contains(output, `"budget"`) {
				t.Errorf("disabled budget output contains a budget comparison: %s", output)
			}
			if warned != tt.wantParseWarn {
				t.Errorf("Warn presence = %t, want %t; output: %s", warned, tt.wantParseWarn, output)
			}
		})
	}
}

// TestS10_CIGuard verifies REQ-01 / S-10 requires an explicit opt-in before eval may run in CI.
func TestS10_CIGuard(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("MRI_EVAL_ALLOW_CI", "")
	if err := evalrun.CIGuard(); err == nil {
		t.Fatal("CIGuard allowed CI execution without explicit opt-in")
	} else if !strings.Contains(err.Error(), "MRI_EVAL_ALLOW_CI=true") {
		t.Errorf("CIGuard refusal does not name the opt-in: %v", err)
	}

	t.Setenv("MRI_EVAL_ALLOW_CI", "true")
	if err := evalrun.CIGuard(); err != nil {
		t.Errorf("CIGuard refused explicit CI opt-in: %v", err)
	}
}

func TestReflectReport_NotesReflectionDegradation(t *testing.T) {
	configureEvalTestEnv(t)
	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	cfg.SelfReflection = true
	reflectFailure := errors.New("reflect provider unavailable")
	provider := &testfake.FakeProvider{Responses: []testfake.ProviderResponse{
		{Output: evalReviewText("reflect")},
		{Err: reflectFailure},
	}}
	fixture := loadEvalFixture(t)
	results := evalrun.RunModes(context.Background(), fixture, []reviewer.EvalMode{reviewer.EvalModeReflect},
		func(reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			return newEvalReviewer(cfg, &testfake.FakeGitLabClient{}, provider, false, ""), nil
		})
	if len(results) != 1 {
		t.Fatalf("RunModes result count = %d, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("reflect mode unexpectedly failed instead of degrading: %v", results[0].Err)
	}

	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	err = evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC),
		Provider:    "fake",
		Model:       "fake-model",
		Fixtures: []evalrun.FixtureReport{{
			Fixture: fixture,
			Modes:   []evalrun.ModeReport{{Result: results[0]}},
		}},
	})
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(report), "reflection not applied (degraded)") {
		t.Errorf("reflect report missing degradation note after %v:\n%s", reflectFailure, report)
	}
}

func TestWriteReport_PreservesFailureStageChain(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	providerErr := errors.New("provider unavailable")
	modeErr := fmt.Errorf("multi-lane fan-out failed: %w", providerErr)
	stageErr := fmt.Errorf("generate review stage: %w", modeErr)
	err := evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 30, 13, 0, 0, 0, time.UTC),
		Provider:    "fake",
		Model:       "fake-model",
		Fixtures: []evalrun.FixtureReport{{
			Fixture: evalrun.Fixture{Name: "01-failure.diff"},
			Modes: []evalrun.ModeReport{{Result: evalrun.ModeResult{
				Mode: reviewer.EvalModeMulti,
				Err:  stageErr,
			}}},
		}},
	})
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if want := "generate review stage: multi-lane fan-out failed: provider unavailable"; !strings.Contains(string(report), want) {
		t.Errorf("failure cell lost stage context; want %q in:\n%s", want, report)
	}
}

func TestWriteReport_SurfacesLaneStoreResolutionDegradation(t *testing.T) {
	configureEvalTestEnv(t)
	t.Setenv("MRI_SERVICE_NAME", "dough-service")
	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	resourceRegistry, err := resources.Load(repoRoot, "")
	if err != nil {
		t.Fatalf("load resource registry: %v", err)
	}
	resolverFailure := errors.New("fake resolver failed")
	retriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Degraded: []string{"store unavailable: " + resolverFailure.Error()}},
	}}
	log := logger.NewWithWriter(slog.LevelDebug, "", io.Discard)
	r := reviewer.New(
		cfg,
		&testfake.FakeGitLabClient{},
		&testfake.FakeProvider{},
		nil,
		project.NewLoader(cfg.Projects),
		prompt.NewComposer(),
		validator.New(cfg),
		mrerrors.NewHandler(cfg, log),
		log,
	)
	r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{
		RepoRoot:         repoRoot,
		ResourceRegistry: resourceRegistry,
		Retriever:        retriever,
		ModelLimits:      map[string]int{cfg.Providers[cfg.AIProvider].Model: 1_000_000},
		Fanout: func(ctx context.Context, _ lane.FanoutInput) (lane.FanoutResult, error) {
			resolutionResult, retrieveErr := retriever.Retrieve(ctx, rag.Query{SetRef: "margherita-pizza-docs"})
			if retrieveErr != nil {
				return lane.FanoutResult{}, retrieveErr
			}
			return lane.FanoutResult{LaneResults: []lane.LaneResult{
				{LaneID: "spec-conformance", Chunks: resolutionResult.Chunks, Degraded: resolutionResult.Degraded},
				{LaneID: "standards"},
				{LaneID: "code-diff"},
			}}, nil
		},
	})
	fixture := loadEvalFixture(t)
	results := evalrun.RunModes(context.Background(), fixture, []reviewer.EvalMode{reviewer.EvalModeMulti},
		func(reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) { return r, nil })
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("multi eval result = %#v, want one successful result", results)
	}

	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	err = evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		Provider:    "fake",
		Model:       "fake-model",
		Fixtures: []evalrun.FixtureReport{{
			Fixture: fixture,
			Modes:   []evalrun.ModeReport{{Result: results[0]}},
		}},
	})
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	want := "- **spec-conformance** — Resource sets: margherita-pizza-docs (no content retrieved — store unavailable: fake resolver failed)"
	if !strings.Contains(string(report), want) {
		t.Errorf("report missing lane store-resolution degradation line %q:\n%s", want, report)
	}
}

type resolverFailureReviewPath struct{ reason string }

func (p resolverFailureReviewPath) RetrieveForReview(context.Context, string) (reviewer.ReviewRAGState, error) {
	return reviewer.ReviewRAGState{Degraded: []string{p.reason}}, nil
}

func TestWriteReport_SurfacesSingleModeStoreResolutionDegradation(t *testing.T) {
	configureEvalTestEnv(t)
	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	r := newEvalReviewer(cfg, &testfake.FakeGitLabClient{}, &testfake.FakeProvider{
		DefaultResponse: testfake.ProviderResponse{Output: evalReviewText("single")},
	}, false, "")
	const reason = "store unavailable: fake resolver failed"
	r.SetRAGReviewPath(resolverFailureReviewPath{reason: reason})
	fixture := loadEvalFixture(t)
	results := evalrun.RunModes(context.Background(), fixture, []reviewer.EvalMode{reviewer.EvalModeSingle},
		func(reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) { return r, nil })
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("single eval result = %#v, want one successful result", results)
	}

	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	err = evalrun.WriteReport(reportPath, evalrun.Report{
		GeneratedAt: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		Provider:    "fake",
		Model:       "fake-model",
		Fixtures: []evalrun.FixtureReport{{
			Fixture: fixture,
			Modes:   []evalrun.ModeReport{{Result: results[0]}},
		}},
	})
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(report), reason) {
		t.Errorf("single-mode report missing store-resolution degradation %q:\n%s", reason, report)
	}
}
