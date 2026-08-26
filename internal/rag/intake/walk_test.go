package intake

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// skippedReason returns the SkipReason recorded for the first entry in
// result.Skipped whose Path ends with suffix, and whether one was found.
func skippedReason(result WalkResult, suffix string) (SkipReason, bool) {
	for _, s := range result.Skipped {
		if strings.HasSuffix(s.Path, suffix) {
			return s.Reason, true
		}
	}
	return "", false
}

func containsSuffix(files []string, suffix string) bool {
	for _, f := range files {
		if strings.HasSuffix(f, suffix) {
			return true
		}
	}
	return false
}

// walkWithTimeout runs Walk in a goroutine and fails the test if it does not
// return within the deadline — a symlink cycle that IS followed would hang
// forever instead of erroring, which a plain call would not catch.
func walkWithTimeout(t *testing.T, opts WalkOptions) (WalkResult, error) {
	t.Helper()
	type outcome struct {
		result WalkResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := Walk(opts)
		done <- outcome{r, err}
	}()
	select {
	case o := <-done:
		return o.result, o.err
	case <-time.After(5 * time.Second):
		t.Fatal("Walk: did not return within 5s — likely followed the symlink cycle")
		return WalkResult{}, nil
	}
}

// TestWalk_DenylistRefusesSecretFiles verifies REQ-03 / S-12: with an
// include of **/*, all nine secret-shaped filenames are refused, each
// counted in FilesSkipped and each named individually in the report.
func TestWalk_DenylistRefusesSecretFiles(t *testing.T) {
	root := t.TempDir()

	denied := []string{
		".env",
		"id_rsa",
		"tls.pem",
		".npmrc",
		".netrc",
		".git-credentials",
		"kubeconfig",
		"signing.key",
		"terraform.tfvars",
	}
	for _, name := range denied {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	// A control file that must NOT be denylisted, so the assertions below
	// prove the denylist is name-specific rather than "skip everything".
	allowed := filepath.Join(root, "README.md")
	if err := os.WriteFile(allowed, []byte("# doc"), 0o644); err != nil {
		t.Fatalf("write control fixture: %v", err)
	}

	result, err := walkWithTimeout(t, WalkOptions{
		Paths:   []string{root},
		Include: []string{"**/*"},
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	if result.FilesSkipped != len(denied) {
		t.Errorf("FilesSkipped: want %d, got %d (skipped=%+v)", len(denied), result.FilesSkipped, result.Skipped)
	}

	for _, name := range denied {
		reason, found := skippedReason(result, name)
		if !found {
			t.Errorf("Skipped: no named entry for %q (skipped=%+v)", name, result.Skipped)
			continue
		}
		if reason != SkipReasonDenylist {
			t.Errorf("Skipped[%q].Reason: want %q, got %q", name, SkipReasonDenylist, reason)
		}
		if containsSuffix(result.Files, name) {
			t.Errorf("Files: denylisted file %q must not be present, got %v", name, result.Files)
		}
	}

	if !containsSuffix(result.Files, "README.md") {
		t.Errorf("Files: control file README.md should be included, got %v", result.Files)
	}
}

// TestWalk_EnforcesObservableLimits verifies REQ-03 / S-13: a self-referential
// symlink is not followed, a deeply nested chain is cut at the configured
// depth cap, an oversized file is skipped, and a FIFO is skipped — each as
// its own named, counted skip reason — and the walk still completes.
func TestWalk_EnforcesObservableLimits(t *testing.T) {
	root := t.TempDir()

	// Class 1: a symlink pointing at its own parent directory (self-cycle).
	branch := filepath.Join(root, "branch")
	if err := os.MkdirAll(branch, 0o755); err != nil {
		t.Fatalf("mkdir branch: %v", err)
	}
	loopLink := filepath.Join(branch, "up")
	if err := os.Symlink(root, loopLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Class 2: a 25-level deep directory chain with a marker file at the
	// bottom, well past the depth cap of 12.
	deep := root
	for i := 1; i <= 25; i++ {
		deep = filepath.Join(deep, "d"+strconv.Itoa(i))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep chain: %v", err)
	}
	deepFile := filepath.Join(deep, "buried.txt")
	if err := os.WriteFile(deepFile, []byte("too deep"), 0o644); err != nil {
		t.Fatalf("write deep file: %v", err)
	}

	// Class 3: a file over maxFileSizeKB.
	const maxFileSizeKB = 1
	bigFile := filepath.Join(root, "oversized.bin")
	if err := os.WriteFile(bigFile, make([]byte, (maxFileSizeKB+1)*1024), 0o644); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}

	// Class 4: a FIFO. Mkfifo is unavailable on some platforms; skip that
	// sub-case there rather than failing the whole test.
	var fifoPath string
	fifoSupported := runtime.GOOS != "windows"
	if fifoSupported {
		fifoPath = filepath.Join(root, "pipe.fifo")
		if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
	} else {
		t.Logf("skipping FIFO sub-case: syscall.Mkfifo unavailable on %s", runtime.GOOS)
	}

	result, err := walkWithTimeout(t, WalkOptions{
		Paths:         []string{root},
		Include:       []string{"**/*"},
		MaxDepth:      12,
		MaxFileSizeKB: maxFileSizeKB,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Class 1: symlink not followed — its target's contents (e.g. "branch"
	// reached again through "branch/up") must not appear duplicated, and the
	// symlink itself must be named as skipped.
	if reason, found := skippedReason(result, filepath.Join("branch", "up")); !found {
		t.Errorf("Skipped: no named entry for the self-referential symlink (skipped=%+v)", result.Skipped)
	} else if reason != SkipReasonSymlinkCycle {
		t.Errorf("Skipped[branch/up].Reason: want %q, got %q", SkipReasonSymlinkCycle, reason)
	}

	// Class 2: files below the depth cap are not indexed, and the skip is
	// named and counted.
	if containsSuffix(result.Files, "buried.txt") {
		t.Errorf("Files: buried.txt is past the depth cap and must not be indexed, got %v", result.Files)
	}
	if reason, found := skippedReason(result, "buried.txt"); !found {
		t.Errorf("Skipped: no named entry for buried.txt (skipped=%+v)", result.Skipped)
	} else if reason != SkipReasonDepthLimit {
		t.Errorf("Skipped[buried.txt].Reason: want %q, got %q", SkipReasonDepthLimit, reason)
	}

	// Class 3: oversized file is skipped, named and counted.
	if containsSuffix(result.Files, "oversized.bin") {
		t.Errorf("Files: oversized.bin exceeds maxFileSizeKB and must not be indexed, got %v", result.Files)
	}
	if reason, found := skippedReason(result, "oversized.bin"); !found {
		t.Errorf("Skipped: no named entry for oversized.bin (skipped=%+v)", result.Skipped)
	} else if reason != SkipReasonFileTooLarge {
		t.Errorf("Skipped[oversized.bin].Reason: want %q, got %q", SkipReasonFileTooLarge, reason)
	}

	// Class 4: FIFO is skipped, named and counted (platform-permitting).
	if fifoSupported {
		if containsSuffix(result.Files, "pipe.fifo") {
			t.Errorf("Files: pipe.fifo must not be indexed, got %v", result.Files)
		}
		if reason, found := skippedReason(result, "pipe.fifo"); !found {
			t.Errorf("Skipped: no named entry for pipe.fifo (skipped=%+v)", result.Skipped)
		} else if reason != SkipReasonUnsupportedType {
			t.Errorf("Skipped[pipe.fifo].Reason: want %q, got %q", SkipReasonUnsupportedType, reason)
		}
	}

	wantSkipped := 3
	if fifoSupported {
		wantSkipped = 4
	}
	if result.FilesSkipped != wantSkipped {
		t.Errorf("FilesSkipped: want %d, got %d (skipped=%+v)", wantSkipped, result.FilesSkipped, result.Skipped)
	}

	_ = fifoPath
}
