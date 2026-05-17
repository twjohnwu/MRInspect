package errors

import (
	"errors"
	"fmt"
	"strings"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
	"mrinspect/internal/validator"
)

type Category string

const (
	CategoryConfiguration Category = "configuration"
	CategoryAPI           Category = "api"
	CategoryValidation    Category = "validation"
	CategorySystem        Category = "system"
	CategoryUnknown       Category = "unknown"
)

type Handler struct {
	cfg config.Config
	log *logger.Logger
}

func NewHandler(cfg config.Config, log *logger.Logger) *Handler {
	return &Handler{cfg: cfg, log: log}
}

func (h *Handler) Categorize(err error) Category {
	if err == nil {
		return CategoryUnknown
	}
	var ve *validator.ValidationError
	if errors.As(err, &ve) {
		return CategoryValidation
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "api_provider_key") || strings.Contains(msg, "gitlab_token") ||
		strings.Contains(msg, "configuration") || strings.Contains(msg, "missing required"):
		return CategoryConfiguration
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "429") || strings.Contains(msg, "503") ||
		strings.Contains(msg, "api error") || strings.Contains(msg, "connection refused"):
		return CategoryAPI
	case strings.Contains(msg, "no such file") || strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "git") || strings.Contains(msg, "repository"):
		return CategorySystem
	default:
		return CategoryUnknown
	}
}

func (h *Handler) ShouldPost(err error, cat Category) bool {
	if err == nil {
		return false
	}
	if !h.cfg.ErrorReporting.PostToMR {
		return false
	}
	// Don't post for transient API rate limits — they resolve automatically
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") {
		return false
	}
	return true
}

func (h *Handler) GenerateMessage(err error, stage string, cat Category) string {
	friendly := h.FriendlyStage(stage)
	steps := h.TroubleshootingSteps(cat, err)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## MRInspect: Review Failed\n\n"))
	sb.WriteString(fmt.Sprintf("An error occurred during **%s**.\n\n", friendly))
	sb.WriteString(fmt.Sprintf("**Error**: `%s`\n\n", truncate(err.Error(), h.cfg.ErrorReporting.MaxErrorMessageLength)))
	sb.WriteString(fmt.Sprintf("**Category**: %s\n\n", cat))

	if len(steps) > 0 {
		sb.WriteString("**Troubleshooting**:\n")
		for _, s := range steps {
			sb.WriteString(fmt.Sprintf("- %s\n", s))
		}
	}
	return sb.String()
}

func (h *Handler) FriendlyStage(stage string) string {
	stages := map[string]string{
		"validateSystem":  "system validation",
		"setupRepository": "repository setup",
		"fetchMRDetails":  "fetching MR details",
		"fetchDiff":       "fetching code changes",
		"generateReview":  "generating AI review",
		"selfReflect":     "self-reflection validation",
		"postReview":      "posting review comment",
	}
	if friendly, ok := stages[stage]; ok {
		return friendly
	}
	return stage
}

func (h *Handler) TroubleshootingSteps(cat Category, err error) []string {
	switch cat {
	case CategoryConfiguration:
		return []string{
			"Verify GITLAB_TOKEN and AI_PROVIDER_KEY are set in CI/CD variables",
			"Check that the token has API access to the target project",
		}
	case CategoryAPI:
		return []string{
			"Check the AI provider status page for outages",
			"Verify the GitLab API is reachable from the CI runner",
			"The job will succeed automatically on next pipeline run",
		}
	case CategoryValidation:
		return []string{
			"Ensure CI_PROJECT_ID and CI_MERGE_REQUEST_IID are set",
			"Check that the diff is within the 300 KB size limit",
		}
	case CategorySystem:
		return []string{
			"Verify the CI runner has git access to the repository",
			"Check file system permissions on the projects directory",
		}
	default:
		return []string{
			"Check the job logs for more details",
			"Contact the platform team if the issue persists",
		}
	}
}

func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
