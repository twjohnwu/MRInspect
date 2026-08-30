package ai

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"mrinspect/internal/config"
)

type blockingRetryTestProvider struct {
	attempts atomic.Int32
}

func (p *blockingRetryTestProvider) Generate(ctx context.Context, _ string, _ GenerateOptions) (string, error) {
	p.attempts.Add(1)
	<-ctx.Done()
	return "", ctx.Err()
}

func (p *blockingRetryTestProvider) Name() string { return "blocking-test" }

func TestWithRetry_PerAttemptTimeoutRetries(t *testing.T) {
	const (
		attempts       = 3
		perCallTimeout = 10 * time.Millisecond
		guardTimeout   = 250 * time.Millisecond
	)

	provider := &blockingRetryTestProvider{}
	decorated := WithRetry(provider, config.APIConfig{
		RetryAttempts:    attempts,
		RetryDelayMs:     0,
		MaxRetryDelayMs:  0,
		PerCallTimeoutMs: int(perCallTimeout / time.Millisecond),
	})
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	start := time.Now()
	_, err := decorated.Generate(ctx, "prompt", GenerateOptions{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Generate error = %v, want context deadline exceeded", err)
	}
	if got := provider.attempts.Load(); got != attempts {
		t.Errorf("Generate attempts = %d, want %d", got, attempts)
	}
	if elapsed >= guardTimeout {
		t.Errorf("Generate elapsed = %v, want less than guard timeout %v", elapsed, guardTimeout)
	}
}
