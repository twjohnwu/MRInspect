package reviewer

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

func (r *MRInspectReviewer) cleanResponse(response string) string {
	markers := []string{"## Code Review", "# Code Review", "## Review", "### MR Info"}
	// Cut at the EARLIEST marker occurrence across the whole list, not the
	// first marker in list-priority order: if the reviewed diff itself
	// quotes a higher-priority marker string near the tail, list-order
	// selection would cut there and discard the entire real review, which
	// is unrecoverable. An echoed marker BEFORE the real content still
	// leaves echo garbage in the result, but that is accepted here because
	// ValidateReviewContent still gates on required sections downstream;
	// a stricter future guard could require the cut tail to actually
	// contain those required sections.
	cutAt := -1
	for _, marker := range markers {
		if idx := strings.Index(response, marker); idx >= 0 && (cutAt == -1 || idx < cutAt) {
			cutAt = idx
		}
	}
	if cutAt >= 0 {
		return response[cutAt:]
	}
	return response
}

// logValidationFailure records forensics for a failed ValidateReviewContent
// attempt: the headings the model actually wrote, and the response length
// before/after cleanResponse. It never fires on a successful attempt.
func (r *MRInspectReviewer) logValidationFailure(attempt int, reviewPrompt, rawResponse, cleaned string, validationErr error, dumpsEnabled bool) {
	fields := []any{
		"attempt", attempt,
		"headings", extractHeadings(rawResponse),
		"responseLenBeforeClean", len(rawResponse),
		"responseLenAfterClean", len(cleaned),
		"error", validationErr.Error(),
	}
	if !dumpsEnabled {
		fields = append(fields,
			"promptSHA", sha256Prefix(reviewPrompt),
			"responseSHA", sha256Prefix(rawResponse),
		)
	}
	r.log.Warn("review validation failed", fields...)
	r.dumpForensics(dumpsEnabled, "Prompt", fmt.Sprintf("attempt %d", attempt), reviewPrompt)
	r.dumpForensics(dumpsEnabled, "Response", fmt.Sprintf("attempt %d", attempt), rawResponse)
}

func sha256Prefix(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", digest)[:12]
}

// dumpForensics logs content wrapped in Start/End marker lines, gated by
// dumpsEnabled. It is failure-only by construction: callers only invoke it
// on a failed-validation path.
func (r *MRInspectReviewer) dumpForensics(dumpsEnabled bool, kind, tag, content string) {
	if !dumpsEnabled {
		return
	}
	r.log.Warn(fmt.Sprintf(
		"======== %s (%s) Start ========\n%s\n======== %s (%s) End ========",
		kind, tag, content, kind, tag,
	))
}

// extractHeadings returns every line the model actually wrote that starts
// with '#', i.e. any markdown heading level, in response order.
func extractHeadings(response string) []string {
	var headings []string
	for _, line := range strings.Split(response, "\n") {
		if strings.HasPrefix(line, "#") {
			headings = append(headings, line)
		}
	}
	return headings
}
