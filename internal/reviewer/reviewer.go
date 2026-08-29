package reviewer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
)

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

// RAGReviewPath supplies retrieval data to the review path. Its API has no indexing
// operation, so the reviewer cannot request store construction through this seam.
type RAGReviewPath interface {
	RetrieveForReview(ctx context.Context, diff string) (ReviewRAGState, error)
}

// RAGIndexer is an explicit tripwire for tests proving the review path does not index.
type RAGIndexer interface {
	Index(ctx context.Context) error
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
	codeDiff, err := r.fetchDiff(ctx)
	if err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}

	stage = "generateReview"
	reviewContent, err := r.generateReview(ctx, codeDiff, mr)
	if err != nil {
		finalErr = err
		r.postErrorComment(ctx, err, stage)
		return
	}

	if r.cfg.SelfReflection {
		reviewContent = r.selfReflect(ctx, reviewContent)
	}

	stage = "postReview"
	if err := r.postReview(ctx, reviewContent); err != nil {
		finalErr = err
		r.log.Error("failed to post review", "error", err)
		return
	}

	r.log.Info("review posted successfully", "mr", r.mrIID, "project", r.projectID)
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

func (r *MRInspectReviewer) fetchDiff(ctx context.Context) (string, error) {
	start := time.Now()
	src := r.validator.GetSourceBranch()
	tgt := r.validator.GetTargetBranch()
	codeDiff, err := r.diff.Fetch(ctx, src, tgt)
	if err != nil {
		return "", fmt.Errorf("fetchDiff: %w", err)
	}
	result, err := r.validator.ValidateDiff(codeDiff)
	if err != nil {
		return "", fmt.Errorf("fetchDiff: validate: %w", err)
	}
	dur := time.Since(start).Milliseconds()
	r.log.LogStep("fetchDiff", &dur, map[string]any{
		"sizeKB":         result.SizeKB,
		"filesChanged":   result.FilesChanged,
		"supportedFiles": result.SupportedFiles,
	})
	return codeDiff, nil
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

	var reviewContent string
	var lastErr error
	for attempt := 1; attempt <= r.cfg.Validation.AIRetryAttempts; attempt++ {
		if attempt > 1 {
			reviewPrompt = r.buildRetryPrompt(reviewPrompt, lastErr)
			r.log.Info("retrying AI call", "attempt", attempt, "reason", lastErr.Error())
		}

		content, err := r.callAI(ctx, reviewPrompt)
		if err != nil {
			lastErr = err
			continue
		}

		content = r.cleanResponse(content)
		if err := r.validator.ValidateReviewContent(content); err != nil {
			lastErr = err
			continue
		}

		reviewContent = content
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
	loadedProject, err := r.loadServiceProject()
	if err != nil {
		return review
	}
	reflectPrompt := r.prompt.ComposeSelfReflectionPrompt(loadedProject, review)
	result, err := r.callAI(ctx, reflectPrompt)
	if err != nil {
		r.log.Warn("self-reflection failed", "error", err)
		return review
	}
	if strings.Contains(result, "REVIEW VALIDATED") {
		r.log.Info("self-reflection: review validated")
		return review
	}
	r.log.Info("self-reflection: review updated")
	return result
}

func (r *MRInspectReviewer) postReview(ctx context.Context, content string) error {
	note := content + r.ragFooter()
	safe := r.validator.SanitizeInput(note)
	_, err := r.gitlab.PostNote(ctx, r.projectID, r.mrIID, safe)
	if err != nil {
		return fmt.Errorf("postReview: %w", err)
	}
	return nil
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
	state := r.rag.State
	if !state.StorePresent && len(state.Degraded) == 0 && len(state.Composition.Evicted) == 0 && len(state.Composition.Degraded) == 0 {
		return ""
	}

	degradedCount := len(state.Degraded) + len(state.Composition.Degraded)
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
	for _, marker := range markers {
		if idx := strings.Index(response, marker); idx >= 0 {
			return response[idx:]
		}
	}
	return response
}

func (r *MRInspectReviewer) buildRetryPrompt(original string, validationErr error) string {
	return original + fmt.Sprintf(
		"\n\n---\nPrevious attempt was rejected: %s\n"+
			"Please ensure your response includes ## Findings and ## Verdict sections.\n",
		validationErr.Error(),
	)
}
