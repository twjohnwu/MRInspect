// Package testfake provides shared, concurrency-safe test doubles.
package testfake

import (
	"context"
	"sort"
	"strings"
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

	// ResponsesByPromptContains routes a lane's turn-1 responses by a
	// substring match against the turn-1 prompt (map key -> that lane's own
	// response queue) — used by multi-lane tests where each lane's prompt
	// carries a distinct marker (e.g. its lane id) and each lane must get
	// its own independent turn 1 -> turn 2 chain. Turn 2 requests carry no
	// prompt (REQ-02: "turn 2 SHALL 忽略" Prompt), so the routing key chosen
	// at turn 1 is remembered by the identity of the Continuation this fake
	// hands back, and looked up again when that same Continuation comes
	// back on turn 2. Unset (nil) leaves the plain TurnResponses queue
	// behavior below unchanged.
	ResponsesByPromptContains map[string][]TurnResponse

	mu                sync.Mutex
	responseIndex     int
	generateCalls     []ProviderCall
	nameCalls         int
	turnResponseIndex int
	turnCalls         []TurnCall
	promptRouteIndex  map[string]int
	continuationRoute map[*ai.Continuation]string
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
// TurnResponse. When ResponsesByPromptContains is set, a turn 1 request
// (Continuation == nil) is routed to the first key whose substring appears
// in the prompt, consuming that key's own queue; a turn 2 request is routed
// back to the same key via the Continuation this fake returned for its
// turn 1. Unrouted requests (no match, or ResponsesByPromptContains unset)
// fall back to the plain TurnResponses queue / DefaultTurnResponse.
func (f *FakeProvider) GenerateTurn(ctx context.Context, req ai.TurnRequest) (ai.TurnResult, error) {
	f.mu.Lock()
	f.turnCalls = append(f.turnCalls, TurnCall{Context: ctx, Request: req})

	routeKey := ""
	if req.Continuation != nil {
		routeKey = f.continuationRoute[req.Continuation]
	} else {
		routeKey = f.matchPromptRouteLocked(req.Prompt)
	}

	response := f.DefaultTurnResponse
	switch {
	case routeKey != "":
		queue := f.ResponsesByPromptContains[routeKey]
		if f.promptRouteIndex == nil {
			f.promptRouteIndex = make(map[string]int)
		}
		idx := f.promptRouteIndex[routeKey]
		if idx < len(queue) {
			response = queue[idx]
			f.promptRouteIndex[routeKey] = idx + 1
		}
	case f.turnResponseIndex < len(f.TurnResponses):
		response = f.TurnResponses[f.turnResponseIndex]
		f.turnResponseIndex++
	}
	f.mu.Unlock()

	if response.Err != nil {
		return ai.TurnResult{}, response.Err
	}

	continuation := ai.NewLocalContinuation(routeKey)
	if routeKey != "" {
		f.mu.Lock()
		if f.continuationRoute == nil {
			f.continuationRoute = make(map[*ai.Continuation]string)
		}
		f.continuationRoute[continuation] = routeKey
		f.mu.Unlock()
	}

	return ai.TurnResult{
		Text:         response.Text,
		ToolCalls:    response.ToolCalls,
		Continuation: continuation,
	}, nil
}

// matchPromptRouteLocked returns the first ResponsesByPromptContains key
// (in sorted order, for determinism) whose substring appears in prompt, or
// "" if none matches or no routes are configured. Callers must hold f.mu.
func (f *FakeProvider) matchPromptRouteLocked(prompt string) string {
	if len(f.ResponsesByPromptContains) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f.ResponsesByPromptContains))
	for key := range f.ResponsesByPromptContains {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.Contains(prompt, key) {
			return key
		}
	}
	return ""
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
