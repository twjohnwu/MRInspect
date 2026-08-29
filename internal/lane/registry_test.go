package lane

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeLaneYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create lane fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write lane fixture: %v", err)
	}
}

func canonicalLanePath(repoRoot string) string {
	return filepath.Join(repoRoot, "projects", "lanes.yaml")
}

func systemLanePath(repoRoot, system string) string {
	return filepath.Join(repoRoot, "projects", system, "lanes.yaml")
}

func sameLanes(got, want []Lane) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Enabled != want[i].Enabled ||
			got[i].Template != want[i].Template || got[i].Intent != want[i].Intent ||
			got[i].TopK != want[i].TopK || got[i].Model != want[i].Model ||
			!slices.Equal(got[i].Resources.Sets, want[i].Resources.Sets) ||
			!slices.Equal(got[i].Resources.Tags, want[i].Resources.Tags) {
			return false
		}
	}
	return true
}

// TestLoad_PreservesDeclarationOrder verifies REQ-01 / S-01: all three
// canonical lanes retain their strict YAML declaration order and field values.
func TestLoad_PreservesDeclarationOrder(t *testing.T) {
	repoRoot := t.TempDir()
	writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: spec-conformance
    enabled: true
    template: ./projects/_lanes/spec-conformance.tmpl.md
    intent: spec and technical documentation conformance
    resources:
      sets: []
      tags: [docs]
  - id: standards
    enabled: true
    template: ./projects/_lanes/standards.tmpl.md
    intent: coding standards and conventions compliance
    resources:
      sets: [official-standards]
      tags: [standards]
  - id: code-diff
    enabled: true
    template: ./projects/_lanes/code-diff.tmpl.md
    intent: general code review
    resources:
      sets: []
      tags: []
`)

	registry, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Lane{
		{ID: "spec-conformance", Enabled: true, Template: "./projects/_lanes/spec-conformance.tmpl.md", Intent: "spec and technical documentation conformance", Resources: Resources{Sets: []string{}, Tags: []string{"docs"}}},
		{ID: "standards", Enabled: true, Template: "./projects/_lanes/standards.tmpl.md", Intent: "coding standards and conventions compliance", Resources: Resources{Sets: []string{"official-standards"}, Tags: []string{"standards"}}},
		{ID: "code-diff", Enabled: true, Template: "./projects/_lanes/code-diff.tmpl.md", Intent: "general code review", Resources: Resources{Sets: []string{}, Tags: []string{}}},
	}
	if !sameLanes(registry.Lanes, want) {
		t.Errorf("Lanes: want ordered declarations %+v, got %+v", want, registry.Lanes)
	}
}

// TestLoad_FourthLaneNeedsNoCodeChange verifies REQ-01 / S-02: an arbitrary
// fourth declaration is loaded as a distinct, complete lane after the first three.
func TestLoad_FourthLaneNeedsNoCodeChange(t *testing.T) {
	repoRoot := t.TempDir()
	writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: spec-conformance
    enabled: true
    template: spec.tmpl.md
    intent: inspect specifications
    resources: {sets: [specs], tags: [docs]}
  - id: standards
    enabled: false
    template: standards.tmpl.md
    intent: inspect standards
    resources: {sets: [style-guide], tags: [standards]}
  - id: code-diff
    enabled: true
    template: diff.tmpl.md
    intent: inspect changed code
    resources: {sets: [], tags: []}
  - id: dependency-risk
    enabled: true
    template: dependency.tmpl.md
    intent: inspect dependency risk
    resources: {sets: [dependency-policy], tags: [security, supply-chain]}
    topK: 9
    model: lane-specific-model
`)

	registry, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Lane{
		{ID: "spec-conformance", Enabled: true, Template: "spec.tmpl.md", Intent: "inspect specifications", Resources: Resources{Sets: []string{"specs"}, Tags: []string{"docs"}}},
		{ID: "standards", Enabled: false, Template: "standards.tmpl.md", Intent: "inspect standards", Resources: Resources{Sets: []string{"style-guide"}, Tags: []string{"standards"}}},
		{ID: "code-diff", Enabled: true, Template: "diff.tmpl.md", Intent: "inspect changed code", Resources: Resources{Sets: []string{}, Tags: []string{}}},
		{ID: "dependency-risk", Enabled: true, Template: "dependency.tmpl.md", Intent: "inspect dependency risk", Resources: Resources{Sets: []string{"dependency-policy"}, Tags: []string{"security", "supply-chain"}}, TopK: 9, Model: "lane-specific-model"},
	}
	if !sameLanes(registry.Lanes, want) {
		t.Errorf("Lanes: want four distinct complete lanes %+v, got %+v", want, registry.Lanes)
	}
}

