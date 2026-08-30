package evalrun_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"mrinspect/internal/reviewer"
	"mrinspect/internal/testfake"
)

func TestEvalRun_AllLanesFailedMarksOnlyMultiFailed(t *testing.T) {
	configureEvalTestEnv(t)
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("MRI_SERVICE_NAME", "dough-service")
	t.Setenv("MRI_RAG_SOURCE", "baked")
	t.Setenv("MRI_LANE_CONCURRENCY", "1")
	t.Setenv("API_RETRY_ATTEMPTS", "1")
	t.Setenv("AI_RETRY_ATTEMPTS", "1")

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	t.Chdir(repoRoot)

	cfg, err := config.LoadForEval()
	if err != nil {
		t.Fatalf("LoadForEval: %v", err)
	}
	fixture := loadEvalFixture(t)
	laneErr := errors.New("injected lane provider outage")
	provider := &testfake.FakeProvider{
		Responses:       []testfake.ProviderResponse{{Output: evalReviewText("single")}},
		DefaultResponse: testfake.ProviderResponse{Err: laneErr},
	}
	results := evalrun.RunModes(
		context.Background(),
		fixture,
		[]reviewer.EvalMode{reviewer.EvalModeSingle, reviewer.EvalModeMulti},
		func(reviewer.EvalMode) (*reviewer.MRInspectReviewer, error) {
			r := newEvalReviewer(cfg, &testfake.FakeGitLabClient{}, provider, false, "")
			r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{
				RepoRoot:    repoRoot,
				ModelLimits: map[string]int{"gpt-5.6": 1_000_000},
			})
			return r, nil
		},
	)
	if len(results) != 2 {
		t.Fatalf("RunModes result count = %d, want 2", len(results))
	}
	if results[0].Err != nil || strings.TrimSpace(results[0].Outcome.ReviewText) == "" {
		t.Errorf("single mode-run was affected by lane failures: outcome=%#v err=%v", results[0].Outcome, results[0].Err)
	}
	if results[1].Err == nil {
		t.Errorf("multi mode-run error = nil after every lane failed; review=%q", results[1].Outcome.ReviewText)
	} else {
		for _, want := range []string{"3 lanes failed", laneErr.Error()} {
			if !strings.Contains(results[1].Err.Error(), want) {
				t.Errorf("multi mode-run error = %q, want %q", results[1].Err, want)
			}
		}
	}

	successText := evalReviewText("single-or-reflect")
	responseBody, err := json.Marshal(map[string]any{
		"output": []any{map[string]any{"content": []any{map[string]any{"text": successText}}}},
	})
	if err != nil {
		t.Fatalf("marshal fake response: %v", err)
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = evalRoundTripper(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode fake-provider request: %v", err)
		}
		status := http.StatusOK
		body := string(responseBody)
		if strings.Contains(payload.Input, "# Review lane:") {
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
	writeEvalMetricsFixture(t, fixturesDir, "01-all-lanes-fail.diff", []byte(
		"--- a/service.go\n"+
			"+++ b/service.go\n"+
			"@@ -1 +1 @@\n"+
			"-oldValue\n"+
			"+newValue\n"))
	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	var progress strings.Builder
	err = evalrun.RunWithConfig(
		context.Background(),
		fixturesDir,
		reportPath,
		cfg,
		logger.NewWithWriter(slog.LevelError, "", io.Discard),
		evalrun.WithProgressWriter(&progress),
	)
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}
	if !strings.Contains(progress.String(), "01-all-lanes-fail.diff single ok (") {
		t.Errorf("single progress did not remain ok:\n%s", progress.String())
	}
	if !strings.Contains(progress.String(), "01-all-lanes-fail.diff multi failed (") {
		t.Errorf("multi progress did not print failed:\n%s", progress.String())
	}
	if strings.Contains(progress.String(), "01-all-lanes-fail.diff multi ok (") {
		t.Errorf("multi progress still printed ok:\n%s", progress.String())
	}

	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(report), "### multi\n\nMode failed:") {
		t.Errorf("report retained a successful multi section after total lane failure:\n%s", report)
	}
}
