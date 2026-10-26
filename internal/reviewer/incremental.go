package reviewer

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"mrinspect/internal/diff"
	"mrinspect/internal/diffbudget"
	"mrinspect/internal/gitlab"
)

// maxOtherFiles caps the "other files" section of an update review.
const maxOtherFiles = 200

const otherFilesHeader = "Other files changed in this MR (not shown in this diff):"

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

// isFullSHA reports whether s is a 40-character lowercase hex commit id.
func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// assessRun is the single place that decides, after generation, whether the
// run may record a state line (REQ-06): reviewed SHA of a full run, MR
// changes facts of a full run, lane failures and degradation to single.
// Update-run sources are marked in fetchUpdateDiff.
func (ir *incrementalRun) assessRun(r *MRInspectReviewer, fetched fetchedDiff, footer footerAggregation) {
	if ir.decision.Mode == ModeFull {
		if fetched.sourceSHA == "" || fetched.sourceSHA != ir.head {
			ir.markDirty(r, "reviewed SHA differs from pinned head")
		}
		switch {
		case fetched.changesErr != nil:
			ir.markDirty(r, fmt.Sprintf("MR file list unavailable: %v", fetched.changesErr))
		case fetched.changesOverflow:
			ir.markDirty(r, "MR file list overflow")
		}
		if fetched.changesErr == nil && fetched.changesSHA != ir.head {
			ir.markDirty(r, "MR file list is not at the pinned head")
		}
		if paths := unseenPaths(fetched); len(paths) > 0 {
			ir.markDirty(r, "content not sent to the model: "+strings.Join(paths, ", "))
		}
	}
	if footer.laneFailures > 0 {
		ir.markDirty(r, fmt.Sprintf("%d lane(s) failed", footer.laneFailures))
	}
	if footer.degradedToSingle {
		ir.markDirty(r, "multi-lane review degraded to single")
	}
}

// unseenPaths lists the files of a full run whose content the model did not
// see (REQ-06): size-budget drops, and files GitLab reported too_large,
// collapsed or truncated that have neither a hunk nor a binary-differs line in
// the sent diff. Non-reviewable drops and paths do not count. Paths are cleaned so they cannot inject lines.
func unseenPaths(fetched fetchedDiff) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if p = cleanPath(p); p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, d := range fetched.dropped {
		if d.Reason == diffbudget.ReasonSizeBudget {
			add(d.Path)
		}
	}
	for _, c := range fetched.allChanges {
		if isEmptyNewFile(c) {
			continue
		}
		if (c.TooLarge || c.Collapsed || c.Truncated()) &&
			!diffbudget.IsNonReviewable(c.NewPath) && !diff.HasFileContent(fetched.diff, c.NewPath) {
			add(c.NewPath)
		}
	}
	return paths
}

