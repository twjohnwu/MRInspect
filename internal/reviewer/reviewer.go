package reviewer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/diff"
	"mrinspect/internal/diffbudget"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/lane"
	"mrinspect/internal/lane/hunk"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/chunk"
	"mrinspect/internal/rag/resources"
)

// ReviewNoteMarker identifies review notes owned by MRInspect.
const ReviewNoteMarker = "<!-- mrinspect:review -->"

// ReviewRAGState is the reviewer-facing RAG result and footer provenance.
type ReviewRAGState struct {
	StorePresent         bool
	Store                rag.StoreResolution
	ResourcesSHA256      string
	PackageVersionPinned bool
	Chunks               []rag.Chunk
	SkippedFiles         int
	Degraded             []string
	Composition          prompt.ComposeResult
}

type fetchedDiff struct {
	diff    string
	changes []gitlab.Change
	dropped []diffbudget.DroppedFile
}

// RAGReviewPath supplies retrieval data to the review path. Its API has no indexing
// operation, so the reviewer cannot request store construction through this seam.
type RAGReviewPath interface {
	RetrieveForReview(ctx context.Context, diff string) (ReviewRAGState, error)
}

// RAGIndexer is an explicit tripwire for tests proving the review path does not index.
type RAGIndexer interface {
	Index(ctx context.Context) error
}

// MultiLaneReviewPath is the injectable wiring required by the multi-lane review path.
type MultiLaneReviewPath struct {
	RepoRoot         string
	ResourceRegistry resources.Registry
	Retriever        rag.Retriever
	FullLoader       rag.FullLoader
	ModelLimits      map[string]int
	Fanout           func(context.Context, lane.FanoutInput) (lane.FanoutResult, error)
}

type footerAggregation struct {
	additionalDegraded int
	laneEvictions      []string
	droppedFiles       []string
	degradedToSingle   bool
}

type namedLaneDegradation struct {
	laneID  string
	message string
}

// MRInspectReviewer orchestrates the full code review pipeline.
type MRInspectReviewer struct {
	cfg        config.Config
	gitlab     interfaces.IGitLabClient
	ai         ai.Provider
	diff       interfaces.IDiffFetcher
	projects   interfaces.IProjectLoader
	prompt     interfaces.IPromptComposer
	validator  interfaces.IReviewValidator
	errHandler interfaces.IErrorHandler
	log        *logger.Logger

	projectID string
	mrIID     string

	// rag keeps the review-only retrieval dependency and the result used in the note footer.
	rag struct {
		ReviewPath RAGReviewPath
		Indexer    RAGIndexer
		State      ReviewRAGState
	}
	multi MultiLaneReviewPath
}

func New(
	cfg config.Config,
	gc interfaces.IGitLabClient,
	provider ai.Provider,
	df interfaces.IDiffFetcher,
	pl interfaces.IProjectLoader,
	pc interfaces.IPromptComposer,
	v interfaces.IReviewValidator,
	eh interfaces.IErrorHandler,
	log *logger.Logger,
) *MRInspectReviewer {
	return &MRInspectReviewer{
		cfg:        cfg,
		gitlab:     gc,
		ai:         provider,
		diff:       df,
		projects:   pl,
		prompt:     pc,
		validator:  v,
		errHandler: eh,
		log:        log,
		projectID:  v.GetProjectID(),
		mrIID:      v.GetMRIID(),
	}
}

// SetRAGReviewPath installs the optional, review-only RAG retrieval path.
// A nil path disables retrieval, preserving the default review behavior.
func (r *MRInspectReviewer) SetRAGReviewPath(path RAGReviewPath) {
	if r != nil {
		r.rag.ReviewPath = path
	}
}

// SetMultiLaneReviewPath installs multi-lane dependencies.
func (r *MRInspectReviewer) SetMultiLaneReviewPath(path MultiLaneReviewPath) {
	if r != nil {
		r.multi = path
	}
}

