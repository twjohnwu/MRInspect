package ai

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

type AnthropicProvider struct {
	client *anthropic.Client
	cfg    config.ProviderConfig
	log    *logger.Logger
}

type AnthropicOption func(*[]option.RequestOption)

func WithAnthropicBaseURL(baseURL string) AnthropicOption {
	return func(options *[]option.RequestOption) {
		*options = append(*options, option.WithBaseURL(baseURL))
	}
}

func WithAnthropicHTTPClient(client *http.Client) AnthropicOption {
	return func(options *[]option.RequestOption) {
		*options = append(*options, option.WithHTTPClient(client))
	}
}

func NewAnthropicProvider(key string, cfg config.ProviderConfig, log *logger.Logger, opts ...AnthropicOption) *AnthropicProvider {
	clientOptions := []option.RequestOption{option.WithAPIKey(key)}
	for _, opt := range opts {
		opt(&clientOptions)
	}
	client := anthropic.NewClient(clientOptions...)
	return &AnthropicProvider{client: client, cfg: cfg, log: log}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	if opts.Model == "" {
		opts.Model = p.cfg.Model
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = p.cfg.MaxTokens
	}

	start := time.Now()
	msg, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(opts.Model)),
		MaxTokens: anthropic.F(int64(opts.MaxTokens)),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	dur := time.Since(start).Milliseconds()

	if err != nil {
		p.log.LogAIAPICall("anthropic", "messages", dur, false, err, nil)
		return "", fmt.Errorf("anthropic Generate: %w", err)
	}

	var usage *logger.TokenUsage
	if !msg.JSON.Usage.IsMissing() && !msg.JSON.Usage.IsNull() {
		usage = &logger.TokenUsage{
			InputTokens:  msg.Usage.InputTokens,
			OutputTokens: msg.Usage.OutputTokens,
		}
	}
	p.log.LogAIAPICall("anthropic", "messages", dur, true, nil, usage)

	if len(msg.Content) == 0 {
		return "", fmt.Errorf("anthropic Generate: empty response")
	}
	block := msg.Content[0]
	if block.Type != "text" {
		return "", fmt.Errorf("anthropic Generate: unexpected content type %q", block.Type)
	}
	return block.Text, nil
}
