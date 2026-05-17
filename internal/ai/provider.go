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
	pcfg := cfg.Providers[cfg.AIProvider]
	switch cfg.AIProvider {
	case config.ProviderAnthropic:
		return NewAnthropicProvider(cfg.AIProviderKey, pcfg, log), nil
	case config.ProviderGemini:
		p, err := NewGeminiProvider(context.Background(), cfg.AIProviderKey, pcfg, log)
		if err != nil {
			return nil, fmt.Errorf("NewProvider: %s: %w", cfg.AIProvider, err)
		}
		return p, nil
	case config.ProviderOpenAI:
		return NewOpenAIProvider(cfg.AIProviderKey, pcfg, log), nil
	default:
		return nil, fmt.Errorf("NewProvider: unknown provider %q", cfg.AIProvider)
	}
}
