package embed

import (
	"context"
	"fmt"
	"time"
)

// RetryOptions configures WithRateLimitRetry.
type RetryOptions struct {
	MaxRetries int
	BaseDelay  time.Duration
	Wait       func(ctx context.Context, d time.Duration) error
	OnRetry    func(attempt int, delay time.Duration)
}

const (
	defaultRetryMaxRetries = 3
	defaultRetryBaseDelay  = 20 * time.Second
)

// WithRateLimitRetry wraps inner so that HTTP 429 (rate-limited) errors are
// retried with linear backoff: the nth retry waits n * BaseDelay. It is the
// Decorator shared by the retrieval-eval runner and the sqlite indexer so
// both retry rate-limited embed calls identically.
func WithRateLimitRetry(inner Embedder, o RetryOptions) Embedder {
	if o.MaxRetries <= 0 {
		o.MaxRetries = defaultRetryMaxRetries
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = defaultRetryBaseDelay
	}
	if o.Wait == nil {
		o.Wait = defaultRetryWait
	}
	return &retryingEmbedder{inner: inner, opts: o}
}

type retryingEmbedder struct {
	inner Embedder
	opts  RetryOptions
}

func (r *retryingEmbedder) Model() string { return r.inner.Model() }
func (r *retryingEmbedder) Dim() int      { return r.inner.Dim() }

func (r *retryingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := r.inner.Embed(ctx, texts)
	for attempt := 1; err != nil && IsRateLimited(err) && attempt <= r.opts.MaxRetries; attempt++ {
		delay := time.Duration(attempt) * r.opts.BaseDelay
		if r.opts.OnRetry != nil {
			r.opts.OnRetry(attempt, delay)
		}
		if waitErr := r.opts.Wait(ctx, delay); waitErr != nil {
			return nil, fmt.Errorf("embed: rate limit retry wait: %w", waitErr)
		}
		vectors, err = r.inner.Embed(ctx, texts)
	}
	return vectors, err
}

func defaultRetryWait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