// Run executes the full review pipeline. It always returns; the process exits 0
// so it never blocks the GitLab pipeline (allow_failure: true semantics).
func (r *MRInspectReviewer) Run(ctx context.Context) {
	r.log.StartReview(0, 0)
	r.log.Info("MRInspect starting", "provider", r.cfg.AIProvider, "crossRepo", r.cfg.CrossRepo.Enabled)

	var finalErr error
	var stage string

	defer func() {
		summary := r.log.CompleteReview(finalErr == nil, finalErr)
		r.log.Info("MRInspect completed",
			"success", finalErr == nil,
			"duration", summary.TotalDuration,
			"apiCalls", summary.APICalls.Total,
		)
		if err := r.log.SaveMetrics(); err != nil {
			r.log.Warn("failed to save metrics", "error", err)
		}
	}()

	stage = "validateSystem"
	if err := r.validateSystem(ctx); err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}

	stage = "fetchMRDetails"
	mr, err := r.fetchMRDetails(ctx)
	if err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}

	stage = "fetchDiff"
	fetched, err := r.fetchDiff(ctx)
	if err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}

	stage = "generateReview"
	reviewContent, footer, err := r.generateReviewForMode(ctx, fetched.diff, fetched.changes, mr)
	if err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}
	footer.droppedFiles = dropPaths(fetched.dropped)

	stage = "postReview"
	if err := r.postReviewWithFooter(ctx, reviewContent, r.ragFooterWithAggregation(footer)); err != nil {
		finalErr = err
		r.log.Error("failed to post review", "error", err)
		return
	}

	r.log.Info("review posted successfully", "mr", r.mrIID, "project", r.projectID)
}

func (r *MRInspectReviewer) generateReviewForMode(ctx context.Context, codeDiff string, changes []gitlab.Change, mr gitlab.MergeRequest) (string, footerAggregation, error) {
	mode := EvalModeSingle
	if r.cfg.SelfReflection {
		mode = EvalModeReflect
	}
	if os.Getenv("MRI_REVIEW_MODE") == "multi" {
		mode = EvalModeMulti
	}
	return r.generateReviewForExplicitMode(ctx, mode, codeDiff, changes, mr)
}

// generateReviewForExplicitMode is the shared generation seam used by both
// production and offline evaluation. Callers decide the mode; this helper does
// not read process-global mode configuration.
func (r *MRInspectReviewer) generateReviewForExplicitMode(ctx context.Context, mode EvalMode, codeDiff string, changes []gitlab.Change, mr gitlab.MergeRequest) (string, footerAggregation, error) {
	content, footer, _, err := r.generateReviewForExplicitModeWithStatus(ctx, mode, codeDiff, changes, mr)
	return content, footer, err
}

