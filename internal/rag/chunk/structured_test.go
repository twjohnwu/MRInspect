package chunk

import "testing"

// structuredHeadings returns the Heading field of every chunk, as a
// set (map[heading]count), for order-independent comparisons — S-10 does
// not require the three operation chunks in any particular order.
func structuredHeadings(chunks []Chunk) map[string]int {
	out := make(map[string]int, len(chunks))
	for _, c := range chunks {
		out[c.Heading]++
	}
	return out
}

// TestChunk_OpenAPIPerOperation verifies REQ-03 / S-10: an OpenAPI document
// with two paths and three operations total yields exactly three operation
// chunks, each with a "paths > <path> > <method>" heading, and a long
// operation is not re-split by any size limit.
func TestChunk_OpenAPIPerOperation(t *testing.T) {
	longSummary := ""
	for i := 0; i < 400; i++ {
		longSummary += "order lookup by id, extended description filler. "
	}

	src := "openapi: 3.0.0\n" + // line 1
		"paths:\n" + // line 2
		"  /orders:\n" + // line 3
		"    get:\n" + // line 4
		"      summary: List orders\n" + // line 5
		"      responses:\n" + // line 6
		"        \"200\":\n" + // line 7
		"          description: OK\n" + // line 8
		"    post:\n" + // line 9
		"      summary: Create order\n" + // line 10
		"      responses:\n" + // line 11
		"        \"201\":\n" + // line 12
		"          description: Created\n" + // line 13
		"  /orders/{id}:\n" + // line 14
		"    post:\n" + // line 15 — the long operation
		"      summary: " + longSummary + "\n" + // line 16
		"      responses:\n" + // line 17
		"        \"200\":\n" + // line 18
		"          description: OK\n" // line 19

	path := "openapi/orders.yaml"
	result, err := Structured(path, src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}

	wantHeadings := map[string]int{
		"paths > /orders > get":       1,
		"paths > /orders > post":      1,
		"paths > /orders/{id} > post": 1,
	}
	got := structuredHeadings(result.Chunks)
	if len(result.Chunks) != 3 {
		t.Fatalf("Structured: chunk count = %d (%v), want 3 (%v)", len(result.Chunks), got, wantHeadings)
	}
	for heading, wantCount := range wantHeadings {
		if got[heading] != wantCount {
			t.Errorf("Structured: heading %q count = %d, want %d (full set: %v)", heading, got[heading], wantCount, got)
		}
	}

	// The long operation ("paths > /orders/{id} > post") must still be
	// exactly one chunk — if a size limit re-split it, this heading would
	// appear more than once (asserted above via wantCount == 1) and total
	// chunk count would exceed 3 (asserted above via len == 3).
	var longOp *Chunk
	for i := range result.Chunks {
		if result.Chunks[i].Heading == "paths > /orders/{id} > post" {
			longOp = &result.Chunks[i]
		}
	}
	if longOp == nil {
		t.Fatalf("Structured: no chunk found with heading %q", "paths > /orders/{id} > post")
	}
	if len(longOp.Text) < len(longSummary) {
		t.Errorf("Structured: long operation chunk Text is shorter than the injected long summary alone (%d bytes) — got %d bytes; the operation must carry its full content whole", len(longSummary), len(longOp.Text))
	}

	// Line-number decision (see dispatch report): gopkg.in/yaml.v3 exposes
	// yaml.Node.Line cheaply while walking the parsed document, so
	// StartLine is asserted for the operation whose source line is known
	// by construction above ("post:" of /orders/{id}, line 15). EndLine is
	// NOT asserted: computing an operation's exact closing line requires
	// locating the next sibling node (or EOF) and is not a field the
	// parser hands back directly — S-10 does not state this requirement,
	// so it is not invented here.
	for _, c := range result.Chunks {
		if c.Heading == "paths > /orders/{id} > post" {
			if c.StartLine != 15 {
				t.Errorf("Structured: /orders/{id} > post StartLine = %d, want 15", c.StartLine)
			}
		}
	}
}

// TestChunk_UnparseableFallsBackAndReports verifies REQ-03 / S-11: a
// syntactically invalid YAML file, in a set configured strategy:
// structured, falls back to lines chunking (producing usable, non-empty
// chunks) and is named in a failures list.
//
// IndexStats does not exist yet (it belongs to T09's indexer, per T05's
// precedent of a local result type) — this test asserts against
// Structured's own Result.Failures. T09 must aggregate this slice into
// IndexStats.Failures for S-11's literal wording ("IndexStats.Failures 含
// 一筆指名該檔的紀錄") to hold end to end; that aggregation is out of scope
// for this task and is not asserted here.
func TestChunk_UnparseableFallsBackAndReports(t *testing.T) {
	// Unterminated double-quoted scalar: syntactically invalid YAML.
	src := "openapi: 3.0.0\n" +
		"paths: \"unterminated\n" +
		"  /orders:\n"

	path := "openapi/broken.yaml"
	result, err := Structured(path, src)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatalf("Structured: fallback produced zero chunks for an unparseable file, want at least one (lines fallback must be usable)")
	}

	var found *Failure
	for i := range result.Failures {
		if result.Failures[i].Path == path {
			found = &result.Failures[i]
		}
	}
	if found == nil {
		t.Fatalf("Structured: Failures = %v, want an entry naming %q", result.Failures, path)
	}
	if found.Reason != FailureReasonUnparseable {
		t.Errorf("Structured: Failures[%q].Reason = %q, want %q", path, found.Reason, FailureReasonUnparseable)
	}
}
