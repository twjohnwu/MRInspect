package ragwire

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"mrinspect/internal/rag"
)

func TestResolvingRetriever_MemoizesStoreResolution(t *testing.T) {
	var calls atomic.Int32
	store := newResolvedStore(rag.ResolverConfig{}, func(context.Context, rag.ResolverConfig) (rag.StoreResolution, error) {
		calls.Add(1)
		return rag.StoreResolution{Path: "unused-by-noop-retriever"}, nil
	})
	retriever := &resolvingRetriever{store: store}

	for range 3 {
		if _, err := retriever.Retrieve(context.Background(), rag.Query{}); err != nil {
			t.Fatalf("sequential Retrieve: %v", err)
		}
	}

	var workers sync.WaitGroup
	workers.Add(4)
	for range 4 {
		go func() {
			defer workers.Done()
			if _, err := retriever.Retrieve(context.Background(), rag.Query{}); err != nil {
				t.Errorf("concurrent Retrieve: %v", err)
			}
		}()
	}
	workers.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want exactly 1", got)
	}
}

func TestResolvingRetriever_CleansTempOnClose(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "resolved-store-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	store := newResolvedStore(rag.ResolverConfig{}, func(context.Context, rag.ResolverConfig) (rag.StoreResolution, error) {
		return rag.StoreResolution{
			Path:    path,
			Cleanup: func() error { return os.Remove(path) },
		}, nil
	})
	retriever := &resolvingRetriever{store: store}
	if _, err := retriever.Retrieve(context.Background(), rag.Query{}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if err := retriever.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("resolved temp file still exists after Close; Stat error = %v", err)
	}
}
