package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// REQ-05 / S-06: new decode fields and the merge_base / compare endpoints.
func TestClient_IncrementalEndpoints(t *testing.T) {
	ctx := context.Background()
	const sha40 = "0123456789abcdef0123456789abcdef01234567"

	serve := func(t *testing.T, handler http.HandlerFunc) *Client {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return newTestClient(server.URL)
	}

	t.Run("GetMergeRequest decodes sha and diff_refs", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"iid":7,"sha":"headsha","diff_refs":{"base_sha":"basesha"}}`))
		})
		mr, err := client.GetMergeRequest(ctx, "42", "7")
		if err != nil {
			t.Fatalf("GetMergeRequest() error: %v", err)
		}
		if mr.SHA != "headsha" {
			t.Errorf("SHA: want headsha, got %q", mr.SHA)
		}
		if mr.DiffRefs.BaseSHA != "basesha" {
			t.Errorf("DiffRefs.BaseSHA: want basesha, got %q", mr.DiffRefs.BaseSHA)
		}
	})

	t.Run("GetMRChanges decodes sha", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"sha":"headsha","changes":[]}`))
		})
		resp, err := client.GetMRChanges(ctx, "42", "7")
		if err != nil {
			t.Fatalf("GetMRChanges() error: %v", err)
		}
		if resp.SHA != "headsha" {
			t.Errorf("SHA: want headsha, got %q", resp.SHA)
		}
	})

	t.Run("ListNotes decodes timestamps", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":1,"body":"x","created_at":"2026-10-01T10:00:00.000Z","updated_at":"2026-10-02T11:00:00.000Z"}]`))
		})
		notes, err := client.ListNotes(ctx, "42", "7")
		if err != nil {
			t.Fatalf("ListNotes() error: %v", err)
		}
		if len(notes) != 1 {
			t.Fatalf("notes: want 1, got %d", len(notes))
		}
		if notes[0].CreatedAt != "2026-10-01T10:00:00.000Z" {
			t.Errorf("CreatedAt: got %q", notes[0].CreatedAt)
		}
		if notes[0].UpdatedAt != "2026-10-02T11:00:00.000Z" {
			t.Errorf("UpdatedAt: got %q", notes[0].UpdatedAt)
		}
	})

	t.Run("GetMergeBase sends refs[] and returns id", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/projects/42/repository/merge_base" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			if got := r.URL.Query()["refs[]"]; !reflect.DeepEqual(got, []string{"aaa", "bbb"}) {
				t.Errorf("refs[]: want [aaa bbb], got %v", got)
			}
			_, _ = w.Write([]byte(`{"id":"` + sha40 + `"}`))
		})
		id, err := client.GetMergeBase(ctx, "42", "aaa", "bbb")
		if err != nil {
			t.Fatalf("GetMergeBase() error: %v", err)
		}
		if id != sha40 {
			t.Errorf("id: want %s, got %q", sha40, id)
		}
	})

	t.Run("Compare sends from/to/straight and decodes diffs", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/projects/42/repository/compare" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			q := r.URL.Query()
			if q.Get("from") != "aaa" || q.Get("to") != "bbb" || q.Get("straight") != "true" {
				t.Errorf("query: got %v", q)
			}
			_, _ = w.Write([]byte(`{"diffs":[{"old_path":"a.go","new_path":"b.go","diff":"@@","a_mode":"100644","b_mode":"100755","too_large":true,"collapsed":true}],"compare_timeout":true}`))
		})
		resp, err := client.Compare(ctx, "42", "aaa", "bbb")
		if err != nil {
			t.Fatalf("Compare() error: %v", err)
		}
		if len(resp.Diffs) != 1 {
			t.Fatalf("Diffs: want 1, got %d", len(resp.Diffs))
		}
		d := resp.Diffs[0]
		if d.AMode != "100644" || d.BMode != "100755" {
			t.Errorf("modes: got %q %q", d.AMode, d.BMode)
		}
		if !d.TooLarge {
			t.Errorf("TooLarge: want true")
		}
		if !d.Collapsed {
			t.Errorf("Collapsed: want true")
		}
		if !resp.CompareTimeout {
			t.Errorf("CompareTimeout: want true")
		}
	})

	t.Run("404 returns error", func(t *testing.T) {
		client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"404 Not Found"}`, http.StatusNotFound)
		})
		if _, err := client.GetMergeBase(ctx, "42", "aaa", "bbb"); err == nil {
			t.Errorf("GetMergeBase: want error on 404")
		}
		if _, err := client.Compare(ctx, "42", "aaa", "bbb"); err == nil {
			t.Errorf("Compare: want error on 404")
		}
	})
}
