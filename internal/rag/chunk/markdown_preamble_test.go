package chunk

import (
	"strings"
	"testing"
)

// TestChunk_PreambleBeforeFirstHeadingIsKept verifies REQ-03 / T25: prose
// before the first ATX heading is retrievable content, rather than silently
// disappearing during heading-aware chunking.
func TestChunk_PreambleBeforeFirstHeadingIsKept(t *testing.T) {
	src := "This guide explains the service.\n" +
		"Read this introduction before configuring it.\n" +
		"\n" +
		"# Setup\n" +
		"Configure the service here.\n"

	chunks, err := Markdown(src)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("Markdown: chunk count = %d, want 2 (including a preamble chunk)", len(chunks))
	}

	preamble := chunks[0]
	if preamble.Heading != "" {
		t.Errorf("preamble Heading = %q, want empty breadcrumb for content before any heading", preamble.Heading)
	}
	if !strings.Contains(preamble.Text, "This guide explains the service.") ||
		!strings.Contains(preamble.Text, "Read this introduction") {
		t.Errorf("preamble Text = %q, want the pre-heading prose to be retrievable", preamble.Text)
	}
}

// TestChunk_NestedFencesWithDifferentMarkers verifies REQ-03 / T25: a ~~~
// line inside a ``` fence is content, not the closing marker for that fence.
func TestChunk_NestedFencesWithDifferentMarkers(t *testing.T) {
	src := "# Document\n" +
		"## Example\n" +
		"```text\n" +
		"~~~\n" +
		"# not a heading\n" +
		"```\n" +
		"## Next\n" +
		"After the fence.\n"

	chunks, err := Markdown(src)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}

	wantHeadings := []string{"Document", "Document > Example", "Document > Next"}
	if len(chunks) != len(wantHeadings) {
		t.Fatalf("Markdown: chunk count = %d, want %d; mixed fence markers must not create a phantom heading boundary", len(chunks), len(wantHeadings))
	}
	for i, want := range wantHeadings {
		if chunks[i].Heading != want {
			t.Errorf("chunks[%d].Heading = %q, want %q", i, chunks[i].Heading, want)
		}
	}
	if !strings.Contains(chunks[1].Text, "# not a heading") {
		t.Errorf("example chunk Text = %q, want the apparent heading inside the ``` fence", chunks[1].Text)
	}
}