// TestLoad_RejectsMissingFieldsAndDuplicateIDs verifies REQ-01 / S-03:
// missing enabled and duplicate IDs are named errors, while a complete lane loads.
func TestLoad_RejectsMissingFieldsAndDuplicateIDs(t *testing.T) {
	t.Run("missing enabled is not defaulted", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: missing-enabled
    template: missing-enabled.tmpl.md
    intent: must be rejected
    resources: {sets: [], tags: []}
`)

		_, err := Load(repoRoot, "")
		if err == nil {
			t.Fatal("Load: want a named error for missing enabled, got nil")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "enabled") {
			t.Errorf("Load error: want missing field %q to be named, got %q", "enabled", err)
		}
	})

	t.Run("duplicate id is named", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: repeated-lane
    enabled: true
    template: first.tmpl.md
    intent: first declaration
    resources: {sets: [], tags: []}
  - id: repeated-lane
    enabled: false
    template: second.tmpl.md
    intent: second declaration
    resources: {sets: [policy], tags: [docs]}
`)

		_, err := Load(repoRoot, "")
		if err == nil {
			t.Fatal("Load: want a named error for duplicate id, got nil")
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "duplicate") || !strings.Contains(message, "repeated-lane") {
			t.Errorf("Load error: want duplicate id %q to be named, got %q", "repeated-lane", err)
		}
	})

	t.Run("complete declaration succeeds", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: complete
    enabled: false
    template: complete.tmpl.md
    intent: valid disabled lane
    resources: {sets: [handbook], tags: [policy]}
    topK: 4
    model: economical-model
`)

		registry, err := Load(repoRoot, "")
		if err != nil {
			t.Fatalf("Load complete declaration: %v", err)
		}
		want := []Lane{{ID: "complete", Enabled: false, Template: "complete.tmpl.md", Intent: "valid disabled lane", Resources: Resources{Sets: []string{"handbook"}, Tags: []string{"policy"}}, TopK: 4, Model: "economical-model"}}
		if !sameLanes(registry.Lanes, want) {
			t.Errorf("Lanes: want complete declaration %+v, got %+v", want, registry.Lanes)
		}
	})
}

// TestLoad_OverlayKeepsPositionAndAppendsNew verifies REQ-01 / S-04: an
// ID override keeps its canonical position and a new overlay ID appends at the tail.
func TestLoad_OverlayKeepsPositionAndAppendsNew(t *testing.T) {
	repoRoot := t.TempDir()
	writeLaneYAML(t, canonicalLanePath(repoRoot), `
lanes:
  - id: a
    enabled: true
    template: canonical-a.tmpl.md
    intent: lane a
    resources: {sets: [a-set], tags: []}
  - id: b
    enabled: true
    template: canonical-b.tmpl.md
    intent: lane b
    resources: {sets: [b-set], tags: [canonical]}
  - id: c
    enabled: false
    template: canonical-c.tmpl.md
    intent: lane c
    resources: {sets: [], tags: [c-tag]}
`)
	writeLaneYAML(t, systemLanePath(repoRoot, "margherita-pizza"), `
lanes:
  - id: b
    enabled: true
    template: system-b.tmpl.md
    intent: lane b
    resources: {sets: [b-set], tags: [canonical]}
  - id: d
    enabled: true
    template: system-d.tmpl.md
    intent: lane d
    resources: {sets: [d-set], tags: [system]}
`)

	registry, err := Load(repoRoot, "margherita-pizza")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Lane{
		{ID: "a", Enabled: true, Template: "canonical-a.tmpl.md", Intent: "lane a", Resources: Resources{Sets: []string{"a-set"}, Tags: []string{}}},
		{ID: "b", Enabled: true, Template: "system-b.tmpl.md", Intent: "lane b", Resources: Resources{Sets: []string{"b-set"}, Tags: []string{"canonical"}}},
		{ID: "c", Enabled: false, Template: "canonical-c.tmpl.md", Intent: "lane c", Resources: Resources{Sets: []string{}, Tags: []string{"c-tag"}}},
		{ID: "d", Enabled: true, Template: "system-d.tmpl.md", Intent: "lane d", Resources: Resources{Sets: []string{"d-set"}, Tags: []string{"system"}}},
	}
	if !sameLanes(registry.Lanes, want) {
		t.Errorf("Lanes: want overlay order and fields %+v, got %+v", want, registry.Lanes)
	}
}
