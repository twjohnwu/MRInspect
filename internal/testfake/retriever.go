package testfake

import (
	"context"
	"sync"
	"time"

	"mrinspect/internal/rag"
)

// RetrieverResponse is one programmed result from FakeRetriever.Retrieve.
type RetrieverResponse struct {
	Result rag.Result
	Err    error
	Delay  time.Duration
}

// RetrieverCloseResponse is one programmed result from FakeRetriever.Close.
type RetrieverCloseResponse struct {
	Err   error
	Delay time.Duration
}

// RetrieverCall records the arguments of one FakeRetriever.Retrieve call.
type RetrieverCall struct {
	Context context.Context
	Query   rag.Query
}

// FakeRetriever is a programmable, concurrency-safe rag.Retriever test double.
// Configure exported fields before use; use the enqueue methods when adding
// responses while calls may be in flight.
type FakeRetriever struct {
	RetrieverName        string
	Responses            []RetrieverResponse
	DefaultResponse      RetrieverResponse
	CloseResponses       []RetrieverCloseResponse
	DefaultCloseResponse RetrieverCloseResponse

	mu                 sync.Mutex
	responseIndex      int
	closeResponseIndex int
	retrieveCalls      []RetrieverCall
	nameCalls          int
	closeCalls         int
}

// Name records the call and returns RetrieverName, or "fake" when unset.
func (f *FakeRetriever) Name() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nameCalls++
	if f.RetrieverName == "" {
		return "fake"
	}
	return f.RetrieverName
}

// Retrieve records its arguments and returns the next programmed response.
func (f *FakeRetriever) Retrieve(ctx context.Context, query rag.Query) (rag.Result, error) {
	recordedQuery := query
	recordedQuery.Terms = append([]string(nil), query.Terms...)

	f.mu.Lock()
	f.retrieveCalls = append(f.retrieveCalls, RetrieverCall{Context: ctx, Query: recordedQuery})
	response := f.DefaultResponse
	if f.responseIndex < len(f.Responses) {
		response = f.Responses[f.responseIndex]
		f.responseIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return rag.Result{}, err
	}
	return cloneRAGResult(response.Result), response.Err
}

// Close records the call and returns the next programmed close response.
func (f *FakeRetriever) Close() error {
	f.mu.Lock()
	f.closeCalls++
	response := f.DefaultCloseResponse
	if f.closeResponseIndex < len(f.CloseResponses) {
		response = f.CloseResponses[f.closeResponseIndex]
		f.closeResponseIndex++
	}
	f.mu.Unlock()

	if response.Delay > 0 {
		time.Sleep(response.Delay)
	}
	return response.Err
}

// EnqueueResponses appends responses to the Retrieve response queue.
func (f *FakeRetriever) EnqueueResponses(responses ...RetrieverResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Responses = append(f.Responses, responses...)
}

// EnqueueCloseResponses appends responses to the Close response queue.
func (f *FakeRetriever) EnqueueCloseResponses(responses ...RetrieverCloseResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseResponses = append(f.CloseResponses, responses...)
}

// RetrieveCallCount returns the number of Retrieve calls.
func (f *FakeRetriever) RetrieveCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.retrieveCalls)
}

// RetrieveCalls returns a snapshot of recorded Retrieve calls.
func (f *FakeRetriever) RetrieveCalls() []RetrieverCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]RetrieverCall, len(f.retrieveCalls))
	for i, call := range f.retrieveCalls {
		calls[i] = call
		calls[i].Query.Terms = append([]string(nil), call.Query.Terms...)
	}
	return calls
}

// NameCallCount returns the number of Name calls.
func (f *FakeRetriever) NameCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nameCalls
}

// CloseCallCount returns the number of Close calls.
func (f *FakeRetriever) CloseCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

func cloneRAGResult(result rag.Result) rag.Result {
	cloned := result
	cloned.Chunks = append([]rag.Chunk(nil), result.Chunks...)
	for i, chunk := range result.Chunks {
		if chunk.Score != nil {
			score := *chunk.Score
			cloned.Chunks[i].Score = &score
		}
	}
	cloned.Degraded = append([]string(nil), result.Degraded...)
	return cloned
}

var _ rag.Retriever = (*FakeRetriever)(nil)
