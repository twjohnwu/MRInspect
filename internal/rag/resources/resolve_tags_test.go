package resources

import "testing"

func loadResolveTagFixture(t *testing.T, content string) Registry {
	t.Helper()
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), content)

	registry, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return registry
}

// TestResolve_TagOnlySelection verifies REQ-01 / T23: consumers can reference
// resource sets by tag, and matching sets retain their declaration order.
func TestResolve_TagOnlySelection(t *testing.T) {
	registry := loadResolveTagFixture(t, `
sets:
  - name: engineering-standards
    tags: [standards]
    mode: full
    paths: [./docs/standards]
  - name: api-guidelines
    tags: [api, standards]
    mode: retrieval
    paths: [./docs/api]
  - name: product-specs
    tags: [spec]
    mode: retrieval
    paths: [./docs/specs]
`)

	matched, unknown := registry.Resolve(nil, []string{"standards"})
	if !sameStrings(setNames(matched), []string{"engineering-standards", "api-guidelines"}) {
		t.Errorf("matched: want [engineering-standards api-guidelines], got %v", setNames(matched))
	}
	if len(unknown) != 0 {
		t.Errorf("unknown: want empty, got %v", unknown)
	}
}

// TestResolve_NamesAndTagsUnionInDeclarationOrder verifies REQ-01 / T23:
// name and tag references form a union, ordered by the sets: declaration.
func TestResolve_NamesAndTagsUnionInDeclarationOrder(t *testing.T) {
	registry := loadResolveTagFixture(t, `
sets:
  - name: alpha
    tags: [core]
    mode: retrieval
    paths: [./docs/alpha]
  - name: beta
    tags: [standards]
    mode: full
    paths: [./docs/beta]
  - name: gamma
    tags: [core]
    mode: retrieval
    paths: [./docs/gamma]
`)

	matched, unknown := registry.Resolve([]string{"gamma", "alpha"}, []string{"standards", "core"})
	if !sameStrings(setNames(matched), []string{"alpha", "beta", "gamma"}) {
		t.Errorf("matched: want [alpha beta gamma] in declaration order, got %v", setNames(matched))
	}
	if len(unknown) != 0 {
		t.Errorf("unknown: want empty, got %v", unknown)
	}
}

// TestResolve_ReportsUnknownTags verifies REQ-01 / T23: a tag reference that
// matches no set is reported as unknown, just as an unknown name is.
func TestResolve_ReportsUnknownTags(t *testing.T) {
	registry := loadResolveTagFixture(t, `
sets:
  - name: build-docs
    tags: [documentation]
    mode: retrieval
    paths: [./docs/build]
`)

	matched, unknown := registry.Resolve(nil, []string{"standards"})
	if len(matched) != 0 {
		t.Errorf("matched: want empty, got %v", setNames(matched))
	}
	if !sameStrings(unknown, []string{"standards"}) {
		t.Errorf("unknown: want [standards], got %v", unknown)
	}
}

// TestResolve_DeduplicatesNameAndTagMatch verifies REQ-01 / T23: a set named
// explicitly and also selected by a tag is returned once.
func TestResolve_DeduplicatesNameAndTagMatch(t *testing.T) {
	registry := loadResolveTagFixture(t, `
sets:
  - name: internal-specs
    tags: [standards]
    mode: retrieval
    paths: [./docs/specs]
  - name: official-standards
    tags: [standards]
    mode: full
    paths: [./docs/standards]
`)

	matched, unknown := registry.Resolve([]string{"internal-specs"}, []string{"standards"})
	if !sameStrings(setNames(matched), []string{"internal-specs", "official-standards"}) {
		t.Errorf("matched: want [internal-specs official-standards] exactly once each, got %v", setNames(matched))
	}
	if len(unknown) != 0 {
		t.Errorf("unknown: want empty, got %v", unknown)
	}
}

// TestResolve_NameAndTagCollisionMatchesBoth verifies REQ-01 / T23. REQ-01
// says consumers "只以 name 或 tag 引用", so a reference that is both a set name
// and another set's tag matches both sets, in declaration order and without duplicates.
func TestResolve_NameAndTagCollisionMatchesBoth(t *testing.T) {
	registry := loadResolveTagFixture(t, `
sets:
  - name: standards
    tags: [normative]
    mode: full
    paths: [./docs/standards]
  - name: engineering-guide
    tags: [standards]
    mode: retrieval
    paths: [./docs/guide]
`)

	matched, unknown := registry.Resolve([]string{"standards"}, []string{"standards"})
	if !sameStrings(setNames(matched), []string{"standards", "engineering-guide"}) {
		t.Errorf("matched: want [standards engineering-guide], got %v", setNames(matched))
	}
	if len(unknown) != 0 {
		t.Errorf("unknown: want empty, got %v", unknown)
	}
}
