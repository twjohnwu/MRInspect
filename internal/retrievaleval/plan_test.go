package retrievaleval

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/lane"
)

// TestPlan_MatchesProductionLaneResolution verifies REQ-03 / S-05: retrieval
// planning preserves production lane enablement, resource resolution, and TopK.
func TestPlan_MatchesProductionLaneResolution(t *testing.T) {
	repoRoot := t.TempDir()
	projectsDir := filepath.Join(repoRoot, "projects")
	for _, path := range []string{
		projectsDir,
		filepath.Join(repoRoot, "docs", "set-a"),
		filepath.Join(repoRoot, "docs", "set-b"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", path, err)
		}
	}

	lanesYAML := []byte(`lanes:
  - id: lane1
    enabled: false
    template: lane1.tmpl.md
    intent: disabled lane
    resources: {sets: [], tags: [alpha]}
  - id: lane2
    enabled: true
    template: lane2.tmpl.md
    intent: tag-selected lane
    resources: {sets: [], tags: [alpha]}
  - id: lane3
    enabled: true
    template: lane3.tmpl.md
    intent: explicitly selected lane
    resources: {sets: [set-b], tags: []}
    topK: 3
`)
	if err := os.WriteFile(filepath.Join(projectsDir, "lanes.yaml"), lanesYAML, 0o644); err != nil {
		t.Fatalf("WriteFile lanes.yaml: %v", err)
	}

	resourcesYAML := []byte(`sets:
  - name: set-a
    tags: [alpha]
    mode: retrieval
    paths: [docs/set-a]
  - name: set-b
    tags: []
    mode: retrieval
    paths: [docs/set-b]
`)
	if err := os.WriteFile(filepath.Join(projectsDir, "resources.yaml"), resourcesYAML, 0o644); err != nil {
		t.Fatalf("WriteFile resources.yaml: %v", err)
	}

	fixture := evalrun.Fixture{
		Name: "01-x.diff",
		Changes: []gitlab.Change{{
			NewPath: "internal/foo/bar.go",
			Diff:    "@@ -1 +1 @@\n-old\n+new\n",
		}},
	}
	wantTerms := lane.Terms(fixture.Changes)

	got, err := BuildPlan(repoRoot, "", []evalrun.Fixture{fixture})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(BuildPlan()) = %d, want 2; triples = %+v", len(got), got)
	}

	want := []struct {
		laneID string
		set    string
		k      int
	}{
		{laneID: "lane2", set: "set-a", k: lane.DefaultLaneTopK},
		{laneID: "lane3", set: "set-b", k: 3},
	}
	for i, triple := range got {
		if triple.Fixture != fixture.Name {
			t.Errorf("triple[%d].Fixture = %q, want %q", i, triple.Fixture, fixture.Name)
		}
		if triple.LaneID != want[i].laneID {
			t.Errorf("triple[%d].LaneID = %q, want %q", i, triple.LaneID, want[i].laneID)
		}
		if triple.Set.Name != want[i].set {
			t.Errorf("triple[%d].Set.Name = %q, want %q", i, triple.Set.Name, want[i].set)
		}
		if triple.K != want[i].k {
			t.Errorf("triple[%d].K = %d, want %d", i, triple.K, want[i].k)
		}
		if !slices.Equal(triple.Terms, wantTerms) {
			t.Errorf("triple[%d].Terms = %v, want lane.Terms(fixture.Changes) = %v", i, triple.Terms, wantTerms)
		}
		if triple.LaneID == "lane1" || triple.Set.Name == "" {
			t.Errorf("triple[%d] includes a disabled lane or zero-set resolution: %+v", i, triple)
		}
	}
}
