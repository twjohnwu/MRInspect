package ragcmd

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// PackageVersion is one generic-package version. CreatedAt is the registry's
// authoritative recency datum; Version is not used to infer recency.
type PackageVersion struct {
	ID        int64
	Version   string
	CreatedAt time.Time
}

// PackageRegistry is the narrow registry seam used by package retention.
// ListPackageVersions returns the versions known to the registry and
// DeletePackage deletes exactly one generic package by its GitLab package ID.
type PackageRegistry interface {
	ListPackageVersions(ctx context.Context) ([]PackageVersion, error)
	DeletePackage(ctx context.Context, packageID int64) error
}

// RetentionResult reports successful deletions, non-fatal cleanup warnings,
// and whether the already-completed publish remains successful.
type RetentionResult struct {
	DeletedPackageIDs []int64
	Warnings          []string
	PublishSucceeded  bool
	Err               error
}

// RunRetention keeps the newest keep package versions according to the
// registry-provided CreatedAt value (REQ-09 / S-50 / S-51).
func RunRetention(ctx context.Context, keep int, registry PackageRegistry) RetentionResult {
	result := RetentionResult{PublishSucceeded: true}
	if keep <= 0 {
		result.Err = fmt.Errorf("retention keep count must be at least 1, got %d", keep)
		return result
	}

	versions, err := registry.ListPackageVersions(ctx)
	if err != nil {
		result.Err = fmt.Errorf("list package versions for retention: %w", err)
		return result
	}

	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].CreatedAt.Before(versions[j].CreatedAt)
	})

	if len(versions) <= keep {
		return result
	}

	for _, version := range versions[:len(versions)-keep] {
		if err := registry.DeletePackage(ctx, version.ID); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("delete package %d during retention: %v", version.ID, err))
			continue
		}
		result.DeletedPackageIDs = append(result.DeletedPackageIDs, version.ID)
	}

	return result
}
