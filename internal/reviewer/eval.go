package reviewer

import (
	"context"
	"fmt"

	"mrinspect/internal/gitlab"
)

// EvalMode identifies one explicit offline evaluation review path.
type EvalMode string

const (
	EvalModeSingle  EvalMode = "single"
	EvalModeMulti   EvalMode = "multi"
	EvalModeReflect EvalMode = "reflect"
)

// EvalInput supplies fixture-owned MR data without fetching it from GitLab.
type EvalInput struct {
	Diff        string
	Changes     []gitlab.Change
	Title       string
	Description string
}

// EvalOutcome returns generated review data without posting it to GitLab.
type EvalOutcome struct {
	ReviewText     string
	ReflectApplied bool
	Degraded       bool
	Mode           EvalMode
}

// RunForEval generates an offline review from caller-owned MR data. It bypasses
// production validation, fetching, and posting by entering directly at the
// shared generation seam.
func (r *MRInspectReviewer) RunForEval(ctx context.Context, mode EvalMode, input EvalInput) (EvalOutcome, error) {
	mr := gitlab.MergeRequest{
		Title:       input.Title,
		Description: input.Description,
	}
	content, footer, status, err := r.generateReviewForExplicitModeWithStatus(ctx, mode, input.Diff, input.Changes, mr)
	if err == nil && mode != EvalModeMulti && (len(r.rag.State.Degraded) > 0 || len(r.rag.State.Composition.Degraded) > 0) {
		content += r.ragFooterWithAggregation(footer)
	}
	outcome := EvalOutcome{
		ReviewText:     content,
		ReflectApplied: status.reflectApplied,
		Degraded:       footer.degradedToSingle,
		Mode:           mode,
	}
	if err != nil {
		return outcome, err
	}
	if mode == EvalModeMulti && status.multiFanout != nil && len(status.multiFanout.LaneResults) == 0 {
		failed := len(status.multiFanout.Failures)
		if failed == 0 {
			return outcome, fmt.Errorf("multi review failed: 0 lanes succeeded and 0 lane failures were recorded")
		}
		return outcome, fmt.Errorf(
			"multi review failed: %d lanes failed; first lane error: %s",
			failed,
			status.multiFanout.Failures[0].Reason,
		)
	}
	return outcome, nil
}
