package retrievaleval

import (
	"math"
	"reflect"
	"testing"

	"mrinspect/internal/rag"
)

// TestMetrics_RecallMRRAndTruncation verifies REQ-03 / S-06 metric scoring and k truncation.
func TestMetrics_RecallMRRAndTruncation(t *testing.T) {
	targetA := Target{Set: "set-a", Path: "a.md", Heading: "Heading A"}
	targetB := Target{Set: "set-b", Path: "b.md", Heading: "Heading B"}

	chunk := func(set, path, heading string) rag.Chunk {
		return rag.Chunk{ResourceSet: set, Source: path, Heading: heading}
	}

	hitA := chunk(targetA.Set, targetA.Path, targetA.Heading)
	hitB := chunk(targetB.Set, targetB.Path, targetB.Heading)
	hitC := chunk("set-c", "c.md", "Heading C")
	hitD := chunk("set-d", "d.md", "Heading D")
	hitE := chunk("set-e", "e.md", "Heading E")

	tests := []struct {
		name       string
		hits       []rag.Chunk
		relevant   []Target
		k          int
		wantRecall float64
		wantMRR    float64
	}{
		{
			name:       "one of two relevant at rank two",
			hits:       []rag.Chunk{hitC, hitA, hitD, hitE},
			relevant:   []Target{targetA, targetB},
			k:          4,
			wantRecall: 0.5,
			wantMRR:    0.5,
		},
		{
			name:       "both relevant with first at rank one",
			hits:       []rag.Chunk{hitA, hitB, hitC, hitD},
			relevant:   []Target{targetA, targetB},
			k:          4,
			wantRecall: 1,
			wantMRR:    1,
		},
		{
			name:       "relevant hits beyond k are ignored",
			hits:       []rag.Chunk{hitC, hitD, hitA, hitB},
			relevant:   []Target{targetA, targetB},
			k:          2,
			wantRecall: 0,
			wantMRR:    0,
		},
		{
			name:       "empty hits",
			hits:       nil,
			relevant:   []Target{targetA},
			k:          4,
			wantRecall: 0,
			wantMRR:    0,
		},
	}

	const tolerance = 1e-9
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recall, mrr := Score(tt.hits, tt.relevant, tt.k)
			if math.Abs(recall-tt.wantRecall) > tolerance {
				t.Errorf("Score() recall = %v, want %v", recall, tt.wantRecall)
			}
			if math.Abs(mrr-tt.wantMRR) > tolerance {
				t.Errorf("Score() MRR = %v, want %v", mrr, tt.wantMRR)
			}
		})
	}
}

// TestMetrics_ShuffleArmAndDistractorCount verifies REQ-03 / S-05 shuffle scoring and distractor counting.
func TestMetrics_ShuffleArmAndDistractorCount(t *testing.T) {
	const k = 4

	chunk := func(set, path, heading string) rag.Chunk {
		return rag.Chunk{ResourceSet: set, Source: path, Heading: heading}
	}

	relevantTarget := Target{Set: "relevant-set", Path: "relevant.md", Heading: "Relevant"}
	paraphraseTarget := Target{Set: "paraphrase-set", Path: "paraphrase.md", Heading: "Paraphrase"}
	distractorTarget := Target{Set: "distractor-set", Path: "distractor.md", Heading: "Distractor"}
	relevant := []Target{relevantTarget}
	paraphrase := []Target{paraphraseTarget}
	distractors := []Target{distractorTarget}
	pool := []rag.Chunk{
		chunk(relevantTarget.Set, relevantTarget.Path, relevantTarget.Heading),
		chunk("filler-set-02", "filler-02.md", "Filler 02"),
		chunk(distractorTarget.Set, distractorTarget.Path, distractorTarget.Heading),
		chunk("filler-set-04", "filler-04.md", "Filler 04"),
		chunk("filler-set-05", "filler-05.md", "Filler 05"),
		chunk(paraphraseTarget.Set, paraphraseTarget.Path, paraphraseTarget.Heading),
		chunk("filler-set-07", "filler-07.md", "Filler 07"),
		chunk("filler-set-08", "filler-08.md", "Filler 08"),
		chunk("filler-set-09", "filler-09.md", "Filler 09"),
		chunk("filler-set-10", "filler-10.md", "Filler 10"),
		chunk("filler-set-11", "filler-11.md", "Filler 11"),
		chunk("filler-set-12", "filler-12.md", "Filler 12"),
		chunk("filler-set-13", "filler-13.md", "Filler 13"),
		chunk("filler-set-14", "filler-14.md", "Filler 14"),
		chunk("filler-set-15", "filler-15.md", "Filler 15"),
		chunk("filler-set-16", "filler-16.md", "Filler 16"),
	}

	if got, _ := Score(pool[:k], relevant, k); got != 1 {
		t.Errorf("Score(off, relevant) recall = %v, want 1", got)
	}
	if got, _ := Score(pool[:k], paraphrase, k); got != 0 {
		t.Errorf("Score(off, paraphrase) recall = %v, want 0", got)
	}
	if got := Distractors(pool[:k], distractors, k); got != 1 {
		t.Errorf("Distractors(off) = %d, want 1", got)
	}

	wantSeeds := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	if got := len(DefaultShuffleSeeds); got != 20 {
		t.Errorf("len(DefaultShuffleSeeds) = %d, want 20", got)
	}
	if !reflect.DeepEqual(DefaultShuffleSeeds, wantSeeds) {
		t.Errorf("DefaultShuffleSeeds = %v, want %v", DefaultShuffleSeeds, wantSeeds)
	}

	poolBefore := append([]rag.Chunk(nil), pool...)
	shuffleRecall, shuffleMRR := ShuffleScore(pool, paraphrase, k, DefaultShuffleSeeds)
	if shuffleRecall <= 0 || shuffleRecall >= 1 {
		t.Errorf("ShuffleScore() recall = %v, want a value strictly between 0 and 1", shuffleRecall)
	}
	repeatedRecall, repeatedMRR := ShuffleScore(pool, paraphrase, k, DefaultShuffleSeeds)
	if repeatedRecall != shuffleRecall || repeatedMRR != shuffleMRR {
		t.Errorf("ShuffleScore() repeated = (%v, %v), want (%v, %v)", repeatedRecall, repeatedMRR, shuffleRecall, shuffleMRR)
	}
	if !reflect.DeepEqual(pool, poolBefore) {
		t.Errorf("ShuffleScore() mutated pool: got %v, want %v", pool, poolBefore)
	}
	if got := ShuffleDistractors(pool, distractors, k, DefaultShuffleSeeds); got < 0 || got > 1 {
		t.Errorf("ShuffleDistractors() = %v, want a value in [0, 1]", got)
	}

	if got := Distractors(nil, distractors, k); got != 0 {
		t.Errorf("Distractors(nil) = %d, want 0", got)
	}
	emptyRecall, emptyMRR := ShuffleScore(nil, relevant, k, DefaultShuffleSeeds)
	if emptyRecall != 0 || emptyMRR != 0 {
		t.Errorf("ShuffleScore(nil) = (%v, %v), want (0, 0)", emptyRecall, emptyMRR)
	}
	if got := ShuffleDistractors(nil, distractors, k, DefaultShuffleSeeds); got != 0 {
		t.Errorf("ShuffleDistractors(nil) = %v, want 0", got)
	}
}
