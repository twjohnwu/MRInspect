package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestEnrichmentConfig covers REQ-01 / S-01: EnrichmentConfig defaults,
// the MRI_REVIEW_ENRICHMENT exact-"true" convention, and value-domain
// validation for the four numeric/enum enrichment env vars.
func TestEnrichmentConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Enrichment.Enabled {
			t.Error("Enrichment.Enabled: want false")
		}
		if cfg.Enrichment.RemoteState != "disabled" {
			t.Errorf("Enrichment.RemoteState: want %q, got %q", "disabled", cfg.Enrichment.RemoteState)
		}
		if cfg.Enrichment.MaxCalls != 3 {
			t.Errorf("Enrichment.MaxCalls: want 3, got %d", cfg.Enrichment.MaxCalls)
		}
		if cfg.Enrichment.ResultBytes != 8192 {
			t.Errorf("Enrichment.ResultBytes: want 8192, got %d", cfg.Enrichment.ResultBytes)
		}
		if cfg.Enrichment.ToolTimeout != 5*time.Second {
			t.Errorf("Enrichment.ToolTimeout: want %s, got %s", 5*time.Second, cfg.Enrichment.ToolTimeout)
		}
	})

	t.Run("MRI_REVIEW_ENRICHMENT requires exact true", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("MRI_REVIEW_ENRICHMENT", "yes")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Enrichment.Enabled {
			t.Error("Enrichment.Enabled: want false for MRI_REVIEW_ENRICHMENT=\"yes\"")
		}
	})

	invalidCases := []struct {
		name string
		env  string
		val  string
	}{
		{"MRI_AI_REMOTE_STATE not enabled/disabled", "MRI_AI_REMOTE_STATE", "sometimes"},
		{"MRI_ENRICHMENT_MAX_CALLS below range", "MRI_ENRICHMENT_MAX_CALLS", "0"},
		{"MRI_ENRICHMENT_MAX_CALLS above range", "MRI_ENRICHMENT_MAX_CALLS", "11"},
		{"MRI_ENRICHMENT_RESULT_BYTES below range", "MRI_ENRICHMENT_RESULT_BYTES", "100"},
		{"MRI_ENRICHMENT_TOOL_TIMEOUT_MS below range", "MRI_ENRICHMENT_TOOL_TIMEOUT_MS", "0"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("AI_PROVIDER_KEY", "key")
			t.Setenv("GITLAB_TOKEN", "token")
			t.Setenv(tc.env, tc.val)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for %s=%q", tc.env, tc.val)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Errorf("error %q does not mention var name %s", err.Error(), tc.env)
			}
			if !strings.Contains(err.Error(), "invalid value") {
				t.Errorf("error %q does not contain %q", err.Error(), "invalid value")
			}
		})
	}

	t.Run("new vars are in .env.example", func(t *testing.T) {
		wanted := []string{
			"MRI_REVIEW_ENRICHMENT",
			"MRI_AI_REMOTE_STATE",
			"MRI_ENRICHMENT_MAX_CALLS",
			"MRI_ENRICHMENT_RESULT_BYTES",
			"MRI_ENRICHMENT_TOOL_TIMEOUT_MS",
		}

		path := filepath.Join("..", "..", ".env.example")
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer file.Close()

		envLine := regexp.MustCompile(`^\s*#?\s*([A-Z][A-Z0-9_]*)=`)
		found := make(map[string]struct{})
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if match := envLine.FindStringSubmatch(scanner.Text()); match != nil {
				found[match[1]] = struct{}{}
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		var missing []string
		for _, name := range wanted {
			if _, ok := found[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) != 0 {
			t.Errorf(".env.example missing enrichment vars: %s", strings.Join(missing, ", "))
		}
	})
}
