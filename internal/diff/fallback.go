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
	result, err := f.primary.Fetch(ctx, sourceBranch, targetBranch)
	if err != nil {
		f.log.Warn("primary diff fetch failed, falling back to API", "error", err)
		return f.secondary.Fetch(ctx, sourceBranch, targetBranch)
	}
	return result, nil
}

// compile-time check that FallbackDiffFetcher satisfies IDiffFetcher
var _ interfaces.IDiffFetcher = (*FallbackDiffFetcher)(nil)

// compile-time checks that local/api fetchers satisfy IDiffFetcher
var _ interfaces.IDiffFetcher = (*LocalDiffFetcher)(nil)
var _ interfaces.IDiffFetcher = (*APIDiffFetcher)(nil)
