package lane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/testfake"
)

func parseTestLimits() ParseLimits {
	return ParseLimits{
		MaxResponseBytes: DefaultMaxResponseBytes,
		MaxFindings:      DefaultMaxFindings,
		MaxJSONDepth:     DefaultMaxJSONDepth,
		MaxFieldChars:    DefaultMaxFieldChars,
	}
}

func parseTestJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal parse fixture: %v", err)
	}
	return string(encoded)
}

func parseTestInt(value int) *int { return &value }

// TestParse_ExtractsJSONFromNoise verifies REQ-04 / S-13: position-tolerant
// extraction returns the same non-trivial findings for all four response shapes.
func TestParse_ExtractsJSONFromNoise(t *testing.T) {
	want := []Finding{
		{
			Title:      "Authorization check can be bypassed",
			Severity:   SeverityHigh,
			Rationale:  "The handler trusts a caller-controlled role.",
			File:       "internal/auth/handler.go",
			Line:       parseTestInt(27),
			EndLine:    parseTestInt(30),
			Category:   "security",
			Suggestion: "Resolve the role from the authenticated principal.",
			Citations:  []Citation{{SourceID: "security-standard", Label: "Authorization"}},
		},
		{
			Title:      "Error loses operation context",
			Severity:   SeverityLow,
			Rationale:  "The returned error cannot identify which lookup failed.",
			File:       "internal/store/user.go",
			Line:       parseTestInt(81),
			Category:   "maintainability",
			Suggestion: "Wrap the error with the user lookup operation.",
		},
	}
	bare := parseTestJSON(t, map[string]any{"laneId": "code-quality", "findings": want})
	responses := map[string]string{
		"bare JSON":           bare,
		"json fence in prose": "Review result follows.\n```json\n" + bare + "\n```\nEnd of result.",
		"untagged fence":      "Review result follows.\n```\n" + bare + "\n```\nEnd of result.",
		"brace JSON in prose": "Review result follows: " + bare + " End of result.",
	}

	for name, raw := range responses {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(raw, parseTestLimits())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.LaneID != "code-quality" {
				t.Errorf("LaneID = %q, want code-quality", got.LaneID)
			}
			if !reflect.DeepEqual(got.Findings, want) {
				t.Errorf("Findings = %#v, want %#v", got.Findings, want)
			}
		})
	}
}

// TestParse_NormalizesAndCountsDropped verifies REQ-04 / S-14: known values
// normalize, unknown fields disappear, and incomplete findings are counted.
func TestParse_NormalizesAndCountsDropped(t *testing.T) {
	raw := `{
  "laneId": "normalization",
  "findings": [
    {
      "title": "Normalized finding",
      "severity": "HIGH",
      "rationale": "This complete finding must remain.",
      "file": "src/normalize.go",
      "line": "42",
      "confidence": "CONFIDENCE-MUST-NOT-SURVIVE"
    },
    {
      "title": "Incomplete finding",
      "severity": "low",
      "file": "src/incomplete.go"
    }
  ]
}`

	got, err := Parse(raw, parseTestLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("finding count = %d, want exactly 1", len(got.Findings))
	}
	finding := got.Findings[0]
	if finding.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want %q", finding.Severity, SeverityHigh)
	}
	if finding.Line == nil || *finding.Line != 42 {
		t.Errorf("Line = %v, want pointer to 42", finding.Line)
	}
	encoded, err := json.Marshal(finding)
	if err != nil {
		t.Fatalf("marshal parsed finding: %v", err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "confidence") {
		t.Errorf("parsed finding retained undefined confidence field: %s", encoded)
	}
	if got.Stats.DroppedFindings != 1 {
		t.Errorf("DroppedFindings = %d, want 1", got.Stats.DroppedFindings)
	}
}

