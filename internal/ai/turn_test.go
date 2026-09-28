package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

var turnToolSpecs = []ToolSpec{
	{Name: "repo_search", Description: "search repo text", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)},
	{Name: "read_file_ranges", Description: "read file line ranges", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
}

func newTurnTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	return logger.New(slog.LevelError, filepath.Join(t.TempDir(), "metrics.json"))
}

func newTurnTestServer(t *testing.T, responses []string) (*httptest.Server, *[][]byte) {
	t.Helper()
	request := 0
	requestBodies := make([][]byte, 0, len(responses))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requestBodies = append(requestBodies, body)
		if request >= len(responses) {
			t.Errorf("unexpected request %d", request+1)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, responses[request])
		request++
	}))
	return server, &requestBodies
}

func decodeTurnRequestBody(t *testing.T, requestBodies [][]byte, index int) map[string]any {
	t.Helper()
	if len(requestBodies) <= index {
		t.Fatalf("captured request bodies: want index %d, got %d bodies", index, len(requestBodies))
	}
	var body map[string]any
	if err := json.Unmarshal(requestBodies[index], &body); err != nil {
		t.Fatalf("unmarshal request body %d: %v", index, err)
	}
	return body
}

func assertArgsEqual(t *testing.T, args json.RawMessage, want string) {
	t.Helper()
	var gotValue map[string]any
	if err := json.Unmarshal(args, &gotValue); err != nil {
		t.Fatalf("unmarshal tool-call args: %v", err)
	}
	var wantValue map[string]any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal expected tool-call args: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("tool-call args: want %#v, got %#v", wantValue, gotValue)
	}
}

// anthropicContentText extracts the text of an Anthropic tool_result (or
// message) "content" value, which the anthropic-sdk-go client may emit as
// either a bare JSON string or an array of {type:"text", text} blocks.
func anthropicContentText(t *testing.T, value any, label string) string {
	t.Helper()
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var builder strings.Builder
		for i, block := range content {
			b := requireMap(t, block, fmt.Sprintf("%s[%d]", label, i))
			if b["type"] != "text" {
				t.Errorf("%s[%d].type: want text, got %#v", label, i, b["type"])
			}
			text, _ := b["text"].(string)
			builder.WriteString(text)
		}
		return builder.String()
	default:
		t.Fatalf("%s: want string or array, got %T", label, value)
		return ""
	}
}

func assertJSONLiteralEqual(t *testing.T, got any, want string) {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal expected JSON: %v", err)
	}
	if !reflect.DeepEqual(got, wantValue) {
		t.Errorf("JSON value:\nwant: %#v\n got: %#v", wantValue, got)
	}
}

func requireMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s: want object, got %T", label, value)
	}
	return result
}

func requireSlice(t *testing.T, value any, label string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s: want array, got %T", label, value)
	}
	return result
}

func assertContainsToolNames(t *testing.T, got []string) {
	t.Helper()
	for _, want := range []string{"repo_search", "read_file_ranges"} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool names %v do not contain %q", got, want)
		}
	}
}

func assertPromptText(t *testing.T, text string) {
	t.Helper()
	if !strings.HasPrefix(text, "review") {
		t.Errorf("prompt %q does not have prefix %q", text, "review")
	}
	if !strings.HasSuffix(text, HintSentence) {
		t.Errorf("prompt %q does not have suffix HintSentence", text)
	}
}

