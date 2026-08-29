package lane

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type fileRegistry struct {
	Lanes []fileLane `yaml:"lanes"`
}

type fileLane struct {
	ID        string         `yaml:"id"`
	Enabled   *bool          `yaml:"enabled"`
	Template  string         `yaml:"template"`
	Intent    string         `yaml:"intent"`
	Resources *fileResources `yaml:"resources"`
	TopK      int            `yaml:"topK"`
	Model     string         `yaml:"model"`
}

type fileResources struct {
	Sets []string `yaml:"sets"`
	Tags []string `yaml:"tags"`
}

// DefaultLaneTopK is the retrieval TopK applied when a lane declaration
// omits topK (or declares it as 0/negative). topK is optional in
// lanes.yaml, but the rag retriever treats TopK <= 0 as "return nothing" —
// without this default, an out-of-the-box lane with no declared topK would
// silently retrieve zero chunks instead of using a sane default.
const DefaultLaneTopK = 8

// Resources selects resource sets by explicit name and by tag.
type Resources struct {
	Sets []string
	Tags []string
}

// Lane is one ordered declaration from the lane registry.
type Lane struct {
	ID        string
	Enabled   bool
	Template  string
	Intent    string
	Resources Resources
	TopK      int
	Model     string
}

// Registry is the ordered result of loading canonical and per-system lanes.
type Registry struct {
	Lanes []Lane
}

// Load reads projects/lanes.yaml (canonical) under repoRoot and merges the
// per-system overlay projects/<system>/lanes.yaml when system is non-empty.
// Overlay declarations replace canonical lanes by ID without changing their
// positions; new overlay lanes append in declaration order (REQ-01).
func Load(repoRoot, system string) (Registry, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Registry{}, fmt.Errorf("Load: resolve repo root: %w", err)
	}

	canonical, found, err := loadFile(filepath.Join(root, "projects", "lanes.yaml"))
	if err != nil {
		return Registry{}, fmt.Errorf("Load: canonical lanes: %w", err)
	}
	if !found {
		return Registry{}, nil
	}

	lanes := canonical
	if system != "" {
		overlay, found, err := loadFile(filepath.Join(root, "projects", system, "lanes.yaml"))
		if err != nil {
			return Registry{}, fmt.Errorf("Load: system lanes: %w", err)
		}
		if found {
			lanes = mergeLanes(lanes, overlay)
		}
	}

	return Registry{Lanes: lanes}, nil
}

func loadFile(path string) ([]Lane, bool, error) {
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
	if err := validateLanes(registry.Lanes); err != nil {
		return nil, false, err
	}
	return convertLanes(registry.Lanes), true, nil
}

func validateLanes(lanes []fileLane) error {
	ids := make(map[string]struct{}, len(lanes))
	for _, lane := range lanes {
		if len(lane.ID) == 0 {
			return missingFieldError(lane.ID, "id")
		}
		if _, exists := ids[lane.ID]; exists {
			return fmt.Errorf("duplicate lane id %q", lane.ID)
		}
		ids[lane.ID] = struct{}{}
		if lane.Enabled == nil {
			return missingFieldError(lane.ID, "enabled")
		}
		if lane.Template == "" {
			return missingFieldError(lane.ID, "template")
		}
		if lane.Intent == "" {
			return missingFieldError(lane.ID, "intent")
		}
		if lane.Resources == nil {
			return missingFieldError(lane.ID, "resources")
		}
	}
	return nil
}

func missingFieldError(id, field string) error {
	if id == "" {
		id = "<missing>"
	}
	return fmt.Errorf("lane id %q is missing required field %q", id, field)
}

func convertLanes(declarations []fileLane) []Lane {
	lanes := make([]Lane, 0, len(declarations))
	for _, declaration := range declarations {
		topK := declaration.TopK
		if topK <= 0 {
			topK = DefaultLaneTopK
		}
		lanes = append(lanes, Lane{
			ID:       declaration.ID,
			Enabled:  *declaration.Enabled,
			Template: declaration.Template,
			Intent:   declaration.Intent,
			Resources: Resources{
				Sets: declaration.Resources.Sets,
				Tags: declaration.Resources.Tags,
			},
			TopK:  topK,
			Model: declaration.Model,
		})
	}
	return lanes
}

func mergeLanes(canonical, overlay []Lane) []Lane {
	merged := append([]Lane(nil), canonical...)
	positions := make(map[string]int, len(merged))
	for i, lane := range merged {
		positions[lane.ID] = i
	}
	for _, lane := range overlay {
		if position, exists := positions[lane.ID]; exists {
			merged[position] = lane
			continue
		}
		positions[lane.ID] = len(merged)
		merged = append(merged, lane)
	}
	return merged
}
