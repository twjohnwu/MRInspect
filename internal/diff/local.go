package diff

import (
	"context"
	"fmt"

	"mrinspect/internal/logger"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// LocalDiffFetcher retrieves a unified diff using the local git repository.
type LocalDiffFetcher struct {
	log *logger.Logger
}

func NewLocalDiffFetcher(log *logger.Logger) *LocalDiffFetcher {
	return &LocalDiffFetcher{log: log}
}

// Fetch opens the current working directory as a git repo and returns the diff
// between targetBranch and sourceBranch.
func (f *LocalDiffFetcher) Fetch(_ context.Context, sourceBranch, targetBranch string) (string, error) {
	repo, err := gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", fmt.Errorf("LocalDiffFetcher: open repo: %w", err)
	}

	sourceHash, err := repo.ResolveRevision(plumbing.Revision("origin/" + sourceBranch))
	if err != nil {
		sourceHash, err = repo.ResolveRevision(plumbing.Revision(sourceBranch))
		if err != nil {
			return "", fmt.Errorf("LocalDiffFetcher: resolve source branch %q: %w", sourceBranch, err)
		}
	}

	targetHash, err := repo.ResolveRevision(plumbing.Revision("origin/" + targetBranch))
	if err != nil {
		targetHash, err = repo.ResolveRevision(plumbing.Revision(targetBranch))
		if err != nil {
			return "", fmt.Errorf("LocalDiffFetcher: resolve target branch %q: %w", targetBranch, err)
		}
	}

	sourceCommit, err := repo.CommitObject(*sourceHash)
	if err != nil {
		return "", fmt.Errorf("LocalDiffFetcher: get source commit: %w", err)
	}
	targetCommit, err := repo.CommitObject(*targetHash)
	if err != nil {
		return "", fmt.Errorf("LocalDiffFetcher: get target commit: %w", err)
	}

	patch, err := targetCommit.Patch(sourceCommit)
	if err != nil {
		return "", fmt.Errorf("LocalDiffFetcher: generate patch: %w", err)
	}

	return patch.String(), nil
}
