package prompt

import (
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/validator"
)

var testMR = gitlab.MergeRequest{
	IID:          42,
	Title:        "Add fermentation timer",
	Author:       gitlab.Author{Name: "Alice"},
	SourceBranch: "feature/timer",
	TargetBranch: "main",
}

var testSvc = config.ServiceConfig{Name: "dough-service", Type: "backend"}

var testProvider = config.AIProvider("gemini")

func testDiff() string {
	return "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n+fix\n"
}

func testValidatorForPrompt() *validator.Validator {
	return validator.New(config.Config{
		Validation: config.ValidationConfig{
			AllowedExtensions: []string{".go", ".ts", ".md"},
			MaxFilesChanged:   50,
			MaxDiffSizeKB:     300,
		},
	})
}

func TestSelectTemplate(t *testing.T) {
	// Each entry maps a service type to a string unique to that template's focus areas.
	cases := []struct {
		serviceType string
		wantToken   string
	}{
		{"frontend", "React"},
		{"ai", "Prompt engineering"},
		{"iac", "State management"},
		{"terraform", "State management"},
		{"backend", "API design"},
		{"unknown", "Code quality"},
	}

	for _, tc := range cases {
		t.Run(tc.serviceType, func(t *testing.T) {
			fn := SelectTemplate(tc.serviceType, config.Config{})
			out := fn(testDiff(), testMR, testSvc, testProvider)
			if !strings.Contains(out, tc.wantToken) {
				t.Errorf("SelectTemplate(%q) output missing %q", tc.serviceType, tc.wantToken)
			}
		})
	}
}

func TestTemplateGenerators(t *testing.T) {
	v := testValidatorForPrompt()

	generators := []struct {
		name string
		fn   func(string, gitlab.MergeRequest, config.ServiceConfig, config.AIProvider) string
	}{
		{"backend", GenerateBackendTemplate},
		{"frontend", GenerateFrontendTemplate},
		{"ai", GenerateAITemplate},
		{"terraform", GenerateTerraformTemplate},
		{"comprehensive", GenerateComprehensiveTemplate},
	}

	requiredSections := []string{
		"### Findings",
		"#### High",
		"#### Medium",
		"#### Low",
		"### Verdict",
		testMR.Title,
		testSvc.Name,
	}

	for _, g := range generators {
		t.Run(g.name, func(t *testing.T) {
			out := g.fn(testDiff(), testMR, testSvc, testProvider)
			for _, s := range requiredSections {
				if !strings.Contains(out, s) {
					t.Errorf("%s template missing %q", g.name, s)
				}
			}
			if err := v.ValidateReviewContent(out); err != nil {
				t.Errorf("%s template fails ValidateReviewContent: %v", g.name, err)
			}
		})
	}
}
