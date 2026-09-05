package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type countingTransport struct {
	calls atomic.Int64
}

func (transport *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("unexpected HTTP request")
}

type statusTransport struct {
	code int
}

func (transport statusTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: transport.code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestStatusError_IsRateLimited(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "429", err: &StatusError{Code: http.StatusTooManyRequests}, want: true},
		{name: "500", err: &StatusError{Code: http.StatusInternalServerError}, want: false},
		{name: "wrapped 429", err: fmt.Errorf("embed request: %w", &StatusError{Code: http.StatusTooManyRequests}), want: true},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimited(tt.err); got != tt.want {
				t.Errorf("IsRateLimited(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestStatusError_RemoteClientsPreserveHTTP429(t *testing.T) {
	client := &http.Client{Transport: statusTransport{code: http.StatusTooManyRequests}}

	for _, provider := range []string{"openai", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			embedder, err := New(provider, "test-key", WithBaseURL("https://embed.test"), WithHTTPClient(client))
			if err != nil {
				t.Fatalf("New(%s): %v", provider, err)
			}
			_, err = embedder.Embed(context.Background(), []string{"text"})
			if err == nil {
				t.Fatal("Embed error = nil, want HTTP 429")
			}
			if !strings.Contains(err.Error(), "HTTP 429") {
				t.Errorf("Embed error = %q, want it to contain %q", err, "HTTP 429")
			}
			if !IsRateLimited(err) {
				t.Errorf("IsRateLimited(%v) = false, want true", err)
			}
		})
	}
}

// TestS03_ConstructorValidation verifies REQ-01 / S-03.
func TestS03_ConstructorValidation(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		key      string
		wantErr  string
	}{
		{name: "empty provider", provider: "", key: "test-key", wantErr: "MRI_EMBED_PROVIDER"},
		{name: "unsupported provider", provider: "anthropic", key: "test-key", wantErr: "MRI_EMBED_PROVIDER"},
		{name: "openai missing key", provider: "openai", key: "", wantErr: "MRI_RAG_EMBED_KEY"},
		{name: "gemini missing key", provider: "gemini", key: "", wantErr: "MRI_RAG_EMBED_KEY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &countingTransport{}
			client := &http.Client{Transport: transport}

			_, err := New(tt.provider, tt.key, WithHTTPClient(client))
			if err == nil {
				t.Errorf("New(%q, key present=%t): want error containing %q, got nil", tt.provider, tt.key != "", tt.wantErr)
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New(%q, key present=%t): want error containing %q, got %q", tt.provider, tt.key != "", tt.wantErr, err)
			}
			if got := transport.calls.Load(); got != 0 {
				t.Errorf("HTTP calls: want 0, got %d", got)
			}
		})
	}
}

// TestS01_OpenAIEmbed verifies REQ-01 / S-01.
func TestS01_OpenAIEmbed(t *testing.T) {
	const key = "openai-test-key"
	texts := []string{"first text", "second text"}
	wantVectors := [][]float32{{0.25, -0.5}, {1.5, 2.25}}

	var (
		calls         int
		requestURL    string
		authorization string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		requestURL = r.URL.String()
		authorization = r.Header.Get("Authorization")

		if r.Method != http.MethodPost {
			t.Errorf("method: want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path: want /v1/embeddings, got %s", r.URL.Path)
		}
		var body struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		} else {
			if !reflect.DeepEqual(body.Input, texts) {
				t.Errorf("input batch: want %#v, got %#v", texts, body.Input)
			}
			if body.Model != OpenAIModel {
				t.Errorf("model: want %q, got %q", OpenAIModel, body.Model)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.25,-0.5],"index":0},{"embedding":[1.5,2.25],"index":1}]}`)
	}))
	defer server.Close()

	embedder, err := New("openai", key, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New(openai): %v", err)
	}
	vectors, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if calls != 1 {
		t.Errorf("HTTP calls: want 1 batch request, got %d", calls)
	}
	if len(vectors) != len(texts) {
		t.Errorf("vector count: want %d, got %d", len(texts), len(vectors))
	}
	if !reflect.DeepEqual(vectors, wantVectors) {
		t.Errorf("vectors: want %#v, got %#v", wantVectors, vectors)
	}
	if authorization != "Bearer "+key {
		t.Errorf("Authorization: want Bearer credential, got %q", authorization)
	}
	if strings.Contains(requestURL, key) {
		t.Errorf("request URL contains credential: %q", requestURL)
	}
}

// TestS02_GeminiEmbed verifies REQ-01 / S-02.
func TestS02_GeminiEmbed(t *testing.T) {
	const key = "gemini-test-key"
	texts := []string{"first text", "second text"}
	wantVectors := [][]float32{{-1.25, 0.75}, {3.5, -4.25}}

	var (
		calls      int
		requestURL string
		apiKey     string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		requestURL = r.URL.String()
		apiKey = r.Header.Get("x-goog-api-key")

		if r.Method != http.MethodPost {
			t.Errorf("method: want POST, got %s", r.Method)
		}
		wantPath := "/v1beta/models/" + GeminiModel + ":batchEmbedContents"
		if r.URL.Path != wantPath {
			t.Errorf("path: want %s, got %s", wantPath, r.URL.Path)
		}
		var body struct {
			Requests []struct {
				Model   string `json:"model"`
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				OutputDimensionality int `json:"outputDimensionality"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		} else {
			if len(body.Requests) != len(texts) {
				t.Errorf("request count in batch: want %d, got %d", len(texts), len(body.Requests))
			}
			for i, request := range body.Requests {
				if i >= len(texts) {
					break
				}
				if request.Model != "models/"+GeminiModel {
					t.Errorf("request %d model: want %q, got %q", i, "models/"+GeminiModel, request.Model)
				}
				if len(request.Content.Parts) != 1 || request.Content.Parts[0].Text != texts[i] {
					t.Errorf("request %d text: want %q, got %#v", i, texts[i], request.Content.Parts)
				}
				if request.OutputDimensionality != GeminiDim {
					t.Errorf("request %d outputDimensionality: want %d, got %d", i, GeminiDim, request.OutputDimensionality)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"embeddings":[{"values":[-1.25,0.75]},{"values":[3.5,-4.25]}]}`)
	}))
	defer server.Close()

	embedder, err := New("gemini", key, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New(gemini): %v", err)
	}
	vectors, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if calls != 1 {
		t.Errorf("HTTP calls: want 1 batch request, got %d", calls)
	}
	if len(vectors) != len(texts) {
		t.Errorf("vector count: want %d, got %d", len(texts), len(vectors))
	}
	if !reflect.DeepEqual(vectors, wantVectors) {
		t.Errorf("vectors: want %#v, got %#v", wantVectors, vectors)
	}
	if apiKey != key {
		t.Errorf("x-goog-api-key: want credential header, got %q", apiKey)
	}
	if strings.Contains(requestURL, key) {
		t.Errorf("request URL contains credential: %q", requestURL)
	}
}
