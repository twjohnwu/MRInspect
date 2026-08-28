package rag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"

	_ "modernc.org/sqlite"
)

// TestResolveStore_FirstSuccessfulSourceWins verifies REQ-12 / S-42.
func TestResolveStore_FirstSuccessfulSourceWins(t *testing.T) {
	t.Setenv("MRI_RAG_SOURCE", "package,artifact,baked")
	packageSource := &fixtureSource{candidate: candidate(t, "package", "2026-01-03T00:00:00Z", "3.0.0", "publisher-a")}
	artifactSource := &fixtureSource{candidate: candidate(t, "artifact", "2025-01-01T00:00:00Z", "2.0.0", "publisher-a")}
	bakedSource := &fixtureSource{candidate: candidate(t, "baked", "2024-01-01T00:00:00Z", "1.0.0", "publisher-a")}
	RegisterSource("package", packageSource)
	RegisterSource("artifact", artifactSource)
	RegisterSource("baked", bakedSource)

	got, err := ResolveStore(context.Background(), fixtureConfig())
	if err != nil {
		t.Fatalf("ResolveStore: %v", err)
	}
	if got.SourceName != "package" || got.BuiltAt != "2026-01-03T00:00:00Z" {
		t.Fatalf("winner = %#v, want package and its built_at", got)
	}
	if artifactSource.tried || bakedSource.tried {
		t.Fatalf("losers tried: artifact=%t baked=%t", artifactSource.tried, bakedSource.tried)
	}
}

// TestResolveStore_FallsBackToBakedFloorOnFetchFailure verifies REQ-12 / S-43.
func TestResolveStore_FallsBackToBakedFloorOnFetchFailure(t *testing.T) {
	t.Setenv("MRI_RAG_SOURCE", "package,artifact,baked")
	RegisterSource("package", &fixtureSource{err: statusError{code: 404}})
	RegisterSource("artifact", &fixtureSource{err: statusError{code: 403}})
	RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "2024-01-01T00:00:00Z", "", "publisher-a")})
	got, err := ResolveStore(context.Background(), fixtureConfig())
	if err != nil {
		t.Fatalf("ResolveStore: %v", err)
	}
	if got.SourceName != "baked" {
		t.Fatalf("winner = %q, want baked", got.SourceName)
	}
	assertDegraded(t, got.Degraded, "package", 404)
	assertDegraded(t, got.Degraded, "artifact", 403)
}

// TestResolveStore_FetchTimeoutDoesNotBlockReview verifies REQ-12 / S-44.
func TestResolveStore_FetchTimeoutDoesNotBlockReview(t *testing.T) {
	t.Run("delayed source falls through to baked", func(t *testing.T) {
		t.Setenv("MRI_RAG_SOURCE", "artifact,baked")
		artifact := &blockingSource{}
		RegisterSource("artifact", artifact)
		RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "2024-01-01T00:00:00Z", "", "publisher-a")})
		config := fixtureConfig()
		config.SourceTimeout = 20 * time.Millisecond
		config.TotalTimeout = time.Second
		started := time.Now()
		got, err := ResolveStore(context.Background(), config)
		if err != nil {
			t.Fatalf("ResolveStore: %v", err)
		}
		if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
			t.Fatalf("resolution took %s, source timeout is %s", elapsed, config.SourceTimeout)
		}
		if !artifact.sawDeadline {
			t.Fatal("artifact source did not receive a per-source context deadline")
		}
		if got.SourceName != "baked" {
			t.Fatalf("winner = %q, want baked", got.SourceName)
		}
		assertDegraded(t, got.Degraded, "artifact", 0)
		if !errors.Is(degradedError(t, got.Degraded, "artifact"), context.DeadlineExceeded) {
			t.Fatalf("artifact degradation = %v, want timeout", degradedError(t, got.Degraded, "artifact"))
		}
	})
	t.Run("oversized source falls through to baked", func(t *testing.T) {
		t.Setenv("MRI_RAG_SOURCE", "artifact,baked")
		path := filepath.Join(t.TempDir(), "oversized.db")
		bytes := make([]byte, 1025)
		if err := os.WriteFile(path, bytes, 0o600); err != nil {
			t.Fatal(err)
		}
		RegisterSource("artifact", &fixtureSource{candidate: StoreCandidate{
			Path:               path,
			SHA256:             sha256Hex(bytes),
			PublisherProjectID: "publisher-a",
		}})
		RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "2024-01-01T00:00:00Z", "", "publisher-a")})
		config := fixtureConfig()
		config.MaxBytes = 1 << 10
		got, err := ResolveStore(context.Background(), config)
		if err != nil || got.SourceName != "baked" {
			t.Fatalf("result = %#v, %v; want baked fallback", got, err)
		}
		assertDegraded(t, got.Degraded, "artifact", 0)
		degraded := strings.ToLower(degradedError(t, got.Degraded, "artifact").Error())
		if !strings.Contains(degraded, "byte") || !strings.Contains(degraded, "cap") {
			t.Fatalf("byte-cap failure was not named in Degraded: %v", degradedError(t, got.Degraded, "artifact"))
		}
	})
}

