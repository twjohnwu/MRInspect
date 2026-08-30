package ai

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/genai"
	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

type GeminiProvider struct {
	client *genai.Client
	cfg    config.ProviderConfig
	log    *logger.Logger
}

type GeminiOption func(*genai.ClientConfig)

func WithGeminiBaseURL(baseURL string) GeminiOption {
	return func(config *genai.ClientConfig) {
		config.HTTPOptions.BaseURL = baseURL
	}
}

func WithGeminiHTTPClient(client *http.Client) GeminiOption {
	return func(config *genai.ClientConfig) {
		config.HTTPClient = client
	}
}

func NewGeminiProvider(ctx context.Context, key string, cfg config.ProviderConfig, log *logger.Logger, opts ...GeminiOption) (*GeminiProvider, error) {
	clientConfig := &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	}
	for _, opt := range opts {
		opt(clientConfig)
	}
	client, err := genai.NewClient(ctx, clientConfig)
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
		p.log.LogAIAPICall("gemini", "generateContent", dur, false, err, nil)
		return "", fmt.Errorf("gemini Generate: %w", err)
	}

	var usage *logger.TokenUsage
	if resp != nil && resp.UsageMetadata != nil {
		usage = &logger.TokenUsage{
			InputTokens:  int64(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
		}
	}
	p.log.LogAIAPICall("gemini", "generateContent", dur, true, nil, usage)

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
