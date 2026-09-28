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

// anthropicHistory is the Anthropic-private continuation payload: every
// message sent or received so far, replayed verbatim and extended with the
// tool-result turn on the next local call (REQ-02).
type anthropicHistory struct {
	messages []anthropic.MessageParam
}

func anthropicToolDefs(tools []ToolSpec) []anthropic.ToolParam {
	defs := make([]anthropic.ToolParam, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F[interface{}](tool.Parameters),
		})
	}
	return defs
}

func anthropicToolUnionParams(tools []anthropic.ToolParam) []anthropic.ToolUnionUnionParam {
	params := make([]anthropic.ToolUnionUnionParam, 0, len(tools))
	for _, tool := range tools {
		params = append(params, tool)
	}
	return params
}

func (p *AnthropicProvider) GenerateTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	model := req.Options.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}

	var messagesSentThisTurn []anthropic.MessageParam
	var messages []anthropic.MessageParam
	endpoint := "turn1"
	if req.Continuation != nil {
		endpoint = "turn2"
		history, ok := req.Continuation.history.(anthropicHistory)
		if !ok {
			return TurnResult{}, fmt.Errorf("anthropic GenerateTurn: invalid continuation")
		}
		blocks := []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(UntrustedFrame)}
		for _, result := range req.ToolResults {
			content := result.Content
			isError := false
			if result.Error != "" {
				content = "error: " + result.Error
				isError = true
			}
			blocks = append(blocks, anthropic.NewToolResultBlock(result.ID, content, isError))
		}
		toolResultMessage := anthropic.NewUserMessage(blocks...)
		messagesSentThisTurn = append(append([]anthropic.MessageParam{}, history.messages...), toolResultMessage)
		messages = messagesSentThisTurn
	} else {
		userMessage := anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt + "\n\n" + HintSentence))
		messagesSentThisTurn = []anthropic.MessageParam{userMessage}
		messages = messagesSentThisTurn
	}

	start := time.Now()
	msg, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(model)),
		MaxTokens: anthropic.F(int64(maxTokens)),
		Messages:  anthropic.F(messages),
		Tools:     anthropic.F(anthropicToolUnionParams(anthropicToolDefs(req.Tools))),
	})
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		p.log.LogAIAPICall("anthropic", "messages/"+endpoint, durationMs, false, err, nil)
		return TurnResult{}, fmt.Errorf("anthropic GenerateTurn: %w", err)
	}

	var text string
	var toolCalls []ToolCall
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{ID: block.ID, Name: block.Name, Args: block.Input})
		}
	}

	usage := &logger.TokenUsage{
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	p.log.LogAIAPICall("anthropic", "messages/"+endpoint, durationMs, true, nil, usage)

	newHistory := anthropicHistory{
		messages: append(append([]anthropic.MessageParam{}, messagesSentThisTurn...), msg.ToParam()),
	}

	return TurnResult{
		Text:      text,
		ToolCalls: toolCalls,
		Continuation: &Continuation{
			mode:    "local",
			history: newHistory,
		},
		Usage: usage,
	}, nil
}

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