// TestGenerateTurn_ToolDefinitionsAndCalls verifies REQ-02 / S-03.
func TestGenerateTurn_ToolDefinitionsAndCalls(t *testing.T) {
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}

	t.Run("openai", func(t *testing.T) {
		responses := []string{
			`{"id":"resp_1","output":[{"type":"reasoning","encrypted_content":"enc"},{"type":"function_call","call_id":"call_1","name":"repo_search","arguments":"{\"query\":\"NewClient\"}"}],"usage":{"input_tokens":10,"output_tokens":5}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider := NewOpenAIProvider("test-key", providerConfig, newTurnTestLogger(t),
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		result, err := provider.GenerateTurn(context.Background(), TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn: %v (RED expected until GreenTask implements it)", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 0)
		if body["store"] != false {
			t.Errorf("store: want false, got %#v", body["store"])
		}
		include := requireSlice(t, body["include"], "include")
		foundEncryptedContent := false
		for _, value := range include {
			if value == "reasoning.encrypted_content" {
				foundEncryptedContent = true
			}
		}
		if !foundEncryptedContent {
			t.Errorf("include: want reasoning.encrypted_content, got %#v", include)
		}
		input := requireSlice(t, body["input"], "input")
		if len(input) != 1 {
			t.Fatalf("input length: want 1, got %d", len(input))
		}
		inputItem := requireMap(t, input[0], "input[0]")
		if inputItem["role"] != "user" {
			t.Errorf("input[0].role: want user, got %#v", inputItem["role"])
		}
		content, ok := inputItem["content"].(string)
		if !ok {
			t.Fatalf("input[0].content: want string, got %T", inputItem["content"])
		}
		assertPromptText(t, content)

		tools := requireSlice(t, body["tools"], "tools")
		if len(tools) != 2 {
			t.Fatalf("tools length: want 2, got %d", len(tools))
		}
		names := make([]string, 0, len(tools))
		for i, value := range tools {
			tool := requireMap(t, value, fmt.Sprintf("tools[%d]", i))
			name, _ := tool["name"].(string)
			names = append(names, name)
			if tool["type"] != "function" {
				t.Errorf("tools[%d].type: want function, got %#v", i, tool["type"])
			}
			if description, _ := tool["description"].(string); description == "" {
				t.Errorf("tools[%d].description: want non-empty string", i)
			}
			if tool["parameters"] == nil {
				t.Errorf("tools[%d].parameters: want non-nil value", i)
			}
		}
		assertContainsToolNames(t, names)

		if len(result.ToolCalls) != 1 {
			t.Fatalf("tool calls length: want 1, got %d", len(result.ToolCalls))
		}
		if result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Name != "repo_search" {
			t.Errorf("tool call: want ID call_1 and name repo_search, got %+v", result.ToolCalls[0])
		}
		assertArgsEqual(t, result.ToolCalls[0].Args, `{"query":"NewClient"}`)
		if result.Continuation == nil {
			t.Error("continuation: want non-nil")
		}
		if result.Usage == nil {
			t.Error("usage: want non-nil")
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		responses := []string{
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"tool_use","id":"toolu_1","name":"repo_search","input":{"query":"NewClient"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider := NewAnthropicProvider("test-key", providerConfig, newTurnTestLogger(t),
			WithAnthropicBaseURL(server.URL), WithAnthropicHTTPClient(server.Client()))
		result, err := provider.GenerateTurn(context.Background(), TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn: %v (RED expected until GreenTask implements it)", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 0)
		messages := requireSlice(t, body["messages"], "messages")
		if len(messages) != 1 {
			t.Fatalf("messages length: want 1, got %d", len(messages))
		}
		message := requireMap(t, messages[0], "messages[0]")
		if message["role"] != "user" {
			t.Errorf("messages[0].role: want user, got %#v", message["role"])
		}
		var promptText string
		switch content := message["content"].(type) {
		case string:
			promptText = content
		case []any:
			if len(content) == 0 {
				t.Fatal("messages[0].content: want at least one text block")
			}
			first := requireMap(t, content[0], "messages[0].content[0]")
			if first["type"] != "text" {
				t.Errorf("messages[0].content[0].type: want text, got %#v", first["type"])
			}
			promptText, _ = first["text"].(string)
		default:
			t.Fatalf("messages[0].content: want string or array, got %T", message["content"])
		}
		assertPromptText(t, promptText)

		tools := requireSlice(t, body["tools"], "tools")
		if len(tools) != 2 {
			t.Fatalf("tools length: want 2, got %d", len(tools))
		}
		names := make([]string, 0, len(tools))
		for i, value := range tools {
			tool := requireMap(t, value, fmt.Sprintf("tools[%d]", i))
			name, _ := tool["name"].(string)
			names = append(names, name)
			if name == "" {
				t.Errorf("tools[%d].name: want non-empty string", i)
			}
			if description, _ := tool["description"].(string); description == "" {
				t.Errorf("tools[%d].description: want non-empty string", i)
			}
			if tool["input_schema"] == nil {
				t.Errorf("tools[%d].input_schema: want non-nil value", i)
			}
		}
		assertContainsToolNames(t, names)

		if len(result.ToolCalls) != 1 {
			t.Fatalf("tool calls length: want 1, got %d", len(result.ToolCalls))
		}
		if result.ToolCalls[0].ID != "toolu_1" || result.ToolCalls[0].Name != "repo_search" {
			t.Errorf("tool call: want ID toolu_1 and name repo_search, got %+v", result.ToolCalls[0])
		}
		assertArgsEqual(t, result.ToolCalls[0].Args, `{"query":"NewClient"}`)
		if result.Continuation == nil {
			t.Error("continuation: want non-nil")
		}
		if result.Usage == nil {
			t.Error("usage: want non-nil")
		}
	})

	t.Run("gemini", func(t *testing.T) {
		responses := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"repo_search","args":{"query":"NewClient"}},"thoughtSignature":"c2ln"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider, err := NewGeminiProvider(context.Background(), "test-key", providerConfig, newTurnTestLogger(t),
			WithGeminiBaseURL(server.URL), WithGeminiHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("NewGeminiProvider: %v", err)
		}
		result, err := provider.GenerateTurn(context.Background(), TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn: %v (RED expected until GreenTask implements it)", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 0)
		contents := requireSlice(t, body["contents"], "contents")
		if len(contents) != 1 {
			t.Fatalf("contents length: want 1, got %d", len(contents))
		}
		content := requireMap(t, contents[0], "contents[0]")
		if content["role"] != "user" {
			t.Errorf("contents[0].role: want user, got %#v", content["role"])
		}
		parts := requireSlice(t, content["parts"], "contents[0].parts")
		if len(parts) < 1 {
			t.Fatal("contents[0].parts: want at least one part")
		}
		firstPart := requireMap(t, parts[0], "contents[0].parts[0]")
		promptText, ok := firstPart["text"].(string)
		if !ok {
			t.Fatalf("contents[0].parts[0].text: want string, got %T", firstPart["text"])
		}
		assertPromptText(t, promptText)

		tools := requireSlice(t, body["tools"], "tools")
		if len(tools) != 1 {
			t.Fatalf("tools length: want 1, got %d", len(tools))
		}
		tool := requireMap(t, tools[0], "tools[0]")
		declarations := requireSlice(t, tool["functionDeclarations"], "tools[0].functionDeclarations")
		if len(declarations) != 2 {
			t.Fatalf("function declarations length: want 2, got %d", len(declarations))
		}
		names := make([]string, 0, len(declarations))
		for i, value := range declarations {
			declaration := requireMap(t, value, fmt.Sprintf("functionDeclarations[%d]", i))
			name, _ := declaration["name"].(string)
			names = append(names, name)
			if description, _ := declaration["description"].(string); description == "" {
				t.Errorf("functionDeclarations[%d].description: want non-empty string", i)
			}
		}
		assertContainsToolNames(t, names)

		if len(result.ToolCalls) != 1 {
			t.Fatalf("tool calls length: want 1, got %d", len(result.ToolCalls))
		}
		if result.ToolCalls[0].ID != "repo_search#0" || result.ToolCalls[0].Name != "repo_search" {
			t.Errorf("tool call: want ID repo_search#0 and name repo_search, got %+v", result.ToolCalls[0])
		}
		assertArgsEqual(t, result.ToolCalls[0].Args, `{"query":"NewClient"}`)
		if result.Continuation == nil {
			t.Error("continuation: want non-nil")
		}
		if result.Usage == nil {
			t.Error("usage: want non-nil")
		}
	})
}

// TestGenerateTurn_LocalReplayContinuation verifies REQ-02 / S-05.
func TestGenerateTurn_LocalReplayContinuation(t *testing.T) {
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}

	t.Run("openai", func(t *testing.T) {
		responses := []string{
			`{"id":"resp_1","output":[{"type":"reasoning","encrypted_content":"enc"},{"type":"function_call","call_id":"call_1","name":"repo_search","arguments":"{\"query\":\"a\"}"},{"type":"function_call","call_id":"call_2","name":"repo_search","arguments":"{\"query\":\"b\"}"}],"usage":{"input_tokens":10,"output_tokens":5}}`,
			`{"id":"resp_2","output":[{"type":"message","content":[{"type":"output_text","text":"final review text"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		ctx := context.Background()
		provider := NewOpenAIProvider("test-key", providerConfig, newTurnTestLogger(t),
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		turn1Result, err := provider.GenerateTurn(ctx, TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn turn 1: %v (RED expected until GreenTask implements it)", err)
		}
		turn2Result, err := provider.GenerateTurn(ctx, TurnRequest{
			Prompt:       "review",
			Tools:        turnToolSpecs,
			Continuation: turn1Result.Continuation,
			ToolResults: []ToolResult{
				{ID: "call_1", Content: "hit"},
				{ID: "call_2", Error: "timeout"},
			},
		})
		if err != nil {
			t.Fatalf("GenerateTurn turn 2: %v", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 1)
		if _, ok := body["previous_response_id"]; ok {
			t.Errorf("previous_response_id: want absent, got %#v", body["previous_response_id"])
		}
		if body["store"] != false {
			t.Errorf("store: want false, got %#v", body["store"])
		}
		input := requireSlice(t, body["input"], "input")
		if len(input) != 6 {
			t.Fatalf("input length: want 6, got %d", len(input))
		}
		expected := []string{
			fmt.Sprintf(`{"role":"user","content":"review\n\n%s"}`, HintSentence),
			`{"type":"reasoning","encrypted_content":"enc"}`,
			`{"type":"function_call","call_id":"call_1","name":"repo_search","arguments":"{\"query\":\"a\"}"}`,
			`{"type":"function_call","call_id":"call_2","name":"repo_search","arguments":"{\"query\":\"b\"}"}`,
			fmt.Sprintf(`{"type":"function_call_output","call_id":"call_1","output":"%s\nhit"}`, UntrustedFrame),
			`{"type":"function_call_output","call_id":"call_2","output":"{\"error\":\"timeout\"}"}`,
		}
		for i, want := range expected {
			item := requireMap(t, input[i], fmt.Sprintf("input[%d]", i))
			assertJSONLiteralEqual(t, item, want)
		}
		output, ok := requireMap(t, input[4], "input[4]")["output"].(string)
		if !ok {
			t.Fatalf("input[4].output: want string, got %T", requireMap(t, input[4], "input[4]")["output"])
		}
		if !strings.HasPrefix(output, UntrustedFrame) {
			t.Errorf("input[4].output: want UntrustedFrame prefix, got %q", output)
		}
		if turn2Result.Text != "final review text" {
			t.Errorf("turn 2 text: want final review text, got %q", turn2Result.Text)
		}
		if len(turn2Result.ToolCalls) != 0 {
			t.Errorf("turn 2 tool calls: want none, got %+v", turn2Result.ToolCalls)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		responses := []string{
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"tool_use","id":"toolu_1","name":"repo_search","input":{"query":"a"}},{"type":"tool_use","id":"toolu_2","name":"repo_search","input":{"query":"b"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`,
			`{"id":"msg_2","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"final review text"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":2}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		ctx := context.Background()
		provider := NewAnthropicProvider("test-key", providerConfig, newTurnTestLogger(t),
			WithAnthropicBaseURL(server.URL), WithAnthropicHTTPClient(server.Client()))
		turn1Result, err := provider.GenerateTurn(ctx, TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn turn 1: %v (RED expected until GreenTask implements it)", err)
		}
		turn2Result, err := provider.GenerateTurn(ctx, TurnRequest{
			Prompt:       "review",
			Tools:        turnToolSpecs,
			Continuation: turn1Result.Continuation,
			ToolResults: []ToolResult{
				{ID: "toolu_1", Content: "hit"},
				{ID: "toolu_2", Error: "timeout"},
			},
		})
		if err != nil {
			t.Fatalf("GenerateTurn turn 2: %v", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 1)
		messages := requireSlice(t, body["messages"], "messages")
		if len(messages) != 3 {
			t.Fatalf("messages length: want 3, got %d", len(messages))
		}
		assistantMessage := requireMap(t, messages[1], "messages[1]")
		if assistantMessage["role"] != "assistant" {
			t.Errorf("messages[1].role: want assistant, got %#v", assistantMessage["role"])
		}
		assistantContent := requireSlice(t, assistantMessage["content"], "messages[1].content")
		if len(assistantContent) != 2 {
			t.Fatalf("messages[1].content length: want 2, got %d", len(assistantContent))
		}
		assertJSONLiteralEqual(t, assistantContent, `[{"type":"tool_use","id":"toolu_1","name":"repo_search","input":{"query":"a"}},{"type":"tool_use","id":"toolu_2","name":"repo_search","input":{"query":"b"}}]`)

		userMessage := requireMap(t, messages[2], "messages[2]")
		if userMessage["role"] != "user" {
			t.Errorf("messages[2].role: want user, got %#v", userMessage["role"])
		}
		userContent := requireSlice(t, userMessage["content"], "messages[2].content")
		if len(userContent) != 3 {
			t.Fatalf("messages[2].content length: want 3, got %d", len(userContent))
		}
		success := requireMap(t, userContent[0], "messages[2].content[0]")
		successText := anthropicContentText(t, success["content"], "messages[2].content[0].content")
		if success["type"] != "tool_result" || success["tool_use_id"] != "toolu_1" || successText != "hit" {
			t.Errorf("success tool result: got %#v", success)
		}
		if isError, exists := success["is_error"]; exists && isError != false {
			t.Errorf("success tool result is_error: want absent or false, got %#v", isError)
		}
		failure := requireMap(t, userContent[1], "messages[2].content[1]")
		failureText := anthropicContentText(t, failure["content"], "messages[2].content[1].content")
		if failure["type"] != "tool_result" || failure["tool_use_id"] != "toolu_2" || failureText != "error: timeout" || failure["is_error"] != true {
			t.Errorf("failure tool result: got %#v", failure)
		}
		frame := requireMap(t, userContent[len(userContent)-1], "messages[2].content[last]")
		if frame["type"] != "text" || frame["text"] != UntrustedFrame {
			t.Errorf("frame block: want last element to be text %q, got %#v", UntrustedFrame, frame)
		}
		if turn2Result.Text != "final review text" {
			t.Errorf("turn 2 text: want final review text, got %q", turn2Result.Text)
		}
		if len(turn2Result.ToolCalls) != 0 {
			t.Errorf("turn 2 tool calls: want none, got %+v", turn2Result.ToolCalls)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		responses := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"repo_search","args":{"query":"a"}},"thoughtSignature":"c2ln"},{"functionCall":{"name":"repo_search","args":{"query":"b"}},"thoughtSignature":"c2ln"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"final review text"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`,
		}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		ctx := context.Background()
		provider, err := NewGeminiProvider(ctx, "test-key", providerConfig, newTurnTestLogger(t),
			WithGeminiBaseURL(server.URL), WithGeminiHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("NewGeminiProvider: %v", err)
		}
		turn1Result, err := provider.GenerateTurn(ctx, TurnRequest{Prompt: "review", Tools: turnToolSpecs})
		if err != nil {
			t.Fatalf("GenerateTurn turn 1: %v (RED expected until GreenTask implements it)", err)
		}
		turn2Result, err := provider.GenerateTurn(ctx, TurnRequest{
			Prompt:       "review",
			Tools:        turnToolSpecs,
			Continuation: turn1Result.Continuation,
			ToolResults: []ToolResult{
				{ID: "repo_search#0", Content: "hit"},
				{ID: "repo_search#1", Error: "timeout"},
			},
		})
		if err != nil {
			t.Fatalf("GenerateTurn turn 2: %v", err)
		}

		body := decodeTurnRequestBody(t, *requestBodies, 1)
		contents := requireSlice(t, body["contents"], "contents")
		if len(contents) != 3 {
			t.Fatalf("contents length: want 3, got %d", len(contents))
		}
		modelContent := requireMap(t, contents[1], "contents[1]")
		if modelContent["role"] != "model" {
			t.Errorf("contents[1].role: want model, got %#v", modelContent["role"])
		}
		modelParts := requireSlice(t, modelContent["parts"], "contents[1].parts")
		assertJSONLiteralEqual(t, modelParts, `[{"functionCall":{"name":"repo_search","args":{"query":"a"}},"thoughtSignature":"c2ln"},{"functionCall":{"name":"repo_search","args":{"query":"b"}},"thoughtSignature":"c2ln"}]`)

		userContent := requireMap(t, contents[2], "contents[2]")
		if userContent["role"] != "user" {
			t.Errorf("contents[2].role: want user, got %#v", userContent["role"])
		}
		parts := requireSlice(t, userContent["parts"], "contents[2].parts")
		if len(parts) != 3 {
			t.Fatalf("contents[2].parts length: want 3, got %d", len(parts))
		}
		successPart := requireMap(t, parts[0], "contents[2].parts[0]")
		successResponse := requireMap(t, successPart["functionResponse"], "contents[2].parts[0].functionResponse")
		if successResponse["id"] != "repo_search#0" || successResponse["name"] != "repo_search" {
			t.Errorf("success function response identity: got %#v", successResponse)
		}
		successPayload := requireMap(t, successResponse["response"], "success function response payload")
		if _, hasErr := successPayload["error"]; hasErr {
			t.Errorf("success function response: unexpected error key in %#v", successPayload)
		}

		failurePart := requireMap(t, parts[1], "contents[2].parts[1]")
		failureResponse := requireMap(t, failurePart["functionResponse"], "contents[2].parts[1].functionResponse")
		if failureResponse["id"] != "repo_search#1" || failureResponse["name"] != "repo_search" {
			t.Errorf("failure function response identity: got %#v", failureResponse)
		}
		failurePayload := requireMap(t, failureResponse["response"], "failure function response payload")
		assertJSONLiteralEqual(t, failurePayload, `{"error":"timeout"}`)

		frame := requireMap(t, parts[len(parts)-1], "contents[2].parts[last]")
		assertJSONLiteralEqual(t, frame, fmt.Sprintf(`{"text":"%s"}`, UntrustedFrame))
		if turn2Result.Text != "final review text" {
			t.Errorf("turn 2 text: want final review text, got %q", turn2Result.Text)
		}
		if len(turn2Result.ToolCalls) != 0 {
			t.Errorf("turn 2 tool calls: want none, got %+v", turn2Result.ToolCalls)
		}
	})
}

// TestGenerateTurn_OpenAIRemoteContinuation verifies REQ-02 / S-04.
func TestGenerateTurn_OpenAIRemoteContinuation(t *testing.T) {
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}
	responses := []string{
		`{"id":"resp_1","output":[{"type":"function_call","call_id":"call_1","name":"repo_search","arguments":"{\"query\":\"NewClient\"}"}],"usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"id":"resp_2","output":[{"type":"message","content":[{"type":"output_text","text":"final review text"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`,
	}
	server, requestBodies := newTurnTestServer(t, responses)
	defer server.Close()

	ctx := context.Background()
	provider := NewOpenAIProvider("test-key", providerConfig, newTurnTestLogger(t),
		WithOpenAIRemoteState(true), WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
	turn1Result, err := provider.GenerateTurn(ctx, TurnRequest{Prompt: "review", Tools: turnToolSpecs})
	if err != nil {
		t.Fatalf("GenerateTurn turn 1: %v (RED expected until GreenTask implements it)", err)
	}

	turn1Body := decodeTurnRequestBody(t, *requestBodies, 0)
	if turn1Body["store"] != true {
		t.Errorf("turn 1 store: want true, got %#v", turn1Body["store"])
	}

	turn2Result, err := provider.GenerateTurn(ctx, TurnRequest{
		Prompt:       "review",
		Tools:        turnToolSpecs,
		Continuation: turn1Result.Continuation,
		ToolResults: []ToolResult{
			{ID: "call_1", Name: "repo_search", Content: "hit"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateTurn turn 2: %v (RED expected until GreenTask implements it)", err)
	}

	turn2Body := decodeTurnRequestBody(t, *requestBodies, 1)
	if turn2Body["previous_response_id"] != "resp_1" {
		t.Errorf("turn 2 previous_response_id: want resp_1, got %#v", turn2Body["previous_response_id"])
	}
	if turn2Body["store"] != true {
		t.Errorf("turn 2 store: want true, got %#v", turn2Body["store"])
	}
	input := requireSlice(t, turn2Body["input"], "input")
	if len(input) != 1 {
		t.Fatalf("turn 2 input length: want 1, got %d", len(input))
	}
	item := requireMap(t, input[0], "input[0]")
	if item["type"] != "function_call_output" {
		t.Errorf("input[0].type: want function_call_output, got %#v", item["type"])
	}
	if item["call_id"] != "call_1" {
		t.Errorf("input[0].call_id: want call_1, got %#v", item["call_id"])
	}
	output, ok := item["output"].(string)
	if !ok {
		t.Fatalf("input[0].output: want string, got %T", item["output"])
	}
	if !strings.HasPrefix(output, UntrustedFrame) {
		t.Errorf("input[0].output: want UntrustedFrame prefix, got %q", output)
	}
	if !strings.Contains(output, "hit") {
		t.Errorf("input[0].output: want to contain %q, got %q", "hit", output)
	}
	tools := requireSlice(t, turn2Body["tools"], "tools")
	if len(tools) == 0 {
		t.Error("turn 2 tools: want non-empty, got none")
	}
	if strings.Contains(string((*requestBodies)[1]), "review") {
		t.Errorf("turn 2 body contains prompt text %q: %s", "review", (*requestBodies)[1])
	}
	if turn2Result.Text != "final review text" {
		t.Errorf("turn 2 text: want final review text, got %q", turn2Result.Text)
	}
}

// enrichmentTranscriptEntry decodes the REQ-05 per-turn transcript fields
// under test; it intentionally omits any content field, since the
// transcript must not record tool result content or raw args.
type enrichmentTranscriptEntry struct {
	Turn         int    `json:"turn"`
	Continuation string `json:"continuation"`
	Prompt       string `json:"prompt"`
	ToolCalls    []struct {
		Name      string `json:"name"`
		ArgsBytes int    `json:"args_bytes"`
		Valid     bool   `json:"valid"`
	} `json:"tool_calls"`
	ToolResults []struct {
		Name      string `json:"name"`
		Error     string `json:"error"`
		Truncated bool   `json:"truncated"`
	} `json:"tool_results"`
}

func readEnrichmentTranscriptEntries(t *testing.T, logDir string) []enrichmentTranscriptEntry {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(logDir, "ai-log-*.jsonl"))
	if err != nil {
		t.Fatalf("glob transcripts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("transcript files = %d, want 1", len(files))
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()

	var entries []enrichmentTranscriptEntry
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var entry enrichmentTranscriptEntry
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("decode transcript: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

// TestGenerateTurn_TranscriptPerTurnNoContent verifies REQ-05 / S-12.
func TestGenerateTurn_TranscriptPerTurnNoContent(t *testing.T) {
	resetTranscriptForTest(t)
	logDir := t.TempDir()
	providerConfig := config.ProviderConfig{Model: "test-model", MaxTokens: 32}
	responses := []string{
		`{"id":"resp_1","output":[{"type":"function_call","call_id":"call_1","name":"repo_search","arguments":"{\"path\":\"/etc/hosts\"}"}],"usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"id":"resp_2","output":[{"type":"message","content":[{"type":"output_text","text":"final review text"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`,
	}
	server, _ := newTurnTestServer(t, responses)
	defer server.Close()

	log := newTurnTestLogger(t)
	provider := NewOpenAIProvider("test-key", providerConfig, log,
		WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
	decorated := WithRetry(provider, config.APIConfig{AILogDir: logDir, RetryAttempts: 1})
	tp, ok := decorated.(TurnProvider)
	if !ok {
		t.Fatalf("WithRetry result %T does not implement TurnProvider (RED expected until GreenTask implements it)", decorated)
	}

	ctx := context.Background()
	turn1Result, err := tp.GenerateTurn(ctx, TurnRequest{Prompt: "review", Tools: turnToolSpecs})
	if err != nil {
		t.Fatalf("GenerateTurn turn 1: %v (RED expected until GreenTask implements it)", err)
	}
	turn2Result, err := tp.GenerateTurn(ctx, TurnRequest{
		Prompt:       "review",
		Tools:        turnToolSpecs,
		Continuation: turn1Result.Continuation,
		ToolResults: []ToolResult{
			{ID: "call_1", Name: "repo_search", Content: "SENTINEL-CONTENT-7"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateTurn turn 2: %v", err)
	}
	if turn2Result.Text != "final review text" {
		t.Errorf("turn 2 text: want final review text, got %q", turn2Result.Text)
	}

	entries := readEnrichmentTranscriptEntries(t, logDir)
	if len(entries) != 2 {
		t.Fatalf("transcript entries: want 2, got %d", len(entries))
	}
	if entries[0].Turn != 1 || entries[1].Turn != 2 {
		t.Errorf("turn numbers: want 1,2, got %d,%d", entries[0].Turn, entries[1].Turn)
	}
	if entries[0].Continuation != "none" || entries[1].Continuation != "local" {
		t.Errorf("continuation: want none,local, got %q,%q", entries[0].Continuation, entries[1].Continuation)
	}
	if len(entries[0].ToolCalls) != 1 || entries[0].ToolCalls[0].Name != "repo_search" || entries[0].ToolCalls[0].ArgsBytes <= 0 {
		t.Errorf("entry 0 tool_calls: got %+v", entries[0].ToolCalls)
	}
	if len(entries[1].ToolResults) != 1 || entries[1].ToolResults[0].Name != "repo_search" {
		t.Errorf("entry 1 tool_results: got %+v", entries[1].ToolResults)
	}
	if entries[1].Prompt != "" {
		t.Errorf("entry 1 prompt: want empty, got %q", entries[1].Prompt)
	}

	files, err := filepath.Glob(filepath.Join(logDir, "ai-log-*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("glob transcript files: %v, %d files", err, len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read transcript file: %v", err)
	}
	for _, forbidden := range []string{"SENTINEL-CONTENT-7", "/etc/hosts", logDir} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("transcript file contains forbidden string %q", forbidden)
		}
	}

	snapshot := log.MetricsSnapshot()
	if len(snapshot.APICalls) != 2 {
		t.Fatalf("APICalls: want 2, got %d", len(snapshot.APICalls))
	}
	if !strings.HasSuffix(snapshot.APICalls[0].Endpoint, "/turn1") {
		t.Errorf("APICalls[0].Endpoint: want suffix /turn1, got %q", snapshot.APICalls[0].Endpoint)
	}
	if !strings.HasSuffix(snapshot.APICalls[1].Endpoint, "/turn2") {
		t.Errorf("APICalls[1].Endpoint: want suffix /turn2, got %q", snapshot.APICalls[1].Endpoint)
	}
}

// newProviderTestConfig builds a minimal config.Config for the newProvider
// seam test: OpenAI provider, given enrichment enable/remote-state values.
func newProviderTestConfig(enabled bool, remoteState string) config.Config {
	return config.Config{
		AIProvider: config.ProviderOpenAI,
		Providers: map[config.AIProvider]config.ProviderConfig{
			config.ProviderOpenAI: {Model: "test-model", MaxTokens: 32},
		},
		API: config.APIConfig{RetryAttempts: 1},
		Enrichment: config.EnrichmentConfig{
			Enabled:     enabled,
			RemoteState: remoteState,
		},
	}
}

// TestNewProvider_StoreFollowsRemoteState verifies REQ-06 / S-13.
func TestNewProvider_StoreFollowsRemoteState(t *testing.T) {
	t.Run("enrichment disabled, remote state enabled", func(t *testing.T) {
		responses := []string{`{"output":[{"content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider, err := newProvider(newProviderTestConfig(false, "enabled"), newTurnTestLogger(t),
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("newProvider: %v", err)
		}
		if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		body := decodeTurnRequestBody(t, *requestBodies, 0)
		if body["store"] != false {
			t.Errorf("store: want false, got %#v", body["store"])
		}
	})

	t.Run("enrichment enabled, remote state disabled", func(t *testing.T) {
		responses := []string{`{"output":[{"content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider, err := newProvider(newProviderTestConfig(true, "disabled"), newTurnTestLogger(t),
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("newProvider: %v", err)
		}
		if _, err := provider.Generate(context.Background(), "review", GenerateOptions{}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		body := decodeTurnRequestBody(t, *requestBodies, 0)
		if body["store"] != false {
			t.Errorf("store: want false, got %#v", body["store"])
		}
	})

	t.Run("enrichment enabled, remote state enabled", func(t *testing.T) {
		responses := []string{`{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`}
		server, requestBodies := newTurnTestServer(t, responses)
		defer server.Close()

		provider, err := newProvider(newProviderTestConfig(true, "enabled"), newTurnTestLogger(t),
			WithOpenAIBaseURL(server.URL), WithOpenAIHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("newProvider: %v", err)
		}
		tp, ok := provider.(TurnProvider)
		if !ok {
			t.Fatalf("provider %T does not implement TurnProvider", provider)
		}
		if _, err := tp.GenerateTurn(context.Background(), TurnRequest{Prompt: "review", Tools: turnToolSpecs}); err != nil {
			t.Fatalf("GenerateTurn: %v (RED expected until GreenTask implements it)", err)
		}
		body := decodeTurnRequestBody(t, *requestBodies, 0)
		if body["store"] != true {
			t.Errorf("store: want true, got %#v", body["store"])
		}
	})
}
