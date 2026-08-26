// Package chunk splits resource-set files into indexed chunks per REQ-03:
// markdown is heading-aware, structured (OpenAPI) splits per operation, and
// lines is the size-bounded fallback.
package chunk

// Chunk is one indexed piece of a source file. Field shapes mirror the
// Chunk type defined by REQ-04 (internal/rag's Retriever interface); the
// two aren't the same Go type yet because internal/rag's root package has
// not been created by an earlier task.
type Chunk struct {
	Text      string
	Heading   string // "H1 > H2 > H3"; empty when not applicable
	StartLine int    // 1-based; 0 when not applicable
	EndLine   int    // 0 when not applicable
	TokenEst  int    // REQ-14's estimator formula; must be > 0
}
