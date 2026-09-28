// Package testfake provides shared, concurrency-safe test doubles.
package testfake

import (
	"context"
	"sync"
	"time"

	"mrinspect/internal/ai"
)

// ProviderResponse is one programmed result from FakeProvider.Generate.
type ProviderResponse struct {
	Output string
	Err    error
	Delay  time.Duration
}

// ProviderCall records the arguments of one FakeProvider.Generate call.
type ProviderCall struct {
	Context context.Context
	Prompt  string
	Options ai.GenerateOptions
}

// ProviderBarrier coordinates concurrent Generate calls. Each call sends one
// arrival before waiting for Release to close or receive a value. Tests should
// provide both channels, wait for all expected arrivals, then release the calls.
// A sequential implementation blocks on its first call and therefore times out.
type ProviderBarrier struct {
	Arrived chan<- struct{}
	Release <-chan struct{}
}

// FakeProvider is a programmable, concurrency-safe ai.Provider test double.
// Configure exported fields before use; use EnqueueResponses when adding
// responses while calls may be in flight.
type FakeProvider struct {
	ProviderName        string
	Responses           []ProviderResponse
	DefaultResponse     ProviderResponse
	TurnResponses       []TurnResponse
	DefaultTurnResponse TurnResponse
	Barrier             *ProviderBarrier

	mu                sync.Mutex
	responseIndex     int
	generateCalls     []ProviderCall
	nameCalls         int
	turnResponseIndex int
	turnCalls         []TurnCall
}

// Generate records its arguments and returns the next programmed response.
func (f *FakeProvider) Generate(ctx context.Context, prompt string, opts ai.GenerateOptions) (string, error) {
	f.mu.Lock()
	f.generateCalls = append(f.generateCalls, ProviderCall{
		Context: ctx,
		Prompt:  prompt,
		Options: opts,
	})
	response := f.DefaultResponse
	if f.responseIndex < len(f.Responses) {
		response = f.Responses[f.responseIndex]
		f.responseIndex++
	}
	barrier := f.Barrier
	f.mu.Unlock()

	if barrier != nil {
		select {
		case barrier.Arrived <- struct{}{}:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		select {
		case <-barrier.Release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err := waitContext(ctx, response.Delay); err != nil {
		return "", err
	}
	return response.Output, response.Err
}

// Name records the call and returns ProviderName, or "fake" when unset.
func (f *FakeProvider) Name() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nameCalls++
	if f.ProviderName == "" {
		return "fake"
	}
	return f.ProviderName
}

// EnqueueResponses appends responses to the Generate response queue.
func (f *FakeProvider) EnqueueResponses(responses ...ProviderResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Responses = append(f.Responses, responses...)
}

// GenerateCallCount returns the number of Generate calls.
func (f *FakeProvider) GenerateCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.generateCalls)
}

// GenerateCalls returns a snapshot of recorded Generate calls.
func (f *FakeProvider) GenerateCalls() []ProviderCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ProviderCall(nil), f.generateCalls...)
}

// NameCallCount returns the number of Name calls.
func (f *FakeProvider) NameCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nameCalls
}

// TurnResponse is one programmed result from FakeProvider.GenerateTurn.
type TurnResponse struct {
	Text      string
	ToolCalls []ai.ToolCall
	Err       error
}

// TurnCall records the arguments of one FakeProvider.GenerateTurn call.
type TurnCall struct {
	Context context.Context
	Request ai.TurnRequest
}

// GenerateTurn records its arguments and returns the next programmed
// TurnResponse (or DefaultTurnResponse). Full programmable behavior
// (ResponsesByPromptContains) lands with the task that needs it.
func (f *FakeProvider) GenerateTurn(ctx context.Context, req ai.TurnRequest) (ai.TurnResult, error) {
	f.mu.Lock()
	f.turnCalls = append(f.turnCalls, TurnCall{Context: ctx, Request: req})
	response := f.DefaultTurnResponse
	if f.turnResponseIndex < len(f.TurnResponses) {
		response = f.TurnResponses[f.turnResponseIndex]
		f.turnResponseIndex++
	}
	f.mu.Unlock()

	if response.Err != nil {
		return ai.TurnResult{}, response.Err
	}
	return ai.TurnResult{
		Text:         response.Text,
		ToolCalls:    response.ToolCalls,
		Continuation: ai.NewLocalContinuation(nil),
	}, nil
}

// GenerateTurnCalls returns a snapshot of recorded GenerateTurn calls.
func (f *FakeProvider) GenerateTurnCalls() []TurnCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TurnCall(nil), f.turnCalls...)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ ai.Provider = (*FakeProvider)(nil)
var _ ai.TurnProvider = (*FakeProvider)(nil)
