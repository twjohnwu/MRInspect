package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

// TestOpenAI_RequestSetsStoreFalse verifies the OpenAI Responses API request
// body sets "store": false so responses are not retained server-side.
func TestOpenAI_RequestSetsStoreFalse(t *testing.T) {
	newLogger := func(t *testing.T) *logger.Logger {
		t.Helper()
		return logger.New(slog.LevelError, filepath.Join(t.TempDir(), "metrics.json"))
	}
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	log := newLogger(t)
	provider := NewOpenAIProvider("test-key", providerConfig, log,
		WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
	if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	v, ok := body["store"]
	if !ok || v != false {
		t.Fatalf("store = %v, ok=%v; want false", v, ok)
	}
}
