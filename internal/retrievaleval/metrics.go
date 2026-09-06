package retrievaleval

import (
	"math/rand"

	"mrinspect/internal/rag"
)

var DefaultShuffleSeeds = []int64{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
	11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
}

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
		target := targetForChunk(hit)
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

func Distractors(hits []rag.Chunk, distractors []Target, k int) int {
	if k <= 0 || len(hits) == 0 || len(distractors) == 0 {
		return 0
	}
	if k < len(hits) {
		hits = hits[:k]
	}

	distractorTargets := make(map[Target]struct{}, len(distractors))
	for _, target := range distractors {
		distractorTargets[target] = struct{}{}
	}

	matchedTargets := make(map[Target]struct{}, len(distractorTargets))
	for _, hit := range hits {
		target := targetForChunk(hit)
		if _, ok := distractorTargets[target]; ok {
			matchedTargets[target] = struct{}{}
		}
	}

	return len(matchedTargets)
}

func ShuffleScore(pool []rag.Chunk, relevant []Target, k int, seeds []int64) (recall, mrr float64) {
	runs := forEachShuffle(pool, k, seeds, func(hits []rag.Chunk) {
		runRecall, runMRR := Score(hits, relevant, k)
		recall += runRecall
		mrr += runMRR
	})
	if runs == 0 {
		return 0, 0
	}

	return recall / float64(runs), mrr / float64(runs)
}

func ShuffleDistractors(pool []rag.Chunk, distractors []Target, k int, seeds []int64) float64 {
	var total int
	runs := forEachShuffle(pool, k, seeds, func(hits []rag.Chunk) {
		total += Distractors(hits, distractors, k)
	})
	if runs == 0 {
		return 0
	}

	return float64(total) / float64(runs)
}

func targetForChunk(chunk rag.Chunk) Target {
	return Target{Set: chunk.ResourceSet, Path: chunk.Source, Heading: chunk.Heading}
}

func forEachShuffle(pool []rag.Chunk, k int, seeds []int64, visit func([]rag.Chunk)) int {
	if len(pool) == 0 || len(seeds) == 0 {
		return 0
	}

	for _, seed := range seeds {
		shuffled := append([]rag.Chunk(nil), pool...)
		rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		if k <= 0 {
			shuffled = shuffled[:0]
		} else if k < len(shuffled) {
			shuffled = shuffled[:k]
		}
		visit(shuffled)
	}

	return len(seeds)
}
