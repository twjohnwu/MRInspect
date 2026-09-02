package embed

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	OpenAIModel = "text-embedding-3-small"
	OpenAIDim   = 1536
	GeminiModel = "gemini-embedding-001"
	GeminiDim   = 768
)

const defaultHTTPTimeout = 60 * time.Second

// Embedder embeds text batches and reports the fixed model and vector dimension.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
	Dim() int
}

type options struct {
	baseURL    string
	httpClient *http.Client
}

// Option configures a remote embedder.
type Option func(*options)

// WithBaseURL overrides the embeddings API base URL.
func WithBaseURL(baseURL string) Option {
	return func(opts *options) {
		opts.baseURL = baseURL
	}
}

// WithHTTPClient overrides the client used for embeddings API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// New constructs an embedder for provider.
func New(provider, key string, opts ...Option) (Embedder, error) {
	if provider != "openai" && provider != "gemini" {
		return nil, fmt.Errorf("MRI_EMBED_PROVIDER must be one of openai or gemini, got %q", provider)
	}
	if key == "" {
		return nil, fmt.Errorf("MRI_RAG_EMBED_KEY must not be empty")
	}

	config := &options{
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(config)
	}

	switch provider {
	case "openai":
		if config.baseURL == "" {
			config.baseURL = openAIBaseURL
		}
		return newOpenAIEmbedder(key, *config), nil
	case "gemini":
		if config.baseURL == "" {
			config.baseURL = geminiBaseURL
		}
		return newGeminiEmbedder(key, *config), nil
	default:
		panic("unreachable provider validation")
	}
}
