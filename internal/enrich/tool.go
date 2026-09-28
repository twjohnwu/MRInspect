package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"mrinspect/internal/ai"
)

// Limits bounds one Executor's per-tool-call resource budget.
type Limits struct {
	MaxCalls    int
	ResultBytes int
	ToolTimeout time.Duration
}

// maxFiles is the maximum number of files a single repo_search scan may
// walk before stopping.
const maxFiles = 20000

// maxScanBytes is the maximum total bytes a single repo_search scan may
// read before stopping.
const maxScanBytes = 64 << 20

// maxLineBytes is the maximum length, in bytes, of a single line considered
// by repo_search; longer lines are skipped.
const maxLineBytes = 4096

// Tool is one in-process, synchronous, pure-Go capability the enrichment
// round loop can invoke via Executor.
type Tool interface {
	Name() string
	Spec() ai.ToolSpec
	Run(ctx context.Context, root string, args json.RawMessage, lim Limits) (content string, code string)
}

func decodeToolArgs(args json.RawMessage, target any) string {
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		const unknownField = "json: unknown field "
		if strings.HasPrefix(err.Error(), unknownField) {
			return strings.TrimPrefix(err.Error(), "json: ")
		}
		return "invalid arguments"
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "invalid arguments"
	}
	return ""
}

func containsNUL(value string) bool {
	return strings.IndexByte(value, 0) >= 0
}

type resultBuilder struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newResultBuilder(limit int) *resultBuilder {
	return &resultBuilder{limit: limit}
}

func (b *resultBuilder) addLine(line string) bool {
	if b.buffer.Len() > 0 {
		b.buffer.WriteByte('\n')
	}
	b.buffer.WriteString(line)
	if b.limit > 0 && b.buffer.Len() >= b.limit {
		b.truncated = true
	}
	return b.truncated
}

func (b *resultBuilder) content() string {
	if !b.truncated {
		return b.buffer.String()
	}
	marker := fmt.Sprintf("\n[truncated at %d bytes]", b.limit)
	if len(marker) >= b.limit {
		return marker[len(marker)-b.limit:]
	}
	prefixBytes := b.buffer.Bytes()
	prefixLength := b.limit - len(marker)
	if prefixLength > len(prefixBytes) {
		prefixLength = len(prefixBytes)
	}
	return string(prefixBytes[:prefixLength]) + marker
}
