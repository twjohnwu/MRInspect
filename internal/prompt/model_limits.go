package prompt

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultModelLimits contains conservative context-window token caps for the
// default models documented in config and README. These values deliberately
// leave headroom below the providers' advertised maximum context windows.
var DefaultModelLimits = map[string]int{
	"claude-3-5-sonnet-20241022": 180_000,
	"claude-sonnet-5":            1_000_000,
	"gemini-2.5-pro":             1_000_000,
	"gemini-3.1-pro-preview":     1_048_576,
	"gemini-3.6-flash":           1_048_576,
	"gpt-5":                      360_000,
	"gpt-5.6":                    1_000_000,
}

// modelLimitsEnvVar names the operator override that extends or overrides
// DefaultModelLimits without requiring a code change, so a free-form model
// override documented in README.md (e.g. ANTHROPIC_MODEL) can also be
// registered with a token budget for multi-lane fan-out preflight (S-33).
const modelLimitsEnvVar = "MRI_MODEL_LIMITS"

// ModelLimitsFromEnv returns DefaultModelLimits merged with the raw value of
// MRI_MODEL_LIMITS captured by config.Load. The format is a comma-separated
// list of "model-name:tokens" pairs, e.g.
// "claude-sonnet-4-5-20250929:200000,gpt-5-mini:120000". Entries in the env
// var override a default of the same model name and add new models
// otherwise. A malformed pair (missing colon, or a non-positive/non-integer
// token count) is a named error - it is never silently skipped, so a typo'd
// override cannot silently reproduce the same operability defect it fixes.
func ModelLimitsFromEnv(raw string) (map[string]int, error) {
	merged := make(map[string]int, len(DefaultModelLimits))
	for model, tokens := range DefaultModelLimits {
		merged[model] = tokens
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return merged, nil
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, tokensStr, found := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		tokensStr = strings.TrimSpace(tokensStr)
		if !found || name == "" || tokensStr == "" {
			return nil, fmt.Errorf("%s: malformed entry %q, want \"model-name:tokens\"", modelLimitsEnvVar, entry)
		}
		tokens, err := strconv.Atoi(tokensStr)
		if err != nil {
			return nil, fmt.Errorf("%s: entry %q has a non-integer token count: %w", modelLimitsEnvVar, entry, err)
		}
		if tokens <= 0 {
			return nil, fmt.Errorf("%s: entry %q has a non-positive token count %d", modelLimitsEnvVar, entry, tokens)
		}
		merged[name] = tokens
	}

	return merged, nil
}
