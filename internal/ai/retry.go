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

// GenerateTurn delegates to the wrapped provider's TurnProvider under the
// same retry policy as Generate, writing one transcript entry per turn
// (REQ-05).
func (p *retryProvider) GenerateTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	tp, ok := p.provider.(TurnProvider)
	if !ok {
		return TurnResult{}, errTurnNotImplemented
	}

	turn := 1
	continuation := "none"
	if req.Continuation != nil {
		turn = 2
		continuation = req.Continuation.Mode()
	}
	prompt := req.Prompt
	if turn == 2 {
		prompt = ""
	}

	var result TurnResult
	err := p.do(ctx, func(attemptCtx context.Context, attempt int) error {
		res, err := tp.GenerateTurn(attemptCtx, req)
		entry := transcriptEntry{
			Timestamp:    time.Now().Format(time.RFC3339Nano),
			Provider:     p.provider.Name(),
			Model:        req.Options.Model,
			Attempt:      attempt,
			Prompt:       prompt,
			Response:     res.Text,
			Turn:         turn,
			Continuation: continuation,
			ToolCalls:    transcriptToolCallsFrom(res.ToolCalls),
			ToolResults:  transcriptToolResultsFrom(req.ToolResults),
		}
		if err != nil {
			entry.Error = err.Error()
		}
		processTranscript.append(p.cfg.AILogDir, entry)
		if err == nil {
			result = res
		}
		return err
	})
	if err != nil {
		return TurnResult{}, err
	}
	return result, nil
}

func (p *retryProvider) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	var output string
	err := p.do(ctx, func(attemptCtx context.Context, attempt int) error {
		out, err := p.provider.Generate(attemptCtx, prompt, opts)
		entry := transcriptEntry{
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Provider:  p.provider.Name(),
			Model:     opts.Model,
			Attempt:   attempt,
			Prompt:    prompt,
			Response:  out,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		processTranscript.append(p.cfg.AILogDir, entry)
		if err == nil {
			output = out
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return output, nil
}

// do runs attempt up to the configured total-attempt count, applying the
// exponential-backoff delay before every retry and stopping early on a
// non-retryable error. Generate and GenerateTurn share this loop; only the
// per-attempt work (the API call plus its transcript entry) differs.
func (p *retryProvider) do(ctx context.Context, attempt func(attemptCtx context.Context, attemptNum int) error) error {
	attempts := p.cfg.RetryAttempts
	if attempts < 1 {
		attempts = 1
	}
	perCallTimeout := time.Duration(p.cfg.PerCallTimeoutMs) * time.Millisecond
	if perCallTimeout <= 0 {
		perCallTimeout = defaultPerCallTimeout
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := time.Duration(math.Min(
				float64(p.cfg.RetryDelayMs)*math.Pow(2, float64(i-1)),
				float64(p.cfg.MaxRetryDelayMs),
			)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, perCallTimeout)
		err := attempt(attemptCtx, i+1)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil || !isRetryable(err) {
			return err
		}
	}
	return lastErr
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
