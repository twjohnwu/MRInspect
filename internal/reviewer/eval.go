package reviewer

import (
	"context"

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
	content, footer, reflectApplied, err := r.generateReviewForExplicitModeWithStatus(ctx, mode, input.Diff, input.Changes, mr)
	outcome := EvalOutcome{
		ReviewText:     content,
		ReflectApplied: reflectApplied,
		Degraded:       footer.degradedToSingle,
		Mode:           mode,
	}
	if err != nil {
		return outcome, err
	}
	return outcome, nil
}
