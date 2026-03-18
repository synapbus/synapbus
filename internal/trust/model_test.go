package trust

import "testing"

func TestClampScore(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"zero", 0.0, 0.0},
		{"one", 1.0, 1.0},
		{"mid", 0.5, 0.5},
		{"below zero", -0.1, MinScore},
		{"far below zero", -10.0, MinScore},
		{"above one", 1.1, MaxScore},
		{"far above one", 100.0, MaxScore},
		{"small positive", 0.001, 0.001},
		{"near max", 0.999, 0.999},
		{"exactly min", MinScore, MinScore},
		{"exactly max", MaxScore, MaxScore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampScore(tt.input)
			if got != tt.want {
				t.Errorf("ClampScore(%f) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}
