package retrievaleval

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const (
	maxGoldenBytes  = 1 << 20
	maxMissingItems = 50
)

var (
	requiredLanes = []string{"spec-conformance", "standards"}
)

type Target struct {
	Set     string `yaml:"set"`
	Path    string `yaml:"path"`
	Heading string `yaml:"heading"`
}

type Entry struct {
	Fixture     string   `yaml:"fixture"`
	Lane        string   `yaml:"lane"`
	Relevant    []Target `yaml:"relevant"`
	Paraphrase  []Target `yaml:"paraphrase"`
	Distractors []Target `yaml:"distractors"`
}

type Golden struct {
	Entries []Entry `yaml:"entries"`
}

func (g Golden) ValidateAgainstPlan(triples []Triple) error {
	type fixtureLane struct {
		fixture string
		lane    string
	}

	allowedSets := make(map[fixtureLane]map[string]struct{})
	entries := make(map[fixtureLane]Entry, len(g.Entries))
	for _, entry := range g.Entries {
		entries[fixtureLane{fixture: entry.Fixture, lane: entry.Lane}] = entry
	}
	for _, triple := range triples {
		key := fixtureLane{fixture: triple.Fixture, lane: triple.LaneID}
		if allowedSets[key] == nil {
			allowedSets[key] = make(map[string]struct{})
		}
		allowedSets[key][triple.Set.Name] = struct{}{}
	}

	for _, entry := range g.Entries {
		key := fixtureLane{fixture: entry.Fixture, lane: entry.Lane}
		for _, list := range targetLists(entry) {
			for _, target := range list.targets {
				if _, ok := allowedSets[key][target.Set]; !ok {
					return fmt.Errorf(
						"load golden: fixture %q lane %q target %s not in a set resolved for this lane",
						entry.Fixture,
						entry.Lane,
						targetReference(target),
					)
				}
			}
		}
	}

	for _, triple := range triples {
		entry := entries[fixtureLane{fixture: triple.Fixture, lane: triple.LaneID}]
		for _, list := range targetLists(entry) {
			found := false
			for _, target := range list.targets {
				if target.Set == triple.Set.Name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf(
					"load golden: fixture %q lane %q list %q has no target in set %q",
					triple.Fixture,
					triple.LaneID,
					list.name,
					triple.Set.Name,
				)
			}
		}
	}
	return nil
}

func LoadGolden(path string, fixtures []string) (Golden, error) {
	if len(fixtures) == 0 {
		return Golden{}, fmt.Errorf("load golden: no fixtures")
	}

	info, err := os.Stat(path)
	if err != nil {
		return Golden{}, fmt.Errorf("load golden: stat: %w", err)
	}
	if info.Size() > maxGoldenBytes {
		return Golden{}, fmt.Errorf("load golden: golden exceeds 1 MiB")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Golden{}, fmt.Errorf("load golden: read: %w", err)
	}
	if !utf8.Valid(content) {
		return Golden{}, fmt.Errorf("load golden: invalid UTF-8")
	}

	var golden Golden
	if err := yaml.Unmarshal(content, &golden); err != nil {
		return Golden{}, fmt.Errorf("load golden: parse YAML: %w", err)
	}
	if err := golden.validateCoverage(fixtures); err != nil {
		return Golden{}, err
	}
	return golden, nil
}

func (g Golden) ValidateAgainstStore(ctx context.Context, storePath string) error {
	db, err := sql.Open("sqlite", "file:"+storePath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("validate golden against store: open: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT s.name, d.rel_path, c.heading FROM chunks c JOIN documents d ON d.id=c.document_id JOIN resource_sets s ON s.id=d.set_id`)
	if err != nil {
		return fmt.Errorf("validate golden against store: query targets: %w", err)
	}
	defer rows.Close()

	stored := make(map[string]struct{})
	for rows.Next() {
		var set, path, heading string
		if err := rows.Scan(&set, &path, &heading); err != nil {
			return fmt.Errorf("validate golden against store: scan target: %w", err)
		}
		stored[targetKey(set, path, heading)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate golden against store: iterate targets: %w", err)
	}

	var missing []string
	for _, entry := range g.Entries {
		for _, list := range targetLists(entry) {
			for _, target := range list.targets {
				if _, ok := stored[targetKey(target.Set, target.Path, target.Heading)]; !ok {
					missing = append(missing, targetReference(target))
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	var message strings.Builder
	message.WriteString("golden targets missing from store:")
	for _, reference := range missing[:min(len(missing), maxMissingItems)] {
		message.WriteByte('\n')
		message.WriteString(reference)
	}
	if omitted := len(missing) - maxMissingItems; omitted > 0 {
		fmt.Fprintf(&message, "\nand %d more", omitted)
	}
	return fmt.Errorf("%s", message.String())
}

func (g Golden) validateCoverage(fixtures []string) error {
	type coverage struct {
		lanes map[string]bool
	}

	byFixture := make(map[string]*coverage, len(fixtures))
	for _, fixture := range fixtures {
		byFixture[fixture] = &coverage{
			lanes: make(map[string]bool, len(requiredLanes)),
		}
	}
	for _, entry := range g.Entries {
		seenTargets := make(map[string]struct{})
		for _, list := range targetLists(entry) {
			for _, target := range list.targets {
				key := targetKey(target.Set, target.Path, target.Heading)
				if _, exists := seenTargets[key]; exists {
					return fmt.Errorf(
						"load golden: fixture %q lane %q duplicate target %s",
						entry.Fixture,
						entry.Lane,
						targetReference(target),
					)
				}
				seenTargets[key] = struct{}{}
			}
		}

		item, ok := byFixture[entry.Fixture]
		if !ok {
			continue
		}
		if item.lanes[entry.Lane] {
			return fmt.Errorf("load golden: duplicate entry for fixture %q lane %q", entry.Fixture, entry.Lane)
		}
		item.lanes[entry.Lane] = true
	}

	for _, fixture := range fixtures {
		item := byFixture[fixture]
		for _, lane := range requiredLanes {
			if !item.lanes[lane] {
				return fmt.Errorf("load golden: fixture %q missing lane %q", fixture, lane)
			}
		}
	}
	return nil
}

type targetList struct {
	name    string
	targets []Target
}

func targetLists(entry Entry) []targetList {
	return []targetList{
		{name: "relevant", targets: entry.Relevant},
		{name: "paraphrase", targets: entry.Paraphrase},
		{name: "distractors", targets: entry.Distractors},
	}
}

func targetKey(set, path, heading string) string {
	return set + "\x00" + path + "\x00" + heading
}

func targetReference(target Target) string {
	return target.Set + "/" + target.Path + "#" + target.Heading
}
