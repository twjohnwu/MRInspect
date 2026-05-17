package prompt

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
)

const outputFormatTemplate = `## Output Format

Provide your review using this exact structure:

## Code Review: MR !{{.PRNumber}}
### MR Info
- **Title**: {{.PRTitle}}
- **Author**: {{.Author}}
- **Branch**: {{.SourceBranch}} → {{.TargetBranch}}
- **Service**: {{.ServiceName}} ({{.SystemName}})
- **Date**: {{.Date}}
- **Standards Referenced**: {{.StandardsReferenced}}

### Scope
| Area | Description | Coverage |
|------|-------------|----------|

### Findings
| # | Severity | Category | Standard | Item | File:Line |
|---|----------|----------|----------|------|-----------|

### Details

Each finding below follows this structure:
> **File**: path/to/file.go:line
> **Standard**: which guideline or rule this violates
> **Why**: why this is a problem
> **Suggestion**: concrete fix or improvement

#### High
<!-- Example:
**Finding 1 — [short title]**
- **File**: internal/service/batch.go:42
- **Standard**: coding-standards.md — Error Handling
- **Why**: The error from db.Begin() is swallowed; a failed transaction will silently corrupt batch state.
- **Suggestion**: Return or wrap the error: if err != nil { return fmt.Errorf("begin tx: %w", err) }
-->

#### Medium
<!-- Same structure as High -->

#### Low
<!-- Same structure as High -->

### Production Readiness
- [ ] No breaking changes without migration path
- [ ] Error handling covers failure cases
- [ ] No secrets or credentials in code

### Positive Observations

### Verdict
<!-- LGTM / Needs Changes / Needs Minor Changes -->
`

type templateData struct {
	PRNumber            int
	PRTitle             string
	Author              string
	SourceBranch        string
	TargetBranch        string
	ServiceName         string
	SystemName          string
	Date                string
	StandardsReferenced string
}

type Composer struct {
	tmpl *template.Template
}

func NewComposer() *Composer {
	t := template.Must(template.New("output").Parse(outputFormatTemplate))
	return &Composer{tmpl: t}
}

func (c *Composer) ComposeReviewPrompt(p project.LoadedProject, diff string, mr gitlab.MergeRequest) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are an expert code reviewer for the **%s** system.\n\n", p.System.Name))
	if p.System.Description != "" {
		sb.WriteString(p.System.Description + "\n\n")
	}
	if len(p.System.Frameworks) > 0 {
		sb.WriteString(fmt.Sprintf("**Frameworks**: %s\n\n", strings.Join(p.System.Frameworks, ", ")))
	}

	catalog := c.buildStandardsCatalog(p)
	if catalog != "" {
		sb.WriteString("## Standards & Guidelines\n\n")
		sb.WriteString(catalog)
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("## Merge Request\n\n"))
	sb.WriteString(fmt.Sprintf("**MR**: !%d — %s\n", mr.IID, mr.Title))
	sb.WriteString(fmt.Sprintf("**Author**: %s\n", mr.Author.Name))
	sb.WriteString(fmt.Sprintf("**Branch**: `%s` → `%s`\n", mr.SourceBranch, mr.TargetBranch))
	sb.WriteString(fmt.Sprintf("**Service**: %s (%s)\n", mr.Title, p.ResolvedServiceType))

	desc := c.extractDescription(mr.Description)
	if desc != "" {
		sb.WriteString("\n**Description**:\n")
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Code Changes\n\n```diff\n")
	sb.WriteString(diff)
	sb.WriteString("\n```\n\n")

	data := templateData{
		PRNumber:            mr.IID,
		PRTitle:             mr.Title,
		Author:              mr.Author.Name,
		SourceBranch:        mr.SourceBranch,
		TargetBranch:        mr.TargetBranch,
		ServiceName:         mr.Title,
		SystemName:          p.System.Name,
		Date:                time.Now().Format("2006-01-02"),
		StandardsReferenced: c.getStandardNames(p),
	}
	var buf bytes.Buffer
	if err := c.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("ComposeReviewPrompt: template: %w", err)
	}
	sb.WriteString(buf.String())

	return sb.String(), nil
}

func (c *Composer) ComposeSelfReflectionPrompt(p project.LoadedProject, reviewContent string) string {
	var sb strings.Builder
	sb.WriteString("You are a senior code review quality assessor.\n\n")
	sb.WriteString("Review the following code review and verify it accurately applies the standards below.\n\n")

	if len(p.SharedDocContents) > 0 || len(p.SystemDocContents) > 0 {
		sb.WriteString("## Standards Applied\n\n")
		sb.WriteString(c.formatDocs(p.SharedDocContents, "Shared Standards"))
		sb.WriteString(c.formatDocs(p.SystemDocContents, "System Standards"))
	}

	sb.WriteString("## Review to Validate\n\n")
	sb.WriteString(reviewContent)
	sb.WriteString("\n\n## Task\n\n")
	sb.WriteString("Identify any findings that are:\n")
	sb.WriteString("- Incorrect or inaccurate\n")
	sb.WriteString("- Missing important issues visible in the diff\n")
	sb.WriteString("- Not aligned with the standards above\n\n")
	sb.WriteString("If the review is accurate and complete, respond with: **REVIEW VALIDATED**\n")
	sb.WriteString("Otherwise, provide corrections in the same review format.\n")

	return sb.String()
}

func (c *Composer) extractDescription(fullDesc string) string {
	if len(fullDesc) > 1000 {
		return fullDesc[:1000] + "..."
	}
	return fullDesc
}

func (c *Composer) formatDocs(docs []project.DocFile, sectionTitle string) string {
	if len(docs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s\n\n", sectionTitle))
	for _, d := range docs {
		sb.WriteString(fmt.Sprintf("#### %s\n\n", d.Filename))
		sb.WriteString(d.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (c *Composer) buildStandardsCatalog(p project.LoadedProject) string {
	all := append(p.SharedDocContents, p.SystemDocContents...)
	if len(all) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, d := range all {
		sb.WriteString(fmt.Sprintf("**%s**\n\n", d.Filename))
		sb.WriteString(d.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (c *Composer) getStandardNames(p project.LoadedProject) string {
	var names []string
	for _, d := range p.SharedDocContents {
		names = append(names, d.Filename)
	}
	for _, d := range p.SystemDocContents {
		names = append(names, d.Filename)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
