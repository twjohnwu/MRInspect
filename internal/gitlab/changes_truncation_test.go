package gitlab

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

func TestGetMRChanges_RequestsRawDiffsAndDecodesOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/42/merge_requests/7/changes" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("access_raw_diffs"); got != "true" {
			t.Errorf("access_raw_diffs: want true, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"overflow":true,"changes":[{"old_path":"a.go","new_path":"a.go","diff":"","a_mode":"100644","b_mode":"100755"}]}`))
	}))
	defer server.Close()

	resp, err := newTestClient(server.URL).GetMRChanges(context.Background(), "42", "7")
	if err != nil {
		t.Fatalf("GetMRChanges() error: %v", err)
	}
	if !resp.Overflow {
		t.Errorf("Overflow: want true")
	}
	if len(resp.Changes) != 1 || resp.Changes[0].AMode != "100644" || resp.Changes[0].BMode != "100755" {
		t.Errorf("modes not decoded: %+v", resp.Changes)
	}
}

func TestGetMRChanges_OverflowLogsOneWarn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"overflow":true,"changes":[]}`))
	}))
	defer server.Close()

	var buf bytes.Buffer
	client := NewClient(config.Config{
		GitLabToken:   "t",
		GitLabAPIBase: server.URL,
		API:           config.APIConfig{TimeoutMs: 1_000},
	}, logger.NewWithWriter(slog.LevelWarn, "", &buf))
	if _, err := client.GetMRChanges(context.Background(), "42", "7"); err != nil {
		t.Fatalf("GetMRChanges() error: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, `"level":"WARN"`); n != 1 {
		t.Fatalf("WARN lines: want 1, got %d\n%s", n, out)
	}
	if !strings.Contains(out, "overflow") || !strings.Contains(out, `"mrIID":"7"`) {
		t.Errorf("warn line missing overflow/mrIID attrs: %s", out)
	}
}

func TestChangeTruncated(t *testing.T) {
	tests := []struct {
		name   string
		change Change
		want   bool
	}{
		{"empty-diff modified file", Change{OldPath: "a.go", NewPath: "a.go", AMode: "100644", BMode: "100644"}, true},
		{"empty-diff file without mode info", Change{OldPath: "a.go", NewPath: "a.go"}, true},
		{"pure rename", Change{OldPath: "a.go", NewPath: "b.go", RenamedFile: true}, false},
		{"deleted file", Change{OldPath: "a.go", NewPath: "a.go", DeletedFile: true}, false},
		{"mode-only change", Change{OldPath: "a.sh", NewPath: "a.sh", AMode: "100644", BMode: "100755"}, false},
		{"file with diff", Change{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.change.Truncated(); got != tt.want {
				t.Errorf("Truncated() = %v, want %v", got, tt.want)
			}
		})
	}
}
