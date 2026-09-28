package ragcmd

import "testing"

// TestEvalFixturesDir verifies REQ-01 / S-02: retrieval evaluation swaps the
// default fixtures directory while preserving explicit and non-retrieval values.
func TestEvalFixturesDir(t *testing.T) {
	tests := []struct {
		name      string
		retrieval bool
		explicit  bool
		value     string
		want      string
	}{
		{
			name:      "retrieval default",
			retrieval: true,
			explicit:  false,
			value:     "eval/fixtures",
			want:      "eval/retrieval-fixtures",
		},
		{
			name:      "retrieval explicit",
			retrieval: true,
			explicit:  true,
			value:     "x",
			want:      "x",
		},
		{
			name:      "non-retrieval default",
			retrieval: false,
			explicit:  false,
			value:     "eval/fixtures",
			want:      "eval/fixtures",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvalFixturesDir(tt.retrieval, tt.explicit, tt.value)
			if got != tt.want {
				t.Errorf("EvalFixturesDir(%t, %t, %q) = %q, want %q", tt.retrieval, tt.explicit, tt.value, got, tt.want)
			}
		})
	}
}
