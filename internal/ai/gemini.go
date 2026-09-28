package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// geminiHistory is the Gemini-private continuation payload: every
// *genai.Content sent or received so far (preserving fields such as
// ThoughtSignature verbatim), plus a call-ID-to-function-name lookup so a
// later ToolResult (which carries only an ID) can be turned back into a
// named function response (REQ-02).
type geminiHistory struct {
	contents []*genai.Content
	names    map[string]string
}

func normalizeGeminiSchemaTypes(schema map[string]any) {
	if schemaType, ok := schema["type"].(string); ok {
		schema["type"] = strings.ToUpper(schemaType)
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			if propertySchema, ok := property.(map[string]any); ok {
				normalizeGeminiSchemaTypes(propertySchema)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		normalizeGeminiSchemaTypes(items)
	}
}

func geminiToolDefs(tools []ToolSpec) []*genai.Tool {
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		var schema genai.Schema
		var rawSchema map[string]any
		if err := json.Unmarshal(tool.Parameters, &rawSchema); err != nil {
			schema = genai.Schema{}
		} else {
			normalizeGeminiSchemaTypes(rawSchema)
			normalized, err := json.Marshal(rawSchema)
			if err != nil || json.Unmarshal(normalized, &schema) != nil {
				schema = genai.Schema{}
			}
		}
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  &schema,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

// parseGeminiContent splits one response Content into its text and
// function-call parts. A function call with no server-assigned ID is given
// a synthetic "<name>#<index>" ID (index counted across all function-call
// parts in this content), which doubles as the ToolCall.ID a later
// ToolResult must echo back.
func parseGeminiContent(content *genai.Content) (text string, toolCalls []ToolCall, err error) {
	if content == nil {
		return "", nil, nil
	}
	index := 0
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.FunctionCall != nil {
			id := part.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("%s#%d", part.FunctionCall.Name, index)
			}
			args, marshalErr := json.Marshal(part.FunctionCall.Args)
			if marshalErr != nil {
				return "", nil, fmt.Errorf("marshal function call args: %w", marshalErr)
			}
			toolCalls = append(toolCalls, ToolCall{ID: id, Name: part.FunctionCall.Name, Args: args})
			index++
			continue
		}
		text += part.Text
	}
	return text, toolCalls, nil
}

func (p *GeminiProvider) GenerateTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	model := req.Options.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}

	var contents []*genai.Content
	var priorNames map[string]string
	endpoint := "turn1"
	if req.Continuation != nil {
		endpoint = "turn2"
		history, ok := req.Continuation.history.(geminiHistory)
		if !ok {
			return TurnResult{}, fmt.Errorf("gemini GenerateTurn: invalid continuation")
		}
		priorNames = history.names
		parts := []*genai.Part{genai.NewPartFromText(UntrustedFrame)}
		for _, result := range req.ToolResults {
			var response map[string]any
			if result.Error != "" {
				response = map[string]any{"error": result.Error}
			} else {
				response = map[string]any{"output": result.Content}
			}
			part := genai.NewPartFromFunctionResponse(priorNames[result.ID], response)
			part.FunctionResponse.ID = result.ID
			parts = append(parts, part)
		}
		contents = append(append([]*genai.Content{}, history.contents...), genai.NewContentFromParts(parts, genai.RoleUser))
	} else {
		contents = []*genai.Content{genai.NewContentFromText(req.Prompt+"\n\n"+HintSentence, genai.RoleUser)}
	}

	start := time.Now()
	resp, err := p.client.Models.GenerateContent(ctx, model, contents, &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxTokens),
		Temperature:     float32Ptr(float32(p.cfg.Temperature)),
		Tools:           geminiToolDefs(req.Tools),
	})
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		p.log.LogAIAPICall("gemini", "generateContent/"+endpoint, durationMs, false, err, nil)
		return TurnResult{}, fmt.Errorf("gemini GenerateTurn: %w", err)
	}
	if resp == nil || len(resp.Candidates) == 0 {
		emptyErr := fmt.Errorf("empty response")
		p.log.LogAIAPICall("gemini", "generateContent/"+endpoint, durationMs, false, emptyErr, nil)
		return TurnResult{}, fmt.Errorf("gemini GenerateTurn: %w", emptyErr)
	}

	candidateContent := resp.Candidates[0].Content
	text, toolCalls, err := parseGeminiContent(candidateContent)
	if err != nil {
		p.log.LogAIAPICall("gemini", "generateContent/"+endpoint, durationMs, false, err, nil)
		return TurnResult{}, fmt.Errorf("gemini GenerateTurn: %w", err)
	}

	var usage *logger.TokenUsage
	if resp.UsageMetadata != nil {
		usage = &logger.TokenUsage{
			InputTokens:  int64(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount + resp.UsageMetadata.ThoughtsTokenCount),
		}
	}
	p.log.LogAIAPICall("gemini", "generateContent/"+endpoint, durationMs, true, nil, usage)

	names := make(map[string]string, len(priorNames)+len(toolCalls))
	for id, name := range priorNames {
		names[id] = name
	}
	for _, call := range toolCalls {
		names[call.ID] = call.Name
	}

	return TurnResult{
		Text:      text,
		ToolCalls: toolCalls,
		Continuation: &Continuation{
			mode: "local",
			history: geminiHistory{
				contents: append(append([]*genai.Content{}, contents...), candidateContent),
				names:    names,
			},
		},
		Usage: usage,
	}, nil
}

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
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount + resp.UsageMetadata.ThoughtsTokenCount),
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
