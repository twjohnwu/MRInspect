package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root containing go.mod")
		}
		dir = parent
	}
}

func changesGlobs(rules []CIRule) []string {
	var globs []string
	for _, rule := range rules {
		globs = append(globs, rule.Changes...)
	}
	return globs
}

// TestCITriggers_CoverAllDeclaredPaths verifies REQ-09 / S-31: every path
// declared by the real resources file is covered by the index job's changes
// globs, while unrelated README-only changes and undeclared CI coverage drift
// are rejected.
func TestCITriggers_CoverAllDeclaredPaths(t *testing.T) {
	root := repoRoot(t)
	resourcesPath := filepath.Join(root, "projects", "resources.yaml")
	indexJob, err := LoadIndexJob(filepath.Join(root, ".gitlab-ci.yml"))
	if err != nil {
		t.Fatalf("LoadIndexJob: %v", err)
	}

	declaredPaths, err := LoadDeclaredPaths(resourcesPath)
	if err != nil {
		t.Fatalf("LoadDeclaredPaths: %v", err)
	}
	changes := changesGlobs(indexJob.Rules)
	if gaps := CoverageGaps(declaredPaths, changes); len(gaps) != 0 {
		t.Errorf("declared resource paths missing changes coverage: %v", gaps)
	}

	if gaps := CoverageGaps([]string{"README.md"}, changes); len(gaps) == 0 {
		t.Error("README.md-only commit unexpectedly matches an index changes glob")
	}

	content, err := os.ReadFile(resourcesPath)
	if err != nil {
		t.Fatalf("read real resources.yaml: %v", err)
	}
	temporaryResourcesPath := filepath.Join(t.TempDir(), "resources.yaml")
	content = append(content, []byte(`
  - name: ci-drift-path
    tags: [test]
    mode: retrieval
    paths:
      - ./docs/not-covered-by-index-rules
`)...)
	if err := os.WriteFile(temporaryResourcesPath, content, 0o644); err != nil {
		t.Fatalf("write temporary resources.yaml: %v", err)
	}

	driftPaths, err := LoadDeclaredPaths(temporaryResourcesPath)
	if err != nil {
		t.Fatalf("LoadDeclaredPaths temporary file: %v", err)
	}
	if gaps := CoverageGaps(driftPaths, changes); !containsString(gaps, "./docs/not-covered-by-index-rules") {
		t.Errorf("undeclared CI coverage drift was not reported; gaps: %v", gaps)
	}
}

// TestCITriggers_ScheduleAndPathBothPublishArtifact verifies REQ-09 / S-46:
// the index job can be triggered by schedule or resource-path changes and
// publishes the retained RAG store artifact.
func TestCITriggers_ScheduleAndPathBothPublishArtifact(t *testing.T) {
	root := repoRoot(t)
	indexJob, err := LoadIndexJob(filepath.Join(root, ".gitlab-ci.yml"))
	if err != nil {
		t.Fatalf("LoadIndexJob: %v", err)
	}

	hasScheduleRule := false
	hasChangesRule := false
	for _, rule := range indexJob.Rules {
		if rule.If == `$CI_PIPELINE_SOURCE == "schedule"` {
			hasScheduleRule = true
		}
		if len(rule.Changes) != 0 {
			hasChangesRule = true
		}
	}
	if !hasScheduleRule {
		t.Error("index job has no schedule trigger rule")
	}
	if !hasChangesRule {
		t.Error("index job has no path changes trigger rule")
	}
	if !containsString(indexJob.ArtifactPaths, "rag-index/mrinspect-rag.sqlite") {
		t.Errorf("artifact paths %v do not include the RAG store", indexJob.ArtifactPaths)
	}
	if indexJob.ArtifactExpireIn != "21 days" {
		t.Errorf("artifact expire_in: want %q, got %q", "21 days", indexJob.ArtifactExpireIn)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
