package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source resolves one REQ-12 named store source into a local candidate file.
type Source interface {
	Resolve(context.Context, SourceRequest) (StoreCandidate, error)
}

// SourceRequest supplies resolver-selected inputs to a REQ-12 source.
type SourceRequest struct {
	Path           string
	PackageVersion string
	MaxBytes       int64
}

// StoreCandidate is the local file and publisher metadata returned by a source.
type StoreCandidate struct {
	Path               string
	SHA256             string
	PublisherProjectID string
	BuiltAt            string
	Version            string
	// Cleanup releases a source-owned local candidate. It is nil for files the
	// resolver does not own, including path and baked sources.
	Cleanup func() error
	// Local identifies candidates supplied by REQ-12's path and baked names.
	// Those files are not published stores, so publisher allowlisting does not
	// apply to them.
	Local bool
}

// DegradedEntry records one named REQ-12 source failure without stopping review.
type DegradedEntry struct {
	Source     string
	StatusCode int
	Err        error
}

// StoreResolution is the winning store and all failed source attempts.
type StoreResolution struct {
	Path       string
	SourceName string
	BuiltAt    string
	Version    string
	Degraded   []DegradedEntry
	// Cleanup releases the winning source-owned local file, if any.
	Cleanup func() error
}

// FileReader reads downloaded store bytes for pre-SQLite digest verification.
type FileReader interface {
	ReadFile(string) ([]byte, error)
}

// StoreOpener performs SQLite's first file-reading validation after digest verification.
type StoreOpener interface {
	OpenAndValidate(context.Context, string) error
}

// ResolverConfig configures REQ-12 source limits, publisher admission, and I/O seams.
type ResolverConfig struct {
	SourceTimeout     time.Duration
	TotalTimeout      time.Duration
	MaxBytes          int64
	AllowedPublishers []string
	FileReader        FileReader
	StoreOpener       StoreOpener
}

const (
	defaultSourceTimeout = 15 * time.Second
	defaultTotalTimeout  = 45 * time.Second
	defaultMaxBytes      = 128 << 20 // 128 MiB
)

var sourceRegistry = struct {
	sync.RWMutex
	sources map[string]Source
}{sources: make(map[string]Source)}

var storeOpenerRegistry = struct {
	sync.RWMutex
	opener StoreOpener
}{}

type osFileReader struct{}

func (osFileReader) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// DefaultResolverConfig returns the REQ-12 default timeout, deadline, and byte-cap values.
func DefaultResolverConfig() ResolverConfig {
	storeOpenerRegistry.RLock()
	opener := storeOpenerRegistry.opener
	storeOpenerRegistry.RUnlock()
	return ResolverConfig{
		SourceTimeout: defaultSourceTimeout,
		TotalTimeout:  defaultTotalTimeout,
		MaxBytes:      defaultMaxBytes,
		FileReader:    osFileReader{},
		StoreOpener:   opener,
	}
}

// RegisterStoreOpener installs the store validator used by DefaultResolverConfig.
func RegisterStoreOpener(opener StoreOpener) {
	storeOpenerRegistry.Lock()
	defer storeOpenerRegistry.Unlock()
	storeOpenerRegistry.opener = opener
}

// RegisterSource adds a named REQ-12 source to the source registry.
func RegisterSource(name string, source Source) {
	sourceRegistry.Lock()
	defer sourceRegistry.Unlock()
	sourceRegistry.sources[name] = source
}

// ResolveStore resolves MRI_RAG_SOURCE under REQ-12 and returns the first valid store.
func ResolveStore(ctx context.Context, config ResolverConfig) (StoreResolution, error) {
	config = normaliseResolverConfig(config)
	chain, err := resolverChain()
	if err != nil {
		return StoreResolution{}, err
	}
	sources, err := registeredSources(chain)
	if err != nil {
		return StoreResolution{}, err
	}

	totalCtx, cancel := context.WithTimeout(ctx, config.TotalTimeout)
	defer cancel()
	resolution := StoreResolution{}
	request := SourceRequest{
		Path:           os.Getenv("MRI_RAG_STORE"),
		PackageVersion: os.Getenv("MRI_RAG_PACKAGE_VERSION"),
		MaxBytes:       config.MaxBytes,
	}
	pathConfigured := request.Path != ""
	for _, named := range sources {
		if named.name == "path" && !pathConfigured {
			continue
		}
		if err := totalCtx.Err(); err != nil {
			return resolution, fmt.Errorf("resolve store: total timeout: %w", err)
		}
		candidate, err := resolveCandidate(totalCtx, named.source, request, config.SourceTimeout)
		if named.name == "path" || named.name == "baked" {
			// Source names, rather than the candidate's publisher field, establish
			// local provenance. This also keeps test fixtures registered under the
			// REQ-12 local names on the same path as production sources.
			candidate.Local = true
			candidate.Cleanup = nil
		}
		if err == nil {
			err = validateCandidate(totalCtx, candidate, config)
		}
		if err != nil {
			err = cleanupCandidate(candidate, err)
			resolution.Degraded = append(resolution.Degraded, DegradedEntry{
				Source: named.name, StatusCode: statusCode(err), Err: err,
			})
			continue
		}
		resolution.Path = candidate.Path
		resolution.SourceName = named.name
		resolution.BuiltAt = candidate.BuiltAt
		resolution.Version = candidate.Version
		resolution.Cleanup = candidate.Cleanup
		return resolution, nil
	}
	return resolution, fmt.Errorf("resolve store: no valid source (%d degraded)", len(resolution.Degraded))
}

