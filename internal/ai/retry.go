package ai

import (
	"context"
	"errors"
	"math"
	"time"

	"mrinspect/internal/config"
)

const defaultPerCallTimeout = 120 * time.Second

type retryProvider struct {
	provider Provider
	cfg      config.APIConfig
}

// WithRetry decorates a provider with the configured total-attempt and
// exponential-backoff policy.
func WithRetry(provider Provider, cfg config.APIConfig) Provider {
	return &retryProvider{provider: provider, cfg: cfg}
}

func (p *retryProvider) Name() string { return p.provider.Name() }

func (p *retryProvider) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	attempts := p.cfg.RetryAttempts
	if attempts < 1 {
		attempts = 1
	}
	perCallTimeout := time.Duration(p.cfg.PerCallTimeoutMs) * time.Millisecond
	if perCallTimeout <= 0 {
		perCallTimeout = defaultPerCallTimeout
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Min(
				float64(p.cfg.RetryDelayMs)*math.Pow(2, float64(attempt-1)),
				float64(p.cfg.MaxRetryDelayMs),
			)) * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, perCallTimeout)
		output, err := p.provider.Generate(attemptCtx, prompt, opts)
		cancel()
		entry := transcriptEntry{
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Provider:  p.provider.Name(),
			Model:     opts.Model,
			Attempt:   attempt + 1,
			Prompt:    prompt,
			Response:  output,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		processTranscript.append(p.cfg.AILogDir, entry)
		if err == nil {
			return output, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isRetryable(err) {
			return "", err
		}
	}
	return "", lastErr
}

type nonRetryableError struct {
	err error
}

func (e nonRetryableError) Error() string { return e.err.Error() }
func (e nonRetryableError) Unwrap() error { return e.err }

func withoutRetry(err error) error {
	return nonRetryableError{err: err}
}

func isRetryable(err error) bool {
	var permanent nonRetryableError
	return !errors.As(err, &permanent)
}
