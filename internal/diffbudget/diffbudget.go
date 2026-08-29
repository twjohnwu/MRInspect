package diffbudget

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/rag/chunk"
)

// DefaultNonReviewablePatterns are pure path patterns (matched against each
// changed file's NewPath, and OldPath for deletions) for files that are
// never worth reviewing and are dropped first when a diff must shrink.
// This is a path-pattern match only — no content or magic-byte inspection,
// so a hand-written file that merely shares one of these names/paths is
// dropped too.
var DefaultNonReviewablePatterns = []string{
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "poetry.lock",
	"Gemfile.lock", "composer.lock",
	"**/__snapshots__/**", "*.snap", "*.generated.*", "*.pb.ts", "*.pb.go",
	"dist/**", "build/**", "*.min.js", "*.map", "vendor/**",
}

const (
	ReasonNonReviewable = "non-reviewable"
	ReasonSizeBudget    = "size budget"
)

// DroppedFile records one whole file removed from a diff to fit budget.
type DroppedFile struct {
	Path     string
	Reason   string
	TokenEst int
}

// WarningLogger is the minimal logging seam for reduction warnings (mirrors
// lane.WarningLogger's shape so *logger.Logger satisfies it structurally).
type WarningLogger interface {
	Warn(string, ...any)
}

// Options configures Reduce.
type Options struct {
	// ModelBudget is the caller-supplied per-model token budget (from
	// prompt.PromptBudgetForModel for the effective model). Reduce applies
	// the MRI_DIFF_PROMPT_SHARE fraction on top of it to leave headroom for
	// the rest of the prompt (instructions, resources, framing).
	ModelBudget int
	// MaxDiffSizeKB is the existing byte-size backstop
	// (config.ValidationConfig.MaxDiffSizeKB). <= 0 disables this backstop.
	MaxDiffSizeKB float64
	// NonReviewablePatterns overrides DefaultNonReviewablePatterns when non-nil.
	NonReviewablePatterns []string
	Logger                WarningLogger
}

// Reduce drops whole changed files until the diff fits both configured budgets.
func Reduce(changes []gitlab.Change, opts Options) ([]gitlab.Change, []DroppedFile, error) {
	patterns := opts.NonReviewablePatterns
	if patterns == nil {
		patterns = DefaultNonReviewablePatterns
	}

	share := 0.85
	if raw := os.Getenv("MRI_DIFF_PROMPT_SHARE"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			if opts.Logger != nil {
				opts.Logger.Warn(fmt.Sprintf("invalid MRI_DIFF_PROMPT_SHARE %q, using default 0.85", raw))
			}
		} else {
			share = parsed
		}
	}
	budget := int(float64(opts.ModelBudget) * share)

	totalTokens := 0
	totalBytes := 0
	for _, change := range changes {
		totalTokens += chunk.TokenEst(change.Diff)
		totalBytes += len(change.Diff)
	}
	if fits(totalTokens, totalBytes, budget, opts.MaxDiffSizeKB) {
		return changes, nil, nil
	}

	kept := make([]gitlab.Change, 0, len(changes))
	dropped := make([]DroppedFile, 0)
	for _, change := range changes {
		tokens := chunk.TokenEst(change.Diff)
		if matchesAnyPath(change, patterns) {
			dropped = append(dropped, DroppedFile{
				Path:     changePath(change),
				Reason:   ReasonNonReviewable,
				TokenEst: tokens,
			})
			totalTokens -= tokens
			totalBytes -= len(change.Diff)
			continue
		}
		kept = append(kept, change)
	}

	for !fits(totalTokens, totalBytes, budget, opts.MaxDiffSizeKB) && len(kept) > 0 {
		largest := 0
		for index := 1; index < len(kept); index++ {
			if len(kept[index].Diff) > len(kept[largest].Diff) {
				largest = index
			}
		}
		change := kept[largest]
		tokens := chunk.TokenEst(change.Diff)
		dropped = append(dropped, DroppedFile{
			Path:     changePath(change),
			Reason:   ReasonSizeBudget,
			TokenEst: tokens,
		})
		totalTokens -= tokens
		totalBytes -= len(change.Diff)
		kept = append(kept[:largest], kept[largest+1:]...)
	}

	if !fits(totalTokens, totalBytes, budget, opts.MaxDiffSizeKB) {
		return kept, dropped, fmt.Errorf("diffbudget: diff cannot fit budget even after dropping all reducible files (tokens=%d, budget=%d)", totalTokens, budget)
	}
	return kept, dropped, nil
}

func fits(tokens, bytes, budget int, maxDiffSizeKB float64) bool {
	return tokens <= budget && (maxDiffSizeKB <= 0 || float64(bytes)/1024 <= maxDiffSizeKB)
}

func matchesAnyPath(change gitlab.Change, patterns []string) bool {
	for _, pattern := range patterns {
		if matchesPath(change.NewPath, pattern) {
			return true
		}
		if change.OldPath != change.NewPath && matchesPath(change.OldPath, pattern) {
			return true
		}
	}
	return false
}

func matchesPath(candidatePath, pattern string) bool {
	candidatePath = filepath.ToSlash(candidatePath)
	pattern = filepath.ToSlash(pattern)

	if !strings.Contains(pattern, "/") {
		matched, err := filepath.Match(pattern, filepath.Base(candidatePath))
		return err == nil && matched
	}
	if strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
		segment := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
		for _, candidateSegment := range strings.Split(candidatePath, "/") {
			if candidateSegment == segment {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return candidatePath == prefix || strings.HasPrefix(candidatePath, prefix+"/")
	}
	return false
}

func changePath(change gitlab.Change) string {
	if change.NewPath != "" {
		return change.NewPath
	}
	return change.OldPath
}

// Trailer renders a deterministic, clearly-marked comment block disclosing
// which whole files were dropped and why. It is meant to be appended to the
// diff string itself so every prompt path that consumes that diff string
// (the single-mode prompt and every per-lane prompt) inherits the
// disclosure without any change to prompt-composition code.
func Trailer(dropped []DroppedFile) string {
	if len(dropped) == 0 {
		return ""
	}

	var trailer strings.Builder
	trailer.WriteString("\n\n<!-- mrinspect:diff-reduction -->\n")
	trailer.WriteString("Excluded from this diff (diff size budget):\n")
	for _, drop := range dropped {
		fmt.Fprintf(&trailer, "- %s (%s)\n", drop.Path, drop.Reason)
	}
	trailer.WriteString("<!-- /mrinspect:diff-reduction -->\n")
	return trailer.String()
}
