package reviewer

import (
	"context"
	"fmt"
	"strings"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane"
	"mrinspect/internal/lane/hunk"
	"mrinspect/internal/rag"
)

func (r *MRInspectReviewer) generateMultiReview(ctx context.Context, codeDiff string, changes []gitlab.Change, mr gitlab.MergeRequest) (string, footerAggregation, error) {
	loadedProject, err := r.loadServiceProject()
	if err != nil {
		return "", footerAggregation{}, fmt.Errorf("multi-lane project load failed: %w", err)
	}

	registry, err := lane.Load(r.multi.RepoRoot, loadedProject.SystemDirectory)
	if err != nil || len(registry.Lanes) == 0 {
		reason := "lanes configuration missing"
		if err != nil {
			reason = fmt.Sprintf("lanes configuration could not be loaded: %v", err)
		}
		// A/S-64 forbids silently replacing a failed prompt composition with a
		// legacy template. This is a separate, named configuration-level fallback.
		return r.generateSingleDegradation(ctx, codeDiff, mr, reason)
	}
	if !hasEnabledLane(registry.Lanes) {
		return r.generateSingleDegradation(ctx, codeDiff, mr, "no runnable lane; degraded to single review mode")
	}

	r.retrieveReviewRAG(ctx, codeDiff)

	input := lane.FanoutInput{
		Lanes:                   registry.Lanes,
		Terms:                   lane.Terms(changes),
		ResourceRegistry:        r.multi.ResourceRegistry,
		Retriever:               r.multi.Retriever,
		FullLoader:              r.multi.FullLoader,
		Project:                 loadedProject,
		Diff:                    codeDiff,
		MergeRequest:            mr,
		Provider:                r.ai,
		Attempts:                r.cfg.Validation.AIRetryAttempts,
		GlobalModel:             r.cfg.Providers[r.cfg.AIProvider].Model,
		ModelLimits:             r.multi.ModelLimits,
		NormativeEvictionPolicy: r.cfg.RAGOnNormativeEviction,
		Concurrency:             r.cfg.LaneConcurrency,
		ConcurrencySet:          r.cfg.LaneConcurrencySet,
		Logger:                  r.log,
	}
	fanout := r.multi.Fanout
	if fanout == nil {
		fanout = lane.Fanout
	}
	result, err := fanout(ctx, input)
	if err != nil {
		return "", footerAggregation{}, fmt.Errorf("multi-lane fan-out failed: %w", err)
	}
	r.logMultiLanePromptBreakdowns(result.LaneResults)

	renderInput, selectorDegraded := r.multiRenderInputWithDegradations(registry.Lanes, result, changes)
	footer := aggregateLaneFooter(result.LaneResults)
	footer = mergeLaneDegradations(footer, result.LaneResults, selectorDegraded)
	if r.cfg.RAGOnNormativeEviction == "fail" {
		if failure, ok := normativeEvictionFailure(result.Failures); ok {
			renderInput.Findings = nil
			renderInput.FailedLanes = []lane.LaneFailure{failure}
			return lane.Render(renderInput), footer, nil
		}
	}

	return lane.Render(renderInput), footer, nil
}

func (r *MRInspectReviewer) generateSingleDegradation(ctx context.Context, codeDiff string, mr gitlab.MergeRequest, reason string) (string, footerAggregation, error) {
	content, err := r.generateReview(ctx, codeDiff, mr)
	if err != nil {
		return "", footerAggregation{}, err
	}
	if r.cfg.SelfReflection {
		content = r.selfReflect(ctx, content)
	}
	return content + "\n\n> MRInspect degradation: " + reason, footerAggregation{degradedToSingle: true}, nil
}

func hasEnabledLane(lanes []lane.Lane) bool {
	for _, declaration := range lanes {
		if declaration.Enabled {
			return true
		}
	}
	return false
}

func normativeEvictionFailure(failures []lane.LaneFailure) (lane.LaneFailure, bool) {
	for _, failure := range failures {
		if strings.Contains(failure.Reason, "normative section evicted") {
			return failure, true
		}
	}
	return lane.LaneFailure{}, false
}

func (r *MRInspectReviewer) multiRenderInputWithDegradations(declarations []lane.Lane, result lane.FanoutResult, changes []gitlab.Change) (lane.RenderInput, []namedLaneDegradation) {
	laneOrder := make([]string, 0, len(declarations))
	renderLanes := make([]lane.RenderLane, 0, len(declarations))
	var selectorDegraded []namedLaneDegradation
	for _, declaration := range declarations {
		laneOrder = append(laneOrder, declaration.ID)
		sets, unknown := r.multi.ResourceRegistry.Resolve(declaration.Resources.Sets, declaration.Resources.Tags)
		setNames := make([]string, 0, len(sets))
		for _, set := range sets {
			setNames = append(setNames, set.Name)
		}
		for _, selector := range unknown {
			selectorDegraded = append(selectorDegraded, namedLaneDegradation{
				laneID:  declaration.ID,
				message: fmt.Sprintf("unknown resource selector: %s", selector),
			})
		}
		renderLanes = append(renderLanes, lane.RenderLane{Declaration: declaration, ResolvedResourceSets: setNames})
	}

	receivedChunks := make(map[string][]rag.Chunk, len(result.LaneResults))
	for _, laneResult := range result.LaneResults {
		receivedChunks[laneResult.LaneID] = laneResult.Chunks
	}

	return lane.RenderInput{
		Findings:       lane.Merge(laneOrder, result.LaneResults),
		Lanes:          renderLanes,
		FailedLanes:    result.Failures,
		ReceivedChunks: receivedChunks,
		Changes:        changes,
		ChangedLines:   hunk.Build(changes),
	}, selectorDegraded
}
