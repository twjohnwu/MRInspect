package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDotenv(t *testing.T) {
	t.Run("parses key-value pairs, skipping blanks and comments", func(t *testing.T) {
		input := "AI_PROVIDER=gemini\n\n# a full-line comment\nAI_PROVIDER_KEY=fake-key\n"
		values, malformed := parseDotenv(strings.NewReader(input))
		want := map[string]string{"AI_PROVIDER": "gemini", "AI_PROVIDER_KEY": "fake-key"}
		if !reflect.DeepEqual(values, want) {
			t.Errorf("values: want %v, got %v", want, values)
		}
		if len(malformed) != 0 {
			t.Errorf("malformed: want none, got %v", malformed)
		}
	})

	t.Run("splits on first equals only", func(t *testing.T) {
		values, _ := parseDotenv(strings.NewReader("KEY=a=b=c\n"))
		if values["KEY"] != "a=b=c" {
			t.Errorf("KEY: want %q, got %q", "a=b=c", values["KEY"])
		}
	})

	t.Run("trims spaces around key and value", func(t *testing.T) {
		values, _ := parseDotenv(strings.NewReader("  KEY  =  value  \n"))
		if values["KEY"] != "value" {
			t.Errorf("KEY: want %q, got %q", "value", values["KEY"])
		}
	})

	t.Run("strips inline comment only when whitespace precedes hash", func(t *testing.T) {
		values, _ := parseDotenv(strings.NewReader("KEY=value # trailing comment\n"))
		if values["KEY"] != "value" {
			t.Errorf("KEY with inline comment: want %q, got %q", "value", values["KEY"])
		}

		values, _ = parseDotenv(strings.NewReader("URL=http://example.com/#fragment\n"))
		if values["URL"] != "http://example.com/#fragment" {
			t.Errorf("URL without preceding whitespace before #: want %q, got %q", "http://example.com/#fragment", values["URL"])
		}
	})

	t.Run("unwraps quoted values, keeping inner hash and spaces", func(t *testing.T) {
		values, _ := parseDotenv(strings.NewReader("KEY=\"a value # not a comment\"\n"))
		if values["KEY"] != "a value # not a comment" {
			t.Errorf("double-quoted KEY: want %q, got %q", "a value # not a comment", values["KEY"])
		}

		values, _ = parseDotenv(strings.NewReader("KEY='a value # not a comment'\n"))
		if values["KEY"] != "a value # not a comment" {
			t.Errorf("single-quoted KEY: want %q, got %q", "a value # not a comment", values["KEY"])
		}
	})

	t.Run("reports malformed lines without applying them", func(t *testing.T) {
		values, malformed := parseDotenv(strings.NewReader("no-equals-here\n=empty-key\nOK=fine\n"))
		if values["OK"] != "fine" {
			t.Errorf("OK: want %q, got %q", "fine", values["OK"])
		}
		if len(values) != 1 {
			t.Errorf("values: want only OK applied, got %v", values)
		}
		wantMalformed := []string{"no-equals-here", "=empty-key"}
		if !reflect.DeepEqual(malformed, wantMalformed) {
			t.Errorf("malformed: want %v, got %v", wantMalformed, malformed)
		}
	})
}

func TestApplyDotenv(t *testing.T) {
	t.Run("existing process env always wins", func(t *testing.T) {
		t.Setenv("AI_PROVIDER", "openai")
		applyDotenv(map[string]string{"AI_PROVIDER": "gemini"})
		if got := os.Getenv("AI_PROVIDER"); got != "openai" {
			t.Errorf("AI_PROVIDER: want unchanged %q, got %q", "openai", got)
		}
	})

	t.Run("absent keys get set", func(t *testing.T) {
		t.Setenv("MRI_DOTENV_TEST_KEY", "")
		os.Unsetenv("MRI_DOTENV_TEST_KEY")
		applyDotenv(map[string]string{"MRI_DOTENV_TEST_KEY": "value"})
		if got := os.Getenv("MRI_DOTENV_TEST_KEY"); got != "value" {
			t.Errorf("MRI_DOTENV_TEST_KEY: want %q, got %q", "value", got)
		}
	})
}

func TestLoadDotenv(t *testing.T) {
	t.Run("missing file is a silent no-op", func(t *testing.T) {
		malformed := LoadDotenv(filepath.Join(t.TempDir(), "does-not-exist.env"))
		if malformed != nil {
			t.Errorf("malformed: want nil, got %v", malformed)
		}
	})

	t.Run("present file is parsed and applied, malformed lines returned", func(t *testing.T) {
		t.Setenv("MRI_DOTENV_LOAD_TEST", "")
		os.Unsetenv("MRI_DOTENV_LOAD_TEST")
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("MRI_DOTENV_LOAD_TEST=loaded\nbroken-line\n"), 0o644); err != nil {
			t.Fatalf("failed to write test .env: %v", err)
		}

		malformed := LoadDotenv(path)

		if got := os.Getenv("MRI_DOTENV_LOAD_TEST"); got != "loaded" {
			t.Errorf("MRI_DOTENV_LOAD_TEST: want %q, got %q", "loaded", got)
		}
		wantMalformed := []string{"broken-line"}
		if !reflect.DeepEqual(malformed, wantMalformed) {
			t.Errorf("malformed: want %v, got %v", wantMalformed, malformed)
		}
	})
}
