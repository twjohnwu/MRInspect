package retrievaleval

import (
	"math"
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
