package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("writeYAML: mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeYAML: write: %v", err)
	}
}

func canonicalPath(repoRoot string) string {
	return filepath.Join(repoRoot, "projects", "resources.yaml")
}

func overlayPath(repoRoot, system string) string {
	return filepath.Join(repoRoot, "projects", system, "resources.yaml")
}

func setNames(sets []Set) []string {
	names := make([]string, len(sets))
	for i, s := range sets {
		names[i] = s.Name
	}
	return names
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestLoad_CanonicalSets verifies REQ-01 / S-01: loading the canonical
// projects/resources.yaml returns sets whose name and tags match the file,
// with paths resolved to absolute paths anchored at the repo root.
func TestLoad_CanonicalSets(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: internal-specs
    tags: [spec, internal]
    mode: retrieval
    paths:
      - ./docs/specs
  - name: official-standards
    tags: [standard]
    mode: full
    paths:
      - ./docs/standards
`)

	reg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Sets) != 2 {
		t.Fatalf("Sets: want 2 sets, got %d (%+v)", len(reg.Sets), reg.Sets)
	}

	specs := reg.Sets[0]
	if specs.Name != "internal-specs" {
		t.Errorf("Sets[0].Name: want %q, got %q", "internal-specs", specs.Name)
	}
	if !sameStrings(specs.Tags, []string{"spec", "internal"}) {
		t.Errorf("Sets[0].Tags: want [spec internal], got %v", specs.Tags)
	}
	wantSpecsPath := filepath.Join(repoRoot, "docs", "specs")
	if !sameStrings(specs.Paths, []string{wantSpecsPath}) {
		t.Errorf("Sets[0].Paths: want [%s], got %v", wantSpecsPath, specs.Paths)
	}

	standards := reg.Sets[1]
	if standards.Name != "official-standards" {
		t.Errorf("Sets[1].Name: want %q, got %q", "official-standards", standards.Name)
	}
	if !sameStrings(standards.Tags, []string{"standard"}) {
		t.Errorf("Sets[1].Tags: want [standard], got %v", standards.Tags)
	}
	wantStandardsPath := filepath.Join(repoRoot, "docs", "standards")
	if !sameStrings(standards.Paths, []string{wantStandardsPath}) {
		t.Errorf("Sets[1].Paths: want [%s], got %v", wantStandardsPath, standards.Paths)
	}
}

// TestLoad_SystemOverlayMergesByName verifies REQ-01 / S-02: a per-system
// overlay overriding an existing set name resolves its paths against the
// repo root (not the overlay file's directory), and leaves other canonical
// sets unchanged.
func TestLoad_SystemOverlayMergesByName(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: internal-specs
    tags: [spec, internal]
    mode: retrieval
    paths:
      - ./docs/specs
  - name: official-standards
    tags: [standard]
    mode: full
    paths:
      - ./docs/standards
`)
	writeYAML(t, overlayPath(repoRoot, "margherita-pizza"), `
sets:
  - name: internal-specs
    tags: [spec, internal]
    mode: retrieval
    paths:
      - ./docs/pizza-specs
`)

	reg, err := Load(repoRoot, "margherita-pizza")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Sets) != 2 {
		t.Fatalf("Sets: want 2 sets, got %d (%+v)", len(reg.Sets), reg.Sets)
	}

	specs := reg.Sets[0]
	wantOverlayPath := filepath.Join(repoRoot, "docs", "pizza-specs")
	if !sameStrings(specs.Paths, []string{wantOverlayPath}) {
		t.Errorf("internal-specs.Paths: want [%s] (repo-root-anchored, overlay wins), got %v", wantOverlayPath, specs.Paths)
	}

	standards := reg.Sets[1]
	wantUnchangedPath := filepath.Join(repoRoot, "docs", "standards")
	if !sameStrings(standards.Paths, []string{wantUnchangedPath}) {
		t.Errorf("official-standards.Paths: want unchanged [%s], got %v", wantUnchangedPath, standards.Paths)
	}
}

