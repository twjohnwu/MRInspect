package enrich

import (
	"context"
	"path/filepath"
	"sort"
	"sync/atomic"

	"mrinspect/internal/ai"
)

// MaxTotalCalls is the hard ceiling on tool calls executed across an entire
// review attempt (all rounds combined), regardless of per-round MaxCalls.
const MaxTotalCalls = 24

// Executor runs tool calls against one repo root, in-process, and tracks
// how many calls have been executed against the review-wide MaxTotalCalls
// budget (or an overridden TotalBudget, see WithTotalBudget).
type Executor struct {
	root        string
	lim         Limits
	registry    Registry
	executed    int64
	totalBudget int64
}

// ExecutorOption configures an Executor at construction time.
type ExecutorOption func(*Executor)

// WithTotalBudget overrides MaxTotalCalls for this Executor — used by tests
// that need a smaller total-call ceiling than the production constant.
func WithTotalBudget(n int64) ExecutorOption {
	return func(e *Executor) { e.totalBudget = n }
}

// New creates an Executor rooted at root (already absolute + EvalSymlinks'd
// by the caller) with the given per-call Limits.
func New(root string, lim Limits, opts ...ExecutorOption) (*Executor, error) {
	e := &Executor{root: root, lim: lim, registry: Default(), totalBudget: MaxTotalCalls}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// NewForRoot resolves root to an absolute, symlink-evaluated path and
// constructs an Executor rooted there — the shared root-resolution sequence
// every caller (review's trigger path and eval) must apply before New, so
// it lives here once instead of being repeated at each call site.
func NewForRoot(root string, lim Limits, opts ...ExecutorOption) (*Executor, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	return New(resolvedRoot, lim, opts...)
}

// Execute runs calls in order, respecting maxCalls (per-round) and the
// executor's total budget, and returns one ToolResult per call.
func (e *Executor) Execute(ctx context.Context, calls []ai.ToolCall, maxCalls int) []ai.ToolResult {
	results := make([]ai.ToolResult, 0, len(calls))
	for index, call := range calls {
		result := ai.ToolResult{ID: call.ID, Name: call.Name}
		if index >= maxCalls {
			result.Error = "call-limit"
			results = append(results, result)
			continue
		}
		if len(call.Args) > ai.MaxArgsBytes {
			result.Content = "tool arguments too large"
			result.Error = "tool-error"
			results = append(results, result)
			continue
		}
		tool, ok := e.registry[call.Name]
		if !ok {
			result.Content = "unknown tool"
			result.Error = "tool-error"
			results = append(results, result)
			continue
		}
		// Only calls that reach a known tool with bounded arguments consume budget.
		if !e.reserveCall() {
			result.Error = "call-limit"
			results = append(results, result)
			continue
		}

		callContext, cancel := context.WithTimeout(ctx, e.lim.ToolTimeout)
		result.Content, result.Error = tool.Run(callContext, e.root, call.Args, e.lim)
		cancel()
		results = append(results, result)
	}
	return results
}

func (e *Executor) reserveCall() bool {
	for {
		current := atomic.LoadInt64(&e.executed)
		if current >= e.totalBudget {
			return false
		}
		if atomic.CompareAndSwapInt64(&e.executed, current, current+1) {
			return true
		}
	}
}

// Executed returns the number of tool calls this Executor has run so far
// against its total budget.
func (e *Executor) Executed() int64 { return atomic.LoadInt64(&e.executed) }

// MaxCalls returns this Executor's configured per-round tool-call limit
// (Limits.MaxCalls), for callers (the round loop) that must bound turn 1's
// tool calls without reaching into the Executor's private Limits.
func (e *Executor) MaxCalls() int { return e.lim.MaxCalls }

// Specs returns the ai.ToolSpec definitions for every tool in this
// Executor's registry, in stable (name-sorted) order, for passing to a
// TurnProvider's tool-definition request.
func (e *Executor) Specs() []ai.ToolSpec {
	names := make([]string, 0, len(e.registry))
	for name := range e.registry {
		names = append(names, name)
	}
	sort.Strings(names)
	specs := make([]ai.ToolSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, e.registry[name].Spec())
	}
	return specs
}
