package embed

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestWithRateLimitRetry_StopsAfterMaxRetries verifies REQ-05 / S-09: rate-limited
// embedding calls retry with linear backoff and stop after the configured limit.
func TestWithRateLimitRetry_StopsAfterMaxRetries(t *testing.T) {
	inner := NewFixture(4)
	inner.FailOn = func(int, []string) error {
		return &StatusError{Code: 429}
	}
	var waits []time.Duration
	wrapped := WithRateLimitRetry(inner, RetryOptions{
		MaxRetries: 3,
		BaseDelay:  20 * time.Second,
		Wait: func(_ context.Context, duration time.Duration) error {
			waits = append(waits, duration)
			return nil
		},
	})

	_, err := wrapped.Embed(context.Background(), []string{"x"})
	if got := inner.Calls(); got != 4 {
		t.Errorf("inner.Calls() = %d, want 4", got)
	}
	wantWaits := []time.Duration{20 * time.Second, 40 * time.Second, 60 * time.Second}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Errorf("retry waits = %v, want %v", waits, wantWaits)
	}
	if err == nil {
		t.Fatal("Embed error = nil, want rate-limit error")
	}
	if !IsRateLimited(err) {
		t.Errorf("IsRateLimited(%v) = false, want true", err)
	}
	if got := wrapped.Model(); got != inner.Model() {
		t.Errorf("wrapped.Model() = %q, want %q", got, inner.Model())
	}
	if got := wrapped.Dim(); got != inner.Dim() {
		t.Errorf("wrapped.Dim() = %d, want %d", got, inner.Dim())
	}

	t.Run("non-rate-limit error is not retried", func(t *testing.T) {
		boom := errors.New("boom")
		inner := NewFixture(4)
		inner.FailOn = func(int, []string) error {
			return boom
		}
		var waits []time.Duration
		wrapped := WithRateLimitRetry(inner, RetryOptions{
			MaxRetries: 3,
			BaseDelay:  20 * time.Second,
			Wait: func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				return nil
			},
		})

		_, err := wrapped.Embed(context.Background(), []string{"x"})
		if got := inner.Calls(); got != 1 {
			t.Errorf("inner.Calls() = %d, want 1", got)
		}
		if len(waits) != 0 {
			t.Errorf("retry waits = %v, want none", waits)
		}
		if !errors.Is(err, boom) {
			t.Errorf("Embed error = %v, want unchanged %v", err, boom)
		}
		if IsRateLimited(err) {
			t.Errorf("IsRateLimited(%v) = true, want false", err)
		}
	})
}
