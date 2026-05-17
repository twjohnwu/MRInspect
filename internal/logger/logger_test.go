package logger

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T) *Logger {
	t.Helper()
	return New(slog.LevelInfo, filepath.Join(t.TempDir(), "metrics.json"))
}

func TestCompleteReview(t *testing.T) {
	l := newTestLogger(t)
	l.StartReview(42, 123)

	l.LogAPICall("gitlab", "/mr", 100, true, nil)
	l.LogAPICall("gemini", "/generate", 200, true, nil)
	l.LogAPICall("gitlab", "/notes", 50, true, nil)
	l.LogAPICall("gitlab", "/diff", 80, false, nil)

	summary := l.CompleteReview(true, nil)

	if summary.APICalls.Total != 4 {
		t.Errorf("Total: want 4, got %d", summary.APICalls.Total)
	}
	if summary.APICalls.Successful != 3 {
		t.Errorf("Successful: want 3, got %d", summary.APICalls.Successful)
	}
	if summary.APICalls.Failed != 1 {
		t.Errorf("Failed: want 1, got %d", summary.APICalls.Failed)
	}
	if summary.TotalDuration == "" {
		t.Error("TotalDuration is empty")
	}
}

func TestFormatDurationViaSummary(t *testing.T) {
	l := newTestLogger(t)
	l.StartReview(1, 1)
	summary := l.CompleteReview(true, nil)

	// Duration is well under 1 minute; should not contain "m" as a minute marker.
	if strings.Contains(summary.TotalDuration, "m") && !strings.Contains(summary.TotalDuration, "ms") {
		t.Errorf("short duration should not contain minute unit, got %q", summary.TotalDuration)
	}
}

func TestSaveMetrics(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "metrics.json")

	readAll := func() []Metrics {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		var out []Metrics
		_ = json.Unmarshal(data, &out)
		return out
	}

	t.Run("first save creates single-element array", func(t *testing.T) {
		l := New(slog.LevelInfo, file)
		l.StartReview(1, 1)
		if err := l.SaveMetrics(); err != nil {
			t.Fatalf("SaveMetrics: %v", err)
		}
		got := readAll()
		if len(got) != 1 {
			t.Errorf("want 1 entry, got %d", len(got))
		}
	})

	t.Run("second save appends to two-element array", func(t *testing.T) {
		l := New(slog.LevelInfo, file)
		l.StartReview(2, 2)
		if err := l.SaveMetrics(); err != nil {
			t.Fatalf("SaveMetrics: %v", err)
		}
		got := readAll()
		if len(got) != 2 {
			t.Errorf("want 2 entries, got %d", len(got))
		}
	})

	t.Run("capped at 100 entries after 101 saves", func(t *testing.T) {
		capFile := filepath.Join(t.TempDir(), "cap.json")
		for i := 0; i < 101; i++ {
			l := New(slog.LevelInfo, capFile)
			l.StartReview(i, i)
			if err := l.SaveMetrics(); err != nil {
				t.Fatalf("SaveMetrics iteration %d: %v", i, err)
			}
		}
		data, err := os.ReadFile(capFile)
		if err != nil {
			t.Fatalf("read metrics file: %v", err)
		}
		var history []Metrics
		if err := json.Unmarshal(data, &history); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(history) != 100 {
			t.Errorf("want 100 entries after cap, got %d", len(history))
		}
	})
}
