package rag_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/rag"
)

func TestPackageSource_DownloadsPinnedVersion(t *testing.T) {
	const version = "2026.08.29"
	bytes := []byte("package store")
	config := testGitLabConfig("", t.TempDir())
	source := rag.NewPackageSource(&config)
	server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("PRIVATE-TOKEN = %q", r.Header.Get("PRIVATE-TOKEN"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		want := "/projects/42/packages/generic/rag-index/" + version + "/mrinspect-rag.sqlite"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = w.Write(bytes)
	}))
	defer server.Close()
	config.APIBase = server.URL

	got, err := source.Resolve(context.Background(), rag.SourceRequest{PackageVersion: version, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	written, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read downloaded candidate: %v", err)
	}
	if string(written) != string(bytes) || got.SHA256 != gitLabDigest(bytes) || got.Version != version {
		t.Fatalf("candidate = %#v, bytes = %q", got, written)
	}
	if got.PublisherProjectID != "42" {
		t.Fatalf("PublisherProjectID = %q, want project ID", got.PublisherProjectID)
	}
}

func TestArtifactSource_DownloadsLatest(t *testing.T) {
	bytes := []byte("artifact store")
	config := testGitLabConfig("", t.TempDir())
	source := rag.NewArtifactSource(&config)
	server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("PRIVATE-TOKEN = %q", r.Header.Get("PRIVATE-TOKEN"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/projects/42/jobs/artifacts/main/raw/mrinspect-rag.sqlite" || r.URL.Query().Get("job") != "rag-index" {
			t.Errorf("artifact request = %s", r.URL.String())
		}
		_, _ = w.Write(bytes)
	}))
	defer server.Close()
	config.APIBase = server.URL

	got, err := source.Resolve(context.Background(), rag.SourceRequest{MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	written, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read downloaded candidate: %v", err)
	}
	if string(written) != string(bytes) || got.SHA256 != gitLabDigest(bytes) {
		t.Fatalf("candidate = %#v, bytes = %q", got, written)
	}
}

func TestGitLabSources_RespectByteCapAndTimeout(t *testing.T) {
	t.Run("package honors context deadline", func(t *testing.T) {
		config := testGitLabConfig("", t.TempDir())
		source := rag.NewPackageSource(&config)
		server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		config.APIBase = server.URL
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		_, err := source.Resolve(ctx, rag.SourceRequest{MaxBytes: 1024})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Resolve error = %v, want context deadline", err)
		}
	})

	t.Run("artifact streams no more than MaxBytes", func(t *testing.T) {
		config := testGitLabConfig("", t.TempDir())
		source := rag.NewArtifactSource(&config)
		server := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 1025)))
		}))
		defer server.Close()
		config.APIBase = server.URL
		_, err := source.Resolve(context.Background(), rag.SourceRequest{MaxBytes: 1024})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "byte") {
			t.Fatalf("Resolve error = %v, want source byte-cap failure", err)
		}
	})
}

func testGitLabConfig(apiBase, dir string) rag.GitLabSourceConfig {
	return rag.GitLabSourceConfig{
		APIBase: apiBase, Token: "test-token", ProjectID: "42", PackageName: "rag-index",
		ArtifactRef: "main", ArtifactJob: "rag-index", StoreName: "mrinspect-rag.sqlite",
		DownloadDir: dir,
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Config: &http.Server{Handler: handler}, Listener: listener}
	server.Start()
	return server
}

func gitLabDigest(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return fmt.Sprintf("%x", sum)
}
