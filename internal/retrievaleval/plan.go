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

func BuildPlan(repoRoot, system string, fixtures []evalrun.Fixture) ([]Triple, error) {
	laneRegistry, err := lane.Load(repoRoot, system)
	if err != nil {
		return nil, fmt.Errorf("plan: load lanes: %w", err)
	}
	resourceRegistry, err := resources.Load(repoRoot, system)
	if err != nil {
		return nil, fmt.Errorf("plan: load resources: %w", err)
	}

	var plan []Triple
	for _, fixture := range fixtures {
		terms := lane.Terms(fixture.Changes)
		for _, laneDeclaration := range laneRegistry.Lanes {
			if !laneDeclaration.Enabled {
				continue
			}

			sets, _ := resourceRegistry.Resolve(
				laneDeclaration.Resources.Sets,
				laneDeclaration.Resources.Tags,
			)
			if len(sets) == 0 {
				continue
			}

			k := laneDeclaration.TopK
			if k <= 0 {
				k = lane.DefaultLaneTopK
			}
			for _, set := range sets {
				plan = append(plan, Triple{
					Fixture: fixture.Name,
					LaneID:  laneDeclaration.ID,
					Set:     set,
					Terms:   terms,
					K:       k,
				})
			}
		}
	}
	return plan, nil
}
