package diff

import (
	"context"
	"strings"
	"testing"

	"mrinspect/internal/diffbudget"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
)

type changesClient struct {
	interfaces.IGitLabClient
	resp gitlab.MRChangesResponse
}

func (c changesClient) GetMRChanges(context.Context, string, string) (gitlab.MRChangesResponse, error) {
	return c.resp, nil
}

func fetchWith(t *testing.T, changes ...gitlab.Change) string {
	t.Helper()
	f := NewAPIDiffFetcher(changesClient{resp: gitlab.MRChangesResponse{Changes: changes}}, "1", "2", nil)
	out, err := f.Fetch(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	return out
}

func TestAPIDiffFetcher_TruncatedFileExcludedAndDisclosed(t *testing.T) {
	out := fetchWith(t,
		gitlab.Change{OldPath: "big.go", NewPath: "big.go"},
		gitlab.Change{OldPath: "ok.go", NewPath: "ok.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
	)
	if strings.Contains(out, "--- a/big.go") || strings.Contains(out, "+++ b/big.go") {
		t.Errorf("truncated file header leaked into diff text:\n%s", out)
	}
	if !strings.Contains(out, "+++ b/ok.go") {
		t.Errorf("reviewable file missing:\n%s", out)
	}
	if !strings.Contains(out, "big.go ("+diffbudget.ReasonDiffUnavailable+")") {
		t.Errorf("truncated file not disclosed in trailer:\n%s", out)
	}
}

func TestAPIDiffFetcher_EmptyDiffRenameDeleteModeNotFlagged(t *testing.T) {
	out := fetchWith(t,
		gitlab.Change{OldPath: "old.go", NewPath: "new.go", RenamedFile: true},
		gitlab.Change{OldPath: "gone.go", NewPath: "gone.go", DeletedFile: true},
		gitlab.Change{OldPath: "run.sh", NewPath: "run.sh", AMode: "100644", BMode: "100755"},
	)
	if strings.Contains(out, diffbudget.ReasonDiffUnavailable) {
		t.Errorf("legitimate empty diff flagged as unavailable:\n%s", out)
	}
	for _, want := range []string{"+++ b/new.go", "+++ /dev/null"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
