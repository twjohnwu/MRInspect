package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

const openaiAPIURL = "https://api.openai.com/v1/responses"

type OpenAIProvider struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	cfg         config.ProviderConfig
	log         *logger.Logger
	remoteState bool
}

type OpenAIOption func(*OpenAIProvider)

func WithOpenAIRemoteState(enabled bool) OpenAIOption {
	return func(provider *OpenAIProvider) {
		provider.remoteState = enabled
	}
}

func WithOpenAIBaseURL(baseURL string) OpenAIOption {
	return func(provider *OpenAIProvider) {
		provider.baseURL = baseURL
	}
}

func WithOpenAIHTTPClient(client *http.Client) OpenAIOption {
	return func(provider *OpenAIProvider) {
		provider.httpClient = client
	}
}

func NewOpenAIProvider(apiKey string, cfg config.ProviderConfig, log *logger.Logger, opts ...OpenAIOption) *OpenAIProvider {
	provider := &OpenAIProvider{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    openaiAPIURL,
		apiKey:     apiKey,
		cfg:        cfg,
		log:        log,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (p *OpenAIProvider) Name() string { return "openai" }

// openaiHistory is the OpenAI-private continuation payload: the original
// turn-1 input item plus every raw output item the API returned, replayed
// verbatim on the next local turn (REQ-02).
type openaiHistory struct {
	inputItem   json.RawMessage
	outputItems []json.RawMessage
	responseID  string
}

func openaiToolDefs(tools []ToolSpec) []map[string]any {
	defs := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		})
	}
	return defs
}

// openaiToolResultOutput renders one ToolResult as the string "output" value
// expected by a function_call_output item: the untrusted-content frame
// prepended to successful content, or a small JSON error envelope on failure.
func openaiToolResultOutput(result ToolResult) (string, error) {
	if result.Error != "" {
		data, err := json.Marshal(map[string]string{"error": result.Error})
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return UntrustedFrame + "\n" + result.Content, nil
}

func (p *OpenAIProvider) GenerateTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	model := req.Options.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}

	input := make([]any, 0, 2+len(req.ToolResults))
	endpoint := "turn1"
	if req.Continuation != nil {
		endpoint = "turn2"
		history, ok := req.Continuation.history.(openaiHistory)
		if !ok {
			return TurnResult{}, fmt.Errorf("openai GenerateTurn: invalid continuation")
		}
		input = append(input, json.RawMessage(history.inputItem))
		for _, item := range history.outputItems {
			input = append(input, json.RawMessage(item))
		}
		for _, result := range req.ToolResults {
			output, err := openaiToolResultOutput(result)
			if err != nil {
				return TurnResult{}, fmt.Errorf("openai GenerateTurn: encode tool result: %w", err)
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": result.ID,
				"output":  output,
			})
		}
	} else {
		inputItem := map[string]any{
			"role":    "user",
			"content": req.Prompt + "\n\n" + HintSentence,
		}
		input = append(input, inputItem)
	}

	reqBody := map[string]any{
		"model":             model,
		"input":             input,
		"tools":             openaiToolDefs(req.Tools),
		"max_output_tokens": maxTokens,
		"store":             false,
		"include":           []string{"reasoning.encrypted_content"},
	}

	envelope, statusCode, durationMs, err := p.doTurnRequest(ctx, reqBody)
	if err != nil {
		p.log.LogAIAPICall("openai", "responses/"+endpoint, durationMs, false, err, nil)
		return TurnResult{}, fmt.Errorf("openai GenerateTurn: %w", err)
	}
	if apiErr := openaiStatusError(statusCode); apiErr != nil {
		p.log.LogAIAPICall("openai", "responses/"+endpoint, durationMs, false, apiErr, nil)
		return TurnResult{}, fmt.Errorf("openai GenerateTurn: %w", apiErr)
	}

	text, toolCalls, err := parseOpenAIOutputItems(envelope.Output)
	if err != nil {
		p.log.LogAIAPICall("openai", "responses/"+endpoint, durationMs, false, err, nil)
		return TurnResult{}, fmt.Errorf("openai GenerateTurn: %w", err)
	}

	var usage *logger.TokenUsage
	if envelope.Usage != nil {
		usage = &logger.TokenUsage{
			InputTokens:  envelope.Usage.InputTokens,
			OutputTokens: envelope.Usage.OutputTokens,
		}
	}
	p.log.LogAIAPICall("openai", "responses/"+endpoint, durationMs, true, nil, usage)

	inputItem, err := json.Marshal(input[0])
	if err != nil {
		return TurnResult{}, fmt.Errorf("openai GenerateTurn: encode continuation input item: %w", err)
	}

	return TurnResult{
		Text:      text,
		ToolCalls: toolCalls,
		Continuation: &Continuation{
			mode: "local",
			history: openaiHistory{
				inputItem:   inputItem,
				outputItems: envelope.Output,
				responseID:  envelope.ID,
			},
		},
		Usage: usage,
	}, nil
}

