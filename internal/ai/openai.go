package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

const openaiAPIURL = "https://api.openai.com/v1/responses"

type OpenAIProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	cfg        config.ProviderConfig
	log        *logger.Logger
}

type OpenAIOption func(*OpenAIProvider)

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
	}

	text, err := p.callWithRetry(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("openai Generate: %w", err)
	}
	return text, nil
}

type openaiResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (p *OpenAIProvider) callWithRetry(ctx context.Context, reqBody map[string]any) (string, error) {
	const maxAttempts = 3
	const baseDelayMs = 1000
	const maxDelayMs = 10000

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Min(
				float64(baseDelayMs)*math.Pow(2, float64(attempt-1)),
				maxDelayMs,
			)) * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		text, status, durationMs, usage, err := p.doRequest(ctx, reqBody)

		if err != nil {
			p.log.LogAIAPICall("openai", "responses", durationMs, false, err, usage)
			lastErr = err
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("HTTP %d", status)
			p.log.LogAIAPICall("openai", "responses", durationMs, false, lastErr, usage)
			continue
		}

		if status >= 400 {
			lastErr = fmt.Errorf("openai API error HTTP %d", status)
			p.log.LogAIAPICall("openai", "responses", durationMs, false, lastErr, usage)
			return "", lastErr
		}

		p.log.LogAIAPICall("openai", "responses", durationMs, true, nil, usage)
		return text, nil
	}
	return "", fmt.Errorf("openai: exceeded retry attempts: %w", lastErr)
}

func (p *OpenAIProvider) doRequest(ctx context.Context, reqBody map[string]any) (text string, statusCode int, durationMs int64, usage *logger.TokenUsage, err error) {
	start := time.Now()
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, time.Since(start).Milliseconds(), nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(data))
	if err != nil {
		return "", 0, time.Since(start).Milliseconds(), nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	durationMs = time.Since(start).Milliseconds()
	if err != nil {
		return "", 0, durationMs, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, durationMs, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, durationMs, nil, nil
	}

	var cr openaiResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", resp.StatusCode, durationMs, nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if cr.Usage != nil {
		usage = &logger.TokenUsage{
			InputTokens:  cr.Usage.InputTokens,
			OutputTokens: cr.Usage.OutputTokens,
		}
	}
	if len(cr.Output) == 0 || len(cr.Output[0].Content) == 0 {
		return "", resp.StatusCode, durationMs, usage, fmt.Errorf("empty output in response")
	}
	return cr.Output[0].Content[0].Text, resp.StatusCode, durationMs, usage, nil
}