// TestResolve_ReportsUnknownSelectors verifies REQ-01 / S-03: resolving a
// selector list against a loaded registry returns only the sets that exist
// as matched, and names every selector that matched nothing as unknown.
func TestResolve_ReportsUnknownSelectors(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: internal-specs
    tags: [spec, internal]
    mode: retrieval
    paths:
      - ./docs/specs
  - name: official-standards
    tags: [standard]
    mode: full
    paths:
      - ./docs/standards
`)

	reg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	matched, unknown := reg.Resolve([]string{"internal-specs", "tech-doc"}, nil)
	if len(matched) != 1 || matched[0].Name != "internal-specs" {
		t.Errorf("matched: want [internal-specs], got %+v", matched)
	}
	if !sameStrings(unknown, []string{"tech-doc"}) {
		t.Errorf("unknown: want [tech-doc], got %v", unknown)
	}
}

// TestLoad_MissingFileIsNotAnError verifies REQ-01 / S-04: a missing
// projects/resources.yaml is not an error condition — Load returns an empty
// set list and a nil error, never panicking.
func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	repoRoot := t.TempDir()

	reg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: want nil error for missing file, got %v", err)
	}
	if len(reg.Sets) != 0 {
		t.Errorf("Sets: want empty, got %+v", reg.Sets)
	}
}

// TestLoad_RejectsEscapingPaths verifies REQ-01 / REQ-11 / S-36: an absolute
// path and a `..`-escaping path are each rejected and individually named,
// while a legitimate relative path still enters the resolved set.
func TestLoad_RejectsEscapingPaths(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: mixed
    tags: [misc]
    mode: retrieval
    paths:
      - /etc
      - ../../secrets
      - ./docs/specs
`)

	reg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Sets) != 1 {
		t.Fatalf("Sets: want 1 set, got %d (%+v)", len(reg.Sets), reg.Sets)
	}

	wantAcceptedPath := filepath.Join(repoRoot, "docs", "specs")
	if !sameStrings(reg.Sets[0].Paths, []string{wantAcceptedPath}) {
		t.Errorf("mixed.Paths: want only [%s], got %v", wantAcceptedPath, reg.Sets[0].Paths)
	}

	if len(reg.RejectedPaths) != 2 {
		t.Fatalf("RejectedPaths: want 2, got %d (%+v)", len(reg.RejectedPaths), reg.RejectedPaths)
	}
	rejectedByPath := map[string]RejectedPath{}
	for _, rp := range reg.RejectedPaths {
		rejectedByPath[rp.Path] = rp
	}
	for _, wantPath := range []string{"/etc", "../../secrets"} {
		rp, ok := rejectedByPath[wantPath]
		if !ok {
			t.Errorf("RejectedPaths: missing entry for %q", wantPath)
			continue
		}
		if rp.Set != "mixed" {
			t.Errorf("RejectedPaths[%q].Set: want %q, got %q", wantPath, "mixed", rp.Set)
		}
		if rp.Reason == "" {
			t.Errorf("RejectedPaths[%q].Reason: want non-empty", wantPath)
		}
	}
}

