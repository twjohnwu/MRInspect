package chunk

import (
	"strings"
	"testing"
)

// headings returns the Heading field of every chunk, in order, for
// convenient bulk assertions.
func headings(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Heading
	}
	return out
}

// TestChunk_HeadingBreadcrumbAndLines verifies REQ-03 / S-08: chunking a
// markdown file with H1/H2/H3 nesting produces one chunk per section, each
// carrying an "H1 > H2 > H3" breadcrumb and start_line/end_line that point
// at the section's actual lines in the source (heading line through the
// last non-blank line before the next heading or EOF).
func TestChunk_HeadingBreadcrumbAndLines(t *testing.T) {
	src := "# Guide\n" + // line 1
		"Intro line.\n" + // line 2
		"\n" + // line 3
		"## Setup\n" + // line 4
		"Setup body line 1.\n" + // line 5
		"Setup body line 2.\n" + // line 6
		"\n" + // line 7
		"### Prerequisites\n" + // line 8
		"Need prereq A.\n" + // line 9
		"Need prereq B.\n" + // line 10
		"\n" + // line 11
		"## Usage\n" + // line 12
		"Usage body.\n" // line 13

	chunks, err := Markdown(src)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}

	wantHeadings := []string{
		"Guide",
		"Guide > Setup",
		"Guide > Setup > Prerequisites",
		"Guide > Usage",
	}
	got := headings(chunks)
	if len(got) != len(wantHeadings) {
		t.Fatalf("Markdown: chunk count = %d (%v), want %d (%v)", len(got), got, len(wantHeadings), wantHeadings)
	}
	for i, want := range wantHeadings {
		if got[i] != want {
			t.Errorf("chunk[%d].Heading = %q, want %q", i, got[i], want)
		}
	}

	// The H3 "Prerequisites" section is the one whose boundaries are least
	// ambiguous to compute by hand (nested three deep, blank line on both
	// sides): its start_line is the "### Prerequisites" line itself, its
	// end_line is the last body line before the trailing blank line.
	prereq := chunks[2]
	if prereq.StartLine != 8 {
		t.Errorf("chunks[2].StartLine = %d, want 8", prereq.StartLine)
	}
	if prereq.EndLine != 10 {
		t.Errorf("chunks[2].EndLine = %d, want 10", prereq.EndLine)
	}

	// TokenEst per REQ-14's formula (ceil(asciiBytes/4.0), no CJK/other
	// runes here): Text is "### Prerequisites\nNeed prereq A.\nNeed prereq
	// B." — 47 ASCII bytes, ceil(47/4.0) = 12.
	if prereq.TokenEst != 12 {
		t.Errorf("chunks[2].TokenEst = %d, want 12 (REQ-14 formula on 47 ASCII bytes)", prereq.TokenEst)
	}
	if !strings.Contains(prereq.Text, "Prerequisites") {
		t.Errorf("chunks[2].Text = %q, want it to contain the section body", prereq.Text)
	}

	usage := chunks[3]
	if usage.StartLine != 12 {
		t.Errorf("chunks[3].StartLine = %d, want 12", usage.StartLine)
	}
	if usage.EndLine != 13 {
		t.Errorf("chunks[3].EndLine = %d, want 13", usage.EndLine)
	}
}

// TestChunk_FencedCodeIsOpaque verifies REQ-03 / S-09: a line beginning
// with '#' inside a fenced code block must not be treated as a heading and
// must not create a chunk boundary — it stays part of its enclosing
// section.
func TestChunk_FencedCodeIsOpaque(t *testing.T) {
	src := "# Doc\n" + // line 1
		"\n" + // line 2
		"## Commands\n" + // line 3
		"\n" + // line 4
		"```bash\n" + // line 5
		"#!/bin/bash\n" + // line 6 — looks like a heading, is not
		"echo hi\n" + // line 7
		"```\n" + // line 8
		"\n" + // line 9
		"## Next\n" + // line 10
		"More text.\n" // line 11

	chunks, err := Markdown(src)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}

	wantHeadings := []string{
		"Doc",
		"Doc > Commands",
		"Doc > Next",
	}
	got := headings(chunks)
	if len(got) != len(wantHeadings) {
		t.Fatalf("Markdown: chunk count = %d (%v), want %d (%v) — a fenced '#!/bin/bash' line must not open a new chunk", len(got), got, len(wantHeadings), wantHeadings)
	}
	for i, want := range wantHeadings {
		if got[i] != want {
			t.Errorf("chunk[%d].Heading = %q, want %q", i, got[i], want)
		}
	}

	commands := chunks[1]
	if commands.StartLine != 3 {
		t.Errorf("chunks[1].StartLine = %d, want 3", commands.StartLine)
	}
	if commands.EndLine != 8 {
		t.Errorf("chunks[1].EndLine = %d, want 8 (fenced block, including the '#!/bin/bash' line, stays inside this section)", commands.EndLine)
	}
	if !strings.Contains(commands.Text, "#!/bin/bash") {
		t.Errorf("chunks[1].Text = %q, want it to still contain the fenced '#!/bin/bash' line", commands.Text)
	}
}
