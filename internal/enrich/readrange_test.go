package enrich

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mrinspect/internal/ai"
)

func TestReadFileRanges_StopsAtRequestedEnd(t *testing.T) {
	root := t.TempDir()
	content := "line-1\nline-2\nneedle-line-3\n" + strings.Repeat("padding\n", 650_000)
	writeTestFile(t, root, "large.txt", content)

	ex := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: 5 * time.Second})
	started := time.Now()
	result := oneResult(t, ex, ai.ToolCall{
		ID:   "range",
		Name: "read_file_ranges",
		Args: json.RawMessage(`{"path":"large.txt","start":3,"end":3}`),
	})
	if result.Error != "" {
		t.Fatalf("read_file_ranges Error = %q, want empty", result.Error)
	}
	if want := "large.txt:3: needle-line-3"; result.Content != want {
		t.Errorf("read_file_ranges Content = %q, want %q", result.Content, want)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Errorf("read_file_ranges took %s, want less than 2s", elapsed)
	}

	timeoutExecutor := newTestExecutor(t, root, Limits{MaxCalls: 10, ResultBytes: 512, ToolTimeout: time.Nanosecond})
	timeout := oneResult(t, timeoutExecutor, ai.ToolCall{
		ID:   "timeout",
		Name: "read_file_ranges",
		Args: json.RawMessage(`{"path":"large.txt","start":3,"end":3}`),
	})
	if timeout.Error != "timeout" {
		t.Errorf("timed-out call Error = %q, want %q", timeout.Error, "timeout")
	}
	if timeout.Content != "" {
		t.Errorf("timed-out call Content = %q, want empty", timeout.Content)
	}
}

func TestReadFileRanges_ChecksContextWhileScanning(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "many-lines.txt", strings.Repeat("line\n", 2_000))
	ctx := &cancelAfterFirstCheckContext{}

	content, code := (&readRangeTool{}).Run(ctx, root, json.RawMessage(`{"path":"many-lines.txt","start":1500,"end":1500}`), Limits{ResultBytes: 512})
	if code != "timeout" {
		t.Errorf("Run code = %q, want %q", code, "timeout")
	}
	if content != "" {
		t.Errorf("Run content = %q, want empty", content)
	}
}

type cancelAfterFirstCheckContext struct {
	checks int
}

func (*cancelAfterFirstCheckContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelAfterFirstCheckContext) Done() <-chan struct{}       { return nil }
func (c *cancelAfterFirstCheckContext) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}
func (*cancelAfterFirstCheckContext) Value(any) any { return nil }
