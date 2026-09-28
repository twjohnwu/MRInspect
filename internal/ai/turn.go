package ai

import "encoding/json"

// ToolSpec describes one callable tool's name, human-readable purpose, and
// JSON-schema-shaped parameters, as sent to a provider's tool-definition API.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one tool invocation a provider's turn 1 response asked for.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult is the outcome of executing one ToolCall, fed back to the
// provider as turn 2 input. Error is a short degraded-mode code (e.g.
// "timeout", "path-rejected", "call-limit", "tool-error") or empty on
// success.
type ToolResult struct {
	ID      string
	Name    string
	Content string
	Error   string
}

// MaxArgsBytes is the maximum allowed size, in bytes, of a single tool
// call's Args before it is rejected as "tool-error".
const MaxArgsBytes = 4096
