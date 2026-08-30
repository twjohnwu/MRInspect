package ai

import (
	"context"
	"errors"
	"testing"

	"mrinspect/internal/config"
)

type retryTestProvider struct {
	failures int
	attempts int
	result   string
	provider string
}

func (p *retryTestProvider) Generate(context.Context, string, GenerateOptions) (string, error) {
	p.attempts++
	if p.attempts <= p.failures {
		return "", errors.New("temporary provider failure")
	}
	return p.result, nil
}

func (p *retryTestProvider) Name() string { return p.provider }

func TestWithRetry_AttemptPolicy(t *testing.T) {
	t.Run("fails twice then succeeds on third attempt", func(t *testing.T) {
		provider := &retryTestProvider{failures: 2, result: "review", provider: "test"}
		decorated := WithRetry(provider, config.APIConfig{
			RetryAttempts:   3,
			RetryDelayMs:    0,
			MaxRetryDelayMs: 0,
		})

		got, err := decorated.Generate(context.Background(), "prompt", GenerateOptions{})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got != "review" {
			t.Errorf("Generate result = %q, want review", got)
		}
		if provider.attempts != 3 {
			t.Errorf("Generate attempts = %d, want 3", provider.attempts)
		}
		if decorated.Name() != "test" {
			t.Errorf("Name() = %q, want test", decorated.Name())
		}
	})

	t.Run("one attempt disables retry", func(t *testing.T) {
		provider := &retryTestProvider{failures: 1, result: "unreachable", provider: "test"}
		decorated := WithRetry(provider, config.APIConfig{
			RetryAttempts:   1,
			RetryDelayMs:    0,
			MaxRetryDelayMs: 0,
		})

		if _, err := decorated.Generate(context.Background(), "prompt", GenerateOptions{}); err == nil {
			t.Fatal("Generate error = nil, want provider failure")
		}
		if provider.attempts != 1 {
			t.Errorf("Generate attempts = %d, want 1", provider.attempts)
		}
	})
}
