package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type fileRegistry struct {
	Sets []fileSet `yaml:"sets"`
}

type fileSet struct {
	Name    string   `yaml:"name"`
	Tags    []string `yaml:"tags"`
	Mode    string   `yaml:"mode"`
	Paths   []string `yaml:"paths"`
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// Load reads projects/resources.yaml (canonical) under repoRoot, merges the
// per-system overlay projects/<system>/resources.yaml when system is
// non-empty, resolves all paths against repoRoot, and returns the ordered,
// merged sets (REQ-01).
func Load(repoRoot, system string) (Registry, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Registry{}, fmt.Errorf("Load: resolve repo root: %w", err)
	}

	canonical, found, err := loadFile(filepath.Join(root, "projects", "resources.yaml"))
	if err != nil {
		return Registry{}, fmt.Errorf("Load: canonical resources: %w", err)
	}
	if !found {
		return Registry{}, nil
	}

	sets := canonical
	if system != "" {
		overlay, found, err := loadFile(filepath.Join(root, "projects", system, "resources.yaml"))
		if err != nil {
			return Registry{}, fmt.Errorf("Load: system resources: %w", err)
		}
		if found {
			sets = mergeSets(sets, overlay)
		}
	}

	return resolveSets(root, sets), nil
}

// Resolve maps consumer-supplied selectors (set names or tags) against the
// registry's loaded sets, returning the matched sets and any selectors that
// matched nothing (REQ-01 / S-03).
func (r Registry) Resolve(selectors []string, tags []string) (matched []Set, unknown []string) {
	// Preserve the original selector-only behavior exactly.
	if len(tags) == 0 {
		for _, selector := range selectors {
			found := false
			for _, set := range r.Sets {
				if set.Name == selector {
					matched = append(matched, set)
					found = true
					break
				}
			}
			if !found {
				unknown = append(unknown, selector)
			}
		}
		return matched, unknown
	}

	selectedNames := make(map[string]bool, len(selectors))
	for _, selector := range selectors {
		selectedNames[selector] = true
	}
	selectedTags := make(map[string]bool, len(tags))
	for _, tag := range tags {
		selectedTags[tag] = true
	}

	matchedNames := make(map[string]bool, len(r.Sets))
	matchedTags := make(map[string]bool, len(tags))
	for _, set := range r.Sets {
		nameMatch := selectedNames[set.Name]
		tagMatch := false
		for _, tag := range set.Tags {
			if selectedTags[tag] {
				tagMatch = true
				matchedTags[tag] = true
			}
		}
		if nameMatch || tagMatch {
			matched = append(matched, set)
		}
		if nameMatch {
			matchedNames[set.Name] = true
		}
	}
	for _, selector := range selectors {
		if !matchedNames[selector] {
			unknown = append(unknown, selector)
		}
	}
	for _, tag := range tags {
		if !matchedTags[tag] {
			unknown = append(unknown, tag)
		}
	}
	return matched, unknown
}

func loadFile(path string) ([]fileSet, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var registry fileRegistry
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return nil, false, err
	}
	if err := validateSets(registry.Sets); err != nil {
		return nil, false, err
	}
	return registry.Sets, true, nil
}

func validateSets(sets []fileSet) error {
	names := make(map[string]struct{}, len(sets))
	for _, set := range sets {
		if _, exists := names[set.Name]; exists {
			return fmt.Errorf("duplicate resource set name %q", set.Name)
		}
		names[set.Name] = struct{}{}
		if set.Mode != ModeFull && set.Mode != ModeRetrieval {
			return fmt.Errorf("resource set %q has invalid or missing mode %q", set.Name, set.Mode)
		}
	}
	return nil
}

func mergeSets(canonical, overlay []fileSet) []fileSet {
	merged := append([]fileSet(nil), canonical...)
	positions := make(map[string]int, len(merged))
	for i, set := range merged {
		positions[set.Name] = i
	}
	for _, set := range overlay {
		if position, exists := positions[set.Name]; exists {
			merged[position] = set
			continue
		}
		positions[set.Name] = len(merged)
		merged = append(merged, set)
	}
	return merged
}

func resolveSets(repoRoot string, declarations []fileSet) Registry {
	registry := Registry{Sets: make([]Set, 0, len(declarations))}
	for _, declaration := range declarations {
		set := Set{
			Name:    declaration.Name,
			Tags:    declaration.Tags,
			Mode:    declaration.Mode,
			Include: declaration.Include,
			Exclude: declaration.Exclude,
		}
		for _, declaredPath := range declaration.Paths {
			resolved, reason := resolvePath(repoRoot, declaredPath)
			if reason != "" {
				registry.RejectedPaths = append(registry.RejectedPaths, RejectedPath{
					Set: declaration.Name, Path: declaredPath, Reason: reason,
				})
				continue
			}
			set.Paths = append(set.Paths, resolved)
		}
		registry.Sets = append(registry.Sets, set)
	}
	return registry
}

func resolvePath(repoRoot, declaredPath string) (string, string) {
	if filepath.IsAbs(declaredPath) {
		return "", "absolute paths are not allowed"
	}

	resolved := filepath.Join(repoRoot, filepath.Clean(declaredPath))
	relative, err := filepath.Rel(repoRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "path escapes repository root"
	}
	return resolved, ""
}
