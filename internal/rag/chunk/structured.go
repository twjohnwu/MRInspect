package chunk

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// FailureReason names why a structured-strategy file could not be chunked
// with its declared strategy and had to fall back (REQ-03 / S-11).
type FailureReason string

// FailureReasonUnparseable marks a structured-strategy file whose content
// could not be parsed as valid YAML/OpenAPI, so chunking fell back to the
// lines strategy.
const FailureReasonUnparseable FailureReason = "unparseable-fallback-to-lines"

// Failure names one file that fell back to a different chunking strategy
// than the one its resource set declared. It mirrors intake.SkippedFile's
// Path+Reason shape (internal/rag/intake/walk.go).
//
// IndexStats does not exist yet — it belongs to T09's indexer — so
// Structured returns its own local Failures slice rather than referencing
// it, following T05's precedent (WalkResult.Skipped) for the same gap.
// T09 must aggregate this slice into IndexStats.Failures for S-11's
// wording to hold end to end.
type Failure struct {
	Path   string
	Reason FailureReason
}

// Result is Structured's return shape: the chunks produced (from the
// structured strategy, or from the lines fallback), plus any failures
// encountered along the way.
type Result struct {
	Chunks   []Chunk
	Failures []Failure
}

// operation is one "paths.<path>.<method>" entry located while walking the
// parsed YAML document, in source order.
type operation struct {
	heading   string
	startLine int
	value     *yaml.Node
}

// Structured splits an OpenAPI document into one chunk per operation
// (REQ-03 / S-10). If source cannot be parsed, it falls back to the lines
// strategy and records a Failure naming path (REQ-03 / S-11).
func Structured(path, source string) (Result, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(source), &root); err != nil {
		return Result{
			Chunks:   Lines(source),
			Failures: []Failure{{Path: path, Reason: FailureReasonUnparseable}},
		}, nil
	}

	ops := findOperations(&root)
	if len(ops) == 0 {
		return Result{}, nil
	}

	lines := strings.Split(source, "\n")
	chunks := make([]Chunk, len(ops))
	for i, op := range ops {
		endLine := operationEndLine(op.value)
		if endLine < op.startLine {
			endLine = op.startLine
		}
		if endLine > len(lines) {
			endLine = len(lines)
		}
		text := strings.Join(lines[op.startLine-1:endLine], "\n")
		chunks[i] = Chunk{
			Text:      text,
			Heading:   op.heading,
			StartLine: op.startLine,
			TokenEst:  TokenEst(text),
		}
	}
	return Result{Chunks: chunks}, nil
}

// operationEndLine returns the final 1-indexed source line occupied by an
// operation value. It derives that boundary entirely from yaml.v3's parsed
// node tree, so syntactically valid continuation lines are retained even when
// their visual indentation resembles a sibling key. Block scalar values need
// special handling because yaml.v3 records their starting line only.
func operationEndLine(valueNode *yaml.Node) int {
	if valueNode == nil {
		return 0
	}

	maxLine := 0
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil {
			return
		}

		endLine := node.Line
		if node.Kind == yaml.ScalarNode {
			endLine += strings.Count(node.Value, "\n")
		}
		if endLine > maxLine {
			maxLine = endLine
		}

		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(valueNode)
	return maxLine
}

// findOperations walks the parsed document for a top-level "paths" mapping
// and returns one operation per "paths.<path>.<method>" entry, in source
// order (mapping nodes' Content preserves file order, not sorted order).
func findOperations(root *yaml.Node) []operation {
	if len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	pathsNode := mappingValue(doc, "paths")
	if pathsNode == nil {
		return nil
	}

	var ops []operation
	for _, path := range mappingPairs(pathsNode) {
		for _, method := range mappingPairs(path.value) {
			ops = append(ops, operation{
				heading:   "paths > " + path.key.Value + " > " + method.key.Value,
				startLine: method.key.Line,
				value:     method.value,
			})
		}
	}
	return ops
}

type mappingPair struct {
	key   *yaml.Node
	value *yaml.Node
}

// mappingPairs returns node's key/value pairs in source order. node must be
// a mapping node (kind == yaml.MappingNode); a non-mapping node yields nil.
func mappingPairs(node *yaml.Node) []mappingPair {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	pairs := make([]mappingPair, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		pairs = append(pairs, mappingPair{key: node.Content[i], value: node.Content[i+1]})
	}
	return pairs
}

// mappingValue returns the value node for key within node, or nil if node
// isn't a mapping or key isn't present.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for _, pair := range mappingPairs(node) {
		if pair.key.Value == key {
			return pair.value
		}
	}
	return nil
}
