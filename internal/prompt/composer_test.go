package prompt

import (
	"strings"
	"testing"
	"text/template"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
)

func testProject() project.LoadedProject {
	return project.LoadedProject{
		System: project.SystemProject{
			Name:        "Test System",
			Description: "A test system for unit tests.",
			Frameworks:  []string{"Go", "PostgreSQL"},
		},
		ResolvedServiceType: "backend",
		SharedDocContents: []project.DocFile{
			{Filename: "coding-standards.md", Content: "# Standards\n\nWrite clean code.\n"},
		},
		SystemDocContents: []project.DocFile{
			{Filename: "review-focus.md", Content: "# Focus\n\nCheck transactions.\n"},
		},
	}
}

func TestOutputFormatTemplateIsValid(t *testing.T) {
	tmpl, err := template.New("t").Parse(outputFormatTemplate)
	if err != nil {
		t.Fatalf("outputFormatTemplate is invalid Go template syntax: %v", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateData{
		PRNumber:            42,
		PRTitle:             "Test MR",
		Author:              "Alice",
		SourceBranch:        "feat",
		TargetBranch:        "main",
		ServiceName:         "my-service",
		SystemName:          "My System",
		Date:                "2026-01-01",
		StandardsReferenced: "coding-standards.md",
	}); err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	out := buf.String()
	for _, s := range []string{
		"### Findings",
		"### Details",
		"#### High",
		"#### Medium",
		"#### Low",
		"### Verdict",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("template output missing required section %q", s)
		}
	}
}

func TestComposeReviewPrompt(t *testing.T) {
	c := NewComposer()
	p := testProject()
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n+fix\n"

	mr := gitlab.MergeRequest{
		IID:          42,
		Title:        "Add fermentation timer",
		Author:       gitlab.Author{Name: "Alice"},
		SourceBranch: "feature/timer",
		TargetBranch: "main",
	}

	out, err := c.ComposeReviewPrompt(p, diff, mr)
	if err != nil {
		t.Fatalf("ComposeReviewPrompt: %v", err)
	}

	for _, s := range []string{
		"## Code Review: MR !42",
		"### Findings",
		"### Details",
		"#### High",
		"#### Medium",
		"#### Low",
		"### Verdict",
		"### Production Readiness",
		"Test System",
		diff,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("ComposeReviewPrompt output missing %q", s)
		}
	}
}

func TestComposeSelfReflectionPrompt(t *testing.T) {
	c := NewComposer()
	p := testProject()
	reviewContent := "## Code Review: MR !42\n### Findings\n...\n### Verdict\nLGTM\n"

	out := c.ComposeSelfReflectionPrompt(p, reviewContent)

	if !strings.Contains(out, "REVIEW VALIDATED") {
		t.Error("self-reflection prompt missing 'REVIEW VALIDATED' instruction")
	}
	if !strings.Contains(out, reviewContent) {
		t.Error("self-reflection prompt missing original review content")
	}
}
