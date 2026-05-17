package ai

import (
	"context"
	"fmt"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
	"google.golang.org/genai"
)

type GeminiProvider struct {
	client *genai.Client
	cfg    config.ProviderConfig
	log    *logger.Logger
}

func NewGeminiProvider(ctx context.Context, key string, cfg config.ProviderConfig, log *logger.Logger) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("NewGeminiProvider: %w", err)
	}
	return &GeminiProvider{client: client, cfg: cfg, log: log}, nil
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	model := opts.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}

	contents := []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}

	start := time.Now()
	resp, err := p.client.Models.GenerateContent(ctx, model, contents, &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxTokens),
		Temperature:     float32Ptr(float32(p.cfg.Temperature)),
	})
	dur := time.Since(start).Milliseconds()

	if err != nil {
		p.log.LogAPICall("gemini", "generateContent", dur, false, err)
		return "", fmt.Errorf("gemini Generate: %w", err)
	}

	p.log.LogAPICall("gemini", "generateContent", dur, true, nil)

	if resp == nil {
		return "", fmt.Errorf("gemini Generate: nil response")
	}
	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("gemini Generate: empty response text")
	}
	return text, nil
}

func float32Ptr(v float32) *float32 { return &v }
