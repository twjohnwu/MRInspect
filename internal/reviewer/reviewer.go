package reviewer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/diff"
	"mrinspect/internal/diffbudget"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/lane"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
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

type generationStatus struct {
	reflectApplied bool
	reflectChanged bool
	multiFanout    *lane.FanoutResult
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
	if r.cfg.ReviewMode == "multi" {
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

func (r *MRInspectReviewer) generateReviewForExplicitModeWithStatus(ctx context.Context, mode EvalMode, codeDiff string, changes []gitlab.Change, mr gitlab.MergeRequest) (string, footerAggregation, generationStatus, error) {
	switch mode {
	case EvalModeSingle:
		content, err := r.generateReview(ctx, codeDiff, mr)
		return content, footerAggregation{}, generationStatus{}, err
	case EvalModeReflect:
		content, err := r.generateReview(ctx, codeDiff, mr)
		reflectApplied := false
		reflectChanged := false
		if err == nil {
			content, reflectApplied, reflectChanged = r.selfReflectWithStatus(ctx, content)
		}
		return content, footerAggregation{}, generationStatus{
			reflectApplied: reflectApplied,
			reflectChanged: reflectChanged,
		}, err
	case EvalModeMulti:
		content, footer, fanout, err := r.generateMultiReview(ctx, codeDiff, changes, mr)
		return content, footer, generationStatus{multiFanout: fanout}, err
	default:
		return "", footerAggregation{}, generationStatus{}, fmt.Errorf("unsupported eval mode %q", mode)
	}
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
		if r.cfg.ReviewMode == "multi" {
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
	budget, ok := r.budgetForModel(true)
	if !ok {
		return changes, nil, nil
	}

	return diffbudget.Reduce(changes, diffbudget.Options{
		ModelBudget:   budget,
		MaxDiffSizeKB: r.cfg.Validation.MaxDiffSizeKB,
		PromptShare:   r.cfg.DiffPromptShare,
		Logger:        r.log,
		InitialDiff:   codeDiff,
		Render: func(kept []gitlab.Change, dropped []diffbudget.DroppedFile) (string, error) {
			rendered, renderErr := diff.ConvertChangesToDiff(kept)
			return rendered + diffbudget.Trailer(dropped), renderErr
		},
	})
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
	dumpsEnabled := r.cfg.ReviewDumpEnabled
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
	reflected, _, _ := r.selfReflectWithStatus(ctx, review)
	return reflected
}

func (r *MRInspectReviewer) selfReflectWithStatus(ctx context.Context, review string) (string, bool, bool) {
	loadedProject, err := r.loadServiceProject()
	if err != nil {
		return review, false, false
	}
	reflectPrompt := r.prompt.ComposeSelfReflectionPrompt(loadedProject, review)
	r.logSelfReflectPromptBreakdown(review, reflectPrompt)
	result, err := r.callAI(ctx, reflectPrompt)
	if err != nil {
		r.log.Warn("self-reflection failed", "error", err)
		return review, false, false
	}
	if strings.Contains(result, "REVIEW VALIDATED") {
		r.log.Info("self-reflection: review validated")
		return review, true, false
	}

	// Never adopt a reflection result that has not been re-validated: a
	// garbage reflection would otherwise silently replace a valid review.
	// Validate the cleaned reflection output before adopting it.
	dumpsEnabled := r.cfg.ReviewDumpEnabled
	cleaned := r.cleanResponse(result)
	if err := r.validator.ValidateReviewContent(cleaned); err != nil {
		r.log.Warn("self-reflection produced an invalid review; keeping original", "error", err.Error())
		r.dumpForensics(dumpsEnabled, "Response", "self-reflection", result)
		return review, false, false
	}

	r.log.Info("self-reflection: review updated")
	return cleaned, true, cleaned != review
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

func (r *MRInspectReviewer) buildRetryPrompt(original string, validationErr error) string {
	return original + fmt.Sprintf(
		"\n\n---\nPrevious attempt was rejected: %s\n"+
			"Please ensure your response includes ## Findings and ## Verdict sections.\n",
		validationErr.Error(),
	)
}
