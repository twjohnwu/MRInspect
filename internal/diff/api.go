package diff

import (
	"context"
	"fmt"

	"mrinspect/internal/diff/disclosure"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/logger"
)

// APIDiffFetcher retrieves a unified diff via the GitLab API.
// The sourceBranch/targetBranch arguments required by IDiffFetcher are accepted
// but not used — the MR is identified by the projectID and mrIID injected at construction.
type APIDiffFetcher struct {
	gitlabClient interfaces.IGitLabClient
	projectID    string
	mrIID        string
	log          *logger.Logger
}

func NewAPIDiffFetcher(gc interfaces.IGitLabClient, projectID, mrIID string, log *logger.Logger) *APIDiffFetcher {
	return &APIDiffFetcher{
		gitlabClient: gc,
		projectID:    projectID,
		mrIID:        mrIID,
		log:          log,
	}
}

func (f *APIDiffFetcher) Fetch(ctx context.Context, sourceBranch, targetBranch string) (string, error) {
	patch, _, err := f.FetchWithSource(ctx, sourceBranch, targetBranch)
	return patch, err
}

// FetchWithSource is Fetch plus the SHA GitLab reports for the MR changes,
// the reviewed SHA of the returned patch.
func (f *APIDiffFetcher) FetchWithSource(ctx context.Context, _, _ string) (string, string, error) {
	changesResp, err := f.gitlabClient.GetMRChanges(ctx, f.projectID, f.mrIID)
	if err != nil {
		return "", "", fmt.Errorf("APIDiffFetcher: %w", err)
	}
	reviewable, unavailable := disclosure.SplitTruncated(changesResp.Changes)
	result, err := ConvertChangesToDiff(reviewable)
	if err != nil {
		return "", "", fmt.Errorf("APIDiffFetcher: convert: %w", err)
	}
	return result + disclosure.Render(unavailable), changesResp.SHA, nil
}
