package enrich

import (
	"context"
	"errors"

	"mrinspect/internal/ai"
)

// RoundResult is the outcome of running one tool-calling enrichment round
// for a single review/lane attempt (REQ-04).
type RoundResult struct {
	Text     string
	Turns    int
	Degraded []string
}

// RunRound drives the turn 1 -> tool execution -> turn 2 round loop for one
// attempt: it sends the prompt to tp, executes any requested tool calls
// through exec (bounded by exec's per-round and total-budget limits), feeds
// the results back as turn 2, and returns the final review text plus any
// degraded tool outcomes.
func RunRound(ctx context.Context, tp ai.TurnProvider, prompt string, exec *Executor, opts ai.GenerateOptions) (RoundResult, error) {
	turn1, err := tp.GenerateTurn(ctx, ai.TurnRequest{
		Prompt:  prompt,
		Tools:   exec.Specs(),
		Options: opts,
	})
	if err != nil {
		return RoundResult{}, err
	}
	if len(turn1.ToolCalls) == 0 {
		return RoundResult{Text: turn1.Text, Turns: 1}, nil
	}

	results := exec.Execute(ctx, turn1.ToolCalls, exec.MaxCalls())

	turn2, err := tp.GenerateTurn(ctx, ai.TurnRequest{
		Tools:        exec.Specs(),
		Continuation: turn1.Continuation,
		ToolResults:  results,
		Options:      opts,
	})
	if err != nil {
		return RoundResult{}, err
	}

	degraded := dedupeDegraded(results)
	if turn2.Text == "" {
		return RoundResult{Turns: 2, Degraded: degraded}, errors.New("enrichment: empty final text")
	}
	return RoundResult{Text: turn2.Text, Turns: 2, Degraded: degraded}, nil
}

// dedupeDegraded builds the deduped "enrichment <code>" degradation list
// from results' non-empty Error codes, in first-seen order.
func dedupeDegraded(results []ai.ToolResult) []string {
	var degraded []string
	seen := make(map[string]struct{})
	for _, result := range results {
		if result.Error == "" {
			continue
		}
		code := "enrichment " + result.Error
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		degraded = append(degraded, code)
	}
	return degraded
}