func (p *OpenAIProvider) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	model := opts.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}

	reqBody := map[string]any{
		"model":             model,
		"input":             prompt,
		"max_output_tokens": maxTokens,
		"store":             false,
	}

	envelope, statusCode, durationMs, err := p.doTurnRequest(ctx, reqBody)
	if err != nil {
		p.log.LogAIAPICall("openai", "responses", durationMs, false, err, nil)
		return "", fmt.Errorf("openai Generate: %w", err)
	}
	if apiErr := openaiStatusError(statusCode); apiErr != nil {
		p.log.LogAIAPICall("openai", "responses", durationMs, false, apiErr, nil)
		return "", fmt.Errorf("openai Generate: %w", apiErr)
	}

	text, _, err := parseOpenAIOutputItems(envelope.Output)
	if err != nil {
		p.log.LogAIAPICall("openai", "responses", durationMs, false, err, nil)
		return "", fmt.Errorf("openai Generate: %w", err)
	}

	var usage *logger.TokenUsage
	if envelope.Usage != nil {
		usage = &logger.TokenUsage{
			InputTokens:  envelope.Usage.InputTokens,
			OutputTokens: envelope.Usage.OutputTokens,
		}
	}
	if text == "" {
		p.log.LogAIAPICall("openai", "responses", durationMs, false, fmt.Errorf("empty output in response"), usage)
		return "", fmt.Errorf("openai Generate: empty output in response")
	}

	p.log.LogAIAPICall("openai", "responses", durationMs, true, nil, usage)
	return text, nil
}

// openaiResponseEnvelope is the decoded Responses API payload shared by
// Generate and GenerateTurn. Output items are kept raw so GenerateTurn can
// replay them verbatim on the next local turn (REQ-02).
type openaiResponseEnvelope struct {
	ID     string            `json:"id"`
	Output []json.RawMessage `json:"output"`
	Usage  *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// openaiOutputItem is one entry of the response's "output" array: either a
// "message" (default when Type is empty, for backward compatibility with
// plain-text responses), a "function_call", or a passthrough item such as
// "reasoning" that carries no text or tool call of its own.
type openaiOutputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func parseOpenAIOutputItems(items []json.RawMessage) (text string, toolCalls []ToolCall, err error) {
	for _, raw := range items {
		var item openaiOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return "", nil, fmt.Errorf("unmarshal output item: %w", err)
		}
		switch item.Type {
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:   item.CallID,
				Name: item.Name,
				Args: json.RawMessage(item.Arguments),
			})
		case "", "message":
			for _, content := range item.Content {
				text += content.Text
			}
		}
	}
	return text, toolCalls, nil
}

// openaiStatusError classifies a non-2xx HTTP status into a retryable error
// (429 / 5xx, returned as-is) or a permanent one (4xx other than 429,
// wrapped with withoutRetry), or nil for a successful status.
func openaiStatusError(statusCode int) error {
	switch {
	case statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		return fmt.Errorf("HTTP %d", statusCode)
	case statusCode >= http.StatusBadRequest:
		return withoutRetry(fmt.Errorf("openai API error HTTP %d", statusCode))
	default:
		return nil
	}
}

func (p *OpenAIProvider) doTurnRequest(ctx context.Context, reqBody map[string]any) (envelope openaiResponseEnvelope, statusCode int, durationMs int64, err error) {
	start := time.Now()
	data, err := json.Marshal(reqBody)
	if err != nil {
		return envelope, 0, time.Since(start).Milliseconds(), err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(data))
	if err != nil {
		return envelope, 0, time.Since(start).Milliseconds(), err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(request)
	durationMs = time.Since(start).Milliseconds()
	if err != nil {
		return envelope, 0, durationMs, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope, resp.StatusCode, durationMs, err
	}

	if resp.StatusCode != http.StatusOK {
		return envelope, resp.StatusCode, durationMs, nil
	}

	if err := json.Unmarshal(body, &envelope); err != nil {
		return envelope, resp.StatusCode, durationMs, fmt.Errorf("unmarshal response: %w", err)
	}
	return envelope, resp.StatusCode, durationMs, nil
}