// TestResolveStore_UnknownSourceListsRegistered verifies REQ-12 / S-47.
func TestResolveStore_UnknownSourceListsRegistered(t *testing.T) {
	t.Setenv("MRI_RAG_SOURCE", "s3,baked")
	baked := &fixtureSource{candidate: candidate(t, "baked", "2024-01-01T00:00:00Z", "", "publisher-a")}
	RegisterSource("baked", baked)
	_, err := ResolveStore(context.Background(), fixtureConfig())
	if err == nil || !strings.Contains(err.Error(), "s3") || !strings.Contains(err.Error(), "baked") {
		t.Fatalf("error = %v, want s3 and registered names", err)
	}
	if baked.tried {
		t.Fatal("unknown source silently fell through to baked")
	}
}

// TestResolveStore_PackageVersionSelection verifies REQ-12 / S-48.
func TestResolveStore_PackageVersionSelection(t *testing.T) {
	t.Setenv("MRI_RAG_SOURCE", "package")
	packageSource := &versionedSource{versions: map[string]StoreCandidate{
		"1.0.0": candidate(t, "package", "2024-01-01T00:00:00Z", "1.0.0", "publisher-a"),
		"2.0.0": candidate(t, "package", "2025-01-01T00:00:00Z", "2.0.0", "publisher-a"),
		"3.0.0": candidate(t, "package", "2026-01-01T00:00:00Z", "3.0.0", "publisher-a"),
	}, latest: "3.0.0"}
	RegisterSource("package", packageSource)
	unsetEnv(t, "MRI_RAG_PACKAGE_VERSION")
	latest, err := ResolveStore(context.Background(), fixtureConfig())
	if err != nil || latest.Version != "3.0.0" {
		t.Fatalf("latest = %#v, %v; want 3.0.0", latest, err)
	}
	t.Setenv("MRI_RAG_PACKAGE_VERSION", "2.0.0")
	middle, err := ResolveStore(context.Background(), fixtureConfig())
	if err != nil || middle.Version != "2.0.0" {
		t.Fatalf("middle = %#v, %v; want 2.0.0", middle, err)
	}
}

