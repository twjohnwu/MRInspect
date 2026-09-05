package retrievaleval

import "mrinspect/internal/rag"

// Score calculates recall and mean reciprocal rank for the first k hits.
// A non-positive k, or an empty relevant set, produces zero-valued metrics.
func Score(hits []rag.Chunk, relevant []Target, k int) (recall, mrr float64) {
	if k <= 0 || len(relevant) == 0 {
		return 0, 0
	}
	if k < len(hits) {
		hits = hits[:k]
	}

	relevantTargets := make(map[Target]struct{}, len(relevant))
	for _, target := range relevant {
		relevantTargets[target] = struct{}{}
	}

	matchedTargets := make(map[Target]struct{}, len(relevantTargets))
	for index, hit := range hits {
		target := Target{Set: hit.ResourceSet, Path: hit.Source, Heading: hit.Heading}
		if _, ok := relevantTargets[target]; !ok {
			continue
		}

		matchedTargets[target] = struct{}{}
		if mrr == 0 {
			mrr = 1 / float64(index+1)
		}
	}

	recall = float64(len(matchedTargets)) / float64(len(relevant))
	return recall, mrr
}