type namedSource struct {
	name   string
	source Source
}

func normaliseResolverConfig(config ResolverConfig) ResolverConfig {
	defaults := DefaultResolverConfig()
	if config.SourceTimeout <= 0 {
		config.SourceTimeout = defaults.SourceTimeout
	}
	if config.TotalTimeout <= 0 {
		config.TotalTimeout = defaults.TotalTimeout
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = defaults.MaxBytes
	}
	if config.FileReader == nil {
		config.FileReader = defaults.FileReader
	}
	if config.StoreOpener == nil {
		config.StoreOpener = defaults.StoreOpener
	}
	return config
}

func resolverChain() ([]string, error) {
	raw, configured := os.LookupEnv("MRI_RAG_SOURCE")
	if !configured {
		raw = "package,artifact,baked"
	}
	parts := strings.Split(raw, ",")
	chain := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, errors.New("resolve store: empty source name")
		}
		chain = append(chain, name)
	}
	return chain, nil
}

func registeredSources(chain []string) ([]namedSource, error) {
	sourceRegistry.RLock()
	defer sourceRegistry.RUnlock()
	missing := make([]string, 0)
	for _, name := range chain {
		if sourceRegistry.sources[name] == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		names := make([]string, 0, len(sourceRegistry.sources))
		for name := range sourceRegistry.sources {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("resolve store: unknown source %q (registered: %s)", missing[0], strings.Join(names, ", "))
	}
	sources := make([]namedSource, 0, len(chain))
	for _, name := range chain {
		sources = append(sources, namedSource{name: name, source: sourceRegistry.sources[name]})
	}
	return sources, nil
}

func resolveCandidate(ctx context.Context, source Source, request SourceRequest, timeout time.Duration) (StoreCandidate, error) {
	sourceCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	candidate, err := source.Resolve(sourceCtx, request)
	if err != nil {
		return StoreCandidate{}, cleanupCandidate(candidate, err)
	}
	if err := sourceCtx.Err(); err != nil {
		return StoreCandidate{}, cleanupCandidate(candidate, err)
	}
	return candidate, nil
}

func cleanupCandidate(candidate StoreCandidate, cause error) error {
	if candidate.Cleanup == nil {
		return cause
	}
	if err := candidate.Cleanup(); err != nil {
		return errors.Join(cause, fmt.Errorf("cleanup candidate: %w", err))
	}
	return cause
}

func validateCandidate(ctx context.Context, candidate StoreCandidate, config ResolverConfig) error {
	info, err := os.Stat(candidate.Path)
	if err != nil {
		return fmt.Errorf("stat candidate: %w", err)
	}
	if info.Size() > config.MaxBytes {
		return fmt.Errorf("candidate byte size %d exceeds byte cap %d", info.Size(), config.MaxBytes)
	}
	bytes, err := config.FileReader.ReadFile(candidate.Path)
	if err != nil {
		return fmt.Errorf("read candidate for digest: %w", err)
	}
	if int64(len(bytes)) > config.MaxBytes {
		return fmt.Errorf("candidate byte size %d exceeds byte cap %d", len(bytes), config.MaxBytes)
	}
	sum := sha256.Sum256(bytes)
	if !strings.EqualFold(fmt.Sprintf("%x", sum), candidate.SHA256) {
		return errors.New("candidate digest verification failed")
	}
	if !candidate.Local && !publisherAllowed(candidate.PublisherProjectID, config.AllowedPublishers) {
		return fmt.Errorf("publisher project %q is not allowed", candidate.PublisherProjectID)
	}
	if err := config.StoreOpener.OpenAndValidate(ctx, candidate.Path); err != nil {
		return fmt.Errorf("validate SQLite store: %w", err)
	}
	return nil
}

func publisherAllowed(publisher string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, projectID := range allowed {
		if publisher == projectID {
			return true
		}
	}
	return false
}

func statusCode(err error) int {
	var status interface{ StatusCode() int }
	if errors.As(err, &status) {
		return status.StatusCode()
	}
	return 0
}
