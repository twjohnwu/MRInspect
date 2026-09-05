package retrievaleval

import (
	"fmt"

	"mrinspect/internal/evalrun"
	"mrinspect/internal/lane"
	"mrinspect/internal/rag/resources"
)

type Triple struct {
	Fixture string
	LaneID  string
	Set     resources.Set
	Terms   []string
	K       int
}

type Plan struct {
	Triples  []Triple
	Warnings []string
}

func BuildPlan(repoRoot, system string, fixtures []evalrun.Fixture) (Plan, error) {
	laneRegistry, err := lane.Load(repoRoot, system)
	if err != nil {
		return Plan{}, fmt.Errorf("plan: load lanes: %w", err)
	}
	resourceRegistry, err := resources.Load(repoRoot, system)
	if err != nil {
		return Plan{}, fmt.Errorf("plan: load resources: %w", err)
	}

	type resolvedLane struct {
		id   string
		sets []resources.Set
		k    int
	}

	plan := Plan{}
	var resolved []resolvedLane
	for _, laneDeclaration := range laneRegistry.Lanes {
		if !laneDeclaration.Enabled {
			continue
		}

		sets, unknown := resourceRegistry.Resolve(
			laneDeclaration.Resources.Sets,
			laneDeclaration.Resources.Tags,
		)
		for _, selector := range unknown {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"warning: lane %q unknown resource selector: %s",
				laneDeclaration.ID,
				selector,
			))
		}
		if len(sets) == 0 {
			continue
		}

		k := laneDeclaration.TopK
		if k <= 0 {
			k = lane.DefaultLaneTopK
		}
		resolved = append(resolved, resolvedLane{id: laneDeclaration.ID, sets: sets, k: k})
	}

	for _, fixture := range fixtures {
		terms := lane.Terms(fixture.Changes)
		for _, lane := range resolved {
			for _, set := range lane.sets {
				plan.Triples = append(plan.Triples, Triple{
					Fixture: fixture.Name,
					LaneID:  lane.id,
					Set:     set,
					Terms:   terms,
					K:       lane.k,
				})
			}
		}
	}
	return plan, nil
}
