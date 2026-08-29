package lane

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
	"mrinspect/internal/ai"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/resources"
)

// FanoutInput contains the ordered lanes and shared inputs for one MR fan-out.
type FanoutInput struct {
	Lanes            []Lane
	Terms            []string
	ResourceRegistry resources.Registry
	Retriever        rag.Retriever
	FullLoader       rag.FullLoader
	Project          project.LoadedProject
	Diff             string
	MergeRequest     gitlab.MergeRequest
	Provider         ai.Provider
	Attempts         int
	GlobalModel      string
	ModelLimits      map[string]int
}

// FanoutResult separates successful lane results from isolated lane failures.
type FanoutResult struct {
	LaneResults []LaneResult
	Failures    []LaneFailure
}

type preparedLane struct {
	enabled bool
	model   string
	budget  int
}

// Fanout executes the enabled review lanes.
func Fanout(ctx context.Context, input FanoutInput) (FanoutResult, error) {
	prepared, err := preflightLanes(input)
	if err != nil {
		return FanoutResult{}, err
	}

	diffTokenEst := chunk.TokenEst(input.Diff)
	results := make([]LaneResult, len(input.Lanes))
	var group errgroup.Group
	for index, declaration := range input.Lanes {
		lane := prepared[index]
		if !lane.enabled {
			continue
		}
		if diffTokenEst > lane.budget {
			results[index] = failedLane(
				declaration.ID,
				FailureKindCompose,
				fmt.Sprintf("compose lane prompt: diff token estimate %d exceeds model budget %d", diffTokenEst, lane.budget),
			)
			continue
		}

		group.Go(func() error {
			results[index] = executeLaneWithOptions(
				ctx,
				composeInput(input, declaration, lane.budget),
				input.Provider,
				input.Attempts,
				ai.GenerateOptions{Model: lane.model},
			)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return FanoutResult{}, err
	}

	return collectFanoutResult(input.Lanes, results), nil
}

func preflightLanes(input FanoutInput) ([]preparedLane, error) {
	prepared := make([]preparedLane, len(input.Lanes))
	for index, declaration := range input.Lanes {
		if !declaration.Enabled {
			continue
		}
		model := declaration.Model
		if model == "" {
			model = input.GlobalModel
		}
		budget, err := prompt.PromptBudgetForModel(model, input.ModelLimits)
		if err != nil {
			return nil, err
		}
		prepared[index] = preparedLane{enabled: true, model: model, budget: budget}
	}
	return prepared, nil
}

func composeInput(input FanoutInput, declaration Lane, budget int) ComposeInput {
	return ComposeInput{
		Lane:             declaration,
		Terms:            input.Terms,
		Budget:           budget,
		ResourceRegistry: input.ResourceRegistry,
		Retriever:        input.Retriever,
		FullLoader:       input.FullLoader,
		Project:          input.Project,
		Diff:             input.Diff,
		MergeRequest:     input.MergeRequest,
	}
}

func collectFanoutResult(lanes []Lane, results []LaneResult) FanoutResult {
	var fanout FanoutResult
	for index, declaration := range lanes {
		if !declaration.Enabled {
			continue
		}
		result := results[index]
		if result.Failure != nil {
			fanout.Failures = append(fanout.Failures, *result.Failure)
			continue
		}
		fanout.LaneResults = append(fanout.LaneResults, result)
	}
	return fanout
}
