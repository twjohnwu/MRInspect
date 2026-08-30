package validator

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
)

type ValidationError struct {
	Message string
	Code    string
}

func (e *ValidationError) Error() string { return e.Message }

type DiffValidationResult struct {
	SizeKB         float64
	FilesChanged   int
	SupportedFiles int
}

type Validator struct {
	cfg config.Config
}

func New(cfg config.Config) *Validator {
	return &Validator{cfg: cfg}
}

func (v *Validator) IsRunningInCrossRepoMode() bool {
	return os.Getenv("CI_PIPELINE_SOURCE") == "trigger"
}

func (v *Validator) GetProjectID() string {
	if v.IsRunningInCrossRepoMode() {
		return os.Getenv("MRI_PROJECT_ID")
	}
	return os.Getenv("CI_PROJECT_ID")
}

func (v *Validator) GetMRIID() string {
	if v.IsRunningInCrossRepoMode() {
		return os.Getenv("MRI_MR_IID")
	}
	return os.Getenv("CI_MERGE_REQUEST_IID")
}

func (v *Validator) GetSourceBranch() string {
	if v.IsRunningInCrossRepoMode() {
		return os.Getenv("MRI_SOURCE_BRANCH")
	}
	return os.Getenv("CI_COMMIT_REF_NAME")
}

func (v *Validator) GetTargetBranch() string {
	if v.IsRunningInCrossRepoMode() {
		return os.Getenv("MRI_TARGET_BRANCH")
	}
	return os.Getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME")
}

func (v *Validator) ValidateEnvironment() error {
	required := []string{"AI_PROVIDER_KEY", "GITLAB_TOKEN"}
	if v.IsRunningInCrossRepoMode() {
		required = append(required, "MRI_PROJECT_ID", "MRI_MR_IID", "MRI_SOURCE_BRANCH", "MRI_TARGET_BRANCH")
	} else {
		required = append(required, "CI_PROJECT_ID", "CI_MERGE_REQUEST_IID")
	}
	var missing []string
	for _, k := range required {
		if os.Getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return &ValidationError{
			Code:    "MISSING_ENV_VARS",
			Message: fmt.Sprintf("missing required environment variables: %s", strings.Join(missing, ", ")),
		}
	}
	return nil
}

func (v *Validator) ValidateDiff(diff string) (DiffValidationResult, error) {
	result := DiffValidationResult{
		SizeKB: float64(len(diff)) / 1024,
	}

	if diff == "" {
		return result, &ValidationError{Code: "EMPTY_DIFF", Message: "diff is empty — no changes to review"}
	}

	if result.SizeKB > v.cfg.Validation.MaxDiffSizeKB {
		return result, &ValidationError{
			Code:    "DIFF_TOO_LARGE",
			Message: fmt.Sprintf("diff size %.1f KB exceeds limit %.0f KB", result.SizeKB, v.cfg.Validation.MaxDiffSizeKB),
		}
	}

	// Count files and supported extensions
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") || strings.HasPrefix(line, "+++ /dev/null") {
			result.FilesChanged++
			filename := strings.TrimPrefix(line, "+++ b/")
			if isSupportedExtension(filename, v.cfg.Validation.AllowedExtensions) {
				result.SupportedFiles++
			}
		}
	}

	if result.FilesChanged > v.cfg.Validation.MaxFilesChanged {
		return result, &ValidationError{
			Code:    "TOO_MANY_FILES",
			Message: fmt.Sprintf("diff contains %d files, exceeds limit of %d", result.FilesChanged, v.cfg.Validation.MaxFilesChanged),
		}
	}

	if result.SupportedFiles == 0 && result.FilesChanged > 0 {
		return result, &ValidationError{
			Code:    "NO_SUPPORTED_FILES",
			Message: "diff contains no files with supported extensions",
		}
	}

	return result, nil
}

func (v *Validator) ValidateMergeRequest(mr gitlab.MergeRequest) error {
	if mr.IID == 0 {
		return &ValidationError{Code: "INVALID_MR", Message: "merge request IID is missing"}
	}
	if mr.Title == "" {
		return &ValidationError{Code: "INVALID_MR", Message: "merge request title is empty"}
	}
	return nil
}

func (v *Validator) ValidateReviewContent(content string) error {
	if len(content) < 100 {
		return &ValidationError{Code: "REVIEW_TOO_SHORT", Message: "review content is too short (< 100 chars)"}
	}
	required := []string{"##", "Findings", "Verdict"}
	for _, section := range required {
		if !strings.Contains(content, section) {
			return &ValidationError{
				Code:    "MISSING_SECTION",
				Message: fmt.Sprintf("review is missing required section: %q", section),
			}
		}
	}
	return nil
}

var unsafePattern = regexp.MustCompile(`(?i)(javascript:|on\w+=|<script|</script)`)

func (v *Validator) SanitizeInput(input string) string {
	input = strings.ReplaceAll(input, "<", "&lt;")
	input = strings.ReplaceAll(input, ">", "&gt;")
	return unsafePattern.ReplaceAllString(input, "")
}

func isSupportedExtension(filename string, allowed []string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range allowed {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