// TestResolveStore_RejectsCorruptDownload verifies REQ-12 / S-49.
func TestResolveStore_RejectsCorruptDownload(t *testing.T) {
	t.Run("digest mismatch is rejected before SQLite first read", func(t *testing.T) {
		t.Setenv("MRI_RAG_SOURCE", "package,baked")
		recorder := &orderRecorder{}
		bad := candidate(t, "package", "", "", "publisher-a")
		bad.SHA256 = sha256Hex([]byte("different bytes"))
		RegisterSource("package", &fixtureSource{candidate: bad})
		RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "", "", "publisher-a")})
		config := fixtureConfig()
		config.FileReader = recordingReader{recorder}
		config.StoreOpener = recordingOpener{recorder: recorder}
		got, err := ResolveStore(context.Background(), config)
		if err != nil || got.SourceName != "baked" {
			t.Fatalf("result = %#v, %v; want baked fallback", got, err)
		}
		if !recorder.before("digest-read", "sqlite-first-read") {
			t.Fatalf("events = %v; digest-read must precede SQLite first file read", recorder.events)
		}
		assertDegraded(t, got.Degraded, "package", 0)
	})
	t.Run("manifest mismatch fails after SQLite read and falls through", func(t *testing.T) {
		t.Setenv("MRI_RAG_SOURCE", "package,baked")
		broken := manifestMismatchStore(t)
		RegisterSource("package", &fixtureSource{candidate: candidateForPath(t, broken, "package", "publisher-a")})
		RegisterSource("baked", &fixtureSource{candidate: candidateForPath(t, validStore(t), "baked", "publisher-a")})
		config := fixtureConfig()
		config.StoreOpener = DefaultResolverConfig().StoreOpener
		got, err := ResolveStore(context.Background(), config)
		if err != nil || got.SourceName != "baked" {
			t.Fatalf("result = %#v, %v; want baked fallback", got, err)
		}
		assertDegraded(t, got.Degraded, "package", 0)
		if !strings.Contains(degradedError(t, got.Degraded, "package").Error(), "chunk_count") {
			t.Fatal("manifest failure was not named in Degraded")
		}
	})
}

// TestResolveStore_PathOnlyWhenExplicit verifies REQ-12 / S-66 (process half).
func TestResolveStore_PathOnlyWhenExplicit(t *testing.T) {
	unsetEnv(t, "MRI_RAG_SOURCE")
	unsetEnv(t, "MRI_RAG_STORE")
	path := &fixtureSource{candidate: candidate(t, "path", "", "", "publisher-a")}
	RegisterSource("package", &fixtureSource{err: errors.New("unavailable")})
	RegisterSource("artifact", &fixtureSource{err: errors.New("unavailable")})
	RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "", "", "publisher-a")})
	RegisterSource("path", path)
	got, err := ResolveStore(context.Background(), fixtureConfig())
	if err != nil || got.SourceName != "baked" {
		t.Fatalf("default result = %#v, %v; want baked", got, err)
	}
	if path.tried {
		t.Fatal("path was tried without explicitly set MRI_RAG_STORE")
	}
	t.Setenv("MRI_RAG_STORE", "/explicit/store.db")
	t.Setenv("MRI_RAG_SOURCE", "path,package,artifact,baked")
	got, err = ResolveStore(context.Background(), fixtureConfig())
	if err != nil || got.SourceName != "path" || !path.tried {
		t.Fatalf("explicit result = %#v, %v; want path winner", got, err)
	}
}

// TestResolveStore_RejectsUnlistedPublisher verifies REQ-12 / S-67.
func TestResolveStore_RejectsUnlistedPublisher(t *testing.T) {
	t.Setenv("MRI_RAG_SOURCE", "package,baked")
	RegisterSource("package", &fixtureSource{candidate: candidateForPath(t, validStore(t), "package", "project-b")})
	RegisterSource("baked", &fixtureSource{candidate: candidate(t, "baked", "", "", "project-a")})
	config := fixtureConfig()
	config.AllowedPublishers = []string{"project-a"}
	got, err := ResolveStore(context.Background(), config)
	if err != nil || got.SourceName != "baked" {
		t.Fatalf("result = %#v, %v; want baked fallback", got, err)
	}
	assertDegraded(t, got.Degraded, "package", 0)
	if !strings.Contains(degradedError(t, got.Degraded, "package").Error(), "project-b") {
		t.Fatal("rejected publisher project id was not named")
	}
}

type fixtureSource struct {
	candidate StoreCandidate
	err       error
	tried     bool
}

func (s *fixtureSource) Resolve(_ context.Context, _ SourceRequest) (StoreCandidate, error) {
	s.tried = true
	return s.candidate, s.err
}

type blockingSource struct{ sawDeadline bool }

