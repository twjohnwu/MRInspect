package lane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"mrinspect/internal/ai"
	"mrinspect/internal/rag"
)

const (
	DefaultMaxResponseBytes = 256 * 1024
	DefaultMaxFindings      = 50
	DefaultMaxJSONDepth     = 16
	DefaultMaxFieldChars    = 4000
	maxRetryOutputBytes     = 4096
)

type LimitName string

const (
	LimitResponseBytes LimitName = "response_bytes"
	LimitFindings      LimitName = "findings"
	LimitJSONDepth     LimitName = "json_depth"
)

type ParseLimits struct {
	MaxResponseBytes int
	MaxFindings      int
	MaxJSONDepth     int
	MaxFieldChars    int
}

type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

type Citation struct {
	SourceID string `json:"sourceId,omitempty"`
	Label    string `json:"label,omitempty"`
}

type Finding struct {
	Title      string     `json:"title"`
	Severity   Severity   `json:"severity"`
	Rationale  string     `json:"rationale"`
	File       string     `json:"file,omitempty"`
	Line       *int       `json:"line,omitempty"`
	EndLine    *int       `json:"endLine,omitempty"`
	Category   string     `json:"category,omitempty"`
	Suggestion string     `json:"suggestion,omitempty"`
	Citations  []Citation `json:"citations,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	Positives  []string   `json:"positives,omitempty"`
	Notes      []string   `json:"notes,omitempty"`
}

type ParseStats struct {
	DroppedFindings  int
	MappedSeverities int
	TruncatedFields  map[string]int
}

type ParsedLane struct {
	LaneID   string
	Findings []Finding
	Stats    ParseStats
}

type ParseFailure struct {
	Cap    LimitName
	Reason string
}

func (f *ParseFailure) Error() string { return f.Reason }

type FailureKind string

const (
	FailureKindParse          FailureKind = "parse"
	FailureKindGenerate       FailureKind = "generate"
	FailureKindNotImplemented FailureKind = "not_implemented"
	FailureKindCompose        FailureKind = "compose"
)

type LaneFailure struct {
	LaneID string
	Kind   FailureKind
	Reason string
}

type LaneResult struct {
	LaneID     string
	Findings   []Finding
	ParseStats ParseStats
	Degraded   []string
	Chunks     []rag.Chunk
	Failure    *LaneFailure
}

var (
	ErrParseNotImplemented       = errors.New("lane Parse is not implemented")
	ErrExecuteLaneNotImplemented = errors.New("lane ExecuteLane is not implemented")
)

type rawLane struct {
	LaneID   string            `json:"laneId"`
	Findings []json.RawMessage `json:"findings"`
}

type rawFinding struct {
	Title      string          `json:"title"`
	Severity   string          `json:"severity"`
	Rationale  string          `json:"rationale"`
	File       string          `json:"file"`
	Line       json.RawMessage `json:"line"`
	EndLine    json.RawMessage `json:"endLine"`
	Category   string          `json:"category"`
	Suggestion string          `json:"suggestion"`
	Citations  []Citation      `json:"citations"`
	Summary    string          `json:"summary"`
	Positives  []string        `json:"positives"`
	Notes      []string        `json:"notes"`
}

func Parse(raw string, limits ParseLimits) (ParsedLane, error) {
	if len(raw) > limits.MaxResponseBytes {
		return ParsedLane{}, newParseFailure(
			LimitResponseBytes,
			"parse lane response: response size %d exceeds limit %d",
			len(raw), limits.MaxResponseBytes,
		)
	}

	candidate, err := extractJSON(raw)
	if err != nil {
		return ParsedLane{}, err
	}
	if err := checkJSONDepth(candidate, limits.MaxJSONDepth); err != nil {
		return ParsedLane{}, err
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &envelope); err != nil {
		return ParsedLane{}, newParseFailure("", "parse lane response: decode JSON envelope: %v", err)
	}
	missingKeys := make([]string, 0, 2)
	if _, ok := envelope["laneId"]; !ok {
		missingKeys = append(missingKeys, "laneId")
	}
	if _, ok := envelope["findings"]; !ok {
		missingKeys = append(missingKeys, "findings")
	}
	if len(missingKeys) > 0 {
		return ParsedLane{}, newParseFailure(
			"",
			"parse lane response: missing required envelope key(s): %s",
			strings.Join(missingKeys, ", "),
		)
	}

	var decoded rawLane
	if err := json.Unmarshal([]byte(candidate), &decoded); err != nil {
		return ParsedLane{}, newParseFailure("", "parse lane response: decode JSON: %v", err)
	}
	if len(decoded.Findings) > limits.MaxFindings {
		return ParsedLane{}, newParseFailure(
			LimitFindings,
			"parse lane response: findings count %d exceeds limit %d",
			len(decoded.Findings), limits.MaxFindings,
		)
	}

	stats := ParseStats{TruncatedFields: make(map[string]int)}
	findings := make([]Finding, 0, len(decoded.Findings))
	for i, encoded := range decoded.Findings {
		finding, keep, err := normalizeFinding(encoded, limits.MaxFieldChars, &stats)
		if err != nil {
			return ParsedLane{}, newParseFailure("", "parse lane response: decode finding %d: %v", i, err)
		}
		if keep {
			findings = append(findings, finding)
		}
	}

	return ParsedLane{LaneID: decoded.LaneID, Findings: findings, Stats: stats}, nil
}

func ExecuteLane(ctx context.Context, input ComposeInput, provider ai.Provider, attempts int) LaneResult {
	return executeLaneWithOptions(ctx, input, provider, attempts, ai.GenerateOptions{})
}

func executeLaneWithOptions(ctx context.Context, input ComposeInput, provider ai.Provider, attempts int, opts ai.GenerateOptions) LaneResult {
	composed, err := Compose(ctx, input)
	if err != nil {
		return failedLane(input.Lane.ID, FailureKindCompose, fmt.Sprintf("compose lane prompt: %v", err))
	}

	prompt := composed.Prompt
	var lastErr error
	lastFailureKind := FailureKindParse
	for attempt := 1; attempt <= attempts; attempt++ {
		output, generateErr := provider.Generate(ctx, prompt, opts)
		if generateErr != nil {
			lastErr = fmt.Errorf("generate lane response: %w", generateErr)
			lastFailureKind = FailureKindGenerate
			prompt = buildRetryPrompt(composed.Prompt, "", lastErr)
			continue
		}

		parsed, parseErr := Parse(output, defaultParseLimits())
		if parseErr == nil && parsed.LaneID != input.Lane.ID {
			parseErr = newParseFailure(
				"",
				"parse lane response: laneId mismatch: expected %q, got %q",
				input.Lane.ID,
				parsed.LaneID,
			)
		}
		if parseErr == nil {
			return LaneResult{
				LaneID:     input.Lane.ID,
				Findings:   parsed.Findings,
				ParseStats: parsed.Stats,
				Degraded:   composed.Degraded,
				Chunks:     composed.Chunks,
			}
		}

		lastErr = parseErr
		lastFailureKind = FailureKindParse
		prompt = buildRetryPrompt(composed.Prompt, output, parseErr)
	}

	if lastErr == nil {
		lastErr = errors.New("no generation attempts configured")
	}
	failureReason := fmt.Sprintf("parse lane response failed after %d attempts: %v", attempts, lastErr)
	if lastFailureKind == FailureKindGenerate {
		failureReason = fmt.Sprintf("lane response generation failed after %d attempts: %v", attempts, lastErr)
	}
	return failedLane(input.Lane.ID, lastFailureKind, failureReason)
}

func defaultParseLimits() ParseLimits {
	return ParseLimits{
		MaxResponseBytes: DefaultMaxResponseBytes,
		MaxFindings:      DefaultMaxFindings,
		MaxJSONDepth:     DefaultMaxJSONDepth,
		MaxFieldChars:    DefaultMaxFieldChars,
	}
}

func newParseFailure(cap LimitName, format string, args ...any) *ParseFailure {
	return &ParseFailure{Cap: cap, Reason: fmt.Sprintf(format, args...)}
}

func extractJSON(raw string) (string, error) {
	if fenced, ok := lastFencedBlock(raw); ok && json.Valid([]byte(fenced)) {
		return fenced, nil
	}
	if object, ok := lastBalancedObject(raw); ok && json.Valid([]byte(object)) {
		return object, nil
	}
	if json.Valid([]byte(raw)) {
		return raw, nil
	}
	return "", newParseFailure("", "parse lane response: no valid JSON object found")
}

func lastFencedBlock(raw string) (string, bool) {
	type fence struct {
		lineStart int
		lineEnd   int
	}

	var fences []fence
	for lineStart := 0; lineStart <= len(raw); {
		lineEnd := strings.IndexByte(raw[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(raw)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSuffix(raw[lineStart:lineEnd], "\r")
		if strings.HasPrefix(line, "```") {
			fences = append(fences, fence{lineStart: lineStart, lineEnd: lineEnd})
		}
		if lineEnd == len(raw) {
			break
		}
		lineStart = lineEnd + 1
	}

	if len(fences) < 2 {
		return "", false
	}
	lastClosingIndex := len(fences) - 1
	if len(fences)%2 != 0 {
		lastClosingIndex--
	}
	opening := fences[lastClosingIndex-1]
	closing := fences[lastClosingIndex]
	contentStart := opening.lineEnd
	if contentStart < len(raw) && raw[contentStart] == '\n' {
		contentStart++
	}
	return raw[contentStart:closing.lineStart], true
}

