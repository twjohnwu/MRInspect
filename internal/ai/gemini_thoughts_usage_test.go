package ai

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

type geminiUsageRoundTripper func(*http.Request) (*http.Response, error)

func (f geminiUsageRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGeminiUsage_IncludesThoughtsTokensInOutput(t *testing.T) {
	httpClient := &http.Client{Transport: geminiUsageRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":5,"thoughtsTokenCount":11,"totalTokenCount":33}}`,
			)),
		}, nil
	})}

	log := logger.New(slog.LevelError, filepath.Join(t.TempDir(), "metrics.json"))
	provider, err := NewGeminiProvider(
		context.Background(),
		"test-key",
		config.ProviderConfig{Model: "test-model", MaxTokens: 32},
		log,
		WithGeminiBaseURL("https://gemini.test"),
		WithGeminiHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	metrics := log.MetricsSnapshot()
	if len(metrics.APICalls) != 1 || metrics.APICalls[0].Usage == nil {
		t.Fatalf("Gemini usage metric = %+v, want one call with usage", metrics.APICalls)
	}
	if got := metrics.APICalls[0].Usage.OutputTokens; got != 16 {
		t.Errorf("Gemini output tokens = %d, want candidates 5 + thoughts 11 = 16", got)
	}
}
