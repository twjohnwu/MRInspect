package reviewer

import (
	"fmt"
	"strings"

	"mrinspect/internal/lane"
)

func aggregateLaneFooter(results []lane.LaneResult) footerAggregation {
	aggregation := footerAggregation{}
	seenEvictions := make(map[string]struct{})
	for _, result := range results {
		aggregation.additionalDegraded += len(result.Degraded)
		for _, degraded := range result.Degraded {
			if !strings.Contains(degraded, "evicted section") {
				continue
			}
			entry := fmt.Sprintf("evicted section [%s]: %s", result.LaneID, strings.Join(strings.Fields(degraded), " "))
			if _, exists := seenEvictions[entry]; exists {
				continue
			}
			seenEvictions[entry] = struct{}{}
			aggregation.laneEvictions = append(aggregation.laneEvictions, entry)
		}
	}
	return aggregation
}

func mergeLaneDegradations(aggregation footerAggregation, results []lane.LaneResult, additional []namedLaneDegradation) footerAggregation {
	existing := make(map[namedLaneDegradation]int)
	for _, result := range results {
		for _, degraded := range result.Degraded {
			existing[namedLaneDegradation{laneID: result.LaneID, message: degraded}]++
		}
	}
	for _, degraded := range additional {
		if existing[degraded] > 0 {
			existing[degraded]--
			continue
		}
		aggregation.additionalDegraded++
	}
	return aggregation
}

func (r *MRInspectReviewer) ragFooter() string {
	return r.ragFooterWithAggregation(footerAggregation{})
}

func (r *MRInspectReviewer) ragFooterWithAggregation(aggregation footerAggregation) string {
	footer := r.ragProvenanceFooter(aggregation)
	if len(aggregation.droppedFiles) > 0 {
		footer += "\n\n_Dropped for diff size budget: " + strings.Join(aggregation.droppedFiles, ", ") + "_"
	}
	return footer
}

func (r *MRInspectReviewer) ragProvenanceFooter(aggregation footerAggregation) string {
	state := r.rag.State
	if !state.StorePresent && len(state.Degraded) == 0 && len(state.Composition.Evicted) == 0 && len(state.Composition.Degraded) == 0 && aggregation.additionalDegraded == 0 {
		return ""
	}

	degradedCount := len(state.Degraded) + len(state.Composition.Degraded) + aggregation.additionalDegraded
	parts := []string{fmt.Sprintf("Degraded entries: %d", degradedCount), fmt.Sprintf("skipped files: %d", state.SkippedFiles)}
	if state.StorePresent {
		parts = append([]string{
			fmt.Sprintf("store built_at: %s", state.Store.BuiltAt),
			fmt.Sprintf("resources_sha256: %s", shortSHA(state.ResourcesSHA256)),
		}, parts...)
		if !state.PackageVersionPinned {
			version := state.Store.Version
			if version == "" {
				version = "unknown"
			}
			parts = append(parts, fmt.Sprintf("store version: %s (unpinned)", version))
		}
	} else {
		parts = append([]string{"store: absent"}, parts...)
	}

	for _, evicted := range state.Composition.Evicted {
		parts = append(parts, fmt.Sprintf("evicted section: %s", evicted.Name))
	}
	parts = append(parts, aggregation.laneEvictions...)
	return "\n\n---\nRAG provenance: " + strings.Join(parts, "; ")
}

func shortSHA(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