func lastBalancedObject(raw string) (string, bool) {
	start := -1
	depth := 0
	inString := false
	escaped := false
	lastStart := -1
	lastEnd := -1

	for i := 0; i < len(raw); i++ {
		char := raw[i]
		if depth == 0 {
			if char == '{' {
				start = i
				depth = 1
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch char {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				lastStart = start
				lastEnd = i + 1
			}
		}
	}

	if lastStart < 0 {
		return "", false
	}
	return raw[lastStart:lastEnd], true
}

func checkJSONDepth(candidate string, maxDepth int) error {
	decoder := json.NewDecoder(strings.NewReader(candidate))
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return newParseFailure("", "parse lane response: scan JSON tokens: %v", err)
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delimiter {
		case '{', '[':
			depth++
			if depth > maxDepth {
				return newParseFailure(
					LimitJSONDepth,
					"parse lane response: JSON depth %d exceeds limit %d",
					depth, maxDepth,
				)
			}
		case '}', ']':
			depth--
		}
	}
}

func normalizeFinding(encoded json.RawMessage, maxFieldChars int, stats *ParseStats) (Finding, bool, error) {
	var raw rawFinding
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return Finding{}, false, err
	}
	if raw.Title == "" || raw.Rationale == "" {
		stats.DroppedFindings++
		return Finding{}, false, nil
	}

	severity := Severity(strings.ToLower(raw.Severity))
	if severity != SeverityHigh && severity != SeverityMedium && severity != SeverityLow {
		severity = SeverityMedium
		stats.MappedSeverities++
	}

	finding := Finding{
		Title:      truncateField(raw.Title, "title", maxFieldChars, stats),
		Severity:   severity,
		Rationale:  truncateField(raw.Rationale, "rationale", maxFieldChars, stats),
		File:       truncateField(raw.File, "file", maxFieldChars, stats),
		Line:       parseLine(raw.Line),
		EndLine:    parseLine(raw.EndLine),
		Category:   truncateField(raw.Category, "category", maxFieldChars, stats),
		Suggestion: truncateField(raw.Suggestion, "suggestion", maxFieldChars, stats),
		Citations:  raw.Citations,
		Summary:    truncateField(raw.Summary, "summary", maxFieldChars, stats),
		Positives:  truncateFields(raw.Positives, "positives", maxFieldChars, stats),
		Notes:      truncateFields(raw.Notes, "notes", maxFieldChars, stats),
	}
	return finding, true, nil
}

