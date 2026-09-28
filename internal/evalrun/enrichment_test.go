package evalrun

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/logger"
	"mrinspect/internal/testfake"
)

// This file is a white-box (package evalrun, not evalrun_test) test: it
// needs withProviderFactory, an unexported RunOption that exists solely so
// tests can inject a fake ai.Provider through the real runLoaded wiring
// (including enrichment) without making an outbound AI call — the exported
// path (ai.NewProvider) has no such seam, and NON-GOALS forbid adding one
// to internal/ai.

// writeEnrichmentEvalFixture writes one valid fixture diff, matching the
// header/hunk shape LoadFixtures requires (see synthesize_changes_test.go
// conventions in the evalrun_test package).
func writeEnrichmentEvalFixture(t *testing.T, dir string) {
	t.Helper()
	content := []byte("# mrinspect-fixture: source=abc123 kind=logic\n" +
		"--- a/internal/service.go\n" +
		"+++ b/internal/service.go\n" +
		"@@ -1 +1 @@\n" +
		"-return oldValue\n" +
		"+return newValue\n")
	if err := os.WriteFile(filepath.Join(dir, "01-enrichment.diff"), content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// enrichmentEvalConfig loads a real eval config with MRI_REVIEW_ENRICHMENT
// set per enabled, the same switch S-14 exercises against `mrinspect eval`.
func enrichmentEvalConfig(t *testing.T, enabled bool) config.Config {
	t.Helper()
	t.Setenv("AI_PROVIDER_KEY", "eval-test-key")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("CI_PROJECT_ID", "")
	t.Setenv("CI_MERGE_REQUEST_IID", "")
	if enabled {
		t.Setenv("MRI_REVIEW_ENRICHMENT", "true")
	} else {
		t.Setenv("MRI_REVIEW_ENRICHMENT", "false")
	}
	projectsDir, err := filepath.Abs(filepath.Join("..", "..", "projects"))
	if err != nil {
		t.Fatalf("resolve projects directory: %v", err)
	}
	t.Setenv("PROJECTS_DIR", projectsDir)
	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	if cfg.Enrichment.ToolTimeout == 0 {
		cfg.Enrichment.ToolTimeout = 5 * time.Second
	}
	return cfg
}

const enrichmentEvalReviewText = "## Code Review\n\n## Findings\nNo material findings were located in the reviewed diff for this attempt.\n\n## Verdict\napproved"

// TestEvalRun_EnrichmentWiring pins the review-enrichment-round STDD
// change's S-14: MRI_REVIEW_ENRICHMENT=true must make `mrinspect eval`
// exercise the same tool-calling round `mrinspect review` does. It runs the
// real runLoaded wiring with a fake provider (via withProviderFactory) and
// checks the observable evidence a fake Generate/GenerateTurn split can
// see: whether the provider's turn requests carry the enrichment tool
// specs.
func TestEvalRun_EnrichmentWiring(t *testing.T) {
	t.Run("enabled: turn requests carry tool specs", func(t *testing.T) {
		dir := t.TempDir()
		writeEnrichmentEvalFixture(t, dir)
		cfg := enrichmentEvalConfig(t, true)
		fake := &testfake.FakeProvider{
			DefaultTurnResponse: testfake.TurnResponse{Text: enrichmentEvalReviewText},
			DefaultResponse:     testfake.ProviderResponse{Output: enrichmentEvalReviewText},
		}
		reportPath := filepath.Join(t.TempDir(), "REPORT.md")
		log := logger.NewWithWriter(slog.LevelError, "", io.Discard)

		err := runLoaded(context.Background(), mustLoadFixtures(t, dir, log), reportPath, cfg, log,
			newRunOptions(withProviderFactory(func(config.Config, *logger.Logger) (ai.Provider, error) {
				return fake, nil
			})))
		if err != nil {
			t.Fatalf("runLoaded: %v", err)
		}

		calls := fake.GenerateTurnCalls()
		if len(calls) == 0 {
			t.Fatal("GenerateTurnCalls() is empty, want at least one turn request with enrichment enabled")
		}
		for i, call := range calls {
			if len(call.Request.Tools) == 0 {
				t.Errorf("call %d: Request.Tools is empty, want enrichment tool specs", i)
			}
		}
	})

	t.Run("disabled: no turn requests carry tools", func(t *testing.T) {
		dir := t.TempDir()
		writeEnrichmentEvalFixture(t, dir)
		cfg := enrichmentEvalConfig(t, false)
		fake := &testfake.FakeProvider{
			DefaultResponse: testfake.ProviderResponse{Output: enrichmentEvalReviewText},
		}
		reportPath := filepath.Join(t.TempDir(), "REPORT.md")
		log := logger.NewWithWriter(slog.LevelError, "", io.Discard)

		err := runLoaded(context.Background(), mustLoadFixtures(t, dir, log), reportPath, cfg, log,
			newRunOptions(withProviderFactory(func(config.Config, *logger.Logger) (ai.Provider, error) {
				return fake, nil
			})))
		if err != nil {
			t.Fatalf("runLoaded: %v", err)
		}

		if got := len(fake.GenerateTurnCalls()); got != 0 {
			t.Errorf("GenerateTurnCalls() length = %d, want 0 with enrichment disabled", got)
		}
	})
}

func mustLoadFixtures(t *testing.T, dir string, log *logger.Logger) []Fixture {
	t.Helper()
	fixtures, err := LoadFixtures(dir, log)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	return fixtures
}
