package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

// TestS07_TokenUsageRecorded verifies REQ-04 / S-07.
func TestS07_TokenUsageRecorded(t *testing.T) {
	newLogger := func(t *testing.T) *logger.Logger {
		t.Helper()
		return logger.New(slog.LevelError, filepath.Join(t.TempDir(), "metrics.json"))
	}
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}

	assertProviderMetrics := func(t *testing.T, log *logger.Logger, service string) {
		t.Helper()
		metrics := log.MetricsSnapshot()
		if len(metrics.APICalls) != 2 {
			t.Fatalf("%s API calls: want 2, got %d", service, len(metrics.APICalls))
		}
		if metrics.APICalls[0].Service != service {
			t.Errorf("first service: want %q, got %q", service, metrics.APICalls[0].Service)
		}
		usage := metrics.APICalls[0].Usage
		if usage == nil {
			t.Errorf("%s first call usage: want input=17 output=5, got nil", service)
		} else {
			if usage.InputTokens != 17 {
				t.Errorf("%s input tokens: want 17, got %d", service, usage.InputTokens)
			}
			if usage.OutputTokens != 5 {
				t.Errorf("%s output tokens: want 5, got %d", service, usage.OutputTokens)
			}
		}
		if metrics.APICalls[1].Usage != nil {
			t.Errorf("%s second call usage: want nil, got %+v", service, metrics.APICalls[1].Usage)
		}
		if metrics.UsageUnknownCalls != 1 {
			t.Errorf("%s usage-unknown calls: want 1, got %d", service, metrics.UsageUnknownCalls)
		}
	}

	t.Run("gitlab call omits usage", func(t *testing.T) {
		log := newLogger(t)
		log.LogAIAPICall("openai", "responses", 1, true, nil, &logger.TokenUsage{
			InputTokens:  17,
			OutputTokens: 5,
		})
		log.LogAPICall("gitlab", "/projects/1/merge_requests/2", 1, true, nil)

		metrics := log.MetricsSnapshot()
		if len(metrics.APICalls) != 2 {
			t.Fatalf("API calls: want 2, got %d", len(metrics.APICalls))
		}
		if metrics.APICalls[0].Usage == nil {
			t.Error("AI usage: want recorded usage, got nil (RED)")
		}
		if metrics.APICalls[1].Usage != nil {
			t.Errorf("GitLab usage: want nil, got %+v", metrics.APICalls[1].Usage)
		}
	})

	// RED must fail above without binding loopback in the Codex sandbox. Once the
	// logger behavior turns green, these provider cases run on a host that permits
	// httptest.Server and exercise both present and absent usage responses.
	if t.Failed() {
		return
	}

	t.Run("openai", func(t *testing.T) {
		responses := []string{
			`{"output":[{"content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":17,"output_tokens":5,"total_tokens":22}}`,
			`{"output":[{"content":[{"type":"output_text","text":"ok"}]}]}`,
		}
		request := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, responses[request])
			request++
		}))
		defer server.Close()

		log := newLogger(t)
		provider := NewOpenAIProvider("test-key", providerConfig, log,
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		for i := 0; i < 2; i++ {
			if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
				t.Fatalf("Generate call %d: %v", i+1, err)
			}
		}
		assertProviderMetrics(t, log, "openai")
	})

	t.Run("anthropic", func(t *testing.T) {
		responses := []string{
			`{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":17,"output_tokens":5}}`,
			`{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null}`,
		}
		request := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, responses[request])
			request++
		}))
		defer server.Close()

		log := newLogger(t)
		provider := NewAnthropicProvider("test-key", providerConfig, log,
			WithAnthropicBaseURL(server.URL), WithAnthropicHTTPClient(server.Client()))
		for i := 0; i < 2; i++ {
			if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
				t.Fatalf("Generate call %d: %v", i+1, err)
			}
		}
		assertProviderMetrics(t, log, "anthropic")
	})

	t.Run("gemini", func(t *testing.T) {
		responses := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":5,"totalTokenCount":22}}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
		}
		request := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, responses[request])
			request++
		}))
		defer server.Close()

		log := newLogger(t)
		provider, err := NewGeminiProvider(context.Background(), "test-key", providerConfig, log,
			WithGeminiBaseURL(server.URL), WithGeminiHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("NewGeminiProvider: %v", err)
		}
		for i := 0; i < 2; i++ {
			if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
				t.Fatalf("Generate call %d: %v", i+1, err)
			}
		}
		assertProviderMetrics(t, log, "gemini")
	})
}
