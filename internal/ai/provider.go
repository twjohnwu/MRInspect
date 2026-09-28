package ai

import (
	"context"
	"fmt"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

type GenerateOptions struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

// Provider is the AI backend abstraction implemented by Anthropic, Gemini, and OpenAI.
type Provider interface {
	Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error)
	Name() string
}

func NewProvider(cfg config.Config, log *logger.Logger) (Provider, error) {
	return newProvider(cfg, log)
}

// newProvider is NewProvider's implementation, with an unexported seam for
// tests to inject OpenAI options (an httptest base URL and HTTP client)
// through the config-driven construction path. NewProvider calls this with
// no extra options.
func newProvider(cfg config.Config, log *logger.Logger, openaiOpts ...OpenAIOption) (Provider, error) {
	pcfg := cfg.Providers[cfg.AIProvider]
	var provider Provider
	switch cfg.AIProvider {
	case config.ProviderAnthropic:
		provider = NewAnthropicProvider(cfg.AIProviderKey, pcfg, log)
	case config.ProviderGemini:
		p, err := NewGeminiProvider(context.Background(), cfg.AIProviderKey, pcfg, log)
		if err != nil {
			return nil, fmt.Errorf("NewProvider: %s: %w", cfg.AIProvider, err)
		}
		provider = p
	case config.ProviderOpenAI:
		opts := append([]OpenAIOption{
			WithOpenAIRemoteState(cfg.Enrichment.Enabled && cfg.Enrichment.RemoteState == "enabled"),
		}, openaiOpts...)
		provider = NewOpenAIProvider(cfg.AIProviderKey, pcfg, log, opts...)
	default:
		return nil, fmt.Errorf("NewProvider: unknown provider %q", cfg.AIProvider)
	}
	return WithRetry(provider, cfg.API), nil
}
