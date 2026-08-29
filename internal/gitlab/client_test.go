package gitlab

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

func TestListNotes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method: want GET, got %s", r.Method)
		}
		if r.URL.Path != "/projects/42/merge_requests/7/notes" {
			t.Errorf("path: want /projects/42/merge_requests/7/notes, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page: want 100, got %q", r.URL.Query().Get("per_page"))
		}

		w.Header().Set("Content-Type", "application/json")
		if requestNumber == 1 {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"id":11,"body":"first","author":{"username":"review-bot"}}]`))
			return
		}
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("page: want 2, got %q", r.URL.Query().Get("page"))
		}
		_, _ = w.Write([]byte(`[{"id":12,"body":"second","author":{"username":"alice"}}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	notes, err := client.ListNotes(context.Background(), "42", "7")
	if err != nil {
		t.Fatalf("ListNotes() error: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("request count: want 2, got %d", requests.Load())
	}
	if len(notes) != 2 || notes[0].ID != 11 || notes[0].Author.Username != "review-bot" || notes[1].Body != "second" {
		t.Fatalf("ListNotes() returned unexpected notes: %+v", notes)
	}
}

func TestUpdateNote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method: want PUT, got %s", r.Method)
		}
		if r.URL.Path != "/projects/42/merge_requests/7/notes/11" {
			t.Errorf("path: want /projects/42/merge_requests/7/notes/11, got %s", r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		if payload["body"] != "updated review" {
			t.Errorf("body: want %q, got %q", "updated review", payload["body"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":11,"body":"updated review","author":{"username":"review-bot"}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	note, err := client.UpdateNote(context.Background(), "42", "7", 11, "updated review")
	if err != nil {
		t.Fatalf("UpdateNote() error: %v", err)
	}
	if note.ID != 11 || note.Body != "updated review" || note.Author.Username != "review-bot" {
		t.Fatalf("UpdateNote() returned unexpected note: %+v", note)
	}
}

func newTestClient(apiBase string) *Client {
	return NewClient(config.Config{
		GitLabToken:   "test-token",
		GitLabAPIBase: apiBase,
		API: config.APIConfig{
			TimeoutMs: 1_000,
		},
	}, logger.New(slog.LevelError, ""))
}

func TestListNotes_PageCap(t *testing.T) {
	const pageCap = 20

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Next-Page", "2")
		_, _ = w.Write([]byte(`[{"id":11,"body":"note","author":{"username":"review-bot"}}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.ListNotes(context.Background(), "42", "7")
	if err == nil {
		t.Fatal("ListNotes() error: want page cap error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "page") {
		t.Fatalf("ListNotes() error: want page cap named, got %q", err)
	}
	if got := requests.Load(); got > pageCap+1 {
		t.Fatalf("request count: want at most %d, got %d", pageCap+1, got)
	}
}
