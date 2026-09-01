package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mrinspect/internal/config"
)

type transcriptTestResult struct {
	response string
	err      error
}

type transcriptTestProvider struct {
	results  []transcriptTestResult
	attempts int
}

func (p *transcriptTestProvider) Generate(context.Context, string, GenerateOptions) (string, error) {
	result := p.results[p.attempts]
	p.attempts++
	return result.response, result.err
}

func (p *transcriptTestProvider) Name() string { return "transcript-test" }

type transcriptTestEntry struct {
	Timestamp string `json:"ts"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Attempt   int    `json:"attempt"`
	Prompt    string `json:"prompt"`
	Response  string `json:"response"`
	Error     string `json:"error"`
}

func TestAITranscriptLogging_DisabledCreatesNoFile(t *testing.T) {
	resetTranscriptForTest(t)
	logDir := filepath.Join(t.TempDir(), "must-not-exist")
	provider := &transcriptTestProvider{results: []transcriptTestResult{{response: "review"}}}
	decorated := WithRetry(provider, config.APIConfig{RetryAttempts: 1})

	if _, err := decorated.Generate(context.Background(), "prompt", GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(logDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transcript directory stat error = %v, want not exist", err)
	}
}

func TestAITranscriptLogging_TwoCallsAppendTwoJSONLines(t *testing.T) {
	resetTranscriptForTest(t)
	logDir := t.TempDir()
	provider := &transcriptTestProvider{results: []transcriptTestResult{
		{response: "first response"},
		{response: "second response"},
	}}
	decorated := WithRetry(provider, config.APIConfig{RetryAttempts: 1, AILogDir: logDir})

	for _, prompt := range []string{"first prompt", "second prompt"} {
		if _, err := decorated.Generate(context.Background(), prompt, GenerateOptions{Model: "test-model"}); err != nil {
			t.Fatalf("Generate(%q): %v", prompt, err)
		}
	}

	entries := readTranscriptEntries(t, logDir)
	if len(entries) != 2 {
		t.Fatalf("transcript entries = %d, want 2", len(entries))
	}
	for i, want := range []transcriptTestEntry{
		{Provider: "transcript-test", Model: "test-model", Attempt: 1, Prompt: "first prompt", Response: "first response"},
		{Provider: "transcript-test", Model: "test-model", Attempt: 1, Prompt: "second prompt", Response: "second response"},
	} {
		got := entries[i]
		if _, err := time.Parse(time.RFC3339Nano, got.Timestamp); err != nil {
			t.Errorf("entry %d timestamp %q is not RFC3339Nano: %v", i, got.Timestamp, err)
		}
		if got.Provider != want.Provider || got.Model != want.Model || got.Attempt != want.Attempt ||
			got.Prompt != want.Prompt || got.Response != want.Response || got.Error != "" {
			t.Errorf("entry %d = %+v, want fields %+v and no error", i, got, want)
		}
	}
}

func TestAITranscriptLogging_FailureAndRetryAreBothLogged(t *testing.T) {
	resetTranscriptForTest(t)
	logDir := t.TempDir()
	provider := &transcriptTestProvider{results: []transcriptTestResult{
		{err: errors.New("temporary failure")},
		{response: "recovered"},
	}}
	decorated := WithRetry(provider, config.APIConfig{
		RetryAttempts:   2,
		RetryDelayMs:    0,
		MaxRetryDelayMs: 0,
		AILogDir:        logDir,
	})

	got, err := decorated.Generate(context.Background(), "retry prompt", GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "recovered" {
		t.Fatalf("Generate response = %q, want recovered", got)
	}

	entries := readTranscriptEntries(t, logDir)
	if len(entries) != 2 {
		t.Fatalf("transcript entries = %d, want 2", len(entries))
	}
	if entries[0].Attempt != 1 || entries[0].Error != "temporary failure" || entries[0].Response != "" {
		t.Errorf("first entry = %+v, want failed attempt 1", entries[0])
	}
	if entries[1].Attempt != 2 || entries[1].Error != "" || entries[1].Response != "recovered" {
		t.Errorf("second entry = %+v, want successful attempt 2 without error", entries[1])
	}
}

func TestAITranscriptLogging_FileErrorDoesNotFailGenerate(t *testing.T) {
	resetTranscriptForTest(t)
	root := t.TempDir()
	blockingFile := filepath.Join(root, "file")
	if err := os.WriteFile(blockingFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	provider := &transcriptTestProvider{results: []transcriptTestResult{{response: "review"}}}
	decorated := WithRetry(provider, config.APIConfig{
		RetryAttempts: 1,
		AILogDir:      filepath.Join(blockingFile, "child"),
	})

	got, err := decorated.Generate(context.Background(), "prompt", GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "review" {
		t.Fatalf("Generate response = %q, want review", got)
	}
}

func resetTranscriptForTest(t *testing.T) {
	t.Helper()
	previous := processTranscript
	previous.mu.Lock()
	if previous.file != nil {
		if err := previous.file.Close(); err != nil {
			t.Fatalf("close transcript: %v", err)
		}
	}
	previous.mu.Unlock()
	processTranscript = &transcriptLogger{}
}

func readTranscriptEntries(t *testing.T, logDir string) []transcriptTestEntry {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(logDir, "ai-log-*.jsonl"))
	if err != nil {
		t.Fatalf("glob transcripts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("transcript files = %d, want 1", len(files))
	}

	file, err := os.Open(files[0])
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()

	var entries []transcriptTestEntry
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var entry transcriptTestEntry
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("decode transcript: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}
