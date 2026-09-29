package evalrun

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/logger"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/testfake"
)

// This file is a white-box (package evalrun, not evalrun_test) test: the
// restricted-modes test needs withProviderFactory, the same unexported seam
// enrichment_test.go documents and reuses.

// TestParseModes verifies the `-modes` flag's comma-separated parsing rules:
// empty string means "use the default three", names are case-insensitive
// and order-preserving, unknown names and duplicates are rejected.
func TestParseModes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []reviewer.EvalMode
		wantErr string
	}{
		{name: "empty", input: "", want: nil},
		{name: "single", input: "single", want: []reviewer.EvalMode{reviewer.EvalModeSingle}},
		{name: "mixed case with spaces", input: "Single, multi", want: []reviewer.EvalMode{reviewer.EvalModeSingle, reviewer.EvalModeMulti}},
		{name: "duplicate", input: "single,single", wantErr: "single"},
		{name: "unknown", input: "bogus", wantErr: "bogus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModes(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseModes(%q) error = nil, want error mentioning %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ParseModes(%q) error = %q, want it to mention %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModes(%q) unexpected error: %v", tt.input, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseModes(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseModes(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestRunWithConfig_ModesRestricted verifies REQ-01 S-14's `-modes` need:
// WithModes(single) must run only the single mode against a real
// RunWithConfig call and leave the report showing no other mode ran.
func TestRunWithConfig_ModesRestricted(t *testing.T) {
	dir := t.TempDir()
	writeEnrichmentEvalFixture(t, dir)
	cfg := enrichmentEvalConfig(t, false)
	fake := &testfake.FakeProvider{
		DefaultResponse: testfake.ProviderResponse{Output: enrichmentEvalReviewText},
	}
	reportPath := filepath.Join(t.TempDir(), "REPORT.md")
	log := logger.NewWithWriter(slog.LevelError, "", io.Discard)

	err := RunWithConfig(context.Background(), dir, reportPath, cfg, log,
		withProviderFactory(func(config.Config, *logger.Logger) (ai.Provider, error) {
			return fake, nil
		}),
		WithModes(reviewer.EvalModeSingle))
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}

	if got := fake.GenerateCallCount(); got != 1 {
		t.Errorf("provider Generate call count = %d, want 1 (single mode only)", got)
	}

	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(report)
	if !strings.Contains(text, "### single") {
		t.Errorf("report missing single mode section: %s", text)
	}
	if strings.Contains(text, "### multi") || strings.Contains(text, "### reflect") {
		t.Errorf("report contains a mode beyond single, want only single: %s", text)
	}
}
