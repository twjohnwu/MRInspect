package prompt

import (
	"fmt"
	"strings"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
)

func GenerateBackendTemplate(diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider) string {
	return buildLegacyPrompt("backend", diff, mr, svc, provider, []string{
		"API design (RESTful conventions, HTTP status codes)",
		"Database queries (N+1 issues, missing indexes, transaction boundaries)",
		"Error handling (proper error propagation, meaningful messages)",
		"Security (input validation, SQL injection, authorization checks)",
		"TypeScript/NestJS patterns (dependency injection, module structure)",
		"Test coverage (unit tests for business logic, edge cases)",
		"Performance (unnecessary computations, missing caching)",
	})
}

func GenerateFrontendTemplate(diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider) string {
	return buildLegacyPrompt("frontend", diff, mr, svc, provider, []string{
		"React component design (hooks, props, state management)",
		"Accessibility (ARIA labels, keyboard navigation, semantic HTML)",
		"Performance (unnecessary re-renders, missing memoization)",
		"CSS/styling consistency",
		"Error boundaries and loading states",
		"Type safety (TypeScript props, event handlers)",
	})
}

func GenerateAITemplate(diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider) string {
	return buildLegacyPrompt("ai", diff, mr, svc, provider, []string{
		"Prompt engineering quality (clarity, specificity, edge cases)",
		"Model configuration (temperature, token limits, model selection)",
		"Error handling for AI API calls (rate limits, timeouts, fallbacks)",
		"Input validation and output parsing",
		"Cost optimization (token usage, caching strategies)",
		"Evaluation coverage",
	})
}

func GenerateTerraformTemplate(diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider) string {
	return buildLegacyPrompt("iac", diff, mr, svc, provider, []string{
		"Resource naming conventions",
		"Security groups and IAM permissions (least privilege)",
		"State management and remote backends",
		"Module structure and reusability",
		"Tagging standards",
		"Cost implications of resource choices",
		"Dependency management and ordering",
	})
}

func GenerateComprehensiveTemplate(diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider) string {
	return buildLegacyPrompt("general", diff, mr, svc, provider, []string{
		"Code quality and maintainability",
		"Security vulnerabilities",
		"Performance issues",
		"Error handling",
		"Test coverage",
		"Documentation",
	})
}

func SelectTemplate(serviceType string, _ config.Config) func(string, gitlab.MergeRequest, config.ServiceConfig, config.AIProvider) string {
	switch strings.ToLower(serviceType) {
	case "frontend":
		return GenerateFrontendTemplate
	case "ai":
		return GenerateAITemplate
	case "iac", "terraform":
		return GenerateTerraformTemplate
	case "backend":
		return GenerateBackendTemplate
	default:
		return GenerateComprehensiveTemplate
	}
}

func buildLegacyPrompt(svcType, diff string, mr gitlab.MergeRequest, svc config.ServiceConfig, provider config.AIProvider, focusAreas []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are an expert %s code reviewer.\n\n", svcType))
	sb.WriteString(fmt.Sprintf("**MR**: !%d - %s\n", mr.IID, mr.Title))
	sb.WriteString(fmt.Sprintf("**Author**: %s\n", mr.Author.Name))
	sb.WriteString(fmt.Sprintf("**Branch**: %s → %s\n", mr.SourceBranch, mr.TargetBranch))
	sb.WriteString(fmt.Sprintf("**Service**: %s (%s)\n\n", svc.Name, svc.Type))

	if mr.Description != "" {
		sb.WriteString("**Description**:\n")
		sb.WriteString(mr.Description)
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Review Focus Areas\n")
	for _, area := range focusAreas {
		sb.WriteString(fmt.Sprintf("- %s\n", area))
	}

	sb.WriteString("\n## Code Changes\n```diff\n")
	sb.WriteString(diff)
	sb.WriteString("\n```\n\n")

	sb.WriteString(legacyOutputFormat(mr, svc))
	return sb.String()
}

func legacyOutputFormat(mr gitlab.MergeRequest, svc config.ServiceConfig) string {
	return fmt.Sprintf(`## Output Format

Provide your review in this exact structure:

## Code Review: MR !%d
### MR Info
- **Title**: %s
- **Service**: %s
- **Date**: %s

### Findings
| # | Severity | Category | Item | File:Line |
|---|----------|----------|------|-----------|

### Details
#### High
#### Medium
#### Low

### Verdict
<!-- LGTM / Needs Changes / Needs Minor Changes -->
`, mr.IID, mr.Title, svc.Name, time.Now().Format("2006-01-02"))
}