// TestParse_RetryReusesRetrieval verifies REQ-04 / S-15: a parse retry only
// repeats Generate, keeps the composed prompt, and does not repeat retrieval.
func TestParse_RetryReusesRetrieval(t *testing.T) {
	registry := loadComposeResourceRegistry(t, `  - name: retry-reference
    mode: retrieval
    paths: []
`)
	lane := Lane{
		ID:        "retry-lane",
		Intent:    "check retry behavior",
		Resources: Resources{Sets: []string{"retry-reference"}},
		TopK:      2,
		Model:     "retry-test-model",
	}
	valid := `{"laneId":"retry-lane","findings":[{"title":"Recovered finding","severity":"medium","rationale":"The second response is valid."}]}`
	const badOutput = "FIRST-BAD-OUTPUT-NOT-JSON"

	retryRetriever := &testfake.FakeRetriever{DefaultResponse: testfake.RetrieverResponse{
		Result: rag.Result{Chunks: []rag.Chunk{{ID: "retry-chunk", Text: "retrieved once", ResourceSet: "retry-reference"}}},
	}}
	retryInput := composeTestInput(t, lane, []string{"retry", "terms"}, registry, "S15-RETRY-DIFF")
	retryInput.Retriever = retryRetriever
	retryProvider := &testfake.FakeProvider{Responses: []testfake.ProviderResponse{
		{Output: badOutput},
		{Output: valid},
	}}

	got := ExecuteLane(context.Background(), retryInput, retryProvider, 2)
	if got.Failure != nil {
		t.Fatalf("ExecuteLane failure = %#v, want success", got.Failure)
	}
	if len(got.Findings) != 1 || got.Findings[0].Title != "Recovered finding" {
		t.Errorf("Findings = %#v, want recovered finding", got.Findings)
	}
	if retryProvider.GenerateCallCount() != 2 {
		t.Errorf("Generate call count = %d, want exactly 2", retryProvider.GenerateCallCount())
	}

	controlRetriever := &testfake.FakeRetriever{DefaultResponse: retryRetriever.DefaultResponse}
	controlInput := composeTestInput(t, lane, []string{"retry", "terms"}, registry, "S15-RETRY-DIFF")
	controlInput.Retriever = controlRetriever
	controlProvider := &testfake.FakeProvider{Responses: []testfake.ProviderResponse{{Output: valid}}}
	control := ExecuteLane(context.Background(), controlInput, controlProvider, 2)
	if control.Failure != nil {
		t.Fatalf("control ExecuteLane failure = %#v, want success", control.Failure)
	}
	if controlProvider.GenerateCallCount() != 1 {
		t.Errorf("control Generate call count = %d, want exactly 1", controlProvider.GenerateCallCount())
	}
	if retryRetriever.RetrieveCallCount() != controlRetriever.RetrieveCallCount() {
		t.Errorf("retry Retrieve call count = %d, single-run baseline = %d", retryRetriever.RetrieveCallCount(), controlRetriever.RetrieveCallCount())
	}
	if controlRetriever.RetrieveCallCount() != 1 {
		t.Errorf("single-run baseline Retrieve call count = %d, want exactly 1", controlRetriever.RetrieveCallCount())
	}

	calls := retryProvider.GenerateCalls()
	if len(calls) == 2 {
		if !strings.HasPrefix(calls[1].Prompt, calls[0].Prompt) {
			t.Error("second Generate prompt is not the first composed prompt with an appended retry hint")
		} else {
			hint := calls[1].Prompt[len(calls[0].Prompt):]
			lowerHint := strings.ToLower(hint)
			if !strings.Contains(hint, badOutput) && !strings.Contains(lowerHint, "parse") && !strings.Contains(lowerHint, "reject") {
				t.Errorf("appended retry hint %q contains neither the bad output nor a parse/rejection reason", hint)
			}
		}
	}
}

// TestParse_GivesUpAtConfiguredAttempts verifies REQ-04 / S-16: the supplied
// attempt count controls Generate calls and exhaustion yields a named failure.
func TestParse_GivesUpAtConfiguredAttempts(t *testing.T) {
	for _, attempts := range []int{2, 3} {
		t.Run(fmt.Sprintf("attempts=%d", attempts), func(t *testing.T) {
			lane := Lane{ID: fmt.Sprintf("always-invalid-%d", attempts), Intent: "exercise configured attempts"}
			input := composeTestInput(t, lane, nil, resources.Registry{}, "S16-INVALID-DIFF")
			provider := &testfake.FakeProvider{DefaultResponse: testfake.ProviderResponse{Output: "still not JSON"}}

			got := ExecuteLane(context.Background(), input, provider, attempts)
			if provider.GenerateCallCount() != attempts {
				t.Errorf("Generate call count = %d, want configured attempts %d", provider.GenerateCallCount(), attempts)
			}
			if got.Failure == nil {
				t.Fatal("Failure = nil, want named parse failure")
			}
			if got.Failure.Kind != FailureKindParse {
				t.Errorf("failure Kind = %q, want %q", got.Failure.Kind, FailureKindParse)
			}
			if got.Failure.LaneID != lane.ID {
				t.Errorf("failure LaneID = %q, want %q", got.Failure.LaneID, lane.ID)
			}
			if !strings.Contains(strings.ToLower(got.Failure.Reason), "parse") {
				t.Errorf("failure Reason = %q, want it to name parsing", got.Failure.Reason)
			}
		})
	}
}

