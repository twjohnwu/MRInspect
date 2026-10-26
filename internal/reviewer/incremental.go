package reviewer

import (
	"context"

	"mrinspect/internal/gitlab"
)

// incrementalRun carries the pinned head/base, the prior state and the
// decision for one run. dirty collects the reasons a run must not record
// a state line (REQ-06).
type incrementalRun struct {
	head, base string
	prior      *State
	decision   Decision
	dirty      []string
}

func (ir *incrementalRun) markDirty(r *MRInspectReviewer, reason string) {
	ir.dirty = append(ir.dirty, reason)
	r.log.Warn("incremental: run is dirty", "reason", reason)
}

// planIncremental pins head/base from the already-fetched MR and decides
// skip / update / full. Any lookup failure degrades to a full review.
func (r *MRInspectReviewer) planIncremental(ctx context.Context, mr gitlab.MergeRequest, projectID string) *incrementalRun {
	run := &incrementalRun{head: mr.SHA, base: mr.DiffRefs.BaseSHA, decision: Decision{Mode: ModeFull}}
	if run.head == "" || run.base == "" {
		run.markDirty(r, "head or base sha missing from merge request")
		return run
	}
	user, err := r.gitlab.CurrentUser(ctx)
	if err != nil {
		r.log.Warn("incremental: cannot resolve bot user, full review", "error", err)
		return run
	}
	notes, err := r.gitlab.ListNotes(ctx, projectID, r.mrIID)
	if err != nil {
		r.log.Warn("incremental: cannot list notes, full review", "error", err)
		return run
	}
	if st, ok := SelectPrior(notes, user.ID); ok {
		run.prior = &st
	}
	isAncestor := func() bool {
		id, err := r.gitlab.GetMergeBase(ctx, projectID, run.prior.SHA, run.head)
		if err != nil {
			r.log.Warn("incremental: merge base lookup failed, full review", "error", err)
			return false
		}
		return id == run.prior.SHA
	}
	run.decision = Decide(run.prior, run.head, run.base, r.cfg.Incremental.FullEvery, isAncestor)
	return run
}
