package testfake

import (
	"context"
	"sync"
	"time"

	"mrinspect/internal/rag"
)

// FullLoaderResponse is one programmed result from FakeFullLoader.LoadFull.
type FullLoaderResponse struct {
	Result rag.FullResult
	Err    error
	Delay  time.Duration
}

// FullLoaderCall records the arguments of one FakeFullLoader.LoadFull call.
type FullLoaderCall struct {
	Context context.Context
	SetRefs []string
}

// FakeFullLoader is a programmable, concurrency-safe rag.FullLoader test double.
// Configure exported fields before use; use EnqueueResponses when adding
// responses while calls may be in flight.
type FakeFullLoader struct {
	Responses       []FullLoaderResponse
	DefaultResponse FullLoaderResponse

	mu            sync.Mutex
	responseIndex int
	loadFullCalls []FullLoaderCall
}

// LoadFull records its arguments and returns the next programmed response.
func (f *FakeFullLoader) LoadFull(ctx context.Context, setRefs []string) (rag.FullResult, error) {
	recordedRefs := append([]string(nil), setRefs...)

	f.mu.Lock()
	f.loadFullCalls = append(f.loadFullCalls, FullLoaderCall{Context: ctx, SetRefs: recordedRefs})
	response := f.DefaultResponse
	if f.responseIndex < len(f.Responses) {
		response = f.Responses[f.responseIndex]
		f.responseIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return rag.FullResult{}, err
	}
	return cloneFullResult(response.Result), response.Err
}

// EnqueueResponses appends responses to the LoadFull response queue.
func (f *FakeFullLoader) EnqueueResponses(responses ...FullLoaderResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Responses = append(f.Responses, responses...)
}

// LoadFullCallCount returns the number of LoadFull calls.
func (f *FakeFullLoader) LoadFullCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.loadFullCalls)
}

// LoadFullCalls returns a snapshot of recorded LoadFull calls.
func (f *FakeFullLoader) LoadFullCalls() []FullLoaderCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]FullLoaderCall, len(f.loadFullCalls))
	for i, call := range f.loadFullCalls {
		calls[i] = call
		calls[i].SetRefs = append([]string(nil), call.SetRefs...)
	}
	return calls
}

func cloneFullResult(result rag.FullResult) rag.FullResult {
	cloned := result
	cloned.Docs = append([]rag.FullDoc(nil), result.Docs...)
	for i, doc := range result.Docs {
		cloned.Docs[i].Bytes = append([]byte(nil), doc.Bytes...)
	}
	cloned.Degraded = append([]string(nil), result.Degraded...)
	return cloned
}

var _ rag.FullLoader = (*FakeFullLoader)(nil)