// TestParse_RejectsOversizedResponses verifies REQ-04 / S-40: structural
// caps fail the lane by name, while a field cap truncates only that field.
func TestParse_RejectsOversizedResponses(t *testing.T) {
	t.Run("response bytes cap", func(t *testing.T) {
		raw := parseTestJSON(t, map[string]any{
			"laneId": "bytes-cap",
			"findings": []map[string]any{{
				"title": "Oversized response", "severity": "high",
				"rationale": strings.Repeat("b", DefaultMaxResponseBytes+1),
			}},
		})
		assertParseCapFailure(t, raw, LimitResponseBytes)
	})

	t.Run("findings cap", func(t *testing.T) {
		findings := make([]map[string]any, DefaultMaxFindings+1)
		for i := range findings {
			findings[i] = map[string]any{
				"title": fmt.Sprintf("Finding %d", i), "severity": "low", "rationale": "valid but one too many",
			}
		}
		raw := parseTestJSON(t, map[string]any{"laneId": "findings-cap", "findings": findings})
		assertParseCapFailure(t, raw, LimitFindings)
	})

	t.Run("JSON depth cap", func(t *testing.T) {
		deepValue := `"leaf"`
		for range DefaultMaxJSONDepth + 1 {
			deepValue = "[" + deepValue + "]"
		}
		raw := `{"laneId":"depth-cap","findings":[],"unknown":` + deepValue + `}`
		assertParseCapFailure(t, raw, LimitJSONDepth)
	})

	t.Run("field chars cap", func(t *testing.T) {
		longSuggestion := strings.Repeat("界", DefaultMaxFieldChars+1)
		raw := parseTestJSON(t, map[string]any{
			"laneId": "field-cap",
			"findings": []map[string]any{
				{
					"title": "Long suggestion", "severity": "medium", "rationale": "Keep this finding and truncate only its suggestion.",
					"suggestion": longSuggestion,
				},
				{
					"title": "Companion finding", "severity": "low", "rationale": "This other finding must survive unchanged.",
					"suggestion": "short companion suggestion",
				},
			},
		})

		got, err := Parse(raw, parseTestLimits())
		if err != nil {
			t.Fatalf("Parse field-cap response: %v", err)
		}
		if len(got.Findings) != 2 {
			t.Fatalf("finding count = %d, want both findings", len(got.Findings))
		}
		if chars := len([]rune(got.Findings[0].Suggestion)); chars != DefaultMaxFieldChars {
			t.Errorf("truncated suggestion chars = %d, want %d", chars, DefaultMaxFieldChars)
		}
		if got.Findings[0].Rationale != "Keep this finding and truncate only its suggestion." {
			t.Errorf("first rationale was altered: %q", got.Findings[0].Rationale)
		}
		if got.Findings[1].Title != "Companion finding" || got.Findings[1].Suggestion != "short companion suggestion" {
			t.Errorf("companion finding was not preserved: %#v", got.Findings[1])
		}
		if got.Stats.TruncatedFields["suggestion"] != 1 {
			t.Errorf("suggestion truncation count = %d, want 1", got.Stats.TruncatedFields["suggestion"])
		}
	})
}

func assertParseCapFailure(t *testing.T, raw string, want LimitName) {
	t.Helper()
	_, err := Parse(raw, parseTestLimits())
	if err == nil {
		t.Fatalf("Parse error = nil, want %s cap failure", want)
	}
	var failure *ParseFailure
	if !errors.As(err, &failure) {
		t.Fatalf("Parse error type = %T, want *ParseFailure: %v", err, err)
	}
	if failure.Cap != want {
		t.Errorf("ParseFailure.Cap = %q, want %q", failure.Cap, want)
	}
	if failure.Reason == "" {
		t.Errorf("ParseFailure.Reason is empty for named cap %q", want)
	}
}

// TestParse_MapsUnknownSeverityToMedium verifies REQ-04 / S-42: every
// out-of-enum severity is retained as medium and contributes to the count.
func TestParse_MapsUnknownSeverityToMedium(t *testing.T) {
	raw := `{
  "laneId": "severity-map",
  "findings": [
    {"title":"Critical model value","severity":"critical","rationale":"Must be retained."},
    {"title":"Informational model value","severity":"info","rationale":"Must also be retained."},
    {"title":"Empty model value","severity":"","rationale":"Must still be retained."}
  ]
}`

	got, err := Parse(raw, parseTestLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Findings) != 3 {
		t.Fatalf("finding count = %d, want all 3 unknown-severity findings", len(got.Findings))
	}
	for i, finding := range got.Findings {
		if finding.Severity != SeverityMedium {
			t.Errorf("finding %d Severity = %q, want %q", i, finding.Severity, SeverityMedium)
		}
	}
	if got.Stats.MappedSeverities != 3 {
		t.Errorf("MappedSeverities = %d, want 3", got.Stats.MappedSeverities)
	}
}

// TestParse_TakesLastFence verifies REQ-04 / S-43: fence selection is based
// on position, so a later untagged fence beats an earlier valid JSON fence.
func TestParse_TakesLastFence(t *testing.T) {
	wrong := []Finding{{
		Title: "OLD WRONG finding", Severity: SeverityLow,
		Rationale: "This belongs to the previous attempt.", File: "old/wrong.go", Line: parseTestInt(9),
	}}
	want := []Finding{{
		Title: "Correct latest finding", Severity: SeverityHigh,
		Rationale: "This belongs to the latest attempt.", File: "new/correct.go", Line: parseTestInt(73),
	}}
	wrongJSON := parseTestJSON(t, map[string]any{"laneId": "last-fence", "findings": wrong})
	correctJSON := parseTestJSON(t, map[string]any{"laneId": "last-fence", "findings": want})
	raw := "Previous output:\n```json\n" + wrongJSON + "\n```\nCorrected output:\n```\n" + correctJSON + "\n```"

	got, err := Parse(raw, parseTestLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got.Findings, want) {
		t.Errorf("Findings = %#v, want latest untagged-fence findings %#v", got.Findings, want)
	}
	if reflect.DeepEqual(got.Findings, wrong) {
		t.Errorf("Findings incorrectly came from the earlier json fence: %#v", got.Findings)
	}
}
