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

// RenderFunc renders the exact artifact produced for a set of kept and
// dropped files. It lets callers account for headers and the disclosure
// trailer in addition to each GitLab change's Diff body.
type RenderFunc func(kept []gitlab.Change, dropped []DroppedFile) (string, error)

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
	// InitialDiff is the caller's pass-through artifact before any files are
	// dropped. When empty, Reduce preserves its legacy API-body accounting.
	InitialDiff string
	// Render, when non-nil, renders the exact post-reduction artifact for fit
	// checks. Its zero value preserves the legacy API-body accounting.
	Render RenderFunc
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
		if err != nil || parsed <= 0 || parsed > 1 {
			if opts.Logger != nil {
				opts.Logger.Warn(fmt.Sprintf("invalid MRI_DIFF_PROMPT_SHARE %q, using default 0.85", raw))
			}
		} else {
			share = parsed
		}
	}
	budget := int(float64(opts.ModelBudget) * share)

	apiTokens, apiBytes := changeMetrics(changes)
	initialFits := fits(apiTokens, apiBytes, budget, opts.MaxDiffSizeKB)
	// initialDiffTokens/initialDiffBytes measure the caller's pass-through
	// artifact (the local go-git diff, when nothing has been dropped yet).
	// It carries per-file diff headers the API-body sum above does not, so
	// it can fail to fit even when the API sum says it does — and vice
	// versa, since it is a single fixed string unrelated to individual
	// change sizes.
	initialDiffTokens, initialDiffBytes := 0, 0
	if opts.InitialDiff != "" {
		initialDiffTokens = chunk.TokenEst(opts.InitialDiff)
		initialDiffBytes = len(opts.InitialDiff)
		initialFits = initialFits && fits(initialDiffTokens, initialDiffBytes, budget, opts.MaxDiffSizeKB)
	}
	if initialFits {
		return changes, nil, nil
	}

	// Seed the reduction loop with whichever measurement is more
	// restrictive (api-body sum vs. the pass-through artifact): fits() is
	// monotonic in tokens/bytes, so if either measurement alone would fail
	// to fit, the max of the two also fails to fit and the loop below is
	// guaranteed to run at least one iteration. Every iteration after the
	// first re-measures the actual rendered kept form via renderedMetrics,
	// so this seed only decides whether reduction starts, never how much
	// is ultimately dropped.
	totalTokens, totalBytes := apiTokens, apiBytes
	if initialDiffTokens > totalTokens {
		totalTokens = initialDiffTokens
	}
	if initialDiffBytes > totalBytes {
		totalBytes = initialDiffBytes
	}

	kept := append([]gitlab.Change(nil), changes...)
	dropped := make([]DroppedFile, 0)
	for index := 0; index < len(kept) && !fits(totalTokens, totalBytes, budget, opts.MaxDiffSizeKB); {
		change := kept[index]
		tokens := chunk.TokenEst(change.Diff)
		if matchesAnyPath(change, patterns) {
			dropped = append(dropped, DroppedFile{
				Path:     changePath(change),
				Reason:   ReasonNonReviewable,
				TokenEst: tokens,
			})
			kept = append(kept[:index], kept[index+1:]...)
			var err error
			totalTokens, totalBytes, err = renderedMetrics(kept, dropped, opts)
			if err != nil {
				return kept, dropped, fmt.Errorf("diffbudget: render reduced diff: %w", err)
			}
			continue
		}
		index++
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
		kept = append(kept[:largest], kept[largest+1:]...)
		var renderErr error
		totalTokens, totalBytes, renderErr = renderedMetrics(kept, dropped, opts)
		if renderErr != nil {
			return kept, dropped, fmt.Errorf("diffbudget: render reduced diff: %w", renderErr)
		}
	}

	if !fits(totalTokens, totalBytes, budget, opts.MaxDiffSizeKB) {
		return kept, dropped, fmt.Errorf("diffbudget: diff cannot fit budget even after dropping all reducible files (tokens=%d, budget=%d)", totalTokens, budget)
	}
	return kept, dropped, nil
}

func renderedMetrics(kept []gitlab.Change, dropped []DroppedFile, opts Options) (int, int, error) {
	if opts.Render == nil {
		tokens, bytes := changeMetrics(kept)
		return tokens, bytes, nil
	}
	rendered, err := opts.Render(kept, dropped)
	if err != nil {
		return 0, 0, err
	}
	return chunk.TokenEst(rendered), len(rendered), nil
}

func changeMetrics(changes []gitlab.Change) (int, int) {
	totalTokens := 0
	totalBytes := 0
	for _, change := range changes {
		totalTokens += chunk.TokenEst(change.Diff)
		totalBytes += len(change.Diff)
	}
	return totalTokens, totalBytes
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
