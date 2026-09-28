package ragcmd

// EvalFixturesDir resolves the fixtures directory for the eval command
// (REQ-01 / S-02). The retrieval-quality check uses a different default
// fixtures directory ("eval/retrieval-fixtures") than the review evaluation
// ("eval/fixtures"), but an explicitly passed -fixtures flag always wins:
// when retrieval is true and explicit is false, the retrieval default is
// returned; otherwise the caller-supplied value passes through unchanged.
func EvalFixturesDir(retrieval, explicit bool, value string) string {
	if retrieval && !explicit {
		return "eval/retrieval-fixtures"
	}
	return value
}
