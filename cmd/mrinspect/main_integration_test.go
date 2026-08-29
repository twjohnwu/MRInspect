//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBinary_IndexNeedsNoReviewCredentials(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}

	binary := filepath.Join(t.TempDir(), "mrinspect")
	build := exec.Command("go", "build", "-o", binary, "./cmd/mrinspect")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}

	fixtureRoot := t.TempDir()
	resourcesPath := filepath.Join(fixtureRoot, "projects", "resources.yaml")
	if err := os.MkdirAll(filepath.Dir(resourcesPath), 0o755); err != nil {
		t.Fatalf("create fixture projects directory: %v", err)
	}
	if err := os.WriteFile(resourcesPath, []byte("sets:\n  - name: fixture\n    mode: retrieval\n    paths:\n      - ./docs\n"), 0o644); err != nil {
		t.Fatalf("write resources fixture: %v", err)
	}
	documentPath := filepath.Join(fixtureRoot, "docs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(documentPath), 0o755); err != nil {
		t.Fatalf("create fixture docs directory: %v", err)
	}
	if err := os.WriteFile(documentPath, []byte("# Fixture\n\nA resource for binary indexing.\n"), 0o644); err != nil {
		t.Fatalf("write resource document: %v", err)
	}

	storePath := filepath.Join(t.TempDir(), "store.sqlite")
	var stderr bytes.Buffer
	run := exec.Command(binary, "index", "--out", storePath)
	run.Dir = fixtureRoot
	run.Env = []string{
		"MRI_SERVICE_NAME=unknown",
		"MRI_RAG_BACKEND=sqlite",
	}
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("mrinspect index: %v\nstderr:\n%s", err, stderr.String())
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("indexed store %q: %v", storePath, err)
	}
	if info.Size() == 0 {
		t.Errorf("indexed store %q is empty", storePath)
	}
}
