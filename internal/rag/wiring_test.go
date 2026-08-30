package rag_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
	"mrinspect/internal/ragwire"

	_ "modernc.org/sqlite"
)

func TestProductionWiring_RegistersBuiltinSources(t *testing.T) {
	baked := productionStore(t)
	rag.RegisterBuiltinSources(rag.BuiltinSourcesConfig{BakedPath: baked})
	t.Setenv("MRI_RAG_SOURCE", "package,artifact,baked")

	got, err := rag.ResolveStore(context.Background(), rag.DefaultResolverConfig())
	if err != nil {
		t.Fatalf("ResolveStore: %v", err)
	}
	if got.SourceName != "baked" || got.Path != baked {
		t.Fatalf("resolution = %#v, want baked fixture after remote failures", got)
	}
	if len(got.Degraded) != 2 || got.Degraded[0].Source != "package" || got.Degraded[1].Source != "artifact" {
		t.Fatalf("Degraded = %#v, want named remote failures", got.Degraded)
	}
}

func TestProductionReviewPath_AssemblesStateForFooter(t *testing.T) {
	store := productionStore(t)
	bytes, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	rag.RegisterSource("package", failingSource{})
	rag.RegisterSource("artifact", failingSource{})
	rag.RegisterSource("baked", fixtureCandidateSource{candidate: rag.StoreCandidate{Path: store, SHA256: digest(bytes)}})
	t.Setenv("MRI_RAG_SOURCE", "package,artifact,baked")

	path := ragwire.NewReviewPath(ragwire.ReviewPathConfig{
		ResolverConfig: rag.DefaultResolverConfig(),
		ResourceSets:   []resources.Set{{Name: "review", Mode: resources.ModeRetrieval}},
	})
	state, err := path.RetrieveForReview(context.Background(), "needle change")
	if err != nil {
		t.Fatalf("RetrieveForReview: %v", err)
	}
	if !state.StorePresent || state.Store.BuiltAt != "2026-08-29T00:00:00Z" {
		t.Fatalf("store state = %#v", state)
	}
	if state.ResourcesSHA256[:8] != "01234567" {
		t.Fatalf("ResourcesSHA256 = %q, want footer prefix", state.ResourcesSHA256)
	}
	if len(state.Chunks) == 0 {
		t.Fatal("Chunks is empty, want retrieval result for footer-capable state")
	}
	if len(state.Degraded) != 2 || !strings.Contains(state.Degraded[0], "package") || !strings.Contains(state.Degraded[1], "artifact") {
		t.Fatalf("Degraded = %#v, want source failures", state.Degraded)
	}
}

type failingSource struct{}

func (failingSource) Resolve(context.Context, rag.SourceRequest) (rag.StoreCandidate, error) {
	return rag.StoreCandidate{}, errors.New("remote unavailable")
}

type fixtureCandidateSource struct{ candidate rag.StoreCandidate }

func (s fixtureCandidateSource) Resolve(context.Context, rag.SourceRequest) (rag.StoreCandidate, error) {
	return s.candidate, nil
}

func productionStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := filepath.Join(dir, "review.md")
	if err := os.WriteFile(doc, []byte("# Review\n\nneedle guidance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, "store.sqlite")
	set := resources.Set{Name: "review", Mode: resources.ModeRetrieval, Paths: []string{dir}}
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{OutputPath: store, Sets: []resources.Set{set}}); err != nil {
		t.Fatalf("index fixture: %v", err)
	}
	db, err := sql.Open("sqlite", store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE schema_meta SET built_at = ?, resources_sha256 = ? WHERE id = 1`, "2026-08-29T00:00:00Z", "0123456789abcdef"); err != nil {
		t.Fatalf("set provenance: %v", err)
	}
	return store
}

// TestBakedFloorSurvivesPublisherAllowlist verifies REQ-12's published-store
// allowlist: "任何其他 project 發佈的 store 一律拒絕". A local baked/path file
// is not published by a project, so its blank publisher is exempt; remote
// sources remain subject to the allowlist (TestResolveStore_RejectsUnlistedPublisher).
func TestBakedFloorSurvivesPublisherAllowlist(t *testing.T) {
	store := productionStore(t)
	bytes, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	rag.RegisterSource("package", failingSource{})
	rag.RegisterSource("artifact", failingSource{})
	rag.RegisterSource("baked", fixtureCandidateSource{candidate: rag.StoreCandidate{
		Path:   store,
		SHA256: digest(bytes),
	}})
	t.Setenv("MRI_RAG_SOURCE", "package,artifact,baked")

	config := rag.DefaultResolverConfig()
	config.AllowedPublishers = []string{"remote-project-id"}
	got, err := rag.ResolveStore(context.Background(), config)
	if err != nil {
		t.Fatalf("ResolveStore: %v", err)
	}
	if got.SourceName != "baked" || got.Path != store {
		t.Fatalf("resolution = %#v, want baked fixture after remote failures", got)
	}
	if len(got.Degraded) != 2 || got.Degraded[0].Source != "package" || got.Degraded[1].Source != "artifact" {
		t.Fatalf("Degraded = %#v, want named remote failures", got.Degraded)
	}
}
