package errors

import (
	"errors"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/validator"
)

func testHandler(postToMR bool) *Handler {
	return NewHandler(config.Config{
		ErrorReporting: config.ErrorReportingConfig{
			PostToMR:              postToMR,
			MaxErrorMessageLength: 2000,
		},
	}, nil)
}

func TestCategorize(t *testing.T) {
	h := testHandler(true)

	tests := []struct {
		name    string
		err     error
		wantCat Category
	}{
		{"nil error", nil, CategoryUnknown},
		{"validation error", &validator.ValidationError{Code: "X", Message: "bad input"}, CategoryValidation},
		{"missing required", errors.New("missing required env var"), CategoryConfiguration},
		{"timeout", errors.New("connection timeout exceeded"), CategoryAPI},
		{"rate limit 429", errors.New("received 429 from api"), CategoryAPI},
		{"no such file", errors.New("no such file or directory"), CategorySystem},
		{"generic error", errors.New("something went wrong"), CategoryUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.Categorize(tt.err)
			if got != tt.wantCat {
				t.Errorf("Categorize: want %q, got %q", tt.wantCat, got)
			}
		})
	}
}

func TestShouldPost(t *testing.T) {
	tests := []struct {
		name     string
		postToMR bool
		err      error
		cat      Category
		want     bool
	}{
		{"PostToMR false", false, errors.New("something"), CategoryAPI, false},
		{"nil error", true, nil, CategoryAPI, false},
		{"rate limit 429", true, errors.New("429 rate limit exceeded"), CategoryAPI, false},
		{"normal API error", true, errors.New("api error: 500 internal server error"), CategoryAPI, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := testHandler(tt.postToMR)
			got := h.ShouldPost(tt.err, tt.cat)
			if got != tt.want {
				t.Errorf("ShouldPost: want %v, got %v", tt.want, got)
			}
		})
	}
}

func TestGenerateMessage(t *testing.T) {
	h := testHandler(true)
	err := errors.New("something broke")
	msg := h.GenerateMessage(err, "fetchMRDetails", CategoryAPI)

	for _, s := range []string{"MRInspect", "fetching MR details", "something broke", string(CategoryAPI)} {
		if !strings.Contains(msg, s) {
			t.Errorf("GenerateMessage missing %q\ngot:\n%s", s, msg)
		}
	}
}

func TestFriendlyStage(t *testing.T) {
	h := testHandler(true)

	tests := []struct {
		stage string
		want  string
	}{
		{"validateSystem", "system validation"},
		{"fetchMRDetails", "fetching MR details"},
		{"unknownStage", "unknownStage"},
	}

	for _, tt := range tests {
		t.Run(tt.stage, func(t *testing.T) {
			got := h.FriendlyStage(tt.stage)
			if got != tt.want {
				t.Errorf("FriendlyStage(%q): want %q, got %q", tt.stage, tt.want, got)
			}
		})
	}
}
