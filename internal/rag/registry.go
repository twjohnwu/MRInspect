package rag

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"mrinspect/internal/rag/resources"
)

// Factory constructs one named REQ-02 retrieval backend from its store and sets.
type Factory func(storePath string, sets []resources.Set) (Retriever, error)

// registry holds the named REQ-02 backend factories. registryMu makes Register
// safe for concurrent setup; New releases the lock before invoking a factory.
var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a named backend factory to the REQ-02 registry.
func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// New selects the environment-configured REQ-02 backend for resolved resource sets.
func New(storePath string, sets []resources.Set) (Retriever, error) {
	if os.Getenv("MRI_RAG_ENABLED") == "false" || len(sets) == 0 {
		return noopRetriever{}, nil
	}

	name := os.Getenv("MRI_RAG_BACKEND")
	if name == "" {
		name = "sqlite"
	}

	registryMu.RLock()
	factory, ok := registry[name]
	names := registeredNamesLocked()
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown RAG backend %q (registered: %s)", name, strings.Join(names, ", "))
	}
	return factory(storePath, sets)
}

func registeredNamesLocked() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
