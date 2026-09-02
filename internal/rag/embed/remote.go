package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	openAIBaseURL = "https://api.openai.com"
	geminiBaseURL = "https://generativelanguage.googleapis.com"
)

type remoteClient struct {
	httpClient *http.Client
	baseURL    string
	key        string
}

func (client remoteClient) postJSON(
	ctx context.Context,
	path string,
	headers map[string]string,
	requestBody any,
	responseBody any,
) error {
	data, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal embedding request: %w", err)
	}

	endpoint := strings.TrimRight(client.baseURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send embedding request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode embedding response: %w", err)
	}
	return nil
}

type openAIEmbedder struct {
	remote remoteClient
}

func newOpenAIEmbedder(key string, config options) *openAIEmbedder {
	return &openAIEmbedder{remote: remoteClient{
		httpClient: config.httpClient,
		baseURL:    config.baseURL,
		key:        key,
	}}
}

func (*openAIEmbedder) Model() string { return OpenAIModel }

func (*openAIEmbedder) Dim() int { return OpenAIDim }

func (embedder *openAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	requestBody := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{
		Model: OpenAIModel,
		Input: texts,
	}
	var responseBody struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}

	if err := embedder.remote.postJSON(
		ctx,
		"/v1/embeddings",
		map[string]string{"Authorization": "Bearer " + embedder.remote.key},
		requestBody,
		&responseBody,
	); err != nil {
		return nil, fmt.Errorf("openai Embed: %w", err)
	}
	if len(responseBody.Data) != len(texts) {
		return nil, fmt.Errorf("openai Embed: response cardinality %d does not match input count %d", len(responseBody.Data), len(texts))
	}

	vectors := make([][]float32, len(texts))
	seen := make([]bool, len(texts))
	for _, item := range responseBody.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("openai Embed: response index %d out of range for input count %d", item.Index, len(texts))
		}
		if seen[item.Index] {
			return nil, fmt.Errorf("openai Embed: duplicate response index %d", item.Index)
		}
		seen[item.Index] = true
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

type geminiEmbedder struct {
	remote remoteClient
}

func newGeminiEmbedder(key string, config options) *geminiEmbedder {
	return &geminiEmbedder{remote: remoteClient{
		httpClient: config.httpClient,
		baseURL:    config.baseURL,
		key:        key,
	}}
}

func (*geminiEmbedder) Model() string { return GeminiModel }

func (*geminiEmbedder) Dim() int { return GeminiDim }

func (embedder *geminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Parts []part `json:"parts"`
	}
	type embedRequest struct {
		Model                string  `json:"model"`
		Content              content `json:"content"`
		OutputDimensionality int     `json:"outputDimensionality"`
	}
	requestBody := struct {
		Requests []embedRequest `json:"requests"`
	}{
		Requests: make([]embedRequest, len(texts)),
	}
	for index, text := range texts {
		requestBody.Requests[index] = embedRequest{
			Model:                "models/" + GeminiModel,
			Content:              content{Parts: []part{{Text: text}}},
			OutputDimensionality: GeminiDim,
		}
	}
	var responseBody struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}

	if err := embedder.remote.postJSON(
		ctx,
		"/v1beta/models/"+GeminiModel+":batchEmbedContents",
		map[string]string{"x-goog-api-key": embedder.remote.key},
		requestBody,
		&responseBody,
	); err != nil {
		return nil, fmt.Errorf("gemini Embed: %w", err)
	}
	if len(responseBody.Embeddings) != len(texts) {
		return nil, fmt.Errorf("gemini Embed: response cardinality %d does not match input count %d", len(responseBody.Embeddings), len(texts))
	}

	vectors := make([][]float32, len(responseBody.Embeddings))
	for index, embedding := range responseBody.Embeddings {
		vectors[index] = embedding.Values
	}
	return vectors, nil
}
