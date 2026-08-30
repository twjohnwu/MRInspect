package prompt

import (
	"testing"
)

func TestModelLimits(t *testing.T) {
	t.Run("env unset returns exactly DefaultModelLimits", func(t *testing.T) {
		got, err := ModelLimitsFromEnv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(DefaultModelLimits) {
			t.Fatalf("got %d entries, want %d", len(got), len(DefaultModelLimits))
		}
		for model, tokens := range DefaultModelLimits {
			if got[model] != tokens {
				t.Errorf("got[%q] = %d, want %d", model, got[model], tokens)
			}
		}
	})

	t.Run("env adds a new model and overrides an existing one", func(t *testing.T) {
		got, err := ModelLimitsFromEnv("claude-sonnet-4-5-20250929:200000, claude-3-5-sonnet-20241022:150000")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["claude-sonnet-4-5-20250929"] != 200_000 {
			t.Errorf("new model not added: got %d", got["claude-sonnet-4-5-20250929"])
		}
		if got["claude-3-5-sonnet-20241022"] != 150_000 {
			t.Errorf("existing model not overridden: got %d", got["claude-3-5-sonnet-20241022"])
		}
		// untouched default entries survive the merge.
		if got["gemini-3.1-pro-preview"] != DefaultModelLimits["gemini-3.1-pro-preview"] {
			t.Errorf("untouched default entry changed: got %d", got["gemini-3.1-pro-preview"])
		}
	})

	t.Run("malformed entries return a named error", func(t *testing.T) {
		cases := []string{"foo", "bar:xyz", "baz:0", "baz:-5"}
		for _, c := range cases {
			_, err := ModelLimitsFromEnv(c)
			if err == nil {
				t.Fatalf("expected error for entry %q, got nil", c)
			}
		}
	})
}