// TestLoad_RejectsMissingMode verifies REQ-01 / S-56: a set that does not
// declare `mode` is a configuration error naming that set — it must never
// silently default to retrieval.
func TestLoad_RejectsMissingMode(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: no-mode-set
    tags: [spec]
    paths:
      - ./docs/specs
`)

	_, err := Load(repoRoot, "")
	if err == nil {
		t.Fatal("Load: want error for missing mode, got nil")
	}
	if !strings.Contains(err.Error(), "no-mode-set") {
		t.Errorf("Load error: want it to name the set %q, got %q", "no-mode-set", err.Error())
	}
}

// TestLoad_PreservesDeclarationOrderAndRejectsDuplicateNames verifies
// REQ-01 / S-69: sets: declaration order survives loading verbatim (never
// read into a map and re-emitted), duplicate names within one declaration
// are rejected by name, an overlay overriding an existing name keeps that
// set's original sequence position, and an overlay introducing a new name
// is appended to the tail of the canonical sequence in the overlay's own
// relative order.
func TestLoad_PreservesDeclarationOrderAndRejectsDuplicateNames(t *testing.T) {
	t.Run("order is preserved verbatim", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: a
    tags: [x]
    mode: retrieval
    paths: [./docs/a]
  - name: b
    tags: [x]
    mode: retrieval
    paths: [./docs/b]
  - name: c
    tags: [x]
    mode: retrieval
    paths: [./docs/c]
`)

		reg, err := Load(repoRoot, "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !sameStrings(setNames(reg.Sets), []string{"a", "b", "c"}) {
			t.Errorf("order: want [a b c], got %v", setNames(reg.Sets))
		}
	})

	t.Run("duplicate name is a named config error", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: a
    tags: [x]
    mode: retrieval
    paths: [./docs/a1]
  - name: a
    tags: [x]
    mode: retrieval
    paths: [./docs/a2]
`)

		_, err := Load(repoRoot, "")
		if err == nil {
			t.Fatal("Load: want error for duplicate name, got nil")
		}
		if !strings.Contains(err.Error(), "a") {
			t.Errorf("Load error: want it to name the duplicate %q, got %q", "a", err.Error())
		}
	})

	t.Run("overlay override keeps original position", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: a
    tags: [x]
    mode: retrieval
    paths: [./docs/a]
  - name: b
    tags: [x]
    mode: retrieval
    paths: [./docs/b]
  - name: c
    tags: [x]
    mode: retrieval
    paths: [./docs/c]
`)
		writeYAML(t, overlayPath(repoRoot, "some-system"), `
sets:
  - name: b
    tags: [x, overridden]
    mode: retrieval
    paths: [./docs/b-overlay]
`)

		reg, err := Load(repoRoot, "some-system")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !sameStrings(setNames(reg.Sets), []string{"a", "b", "c"}) {
			t.Errorf("order after override: want [a b c] (b keeps its position), got %v", setNames(reg.Sets))
		}
	})

	t.Run("overlay new name appends to tail in overlay order", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: a
    tags: [x]
    mode: retrieval
    paths: [./docs/a]
  - name: b
    tags: [x]
    mode: retrieval
    paths: [./docs/b]
  - name: c
    tags: [x]
    mode: retrieval
    paths: [./docs/c]
`)
		writeYAML(t, overlayPath(repoRoot, "some-system"), `
sets:
  - name: b
    tags: [x, overridden]
    mode: retrieval
    paths: [./docs/b-overlay]
  - name: d
    tags: [x]
    mode: retrieval
    paths: [./docs/d]
  - name: e
    tags: [x]
    mode: retrieval
    paths: [./docs/e]
`)

		reg, err := Load(repoRoot, "some-system")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !sameStrings(setNames(reg.Sets), []string{"a", "b", "c", "d", "e"}) {
			t.Errorf("order after new-name overlay: want [a b c d e], got %v", setNames(reg.Sets))
		}
	})
}

// TestLoad_ParsesIncludeExclude verifies REQ-03 / T28: include and exclude
// declarations retain their order, while a set without either remains valid.
func TestLoad_ParsesIncludeExclude(t *testing.T) {
	repoRoot := t.TempDir()
	writeYAML(t, canonicalPath(repoRoot), `
sets:
  - name: filtered
    mode: retrieval
    paths: [./docs]
    include: ["*.md", "docs/*.md"]
    exclude: ["skip*", "drafts/*"]
  - name: unfiltered
    mode: full
    paths: [./standards]
`)

	reg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Sets) != 2 {
		t.Fatalf("Sets: want 2, got %d (%+v)", len(reg.Sets), reg.Sets)
	}
	if !sameStrings(reg.Sets[0].Include, []string{"*.md", "docs/*.md"}) {
		t.Errorf("filtered.Include: want [*.md docs/*.md], got %v", reg.Sets[0].Include)
	}
	if !sameStrings(reg.Sets[0].Exclude, []string{"skip*", "drafts/*"}) {
		t.Errorf("filtered.Exclude: want [skip* drafts/*], got %v", reg.Sets[0].Exclude)
	}
	if len(reg.Sets[1].Include) != 0 || len(reg.Sets[1].Exclude) != 0 {
		t.Errorf("unfiltered filters: want empty/nil slices, got Include=%v Exclude=%v", reg.Sets[1].Include, reg.Sets[1].Exclude)
	}
}
