package reviewer

import "testing"

func TestBuildPromptBreakdown_Format(t *testing.T) {
	got := BuildPromptBreakdown([]Section{
		{Name: "base prompt", TokenEst: 30},
		{Name: "diff", TokenEst: 70},
	})

	want := "Prompt composition breakdown (estimated tokens per section):\n" +
		"| Section | Tokens | % of total |\n" +
		"|---------|--------|------------|\n" +
		"| base prompt | 30 | 30.0% |\n" +
		"| diff | 70 | 70.0% |\n" +
		"| **total** | 100 | 100.0% |\n"

	if got != want {
		t.Errorf("BuildPromptBreakdown() =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildPromptBreakdown_EmptySections(t *testing.T) {
	got := BuildPromptBreakdown(nil)
	want := "Prompt composition breakdown (estimated tokens per section):\n" +
		"| Section | Tokens | % of total |\n" +
		"|---------|--------|------------|\n" +
		"| **total** | 0 | 100.0% |\n"

	if got != want {
		t.Errorf("BuildPromptBreakdown(nil) =\n%q\nwant\n%q", got, want)
	}
}
