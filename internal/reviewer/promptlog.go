package reviewer

import (
	"strings"

	"mrinspect/internal/diffbudget"
	"mrinspect/internal/lane"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag/chunk"
)

// diffReductionMarker is diffbudget.Trailer's stable disclosure marker; it
// lets the breakdown logging split the trailer back out of a diff string
// without needing the original dropped-file list threaded through.
const diffReductionMarker = "<!-- mrinspect:diff-reduction -->"

// splitDiffTrailer separates a diffbudget.Trailer disclosure block (if any)
// from the diff text it was appended to, so the breakdown table can report
// them as distinct sections instead of double-counting the trailer as diff
// content.
func splitDiffTrailer(codeDiff string) (diffText, trailerText string) {
	if idx := strings.LastIndex(codeDiff, "\n\n"+diffReductionMarker); idx >= 0 {
		return codeDiff[:idx], codeDiff[idx:]
	}
	return codeDiff, ""
}

func (r *MRInspectReviewer) mergedModelLimits() (map[string]int, error) {
	limits, err := prompt.ModelLimitsFromEnv()
	if err != nil {
		limits = prompt.DefaultModelLimits
	}
	merged := make(map[string]int, len(limits)+len(r.multi.ModelLimits))
	for model, tokens := range limits {
		merged[model] = tokens
	}
	for model, tokens := range r.multi.ModelLimits {
		merged[model] = tokens
	}
	return merged, err
}

// budgetForModel centralizes the model-limit lookup. The reduction path warns
// on invalid configuration or an unknown model; breakdown logging stays silent
// because reduceDiff has already emitted those warnings for the same run.
func (r *MRInspectReviewer) budgetForModel(warn bool) (int, bool) {
	limits, limitsErr := r.mergedModelLimits()
	if limitsErr != nil && warn {
		r.log.Warn("invalid model limits configuration; using defaults", "error", limitsErr.Error())
	}

	model := r.cfg.Providers[r.cfg.AIProvider].Model
	budget, err := prompt.PromptBudgetForModel(model, limits)
	if err != nil {
		if warn {
			r.log.Warn("diff budget unavailable; skipping diff-size reduction", "model", model, "error", err.Error())
		}
		return 0, false
	}
	return budget, true
}

// logPromptBreakdown emits the always-on prompt-composition breakdown table
// as one multi-line Info log (incident-proven observability: a per-section
// token share, e.g. "diff = 93.6%", was what located a prior failure's root
// cause). It is deliberately independent of the forensic dump gate.
func (r *MRInspectReviewer) logPromptBreakdown(label string, sections []Section, laneID string, withBudget bool) {
	table := BuildPromptBreakdown(sections)
	total := 0
	for _, section := range sections {
		total += section.TokenEst
	}
	args := []any{"est", total}
	if withBudget {
		if budget, ok := r.budgetForModel(false); ok {
			args = append(args, "budget", budget)
		}
	}
	if laneID != "" {
		args = append(args, "lane", laneID)
	}
	r.log.Info(label+"\n"+table, args...)
}

// logSinglePromptBreakdown logs the single-mode breakdown after the review
// prompt is composed: the base prompt (metadata+instructions), the reduced
// diff, and the diffbudget disclosure trailer when one is present.
func (r *MRInspectReviewer) logSinglePromptBreakdown(reviewPrompt, codeDiff string) {
	diffText, trailerText := splitDiffTrailer(codeDiff)
	totalTokens := chunk.TokenEst(reviewPrompt)
	diffTokens := chunk.TokenEst(diffText)
	trailerTokens := chunk.TokenEst(trailerText)
	baseTokens := totalTokens - diffTokens - trailerTokens
	if baseTokens < 0 {
		baseTokens = 0
	}

	sections := []Section{
		{Name: "base prompt (metadata+instructions)", TokenEst: baseTokens},
		{Name: "diff", TokenEst: diffTokens},
	}
	if trailerText != "" {
		sections = append(sections, Section{Name: "diffbudget trailer", TokenEst: trailerTokens})
	}
	r.logPromptBreakdown("Prompt composition breakdown", sections, "", true)
}

// logMultiLanePromptBreakdowns logs one breakdown table per lane that
// completed composition (a lane present only as a LaneFailure never
// composed a prompt, so it has nothing to log).
func (r *MRInspectReviewer) logMultiLanePromptBreakdowns(results []lane.LaneResult) {
	for _, result := range results {
		if len(result.Breakdown) == 0 {
			continue
		}
		sections := make([]Section, len(result.Breakdown))
		for i, section := range result.Breakdown {
			sections[i] = Section{Name: section.Name, TokenEst: section.TokenEst}
		}
		r.logPromptBreakdown("Prompt composition breakdown", sections, result.LaneID, false)
	}
}

// logSelfReflectPromptBreakdown logs a small breakdown before the
// self-reflection AI call: the original review and the surrounding
// reflection instructions.
func (r *MRInspectReviewer) logSelfReflectPromptBreakdown(review, reflectPrompt string) {
	reviewTokens := chunk.TokenEst(review)
	instructionTokens := chunk.TokenEst(reflectPrompt) - reviewTokens
	if instructionTokens < 0 {
		instructionTokens = 0
	}
	sections := []Section{
		{Name: "original review", TokenEst: reviewTokens},
		{Name: "reflection instructions", TokenEst: instructionTokens},
	}
	r.logPromptBreakdown("Self-reflection prompt breakdown", sections, "", false)
}

func dropPaths(dropped []diffbudget.DroppedFile) []string {
	paths := make([]string, 0, len(dropped))
	for _, d := range dropped {
		paths = append(paths, d.Path)
	}
	return paths
}
