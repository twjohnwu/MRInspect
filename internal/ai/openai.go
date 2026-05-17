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
	apiKey     string
	cfg        config.ProviderConfig
	log        *logger.Logger
}

func NewOpenAIProvider(apiKey string, cfg config.ProviderConfig, log *logger.Logger) *OpenAIProvider {
	return &OpenAIProvider{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiKey:     apiKey,
		cfg:        cfg,
		log:        log,
	}
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

		text, status, err := p.doRequest(ctx, reqBody)
		dur := time.Now().UnixMilli()
		_ = dur

		if err != nil {
			p.log.LogAPICall("openai", "responses", 0, false, err)
			lastErr = err
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("HTTP %d", status)
			p.log.LogAPICall("openai", "responses", 0, false, lastErr)
			continue
		}

		if status >= 400 {
			lastErr = fmt.Errorf("openai API error HTTP %d", status)
			p.log.LogAPICall("openai", "responses", 0, false, lastErr)
			return "", lastErr
		}

		p.log.LogAPICall("openai", "responses", 0, true, nil)
		return text, nil
	}
	return "", fmt.Errorf("openai: exceeded retry attempts: %w", lastErr)
}

func (p *OpenAIProvider) doRequest(ctx context.Context, reqBody map[string]any) (text string, statusCode int, err error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, err
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiAPIURL, bytes.NewReader(data))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	dur := time.Since(start).Milliseconds()
	_ = dur
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}

	var cr openaiResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", resp.StatusCode, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(cr.Output) == 0 || len(cr.Output[0].Content) == 0 {
		return "", resp.StatusCode, fmt.Errorf("empty output in response")
	}
	return cr.Output[0].Content[0].Text, resp.StatusCode, nil
}
