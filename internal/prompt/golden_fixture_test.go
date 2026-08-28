package prompt

import (
	"regexp"
	"strings"
	"testing"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
)

type goldenFixture struct {
	t       *testing.T
	project project.LoadedProject
	diff    string
	mr      gitlab.MergeRequest
}

func newGoldenFixture(t *testing.T) goldenFixture {
	t.Helper()

	return goldenFixture{
		t: t,
		project: project.LoadedProject{
			System: project.SystemProject{
				Name:        "Golden Test System",
				Description: "A fixed system used to capture the pre-RAG prompt contract.",
				Frameworks:  []string{"Go", "PostgreSQL"},
			},
			ResolvedServiceType: "backend",
			SharedDocContents: []project.DocFile{
				{Filename: "coding-standards.md", Content: "# Coding Standards\n\nReturn contextual errors.\n"},
			},
			SystemDocContents: []project.DocFile{
				{Filename: "review-focus.md", Content: "# Review Focus\n\nProtect transaction boundaries.\n"},
			},
		},
		diff: "--- a/internal/payment.go\n+++ b/internal/payment.go\n@@ -10,3 +10,4 @@ func Charge() error {\n+\treturn nil\n }\n",
		mr: gitlab.MergeRequest{
			IID:          314,
			Title:        "Preserve payment transaction errors",
			Description:  "Ensure failed charges retain their original error context.",
			Author:       gitlab.Author{Name: "Golden Fixture"},
			SourceBranch: "fix/payment-errors",
			TargetBranch: "main",
		},
	}
}

var goldenDateLine = regexp.MustCompile(`^- \*\*Date\*\*: \d{4}-\d{2}-\d{2}$`)

// normalizeGoldenDate is the only normalization permitted for the S-27 golden
// comparison; every other byte must match exactly. A missing or duplicate Date
// line is a regression.
func (f goldenFixture) normalizeGoldenDate(s string) string {
	f.t.Helper()

	lines := strings.Split(s, "\n")
	matches := 0
	for i, line := range lines {
		if goldenDateLine.MatchString(line) {
			matches++
			lines[i] = "- **Date**: <DATE>"
		}
	}
	if matches != 1 {
		f.t.Fatalf("golden Date line count = %d, want 1", matches)
	}

	return strings.Join(lines, "\n")
}

func TestGoldenFixtureDeterminism(t *testing.T) {
	fixture := newGoldenFixture(t)
	composer := NewComposer()

	first, err := composer.ComposeReviewPrompt(fixture.project, fixture.diff, fixture.mr)
	if err != nil {
		t.Fatalf("first ComposeReviewPrompt: %v", err)
	}
	second, err := composer.ComposeReviewPrompt(fixture.project, fixture.diff, fixture.mr)
	if err != nil {
		t.Fatalf("second ComposeReviewPrompt: %v", err)
	}
	if first != second {
		t.Fatal("two in-process renders of the golden fixture differ")
	}

	if fixture.normalizeGoldenDate(first) != fixture.normalizeGoldenDate(second) {
		t.Fatal("normalized golden fixture output is not stable")
	}
}
