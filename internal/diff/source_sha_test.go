package diff

// REQ-06 / S-08 (g) reviewed SHA: fetchers report the source commit their
// patch corresponds to.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/logger"
)

const apiSourceSHA = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

var hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// newLocalRepo builds a repo in a temp dir with main (one commit) and feature
// (one more commit), makes it the working directory and returns the feature
// tip.
func newLocalRepo(t *testing.T) plumbing.Hash {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInitWithOptions(dir, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("init repo: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	sig := &object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(1_700_000_000, 0)}
	commit := func(content, msg string) plumbing.Hash {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := wt.Add("f.txt"); err != nil {
			t.Fatalf("add: %v", err)
		}
		h, err := wt.Commit(msg, &gogit.CommitOptions{Author: sig, Committer: sig})
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		return h
	}
	commit("base\n", "base")
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	tip := commit("base\nchanged\n", "change")
	t.Chdir(dir)
	return tip
}

func TestFetchWithSource_Local(t *testing.T) {
	tip := newLocalRepo(t)

	patch, sha, err := NewLocalDiffFetcher(nil).FetchWithSource(context.Background(), "feature", "main")
	if err != nil {
		t.Fatalf("FetchWithSource() error: %v", err)
	}
	if !strings.Contains(patch, "+changed") {
		t.Errorf("patch lacks the committed change: %q", patch)
	}
	if !hex40.MatchString(sha) {
		t.Errorf("sourceSHA = %q, want 40 hex chars", sha)
	}
	if sha != tip.String() {
		t.Errorf("sourceSHA = %q, want source branch tip %q", sha, tip)
	}
}

func TestFetchWithSource_API(t *testing.T) {
	client := changesClient{resp: gitlab.MRChangesResponse{
		SHA:     apiSourceSHA,
		Changes: []gitlab.Change{{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"}},
	}}

	patch, sha, err := NewAPIDiffFetcher(client, "1", "2", nil).FetchWithSource(context.Background(), "", "")
	if err != nil {
		t.Fatalf("FetchWithSource() error: %v", err)
	}
	if !strings.Contains(patch, "+++ b/a.go") {
		t.Errorf("patch lacks a.go: %q", patch)
	}
	if sha != apiSourceSHA {
		t.Errorf("sourceSHA = %q, want %q", sha, apiSourceSHA)
	}
}

// stubSource implements both IDiffFetcher and IDiffSourceFetcher.
type stubSource struct {
	patch, sha string
	err        error
}

func (s stubSource) Fetch(context.Context, string, string) (string, error) { return s.patch, s.err }
func (s stubSource) FetchWithSource(context.Context, string, string) (string, string, error) {
	return s.patch, s.sha, s.err
}

// stubPlain implements only IDiffFetcher.
type stubPlain struct {
	patch string
	err   error
}

func (s stubPlain) Fetch(context.Context, string, string) (string, error) { return s.patch, s.err }

func TestFetchWithSource_Fallback(t *testing.T) {
	const localSHA = "1234567890123456789012345678901234567890"
	failing := stubSource{err: errors.New("no local repo")}
	tests := []struct {
		name               string
		primary, secondary interfaces.IDiffFetcher
		wantPatch, wantSHA string
	}{
		{"primary fails, API secondary", failing, stubSource{patch: "api patch", sha: apiSourceSHA}, "api patch", apiSourceSHA},
		{"primary succeeds", stubSource{patch: "local patch", sha: localSHA}, stubSource{patch: "api patch", sha: apiSourceSHA}, "local patch", localSHA},
		{"chosen primary lacks source support", stubPlain{patch: "plain patch"}, stubSource{patch: "api patch", sha: apiSourceSHA}, "plain patch", ""},
		{"chosen secondary lacks source support", failing, stubPlain{patch: "plain patch"}, "plain patch", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFallbackDiffFetcher(tt.primary, tt.secondary, logger.New(0, filepath.Join(t.TempDir(), "metrics.json")))
			patch, sha, err := f.FetchWithSource(context.Background(), "feature", "main")
			if err != nil {
				t.Fatalf("FetchWithSource() error: %v", err)
			}
			if patch != tt.wantPatch {
				t.Errorf("patch = %q, want %q", patch, tt.wantPatch)
			}
			if sha != tt.wantSHA {
				t.Errorf("sourceSHA = %q, want %q", sha, tt.wantSHA)
			}
		})
	}
}