// planIncremental pins head/base from the already-fetched MR and decides
// skip / update / full. Any lookup failure degrades to a full review.
func (r *MRInspectReviewer) planIncremental(ctx context.Context, mr gitlab.MergeRequest, projectID string) *incrementalRun {
	run := &incrementalRun{head: mr.SHA, base: mr.DiffRefs.BaseSHA, decision: Decision{Mode: ModeFull}}
	if !isFullSHA(run.head) || !isFullSHA(run.base) {
		run.head, run.base = "", ""
		run.markDirty(r, "head or base sha missing or malformed in merge request")
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

// fetchUpdateDiff builds the reviewed diff for an update run from the
// prior..head compare. ok is false when the run is not an update or the
// compare cannot be trusted; the plan then falls back to a full round-0
// review and the caller uses fetchDiff. The disabled path never gets here
// with a non-nil plan.
func (r *MRInspectReviewer) fetchUpdateDiff(ctx context.Context, plan *incrementalRun) (fetchedDiff, bool) {
	if plan == nil || plan.decision.Mode != ModeUpdate {
		return fetchedDiff{}, false
	}
	fallback := func(reason string, args ...any) (fetchedDiff, bool) {
		r.log.Warn("incremental: "+reason+", full review", args...)
		plan.decision = Decision{Mode: ModeFull}
		return fetchedDiff{}, false
	}

	start := time.Now()
	cmp, err := r.gitlab.Compare(ctx, r.projectID, plan.prior.SHA, plan.head)
	if err != nil {
		return fallback("compare failed", "error", err)
	}
	if cmp.CompareTimeout {
		return fallback("compare timed out")
	}
	for _, c := range cmp.Diffs {
		if c.TooLarge || c.Collapsed || c.Truncated() {
			return fallback("compare diff incomplete", "path", c.NewPath)
		}
	}
	rendered, err := diff.ConvertChangesToDiff(cmp.Diffs)
	if err != nil {
		return fallback("cannot render compare diff", "error", err)
	}
	fetched, err := r.reduceAndValidate(start, rendered, cmp.Diffs)
	if err != nil {
		return fallback("cannot reduce compare diff", "error", err)
	}
	for _, d := range fetched.dropped {
		if d.Reason == diffbudget.ReasonSizeBudget {
			return fallback("update diff exceeds the size budget", "path", d.Path)
		}
	}

	var mrPaths []string
	changes, err := r.gitlab.GetMRChanges(ctx, r.projectID, r.mrIID)
	switch {
	case err != nil:
		plan.markDirty(r, fmt.Sprintf("MR file list unavailable: %v", err))
	case changes.Overflow:
		plan.markDirty(r, "MR file list overflow")
	case changes.SHA != plan.head:
		plan.markDirty(r, "MR file list is not at the pinned head")
	default:
		mrPaths = make([]string, 0, len(changes.Changes))
		for _, c := range changes.Changes {
			mrPaths = append(mrPaths, c.NewPath)
		}
	}
	deltaPaths := make([]string, 0, len(cmp.Diffs))
	for _, c := range cmp.Diffs {
		deltaPaths = append(deltaPaths, c.NewPath)
	}
	section, truncated := otherFilesSection(mrPaths, deltaPaths)
	if truncated {
		plan.markDirty(r, "file list truncated")
	}
	if section != "" {
		fetched.diff += "\n\n" + section
	}
	return fetched, true
}

// otherFilesSection lists MR files absent from the update delta so the
// model knows they exist without seeing their diffs. Paths are deduped,
// sorted, stripped of control characters and capped; truncated reports a cap
// hit, which makes the run dirty (REQ-06).
func otherFilesSection(mrPaths, deltaPaths []string) (section string, truncated bool) {
	inDelta := make(map[string]bool, len(deltaPaths))
	for _, p := range deltaPaths {
		inDelta[cleanPath(p)] = true
	}
	seen := make(map[string]bool, len(mrPaths))
	var others []string
	for _, p := range mrPaths {
		p = cleanPath(p)
		if p == "" || inDelta[p] || seen[p] {
			continue
		}
		seen[p] = true
		others = append(others, p)
	}
	if len(others) == 0 {
		return "", false
	}
	sort.Strings(others)
	more := 0
	if len(others) > maxOtherFiles {
		more = len(others) - maxOtherFiles
		others = others[:maxOtherFiles]
	}
	lines := append([]string{otherFilesHeader}, others...)
	if more > 0 {
		lines = append(lines, fmt.Sprintf("... and %d more", more))
	}
	return strings.Join(lines, "\n"), more > 0
}

// cleanPath drops control characters so a crafted path cannot inject lines.
func cleanPath(p string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, p)
}

// postIncrementalNote always posts a new note (never UpdateNote) carrying
// the marker, the state line when the run is clean, and a mode header.
// Dirty sources are all collected before this point (assessRun).
func (r *MRInspectReviewer) postIncrementalNote(ctx context.Context, plan *incrementalRun, content, footer string) error {
	var b strings.Builder
	b.WriteString(ReviewNoteMarker + "\n")
	if len(plan.dirty) == 0 {
		b.WriteString(FormatState(State{SHA: plan.head, Base: plan.base, Mode: plan.decision.Mode, Round: plan.decision.Round}) + "\n")
	}
	if plan.decision.Mode == ModeUpdate {
		b.WriteString("Update review " + shortSHA(plan.prior.SHA) + ".." + shortSHA(plan.head))
	} else {
		b.WriteString("Full review at " + shortSHA(plan.head))
	}
	b.WriteString("\n\n" + r.validator.SanitizeInput(content+footer))
	if _, err := r.gitlab.PostNote(ctx, r.projectID, r.mrIID, b.String()); err != nil {
		return fmt.Errorf("postReview: %w", err)
	}
	return nil
}

// isEmptyNewFile reports a newly added file that GitLab returns with an empty
// diff and no size flags; it has no content to send, so it is not unseen.
func isEmptyNewFile(c gitlab.Change) bool {
	return c.NewFile && c.Diff == "" && !c.TooLarge && !c.Collapsed
}
