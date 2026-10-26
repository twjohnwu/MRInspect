package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIncrementalConfig covers REQ-01 / S-01: IncrementalConfig defaults,
// the MRI_INCREMENTAL_REVIEW exact-"true" convention, value-domain
// validation for MRI_INCREMENTAL_FULL_EVERY, and .env.example coverage.
func TestIncrementalConfig(t *testing.T) {
	loadWith := func(t *testing.T, env map[string]string) (Config, error) {
		t.Helper()
		clearConfigEnv(t)
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		for k, v := range env {
			t.Setenv(k, v)
		}
		return Load()
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadWith(t, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Incremental.Enabled {
			t.Error("Incremental.Enabled: want false")
		}
		if cfg.Incremental.FullEvery != 5 {
			t.Errorf("Incremental.FullEvery: want 5, got %d", cfg.Incremental.FullEvery)
		}
	})

	t.Run("MRI_INCREMENTAL_REVIEW requires exact true", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{"MRI_INCREMENTAL_REVIEW": "yes"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Incremental.Enabled {
			t.Error("Incremental.Enabled: want false for MRI_INCREMENTAL_REVIEW=\"yes\"")
		}

		cfg, err = loadWith(t, map[string]string{"MRI_INCREMENTAL_REVIEW": "true"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Incremental.Enabled {
			t.Error("Incremental.Enabled: want true for MRI_INCREMENTAL_REVIEW=\"true\"")
		}
	})

	t.Run("MRI_INCREMENTAL_FULL_EVERY zero is valid", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{"MRI_INCREMENTAL_FULL_EVERY": "0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Incremental.FullEvery != 0 {
			t.Errorf("Incremental.FullEvery: want 0, got %d", cfg.Incremental.FullEvery)
		}
	})

	for _, val := range []string{"-1", "101"} {
		t.Run("MRI_INCREMENTAL_FULL_EVERY out of range "+val, func(t *testing.T) {
			_, err := loadWith(t, map[string]string{"MRI_INCREMENTAL_FULL_EVERY": val})
			if err == nil {
				t.Fatalf("expected error for MRI_INCREMENTAL_FULL_EVERY=%q", val)
			}
			if !strings.Contains(err.Error(), "MRI_INCREMENTAL_FULL_EVERY") {
				t.Errorf("error %q does not mention var name", err.Error())
			}
			if !strings.Contains(err.Error(), "invalid value") {
				t.Errorf("error %q does not contain %q", err.Error(), "invalid value")
			}
		})
	}

	t.Run("new vars are in .env.example", func(t *testing.T) {
		wanted := []string{"MRI_INCREMENTAL_REVIEW", "MRI_INCREMENTAL_FULL_EVERY"}

		path := filepath.Join("..", "..", ".env.example")
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer func() { _ = file.Close() }()

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

		for _, name := range wanted {
			if _, ok := found[name]; !ok {
				t.Errorf("%s missing from .env.example", name)
			}
		}
	})
}
