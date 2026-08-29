package ragwire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/intake"
	"mrinspect/internal/rag/resources"
)

type resolveStoreFunc func(context.Context, rag.ResolverConfig) (rag.StoreResolution, error)

// resolvedStore memoizes one production resolution and owns its cleanup.
type resolvedStore struct {
	config       rag.ResolverConfig
	resolveStore resolveStoreFunc
	once         sync.Once
	resolution   rag.StoreResolution
	err          error
	mu           sync.Mutex
	closed       bool
	closeOnce    sync.Once
	closeErr     error
}

func newResolvedStore(config rag.ResolverConfig, resolver resolveStoreFunc) *resolvedStore {
	if resolver == nil {
		resolver = rag.ResolveStore
	}
	return &resolvedStore{config: config, resolveStore: resolver}
}

func (s *resolvedStore) resolve(ctx context.Context) (rag.StoreResolution, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return rag.StoreResolution{}, errors.New("resolve store: retriever is closed")
	}
	s.once.Do(func() {
		s.resolution, s.err = s.resolveStore(ctx, s.config)
	})
	return s.resolution, s.err
}

func (s *resolvedStore) close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		// Do not start an unused download merely because the retriever closes.
		s.once.Do(func() {
			s.err = errors.New("resolve store: retriever is closed")
		})
		if s.resolution.Cleanup != nil {
			s.closeErr = s.resolution.Cleanup()
		}
	})
	return s.closeErr
}

// resolvingRetriever gives each concurrent lane an independently opened store
// handle while sharing one resolved store with ReviewPath.
type resolvingRetriever struct {
	config ReviewPathConfig
	store  *resolvedStore
}

func (*resolvingRetriever) Name() string { return "production" }

func (r *resolvingRetriever) Retrieve(ctx context.Context, query rag.Query) (rag.Result, error) {
	resolution, err := r.store.resolve(ctx)
	if err != nil {
		degraded := degradedEntries(resolution.Degraded)
		degraded = append(degraded, fmt.Sprintf("store unavailable: %v", err))
		return rag.Result{Degraded: degraded}, nil
	}

	retriever, err := rag.New(resolution.Path, r.config.ResourceSets)
	if err != nil {
		return rag.Result{}, fmt.Errorf("create RAG retriever: %w", err)
	}
	defer retriever.Close()

	result, err := retriever.Retrieve(ctx, query)
	if err != nil {
		return rag.Result{}, err
	}
	result.Degraded = append(degradedEntries(resolution.Degraded), result.Degraded...)
	return result, nil
}

func (r *resolvingRetriever) Close() error { return r.store.close() }

// resourceFullLoader loads normative sets directly from their declared files;
// full-mode material deliberately bypasses the retrieval store.
type resourceFullLoader struct{ sets []resources.Set }

func (l resourceFullLoader) LoadFull(ctx context.Context, setRefs []string) (rag.FullResult, error) {
	setsByName := make(map[string]resources.Set, len(l.sets))
	for _, set := range l.sets {
		setsByName[set.Name] = set
	}

	var result rag.FullResult
	for _, setRef := range setRefs {
		set, ok := setsByName[setRef]
		if !ok {
			return rag.FullResult{}, fmt.Errorf("LoadFull: unknown resource set %q", setRef)
		}
		if set.Mode != resources.ModeFull {
			return rag.FullResult{}, fmt.Errorf("LoadFull: resource set %q uses mode %q", setRef, set.Mode)
		}
		files, degraded, err := fullSetFiles(set)
		if err != nil {
			return rag.FullResult{}, err
		}
		result.Degraded = append(result.Degraded, degraded...)
		for _, path := range files {
			if err := ctx.Err(); err != nil {
				return rag.FullResult{}, err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				result.Degraded = append(result.Degraded, fmt.Sprintf("full resource %q unavailable: %v", path, err))
				continue
			}
			result.Docs = append(result.Docs, rag.FullDoc{
				Source: path, ResourceSet: set.Name, Bytes: content, TokenEst: chunk.TokenEst(string(content)),
			})
		}
	}
	return result, nil
}

func fullSetFiles(set resources.Set) ([]string, []string, error) {
	var directFiles []string
	var directories []string
	var degraded []string
	for _, path := range set.Paths {
		info, err := os.Stat(path)
		if err != nil {
			degraded = append(degraded, fmt.Sprintf("full resource path %q unavailable: %v", path, err))
			continue
		}
		if info.Mode().IsRegular() {
			directFiles = append(directFiles, path)
			continue
		}
		if info.IsDir() {
			directories = append(directories, path)
			continue
		}
		degraded = append(degraded, fmt.Sprintf("full resource path %q is not a regular file or directory", path))
	}

	walked, err := intake.Walk(intake.WalkOptions{
		Paths: directories, Include: set.Include, Exclude: set.Exclude,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("LoadFull: walk resource set %q: %w", set.Name, err)
	}
	for _, skipped := range walked.Skipped {
		degraded = append(degraded, fmt.Sprintf("full resource %q skipped: %s", skipped.Path, skipped.Reason))
	}
	return append(directFiles, walked.Files...), degraded, nil
}
