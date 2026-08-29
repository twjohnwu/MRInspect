package chunk

import (
	"strings"
	"testing"
)

func structuredChunk(t *testing.T, chunks []Chunk, heading string) Chunk {
	t.Helper()
	for _, c := range chunks {
		if c.Heading == heading {
			return c
		}
	}
	t.Fatalf("Structured: no chunk with heading %q", heading)
	return Chunk{}
}

// TestStructured_BoundarySiblingOperations verifies REQ-03 / T26: adjacent
// operations have parser-derived boundaries and do not consume each other.
func TestStructured_BoundarySiblingOperations(t *testing.T) {
	src := "paths:\n" +
		"  /pets:\n" +
		"    get:\n" +
		"      summary: List pets\n" +
		"    post:\n" +
		"      summary: Create pet\n"
	result, err := Structured("openapi.yaml", src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	get := structuredChunk(t, result.Chunks, "paths > /pets > get")
	post := structuredChunk(t, result.Chunks, "paths > /pets > post")
	if strings.Contains(get.Text, "Create pet") || strings.Contains(post.Text, "List pets") {
		t.Errorf("sibling operation boundaries crossed: get=%q post=%q", get.Text, post.Text)
	}
}

// TestStructured_BoundaryBeforeComponents verifies REQ-03 / T26: the last
// operation ends at its parsed subtree, before a following components mapping.
func TestStructured_BoundaryBeforeComponents(t *testing.T) {
	src := "paths:\n" +
		"  /pets:\n" +
		"    get:\n" +
		"      summary: List pets\n" +
		"components:\n" +
		"  schemas:\n" +
		"    Pet:\n" +
		"      type: object\n"
	result, err := Structured("openapi.yaml", src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	op := structuredChunk(t, result.Chunks, "paths > /pets > get")
	if strings.Contains(op.Text, "components:") || strings.Contains(op.Text, "schemas:") || strings.Contains(op.Text, "type: object") {
		t.Errorf("operation chunk swallowed components: %q", op.Text)
	}
}

// TestStructured_BoundaryFlowCollectionAtColumnZero verifies REQ-03 / T26:
// parser subtree lines, not indentation heuristics, retain a flow collection
// continuation and the subsequent operation field.  Coverage intentionally
// covers every operation-content line (starting at get:); the paths scaffold
// is not itself an operation chunk.
func TestStructured_BoundaryFlowCollectionAtColumnZero(t *testing.T) {
	src := "paths:\n" +
		"  /pets:\n" +
		"    get:\n" +
		"      tags: [a,\n" +
		"b]\n" +
		"      summary: List pets\n"
	result, err := Structured("openapi.yaml", src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if len(result.Chunks) != 1 {
		t.Fatalf("Structured: chunk count = %d, want 1", len(result.Chunks))
	}
	for _, line := range strings.Split(src, "\n")[2:] {
		if line == "" {
			continue
		}
		found := false
		for _, c := range result.Chunks {
			if strings.Contains(c.Text, line) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("operation content line %q appears in no chunk", line)
		}
	}
}

// TestStructured_BoundaryBlockScalar verifies REQ-03 / T26: a block scalar's
// continuation lines remain within the operation subtree.
func TestStructured_BoundaryBlockScalar(t *testing.T) {
	src := "paths:\n" +
		"  /pets:\n" +
		"    get:\n" +
		"      description: |\n" +
		"        First description line.\n" +
		"        Second description line.\n" +
		"      summary: List pets\n"
	result, err := Structured("openapi.yaml", src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	op := structuredChunk(t, result.Chunks, "paths > /pets > get")
	for _, want := range []string{"First description line.", "Second description line.", "summary: List pets"} {
		if !strings.Contains(op.Text, want) {
			t.Errorf("operation chunk Text = %q, want %q", op.Text, want)
		}
	}
}

// TestStructured_BoundaryLastOperationAtEOF verifies REQ-03 / T26: an
// operation ending at EOF is retained even when the document has no newline.
func TestStructured_BoundaryLastOperationAtEOF(t *testing.T) {
	src := "paths:\n" +
		"  /pets:\n" +
		"    get:\n" +
		"      summary: List pets"
	result, err := Structured("openapi.yaml", src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	op := structuredChunk(t, result.Chunks, "paths > /pets > get")
	if !strings.Contains(op.Text, "summary: List pets") {
		t.Errorf("operation chunk Text = %q, want EOF summary", op.Text)
	}
}
