package config

import (
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AI_PROVIDER_KEY", "GITLAB_TOKEN", "AI_PROVIDER",
		"PROJECTS_DIR", "GITLAB_API_BASE", "CI_PIPELINE_SOURCE",
		"MRI_REVIEW_MODE", "MRI_REVIEW_DUMP_ENABLED",
		"MRI_RAG_ON_NORMATIVE_EVICTION", "MRI_LANE_CONCURRENCY",
		"MRI_DIFF_PROMPT_SHARE", "MRI_MODEL_LIMITS",
	} {
		t.Setenv(k, "")
	}
}

func TestLoad(t *testing.T) {
	t.Run("missing AI_PROVIDER_KEY", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("GITLAB_TOKEN", "token")
		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing AI_PROVIDER_KEY")
		}
	})

	t.Run("missing GITLAB_TOKEN", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing GITLAB_TOKEN")
		}
	})

	t.Run("unknown AI_PROVIDER", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("AI_PROVIDER", "foobar")
		_, err := Load()
		if err == nil {
			t.Fatal("expected error for unknown AI_PROVIDER")
		}
	})

	t.Run("valid minimal env uses defaults", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AIProvider != ProviderOpenAI {
			t.Errorf("AIProvider: want %q, got %q", ProviderOpenAI, cfg.AIProvider)
		}
		if cfg.API.RetryAttempts != 3 {
			t.Errorf("API.RetryAttempts: want 3, got %d", cfg.API.RetryAttempts)
		}
		if cfg.Validation.MaxDiffSizeKB != 300 {
			t.Errorf("MaxDiffSizeKB: want 300, got %g", cfg.Validation.MaxDiffSizeKB)
		}
		want := "./projects/registry.yaml"
		if cfg.Projects.RegistryFile != want {
			t.Errorf("RegistryFile: want %q, got %q", want, cfg.Projects.RegistryFile)
		}
		if cfg.ReviewMode != "single" {
			t.Errorf("ReviewMode: want %q, got %q", "single", cfg.ReviewMode)
		}
		if cfg.ReviewDumpEnabled {
			t.Error("ReviewDumpEnabled: want false")
		}
		if cfg.RAGOnNormativeEviction != "warn" {
			t.Errorf("RAGOnNormativeEviction: want %q, got %q", "warn", cfg.RAGOnNormativeEviction)
		}
	})

	t.Run("review behavior is captured at load time", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("MRI_REVIEW_MODE", "multi")
		t.Setenv("MRI_REVIEW_DUMP_ENABLED", "true")
		t.Setenv("MRI_RAG_ON_NORMATIVE_EVICTION", "fail")
		t.Setenv("MRI_LANE_CONCURRENCY", "abc")
		t.Setenv("MRI_DIFF_PROMPT_SHARE", "0.7")
		t.Setenv("MRI_MODEL_LIMITS", "custom-model:1234")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		t.Setenv("MRI_REVIEW_MODE", "single")
		t.Setenv("MRI_REVIEW_DUMP_ENABLED", "false")
		t.Setenv("MRI_RAG_ON_NORMATIVE_EVICTION", "warn")
		t.Setenv("MRI_LANE_CONCURRENCY", "8")
		t.Setenv("MRI_DIFF_PROMPT_SHARE", "0.9")
		t.Setenv("MRI_MODEL_LIMITS", "other-model:5678")

		if cfg.ReviewMode != "multi" || !cfg.ReviewDumpEnabled || cfg.RAGOnNormativeEviction != "fail" {
			t.Errorf("loaded review behavior changed after env mutation: %#v", cfg)
		}
		if cfg.LaneConcurrency != "abc" || !cfg.LaneConcurrencySet {
			t.Errorf("LaneConcurrency = %q (set=%t), want %q (set=true)", cfg.LaneConcurrency, cfg.LaneConcurrencySet, "abc")
		}
		if cfg.DiffPromptShare != "0.7" {
			t.Errorf("DiffPromptShare: want %q, got %q", "0.7", cfg.DiffPromptShare)
		}
		if cfg.ModelLimits != "custom-model:1234" {
			t.Errorf("ModelLimits: want %q, got %q", "custom-model:1234", cfg.ModelLimits)
		}
	})

	t.Run("AI_PROVIDER=anthropic", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("AI_PROVIDER", "anthropic")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AIProvider != ProviderAnthropic {
			t.Errorf("want %q, got %q", ProviderAnthropic, cfg.AIProvider)
		}
	})

	t.Run("AI_PROVIDER=openai", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("AI_PROVIDER", "openai")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AIProvider != ProviderOpenAI {
			t.Errorf("want %q, got %q", ProviderOpenAI, cfg.AIProvider)
		}
	})
}
