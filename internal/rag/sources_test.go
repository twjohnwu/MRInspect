package rag_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mrinspect/internal/rag"
)

func TestPathSource_UsesExplicitStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "explicit.sqlite")
	bytes := []byte("local store bytes")
	if err := os.WriteFile(store, bytes, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := rag.NewPathSource().Resolve(context.Background(), rag.SourceRequest{Path: store})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != store {
		t.Fatalf("Path = %q, want %q", got.Path, store)
	}
	if got.SHA256 != digest(bytes) {
		t.Fatalf("SHA256 = %q, want digest of local bytes", got.SHA256)
	}
	// A local path has no remote publisher to attest. It self-attests only by
	// calculating the digest of the file it opened; allowlist enforcement is for
	// remote PublisherProjectID values in ResolveStore.
	if got.PublisherProjectID != "" {
		t.Fatalf("PublisherProjectID = %q, want no remote publisher claim", got.PublisherProjectID)
	}
}

func TestBakedSource_UsesProgramConstant(t *testing.T) {
	if rag.BakedStorePath != "/app/.rag/mrinspect-rag.sqlite" {
		t.Fatalf("BakedStorePath = %q, want compile-time image path", rag.BakedStorePath)
	}

	fixture := filepath.Join(t.TempDir(), "baked.sqlite")
	if err := os.WriteFile(fixture, []byte("baked store bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MRI_RAG_STORE", filepath.Join(t.TempDir(), "must-not-be-read.sqlite"))
	got, err := rag.NewBakedSource(fixture).Resolve(context.Background(), rag.SourceRequest{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != fixture {
		t.Fatalf("Path = %q, want constructor-selected baked path %q", got.Path, fixture)
	}
}

func digest(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return fmt.Sprintf("%x", sum)
}
