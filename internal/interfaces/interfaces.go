package interfaces

import (
	"context"

	"mrinspect/internal/gitlab"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/project"
	"mrinspect/internal/validator"
)

// IGitLabClient abstracts all GitLab API operations.
type IGitLabClient interface {
	HealthCheck(ctx context.Context) bool
	GetMergeRequest(ctx context.Context, projectID, mrIID string) (gitlab.MergeRequest, error)
	GetMRChanges(ctx context.Context, projectID, mrIID string) (gitlab.MRChangesResponse, error)
	ListNotes(ctx context.Context, projectID, mrIID string) ([]gitlab.Note, error)
	PostNote(ctx context.Context, projectID, mrIID, body string) (gitlab.Note, error)
	UpdateNote(ctx context.Context, projectID, mrIID string, noteID int, body string) (gitlab.Note, error)
}

// IDiffFetcher abstracts diff retrieval; implementations decide how to use the branch args.
type IDiffFetcher interface {
	Fetch(ctx context.Context, sourceBranch, targetBranch string) (string, error)
}

// IProjectLoader abstracts loading review projects from disk.
type IProjectLoader interface {
	IsAvailable() bool
	LoadProfile(serviceName, serviceType string) (project.LoadedProject, error)
}

// IPromptComposer abstracts building AI prompts from a loaded project.
type IPromptComposer interface {
	ComposeReviewPrompt(p project.LoadedProject, diff string, mr gitlab.MergeRequest) (string, error)
	ComposeSelfReflectionPrompt(p project.LoadedProject, reviewContent string) string
}

// IReviewValidator abstracts all input validation and env-var access needed by the reviewer.
// IsRunningInCrossRepoMode is intentionally excluded — the reviewer never calls it directly.
type IReviewValidator interface {
	ValidateEnvironment() error
	ValidateMergeRequest(mr gitlab.MergeRequest) error
	ValidateDiff(diff string) (validator.DiffValidationResult, error)
	ValidateReviewContent(content string) error
	SanitizeInput(input string) string
	GetProjectID() string
	GetMRIID() string
	GetSourceBranch() string
	GetTargetBranch() string
}

// IErrorHandler abstracts error categorization and user-facing message generation.
type IErrorHandler interface {
	Categorize(err error) mrerrors.Category
	ShouldPost(err error, cat mrerrors.Category) bool
	GenerateMessage(err error, stage string, cat mrerrors.Category) string
}
