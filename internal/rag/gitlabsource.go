package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const latestPackageVersion = "latest"

// BakedStorePath is the image-local REQ-06 fallback. It is deliberately a
// program constant, rather than an environment default.
const BakedStorePath = "/app/.rag/mrinspect-rag.sqlite"

// PathSource resolves an explicitly supplied local store path.
type PathSource struct{}

func NewPathSource() *PathSource { return &PathSource{} }

func (s *PathSource) Resolve(ctx context.Context, request SourceRequest) (StoreCandidate, error) {
	if request.Path == "" {
		return StoreCandidate{}, fmt.Errorf("path source: store path is empty")
	}
	return localCandidate(ctx, request.Path)
}

// BakedSource resolves the image-local fallback store.
type BakedSource struct{ path string }

func NewBakedSource(path string) *BakedSource {
	if path == "" {
		path = BakedStorePath
	}
	return &BakedSource{path: path}
}

func (s *BakedSource) Resolve(ctx context.Context, _ SourceRequest) (StoreCandidate, error) {
	return localCandidate(ctx, s.path)
}

func localCandidate(ctx context.Context, path string) (StoreCandidate, error) {
	if err := ctx.Err(); err != nil {
		return StoreCandidate{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("open local store: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return StoreCandidate{}, fmt.Errorf("hash local store: %w", err)
	}
	return StoreCandidate{Path: path, SHA256: fmt.Sprintf("%x", hash.Sum(nil)), Local: true}, nil
}

// GitLabSourceConfig supplies GitLab API identity and filesystem dependencies
// shared by the two remote REQ-12 sources.
type GitLabSourceConfig struct {
	APIBase     string
	Token       string
	ProjectID   string
	PackageName string
	ArtifactRef string
	ArtifactJob string
	StoreName   string
	DownloadDir string
	HTTPClient  *http.Client
}

type PackageSource struct{ config *GitLabSourceConfig }

func NewPackageSource(config *GitLabSourceConfig) *PackageSource {
	return &PackageSource{config: config}
}

func (s *PackageSource) Resolve(ctx context.Context, request SourceRequest) (StoreCandidate, error) {
	if s.config == nil {
		return StoreCandidate{}, fmt.Errorf("package source: missing configuration")
	}
	version := request.PackageVersion
	if version == "" {
		version = latestPackageVersion
	}
	path := fmt.Sprintf("/projects/%s/packages/generic/%s/%s/%s", url.PathEscape(s.config.ProjectID), url.PathEscape(s.config.PackageName), url.PathEscape(version), url.PathEscape(s.config.StoreName))
	candidate, err := downloadGitLabStore(ctx, s.config, path, request.MaxBytes)
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("package source: %w", err)
	}
	candidate.Version = version
	return candidate, nil
}

type ArtifactSource struct{ config *GitLabSourceConfig }

func NewArtifactSource(config *GitLabSourceConfig) *ArtifactSource {
	return &ArtifactSource{config: config}
}

func (s *ArtifactSource) Resolve(ctx context.Context, request SourceRequest) (StoreCandidate, error) {
	if s.config == nil {
		return StoreCandidate{}, fmt.Errorf("artifact source: missing configuration")
	}
	path := fmt.Sprintf("/projects/%s/jobs/artifacts/%s/raw/%s?job=%s", url.PathEscape(s.config.ProjectID), url.PathEscape(s.config.ArtifactRef), url.PathEscape(s.config.StoreName), url.QueryEscape(s.config.ArtifactJob))
	candidate, err := downloadGitLabStore(ctx, s.config, path, request.MaxBytes)
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("artifact source: %w", err)
	}
	candidate.Version = latestPackageVersion
	return candidate, nil
}

func downloadGitLabStore(ctx context.Context, config *GitLabSourceConfig, path string, maxBytes int64) (StoreCandidate, error) {
	if maxBytes <= 0 {
		return StoreCandidate{}, fmt.Errorf("download byte cap must be positive")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.APIBase, "/")+path, nil)
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("PRIVATE-TOKEN", config.Token)
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return StoreCandidate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return StoreCandidate{}, httpStatusError{code: response.StatusCode}
	}
	directory := config.DownloadDir
	if directory == "" {
		directory = os.TempDir()
	}
	file, err := os.CreateTemp(directory, ".mrinspect-rag-*")
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("create download file: %w", err)
	}
	pathOnDisk := file.Name()
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(pathOnDisk)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, maxBytes+1))
	if err != nil {
		return StoreCandidate{}, fmt.Errorf("stream download: %w", err)
	}
	if written > maxBytes {
		return StoreCandidate{}, fmt.Errorf("download byte size %d exceeds byte cap %d", written, maxBytes)
	}
	if err := file.Close(); err != nil {
		return StoreCandidate{}, fmt.Errorf("close download file: %w", err)
	}
	success = true
	return StoreCandidate{
		Path:               pathOnDisk,
		SHA256:             fmt.Sprintf("%x", hash.Sum(nil)),
		PublisherProjectID: config.ProjectID,
		Cleanup:            func() error { return os.Remove(pathOnDisk) },
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(bytes []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(bytes)
}

type httpStatusError struct{ code int }

func (e httpStatusError) Error() string   { return fmt.Sprintf("GitLab API status %d", e.code) }
func (e httpStatusError) StatusCode() int { return e.code }

// BuiltinSourcesConfig configures production registration for all four built-in
// REQ-12 source names. An empty BakedPath means BakedStorePath.
type BuiltinSourcesConfig struct {
	GitLab    GitLabSourceConfig
	BakedPath string
}

func RegisterBuiltinSources(config BuiltinSourcesConfig) {
	RegisterSource("path", NewPathSource())
	RegisterSource("package", NewPackageSource(&config.GitLab))
	RegisterSource("artifact", NewArtifactSource(&config.GitLab))
	RegisterSource("baked", NewBakedSource(config.BakedPath))
}