func (r *MRInspectReviewer) generateReviewForExplicitModeWithStatus(ctx context.Context, mode EvalMode, codeDiff string, changes []gitlab.Change, mr gitlab.MergeRequest) (string, footerAggregation, bool, error) {
	switch mode {
	case EvalModeSingle:
		content, err := r.generateReview(ctx, codeDiff, mr)
		return content, footerAggregation{}, false, err
	case EvalModeReflect:
		content, err := r.generateReview(ctx, codeDiff, mr)
		reflectApplied := false
		if err == nil {
			content, reflectApplied = r.selfReflectWithStatus(ctx, content)
		}
		return content, footerAggregation{}, reflectApplied, err
	case EvalModeMulti:
		content, footer, err := r.generateMultiReview(ctx, codeDiff, changes, mr)
		return content, footer, false, err
	default:
		return "", footerAggregation{}, false, fmt.Errorf("unsupported eval mode %q", mode)
	}
}

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
		Lanes:            registry.Lanes,
		Terms:            lane.Terms(changes),
		ResourceRegistry: r.multi.ResourceRegistry,
		Retriever:        r.multi.Retriever,
		FullLoader:       r.multi.FullLoader,
		Project:          loadedProject,
		Diff:             codeDiff,
		MergeRequest:     mr,
		Provider:         r.ai,
		Attempts:         r.cfg.Validation.AIRetryAttempts,
		GlobalModel:      r.cfg.Providers[r.cfg.AIProvider].Model,
		ModelLimits:      r.multi.ModelLimits,
		Logger:           r.log,
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
	if os.Getenv("MRI_RAG_ON_NORMATIVE_EVICTION") == "fail" {
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

func (r *MRInspectReviewer) multiRenderInput(declarations []lane.Lane, result lane.FanoutResult, changes []gitlab.Change) lane.RenderInput {
	renderInput, _ := r.multiRenderInputWithDegradations(declarations, result, changes)
	return renderInput
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

func aggregateLaneFooter(results []lane.LaneResult) footerAggregation {
	aggregation := footerAggregation{}
	seenEvictions := make(map[string]struct{})
	for _, result := range results {
		aggregation.additionalDegraded += len(result.Degraded)
		for _, degraded := range result.Degraded {
			if !strings.Contains(degraded, "evicted section") {
				continue
			}
			entry := fmt.Sprintf("evicted section [%s]: %s", result.LaneID, strings.Join(strings.Fields(degraded), " "))
			if _, exists := seenEvictions[entry]; exists {
				continue
			}
			seenEvictions[entry] = struct{}{}
			aggregation.laneEvictions = append(aggregation.laneEvictions, entry)
		}
	}
	return aggregation
}

func mergeLaneDegradations(aggregation footerAggregation, results []lane.LaneResult, additional []namedLaneDegradation) footerAggregation {
	existing := make(map[namedLaneDegradation]int)
	for _, result := range results {
		for _, degraded := range result.Degraded {
			existing[namedLaneDegradation{laneID: result.LaneID, message: degraded}]++
		}
	}
	for _, degraded := range additional {
		if existing[degraded] > 0 {
			existing[degraded]--
			continue
		}
		aggregation.additionalDegraded++
	}
	return aggregation
}

func (r *MRInspectReviewer) validateSystem(ctx context.Context) error {
	start := time.Now()
	if err := r.validator.ValidateEnvironment(); err != nil {
		return fmt.Errorf("validateSystem: %w", err)
	}
	if !r.gitlab.HealthCheck(ctx) {
		return fmt.Errorf("validateSystem: GitLab API is not reachable")
	}
	dur := time.Since(start).Milliseconds()
	r.log.LogStep("validateSystem", &dur, nil)
	return nil
}

func (r *MRInspectReviewer) fetchMRDetails(ctx context.Context) (gitlab.MergeRequest, error) {
	start := time.Now()
	mr, err := r.gitlab.GetMergeRequest(ctx, r.projectID, r.mrIID)
	if err != nil {
		return gitlab.MergeRequest{}, fmt.Errorf("fetchMRDetails: %w", err)
	}
	if err := r.validator.ValidateMergeRequest(mr); err != nil {
		return gitlab.MergeRequest{}, fmt.Errorf("fetchMRDetails: validate: %w", err)
	}
	dur := time.Since(start).Milliseconds()
	r.log.LogStep("fetchMRDetails", &dur, map[string]any{"mrIID": mr.IID, "title": mr.Title})
	return mr, nil
}

func (r *MRInspectReviewer) fetchDiff(ctx context.Context) (fetchedDiff, error) {
	start := time.Now()
	src := r.validator.GetSourceBranch()
	tgt := r.validator.GetTargetBranch()
	codeDiff, err := r.diff.Fetch(ctx, src, tgt)
	if err != nil {
		return fetchedDiff{}, fmt.Errorf("fetchDiff: %w", err)
	}

	changesResp, err := r.gitlab.GetMRChanges(ctx, r.projectID, r.mrIID)
	if err != nil {
		if os.Getenv("MRI_REVIEW_MODE") == "multi" {
			return fetchedDiff{}, fmt.Errorf("fetchDiff: %w", err)
		}
		r.log.Warn("GetMRChanges failed in single mode; skipping diff-size reduction", "error", err.Error())
		result, validateErr := r.validator.ValidateDiff(codeDiff)
		if validateErr != nil {
			return fetchedDiff{}, fmt.Errorf("fetchDiff: validate: %w", validateErr)
		}
		dur := time.Since(start).Milliseconds()
		r.log.LogStep("fetchDiff", &dur, map[string]any{
			"sizeKB":         result.SizeKB,
			"filesChanged":   result.FilesChanged,
			"supportedFiles": result.SupportedFiles,
			"droppedFiles":   0,
		})
		return fetchedDiff{diff: codeDiff}, nil
	}

	kept, dropped, err := r.reduceDiff(codeDiff, changesResp.Changes)
	if err != nil {
		return fetchedDiff{}, fmt.Errorf("fetchDiff: %w", err)
	}

	reduced := codeDiff
	if len(dropped) > 0 {
		rebuilt, convErr := diff.ConvertChangesToDiff(kept)
		if convErr != nil {
			return fetchedDiff{}, fmt.Errorf("fetchDiff: %w", convErr)
		}
		reduced = rebuilt + diffbudget.Trailer(dropped)
	}

	result, err := r.validator.ValidateDiff(reduced)
	if err != nil {
		return fetchedDiff{}, fmt.Errorf("fetchDiff: validate: %w", err)
	}
	dur := time.Since(start).Milliseconds()
	r.log.LogStep("fetchDiff", &dur, map[string]any{
		"sizeKB":         result.SizeKB,
		"filesChanged":   result.FilesChanged,
		"supportedFiles": result.SupportedFiles,
		"droppedFiles":   len(dropped),
	})
	return fetchedDiff{diff: reduced, changes: kept, dropped: dropped}, nil
}

// reduceDiff applies the diff-size reduction stage: dropping whole files
// (never truncating a hunk — a truncated hunk would make the model report
// findings against code that does not exist) so an oversized diff shrinks
// to fit the effective model's prompt budget instead of hard-failing the
// whole run. A model with no registered token budget (unknown to both
// prompt.ModelLimitsFromEnv and the multi-lane ModelLimits override) skips
// reduction entirely and relies on the existing ValidateDiff KB backstop,
// so an unconfigured model cannot turn this new stage into a new class of
// failure for setups that worked before it existed.
func (r *MRInspectReviewer) reduceDiff(codeDiff string, changes []gitlab.Change) ([]gitlab.Change, []diffbudget.DroppedFile, error) {
	limits, err := prompt.ModelLimitsFromEnv()
	if err != nil {
		r.log.Warn("invalid model limits configuration; using defaults", "error", err.Error())
		limits = prompt.DefaultModelLimits
	}
	merged := make(map[string]int, len(limits)+len(r.multi.ModelLimits))
	for model, tokens := range limits {
		merged[model] = tokens
	}
	for model, tokens := range r.multi.ModelLimits {
		merged[model] = tokens
	}

	model := r.cfg.Providers[r.cfg.AIProvider].Model
	budget, err := prompt.PromptBudgetForModel(model, merged)
	if err != nil {
		r.log.Warn("diff budget unavailable; skipping diff-size reduction", "model", model, "error", err.Error())
		return changes, nil, nil
	}

	return diffbudget.Reduce(changes, diffbudget.Options{
		ModelBudget:   budget,
		MaxDiffSizeKB: r.cfg.Validation.MaxDiffSizeKB,
		Logger:        r.log,
		InitialDiff:   codeDiff,
		Render: func(kept []gitlab.Change, dropped []diffbudget.DroppedFile) (string, error) {
			rendered, renderErr := diff.ConvertChangesToDiff(kept)
			return rendered + diffbudget.Trailer(dropped), renderErr
		},
	})
}

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

// resolvedPromptBudget mirrors reduceDiff's model-limit lookup (merged env
// config and multi-lane overrides) without its warn-on-failure logging, so
// breakdown logging can silently omit the budget field for a model with no
// registered budget entry instead of duplicating a warning already emitted
// by reduceDiff earlier in the same run.
func (r *MRInspectReviewer) resolvedPromptBudget() (int, bool) {
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
	budget, err := prompt.PromptBudgetForModel(r.cfg.Providers[r.cfg.AIProvider].Model, merged)
	if err != nil {
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
		if budget, ok := r.resolvedPromptBudget(); ok {
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

func (r *MRInspectReviewer) generateReview(ctx context.Context, codeDiff string, mr gitlab.MergeRequest) (string, error) {
	start := time.Now()
	r.retrieveReviewRAG(ctx, codeDiff)

	var reviewPrompt string
	loadedProject, projectErr := r.loadServiceProject()
	if projectErr == nil {
		var err error
		reviewPrompt, err = r.prompt.ComposeReviewPrompt(loadedProject, codeDiff, mr)
		if err != nil {
			return "", fmt.Errorf("prompt composition failed: %w", err)
		}
	}
	if projectErr != nil {
		r.log.Info("using legacy template", "reason", projectErr.Error())
		tmplFn := prompt.SelectTemplate(r.cfg.Service.Type, r.cfg)
		reviewPrompt = tmplFn(codeDiff, mr, r.cfg.Service, r.cfg.AIProvider)
	}
	r.logSinglePromptBreakdown(reviewPrompt, codeDiff)

	var reviewContent string
	var lastErr error
	dumpsEnabled := reviewDumpsEnabled()
	for attempt := 1; attempt <= r.cfg.Validation.AIRetryAttempts; attempt++ {
		if attempt > 1 {
			reviewPrompt = r.buildRetryPrompt(reviewPrompt, lastErr)
			r.log.Info("retrying AI call", "attempt", attempt, "reason", lastErr.Error())
		}

		rawResponse, err := r.callAI(ctx, reviewPrompt)
		if err != nil {
			lastErr = err
			continue
		}

		cleaned := r.cleanResponse(rawResponse)
		if err := r.validator.ValidateReviewContent(cleaned); err != nil {
			lastErr = err
			r.logValidationFailure(attempt, reviewPrompt, rawResponse, cleaned, err, dumpsEnabled)
			continue
		}

		reviewContent = cleaned
		break
	}

	if reviewContent == "" {
		if lastErr != nil {
			return "", fmt.Errorf("generateReview: all attempts failed: %w", lastErr)
		}
		return "", fmt.Errorf("generateReview: no review generated")
	}

	dur := time.Since(start).Milliseconds()
	r.log.LogStep("generateReview", &dur, map[string]any{"contentLength": len(reviewContent)})
	return reviewContent, nil
}

func (r *MRInspectReviewer) callAI(ctx context.Context, reviewPrompt string) (string, error) {
	opts := ai.GenerateOptions{
		Model:     r.cfg.Providers[r.cfg.AIProvider].Model,
		MaxTokens: r.cfg.Providers[r.cfg.AIProvider].MaxTokens,
	}
	return r.ai.Generate(ctx, reviewPrompt, opts)
}

func (r *MRInspectReviewer) selfReflect(ctx context.Context, review string) string {
	reflected, _ := r.selfReflectWithStatus(ctx, review)
	return reflected
}

func (r *MRInspectReviewer) selfReflectWithStatus(ctx context.Context, review string) (string, bool) {
	loadedProject, err := r.loadServiceProject()
	if err != nil {
		return review, false
	}
	reflectPrompt := r.prompt.ComposeSelfReflectionPrompt(loadedProject, review)
	r.logSelfReflectPromptBreakdown(review, reflectPrompt)
	result, err := r.callAI(ctx, reflectPrompt)
	if err != nil {
		r.log.Warn("self-reflection failed", "error", err)
		return review, false
	}
	if strings.Contains(result, "REVIEW VALIDATED") {
		r.log.Info("self-reflection: review validated")
		return review, true
	}

	// Never adopt a reflection result that has not been re-validated: a
	// garbage reflection would otherwise silently replace a valid review.
	// Validate the cleaned reflection output before adopting it.
	dumpsEnabled := reviewDumpsEnabled()
	cleaned := r.cleanResponse(result)
	if err := r.validator.ValidateReviewContent(cleaned); err != nil {
		r.log.Warn("self-reflection produced an invalid review; keeping original", "error", err.Error())
		r.dumpForensics(dumpsEnabled, "Response", "self-reflection", result)
		return review, false
	}

	r.log.Info("self-reflection: review updated")
	return cleaned, true
}

func (r *MRInspectReviewer) postReview(ctx context.Context, content string) error {
	return r.postReviewWithFooter(ctx, content, r.ragFooter())
}

func (r *MRInspectReviewer) postReviewWithFooter(ctx context.Context, content, footer string) error {
	// Preserve the existing content/footer sanitization byte-for-byte, then add
	// the stable HTML marker so the sanitizer cannot escape it.
	safe := ReviewNoteMarker + "\n" + r.validator.SanitizeInput(content+footer)
	currentUser, currentErr := r.gitlab.CurrentUser(ctx)
	if currentErr != nil {
		r.log.Warn("failed to get current GitLab user; falling back to PostNote", "error", currentErr)
	} else {
		if notes, listErr := r.gitlab.ListNotes(ctx, r.projectID, r.mrIID); listErr != nil {
			r.log.Warn("failed to list GitLab notes; falling back to PostNote", "error", listErr)
		} else {
			for _, note := range notes {
				if strings.Contains(note.Body, ReviewNoteMarker) && sameAuthor(note.Author, currentUser) {
					if _, err := r.gitlab.UpdateNote(ctx, r.projectID, r.mrIID, note.ID, safe); err != nil {
						return fmt.Errorf("postReview: %w", err)
					}
					return nil
				}
			}
		}
	}

	_, err := r.gitlab.PostNote(ctx, r.projectID, r.mrIID, safe)
	if err != nil {
		return fmt.Errorf("postReview: %w", err)
	}
	return nil
}

func sameAuthor(left, right gitlab.Author) bool {
	return left.ID != 0 && left.ID == right.ID ||
		left.Username != "" && left.Username == right.Username
}

// retrieveReviewRAG never indexes. Retrieval failures degrade the review so a missing
// store cannot block it (REQ-07).
func (r *MRInspectReviewer) retrieveReviewRAG(ctx context.Context, codeDiff string) {
	if r.rag.ReviewPath == nil {
		return
	}

	state, err := r.rag.ReviewPath.RetrieveForReview(ctx, codeDiff)
	if err != nil {
		state.Degraded = append(state.Degraded, fmt.Sprintf("RAG retrieval failed: %v", err))
	}
	r.rag.State = state
}

func (r *MRInspectReviewer) ragFooter() string {
	return r.ragFooterWithAggregation(footerAggregation{})
}

func (r *MRInspectReviewer) ragFooterWithAggregation(aggregation footerAggregation) string {
	footer := r.ragProvenanceFooter(aggregation)
	if len(aggregation.droppedFiles) > 0 {
		footer += "\n\n_Dropped for diff size budget: " + strings.Join(aggregation.droppedFiles, ", ") + "_"
	}
	return footer
}

func (r *MRInspectReviewer) ragProvenanceFooter(aggregation footerAggregation) string {
	state := r.rag.State
	if !state.StorePresent && len(state.Degraded) == 0 && len(state.Composition.Evicted) == 0 && len(state.Composition.Degraded) == 0 && aggregation.additionalDegraded == 0 {
		return ""
	}

	degradedCount := len(state.Degraded) + len(state.Composition.Degraded) + aggregation.additionalDegraded
	parts := []string{fmt.Sprintf("Degraded entries: %d", degradedCount), fmt.Sprintf("skipped files: %d", state.SkippedFiles)}
	if state.StorePresent {
		parts = append([]string{
			fmt.Sprintf("store built_at: %s", state.Store.BuiltAt),
			fmt.Sprintf("resources_sha256: %s", shortSHA(state.ResourcesSHA256)),
		}, parts...)
		if !state.PackageVersionPinned {
			version := state.Store.Version
			if version == "" {
				version = "unknown"
			}
			parts = append(parts, fmt.Sprintf("store version: %s (unpinned)", version))
		}
	} else {
		parts = append([]string{"store: absent"}, parts...)
	}

	for _, evicted := range state.Composition.Evicted {
		parts = append(parts, fmt.Sprintf("evicted section: %s", evicted.Name))
	}
	parts = append(parts, aggregation.laneEvictions...)
	return "\n\n---\nRAG provenance: " + strings.Join(parts, "; ")
}

func shortSHA(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func (r *MRInspectReviewer) postErrorComment(ctx context.Context, err error, stage string) {
	if err == nil {
		return
	}
	cat := r.errHandler.Categorize(err)
	r.log.LogError("review_error", stage, string(cat), err)
	if !r.errHandler.ShouldPost(err, cat) {
		return
	}
	msg := r.errHandler.GenerateMessage(err, stage, cat)
	if _, postErr := r.gitlab.PostNote(ctx, r.projectID, r.mrIID, msg); postErr != nil {
		r.log.Warn("failed to post error comment", "error", postErr)
	}
}

func (r *MRInspectReviewer) loadServiceProject() (project.LoadedProject, error) {
	if !r.projects.IsAvailable() {
		return project.LoadedProject{}, fmt.Errorf("projects directory not available")
	}
	return r.projects.LoadProfile(r.cfg.Service.Name, r.cfg.Service.Type)
}

func (r *MRInspectReviewer) cleanResponse(response string) string {
	markers := []string{"## Code Review", "# Code Review", "## Review", "### MR Info"}
	// Cut at the EARLIEST marker occurrence across the whole list, not the
	// first marker in list-priority order: if the reviewed diff itself
	// quotes a higher-priority marker string near the tail, list-order
	// selection would cut there and discard the entire real review, which
	// is unrecoverable. An echoed marker BEFORE the real content still
	// leaves echo garbage in the result, but that is accepted here because
	// ValidateReviewContent still gates on required sections downstream;
	// a stricter future guard could require the cut tail to actually
	// contain those required sections.
	cutAt := -1
	for _, marker := range markers {
		if idx := strings.Index(response, marker); idx >= 0 && (cutAt == -1 || idx < cutAt) {
			cutAt = idx
		}
	}
	if cutAt >= 0 {
		return response[cutAt:]
	}
	return response
}

// reviewDumpsEnabled reports whether failure-only prompt/response dumps are
// enabled. Dumps are disabled by default; setting MRI_REVIEW_DUMP_ENABLED to
// the exact string "true" turns them on. Callers read this once per run, not
// once per attempt.
func reviewDumpsEnabled() bool {
	return os.Getenv("MRI_REVIEW_DUMP_ENABLED") == "true"
}

// logValidationFailure records forensics for a failed ValidateReviewContent
// attempt: the headings the model actually wrote, and the response length
// before/after cleanResponse. It never fires on a successful attempt.
func (r *MRInspectReviewer) logValidationFailure(attempt int, reviewPrompt, rawResponse, cleaned string, validationErr error, dumpsEnabled bool) {
	fields := []any{
		"attempt", attempt,
		"headings", extractHeadings(rawResponse),
		"responseLenBeforeClean", len(rawResponse),
		"responseLenAfterClean", len(cleaned),
		"error", validationErr.Error(),
	}
	if !dumpsEnabled {
		fields = append(fields,
			"promptSHA", sha256Prefix(reviewPrompt),
			"responseSHA", sha256Prefix(rawResponse),
		)
	}
	r.log.Warn("review validation failed", fields...)
	r.dumpForensics(dumpsEnabled, "Prompt", fmt.Sprintf("attempt %d", attempt), reviewPrompt)
	r.dumpForensics(dumpsEnabled, "Response", fmt.Sprintf("attempt %d", attempt), rawResponse)
}

func sha256Prefix(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", digest)[:12]
}

// dumpForensics logs content wrapped in Start/End marker lines, gated by
// dumpsEnabled. It is failure-only by construction: callers only invoke it
// on a failed-validation path.
func (r *MRInspectReviewer) dumpForensics(dumpsEnabled bool, kind, tag, content string) {
	if !dumpsEnabled {
		return
	}
	r.log.Warn(fmt.Sprintf(
		"======== %s (%s) Start ========\n%s\n======== %s (%s) End ========",
		kind, tag, content, kind, tag,
	))
}

// extractHeadings returns every line the model actually wrote that starts
// with '#', i.e. any markdown heading level, in response order.
func extractHeadings(response string) []string {
	var headings []string
	for _, line := range strings.Split(response, "\n") {
		if strings.HasPrefix(line, "#") {
			headings = append(headings, line)
		}
	}
	return headings
}

func (r *MRInspectReviewer) buildRetryPrompt(original string, validationErr error) string {
	return original + fmt.Sprintf(
		"\n\n---\nPrevious attempt was rejected: %s\n"+
			"Please ensure your response includes ## Findings and ## Verdict sections.\n",
		validationErr.Error(),
	)
}