func parseLine(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return &value
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil
	}
	value, err := strconv.Atoi(encoded)
	if err != nil {
		return nil
	}
	return &value
}

func truncateField(value, name string, maxChars int, stats *ParseStats) string {
	characters := []rune(value)
	if len(characters) <= maxChars {
		return value
	}
	stats.TruncatedFields[name]++
	return string(characters[:maxChars])
}

func truncateFields(values []string, name string, maxChars int, stats *ParseStats) []string {
	for i := range values {
		values[i] = truncateField(values[i], name, maxChars, stats)
	}
	return values
}

func buildRetryPrompt(original, previousOutput string, previousErr error) string {
	if len(previousOutput) > maxRetryOutputBytes {
		retainedBytes := maxRetryOutputBytes
		for {
			marker := fmt.Sprintf("\n[truncated %d bytes]", len(previousOutput)-retainedBytes)
			nextRetainedBytes := maxRetryOutputBytes - len(marker)
			if nextRetainedBytes == retainedBytes {
				previousOutput = previousOutput[:retainedBytes] + marker
				break
			}
			retainedBytes = nextRetainedBytes
		}
	}
	return original + fmt.Sprintf(
		"\n\n---\nPrevious attempt was rejected because its lane response could not be parsed: %s\n"+
			"Previous output:\n%s\nPlease follow this lane response contract:\n%s\n",
		previousErr.Error(), previousOutput, LaneOutputContract,
	)
}

func failedLane(laneID string, kind FailureKind, reason string) LaneResult {
	return LaneResult{LaneID: laneID, Failure: &LaneFailure{LaneID: laneID, Kind: kind, Reason: reason}}
}
