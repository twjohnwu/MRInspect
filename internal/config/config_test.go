package config

import (
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AI_PROVIDER_KEY", "GITLAB_TOKEN", "AI_PROVIDER",
		"PROJECTS_DIR", "GITLAB_API_BASE", "CI_PIPELINE_SOURCE",
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
		if cfg.AIProvider != ProviderGemini {
			t.Errorf("AIProvider: want %q, got %q", ProviderGemini, cfg.AIProvider)
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
