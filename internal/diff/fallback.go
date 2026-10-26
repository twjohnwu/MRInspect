package diff

import (
	"context"

	"mrinspect/internal/interfaces"
	"mrinspect/internal/logger"
)

// FallbackDiffFetcher tries a primary IDiffFetcher and, on failure, falls back to a secondary.
// This encapsulates the try-local-then-API strategy so the reviewer stays unaware of it.
type FallbackDiffFetcher struct {
	primary   interfaces.IDiffFetcher
	secondary interfaces.IDiffFetcher
	log       *logger.Logger
}

func NewFallbackDiffFetcher(primary, secondary interfaces.IDiffFetcher, log *logger.Logger) *FallbackDiffFetcher {
	return &FallbackDiffFetcher{primary: primary, secondary: secondary, log: log}
}

func (f *FallbackDiffFetcher) Fetch(ctx context.Context, sourceBranch, targetBranch string) (string, error) {
	patch, _, err := f.FetchWithSource(ctx, sourceBranch, targetBranch)
	return patch, err
}

// FetchWithSource reports the source SHA of whichever fetcher produced the
// patch; a fetcher without IDiffSourceFetcher support yields "".
func (f *FallbackDiffFetcher) FetchWithSource(ctx context.Context, sourceBranch, targetBranch string) (string, string, error) {
	result, sha, err := fetchWithSource(ctx, f.primary, sourceBranch, targetBranch)
	if err != nil {
		f.log.Warn("primary diff fetch failed, falling back to API", "error", err)
		return fetchWithSource(ctx, f.secondary, sourceBranch, targetBranch)
	}
	return result, sha, nil
}

func fetchWithSource(ctx context.Context, fetcher interfaces.IDiffFetcher, sourceBranch, targetBranch string) (string, string, error) {
	if sf, ok := fetcher.(interfaces.IDiffSourceFetcher); ok {
		return sf.FetchWithSource(ctx, sourceBranch, targetBranch)
	}
	patch, err := fetcher.Fetch(ctx, sourceBranch, targetBranch)
	return patch, "", err
}

// compile-time check that FallbackDiffFetcher satisfies IDiffFetcher
var _ interfaces.IDiffFetcher = (*FallbackDiffFetcher)(nil)

// compile-time checks that local/api fetchers satisfy IDiffFetcher
var _ interfaces.IDiffFetcher = (*LocalDiffFetcher)(nil)
var _ interfaces.IDiffFetcher = (*APIDiffFetcher)(nil)
