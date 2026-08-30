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

func TestEvalRun_ProgressReportsFixtureModesAndTotals(t *testing.T) {
	projectsDir, err := filepath.Abs(filepath.Join("..", "..", "projects"))
	if err != nil {
		t.Fatalf("resolve projects directory: %v", err)
	}
	t.Setenv("AI_PROVIDER_KEY", "eval-test-key")
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("AI_REVIEW_METRICS_FILE", "")
	t.Setenv("MRI_SERVICE_NAME", "dough-service")
	t.Setenv("PROJECTS_DIR", projectsDir)
	t.Setenv("MRI_RAG_SOURCE", "baked")
	t.Setenv("MRI_LANE_CONCURRENCY", "1")
	t.Setenv("API_RETRY_ATTEMPTS", "1")
	t.Setenv("AI_RETRY_ATTEMPTS", "1")

	responseText := "## Code Review\n### Findings\nNo blocking issue. " + strings.Repeat("Evidence remains specific. ", 8) + "\n### Verdict\nNeeds Minor Changes\n"
	responseBody, err := json.Marshal(map[string]any{
		"output": []any{map[string]any{"content": []any{map[string]any{"text": responseText}}}},
		"usage":  map[string]any{"input_tokens": 17, "output_tokens": 5},
	})
	if err != nil {
		t.Fatalf("marshal fake response: %v", err)
	}

	failureLock := make(chan struct{}, 1)
	failureLock <- struct{}{}
	failedSecondFixture := false
	oldTransport := http.DefaultTransport
	http.DefaultTransport = evalRoundTripper(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode fake-provider request: %v", err)
		}

		<-failureLock
		fail := strings.Contains(payload.Input, "02-beta.diff") && !failedSecondFixture
		if fail {
			failedSecondFixture = true
		}
		failureLock <- struct{}{}

		status := http.StatusOK
		body := string(responseBody)
		if fail {
			status = http.StatusBadRequest
			body = ""
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	fixturesDir := t.TempDir()
	for _, fixture := range []struct {
		name string
		path string
	}{
		{name: "01-alpha.diff", path: "alpha.go"},
		{name: "02-beta.diff", path: "beta.go"},
	} {
		writeEvalMetricsFixture(t, fixturesDir, fixture.name, []byte(
			"--- a/"+fixture.path+"\n"+
				"+++ b/"+fixture.path+"\n"+
				"@@ -1 +1 @@\n"+
				"-oldValue\n"+
				"+newValue\n"))
	}

	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	var progress strings.Builder
	err = evalrun.RunWithConfig(
		context.Background(),
		fixturesDir,
		filepath.Join(t.TempDir(), "REPORT.md"),
		cfg,
		logger.NewWithWriter(slog.LevelError, "", io.Discard),
		evalrun.WithProgressWriter(&progress),
	)
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(progress.String()), "\n")
	if len(lines) != 15 {
		t.Fatalf("progress line count = %d, want 15:\n%s", len(lines), progress.String())
	}
	wantExact := map[int]string{
		0:  "[1/2] 01-alpha.diff",
		1:  "[1/2] 01-alpha.diff single ...",
		3:  "[1/2] 01-alpha.diff multi ...",
		5:  "[1/2] 01-alpha.diff reflect ...",
		7:  "[2/2] 02-beta.diff",
		8:  "[2/2] 02-beta.diff single ...",
		10: "[2/2] 02-beta.diff multi ...",
		12: "[2/2] 02-beta.diff reflect ...",
	}
	for lineIndex, want := range wantExact {
		if lines[lineIndex] != want {
			t.Errorf("progress line %d = %q, want %q", lineIndex+1, lines[lineIndex], want)
		}
	}
	for _, expectation := range []struct {
		line   int
		prefix string
	}{
		{line: 2, prefix: "[1/2] 01-alpha.diff single ok ("},
		{line: 4, prefix: "[1/2] 01-alpha.diff multi degraded ("},
		{line: 6, prefix: "[1/2] 01-alpha.diff reflect ok ("},
		{line: 9, prefix: "[2/2] 02-beta.diff single failed ("},
		{line: 11, prefix: "[2/2] 02-beta.diff multi degraded ("},
		{line: 13, prefix: "[2/2] 02-beta.diff reflect ok ("},
	} {
		if !strings.HasPrefix(lines[expectation.line], expectation.prefix) || !strings.HasSuffix(lines[expectation.line], "s)") {
			t.Errorf("progress line %d = %q, want prefix %q and seconds suffix", expectation.line+1, lines[expectation.line], expectation.prefix)
		}
	}
	if !strings.HasPrefix(lines[14], "Totals: 5 modes ok, 1 failed (") || !strings.HasSuffix(lines[14], "s wall)") {
		t.Errorf("totals line = %q, want mode counts and wall time", lines[14])
	}
}
