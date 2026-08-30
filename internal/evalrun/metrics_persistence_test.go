package evalrun_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/evalrun"
	"mrinspect/internal/logger"
)

type evalRoundTripper func(*http.Request) (*http.Response, error)

func (f evalRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestEvalRun_PersistsPerModeMetricsIncrementally(t *testing.T) {
	projectsDir, err := filepath.Abs(filepath.Join("..", "..", "projects"))
	if err != nil {
		t.Fatalf("resolve projects directory: %v", err)
	}
	metricsBase := filepath.Join(t.TempDir(), "eval-metrics")
	t.Setenv("AI_PROVIDER_KEY", "eval-test-key")
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("AI_REVIEW_METRICS_FILE", metricsBase)
	t.Setenv("MRI_SERVICE_NAME", "dough-service")
	t.Setenv("PROJECTS_DIR", projectsDir)
	t.Setenv("MRI_RAG_SOURCE", "baked")

	responseText := "## Code Review\n### Findings\nNo blocking issue. " + strings.Repeat("Evidence remains specific. ", 8) + "\n### Verdict\nNeeds Minor Changes\n"
	responseBody, err := json.Marshal(map[string]any{
		"output": []any{map[string]any{"content": []any{map[string]any{"text": responseText}}}},
		"usage":  map[string]any{"input_tokens": 17, "output_tokens": 5},
	})
	if err != nil {
		t.Fatalf("marshal fake response: %v", err)
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = evalRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(responseBody))),
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	fixturesDir := t.TempDir()
	writeEvalMetricsFixture(t, fixturesDir, "01-metrics.diff", []byte(
		"--- a/service.go\n"+
			"+++ b/service.go\n"+
			"@@ -1 +1 @@\n"+
			"-oldValue\n"+
			"+newValue\n"))
	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	err = evalrun.RunWithConfig(
		context.Background(),
		fixturesDir,
		filepath.Join(t.TempDir(), "REPORT.md"),
		cfg,
		logger.NewWithWriter(slog.LevelError, "", io.Discard),
	)
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}

	for _, mode := range []string{"single", "multi", "reflect"} {
		path := metricsBase + ".01-metrics.diff." + mode + ".json"
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("metrics file for %s mode was not persisted after run: %v", mode, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("metrics file for %s mode is empty", mode)
		}
	}
}

func writeEvalMetricsFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