func (s *blockingSource) Resolve(ctx context.Context, _ SourceRequest) (StoreCandidate, error) {
	_, s.sawDeadline = ctx.Deadline()
	<-ctx.Done()
	return StoreCandidate{}, ctx.Err()
}

type versionedSource struct {
	versions map[string]StoreCandidate
	latest   string
}

func (s *versionedSource) Resolve(_ context.Context, request SourceRequest) (StoreCandidate, error) {
	version := request.PackageVersion
	if version == "" {
		version = s.latest
	}
	return s.versions[version], nil
}

type statusError struct{ code int }

func (e statusError) Error() string   { return fmt.Sprintf("HTTP %d", e.code) }
func (e statusError) StatusCode() int { return e.code }

type orderRecorder struct{ events []string }

func (r *orderRecorder) add(event string) { r.events = append(r.events, event) }
func (r *orderRecorder) before(first, second string) bool {
	a, b := -1, -1
	for i, event := range r.events {
		if event == first && a < 0 {
			a = i
		}
		if event == second && b < 0 {
			b = i
		}
	}
	return a >= 0 && b >= 0 && a < b
}

type recordingReader struct{ recorder *orderRecorder }

func (r recordingReader) ReadFile(path string) ([]byte, error) {
	r.recorder.add("digest-read")
	return os.ReadFile(path)
}

type recordingOpener struct{ recorder *orderRecorder }

func (o recordingOpener) OpenAndValidate(_ context.Context, _ string) error {
	o.recorder.add("sqlite-first-read")
	return nil
}

func fixtureConfig() ResolverConfig {
	return ResolverConfig{SourceTimeout: time.Second, TotalTimeout: 2 * time.Second, MaxBytes: 1 << 20, AllowedPublishers: []string{"publisher-a", "project-a"}, FileReader: osReader{}, StoreOpener: acceptingOpener{}}
}

type osReader struct{}

func (osReader) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

type acceptingOpener struct{}

func (acceptingOpener) OpenAndValidate(context.Context, string) error { return nil }
func candidate(t *testing.T, source, builtAt, version, publisher string) StoreCandidate {
	t.Helper()
	path := filepath.Join(t.TempDir(), source+".db")
	bytes := []byte("well-formed fixture " + source)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return StoreCandidate{Path: path, SHA256: sha256Hex(bytes), PublisherProjectID: publisher, BuiltAt: builtAt, Version: version}
}
func candidateForPath(t *testing.T, path, source, publisher string) StoreCandidate {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return StoreCandidate{Path: path, SHA256: sha256Hex(bytes), PublisherProjectID: publisher, Version: source}
}
func sha256Hex(bytes []byte) string { sum := sha256.Sum256(bytes); return fmt.Sprintf("%x", sum) }
func assertDegraded(t *testing.T, entries []DegradedEntry, source string, status int) {
	t.Helper()
	entry := degradedEntry(t, entries, source)
	if entry.StatusCode != status {
		t.Fatalf("%s status = %d, want %d", source, entry.StatusCode, status)
	}
}
func degradedError(t *testing.T, entries []DegradedEntry, source string) error {
	t.Helper()
	return degradedEntry(t, entries, source).Err
}
func degradedEntry(t *testing.T, entries []DegradedEntry, source string) DegradedEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf("no Degraded entry for %q: %#v", source, entries)
	return DegradedEntry{}
}

func manifestMismatchStore(t *testing.T) string {
	t.Helper()
	path := validStore(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE schema_meta SET chunk_count = chunk_count + 1 WHERE id = 1`); err != nil {
		t.Fatalf("corrupt chunk_count: %v", err)
	}
	return path
}

func validStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, []byte("# title\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "store.db")
	if _, err := sqlite.Index(context.Background(), sqlite.IndexOptions{OutputPath: path, Sets: []resources.Set{{Name: "fixture", Paths: []string{doc}}}}); err != nil {
		t.Fatalf("index fixture: %v", err)
	}
	return path
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	previous, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}
