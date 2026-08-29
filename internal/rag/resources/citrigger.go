package resources

import (
	"fmt"
	"os"
	pathpkg "path"
	"strings"

	"gopkg.in/yaml.v3"
)

// CIIndexJob is the subset of the index job's CI configuration required to
// verify REQ-09 trigger and artifact behaviour.
type CIIndexJob struct {
	Rules            []CIRule
	ArtifactPaths    []string
	ArtifactExpireIn string
}

// CIRule is one CI rule relevant to the index job.
type CIRule struct {
	If      string
	Changes []string
}

// LoadDeclaredPaths loads every path declared by projects/resources.yaml.
func LoadDeclaredPaths(resourcesYAMLPath string) ([]string, error) {
	data, err := os.ReadFile(resourcesYAMLPath)
	if err != nil {
		return nil, fmt.Errorf("read declared resources: %w", err)
	}

	var registry fileRegistry
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse declared resources: %w", err)
	}

	var paths []string
	for _, set := range registry.Sets {
		paths = append(paths, set.Paths...)
	}
	return paths, nil
}

// LoadIndexJob loads the index job's rules and artifacts from .gitlab-ci.yml.
func LoadIndexJob(ciYAMLPath string) (CIIndexJob, error) {
	data, err := os.ReadFile(ciYAMLPath)
	if err != nil {
		return CIIndexJob{}, fmt.Errorf("read CI configuration: %w", err)
	}

	var document struct {
		Index *struct {
			Rules     []CIRule `yaml:"rules"`
			Artifacts struct {
				Paths    []string `yaml:"paths"`
				ExpireIn string   `yaml:"expire_in"`
			} `yaml:"artifacts"`
		} `yaml:"index"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return CIIndexJob{}, fmt.Errorf("parse CI configuration: %w", err)
	}
	if document.Index == nil {
		return CIIndexJob{}, fmt.Errorf("CI configuration has no index job")
	}

	return CIIndexJob{
		Rules:            document.Index.Rules,
		ArtifactPaths:    document.Index.Artifacts.Paths,
		ArtifactExpireIn: document.Index.Artifacts.ExpireIn,
	}, nil
}

// CoverageGaps returns declarations not covered by changesGlobs. A declared
// directory is covered when a glob matches files beneath that directory; a
// declared file is covered when the glob matches that file itself.
func CoverageGaps(declaredPaths []string, changesGlobs []string) []string {
	var gaps []string
	for _, declared := range declaredPaths {
		path := normalizeCIPath(declared)
		if path == "" || !coveredByChanges(path, changesGlobs) {
			gaps = append(gaps, declared)
		}
	}
	return gaps
}

func normalizeCIPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return strings.TrimSuffix(value, "/")
}

func coveredByChanges(declared string, globs []string) bool {
	isFile := pathpkg.Ext(pathpkg.Base(declared)) != ""
	for _, glob := range globs {
		glob = normalizeCIPath(glob)
		if glob == "" {
			continue
		}
		if isFile {
			if gitLabGlobMatch(glob, declared) {
				return true
			}
			continue
		}

		// A literal path below a declaration is itself evidence that the glob
		// can select a file in that directory. The matcher handles wildcard
		// prefixes (for example docs*/**/*) for the remaining cases.
		if strings.HasPrefix(glob, declared+"/") ||
			gitLabGlobMatch(glob, declared+"/.mrinspect") ||
			gitLabGlobMatch(glob, declared+"/.mrinspect.md") ||
			gitLabGlobMatch(glob, declared+"/nested/.mrinspect") {
			return true
		}
	}
	return false
}

// gitLabGlobMatch implements the path portion of GitLab's changes glob
// behaviour needed here: * does not cross a slash, while ** does.
func gitLabGlobMatch(pattern, name string) bool {
	var match func(int, int) bool
	match = func(patternAt, nameAt int) bool {
		if patternAt == len(pattern) {
			return nameAt == len(name)
		}
		if strings.HasPrefix(pattern[patternAt:], "**/") {
			if match(patternAt+3, nameAt) {
				return true
			}
			for next := nameAt; next < len(name); next++ {
				if name[next] == '/' && match(patternAt+3, next+1) {
					return true
				}
			}
			return false
		}
		if strings.HasPrefix(pattern[patternAt:], "**") {
			for next := nameAt; next <= len(name); next++ {
				if match(patternAt+2, next) {
					return true
				}
			}
			return false
		}
		switch pattern[patternAt] {
		case '*':
			for next := nameAt; next <= len(name); next++ {
				if match(patternAt+1, next) {
					return true
				}
				if next == len(name) || name[next] == '/' {
					break
				}
			}
		case '?':
			if nameAt < len(name) && name[nameAt] != '/' && match(patternAt+1, nameAt+1) {
				return true
			}
		default:
			if nameAt < len(name) && pattern[patternAt] == name[nameAt] && match(patternAt+1, nameAt+1) {
				return true
			}
		}
		return false
	}
	return match(0, 0)
}
