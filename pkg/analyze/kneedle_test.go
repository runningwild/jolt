package analyze

import (
	"testing"
)

func TestFindKnee(t *testing.T) {
	tests := []struct {
		name     string
		points   []Point
		wantX    float64
	}{
		{
			name: "Perfect Knee",
			points: []Point{
				{X: 1, Y: 10},
				{X: 2, Y: 20},
				{X: 3, Y: 28}, // Knee
				{X: 4, Y: 30},
				{X: 5, Y: 31},
			},
			wantX: 3,
		},
		{
			name: "Linear",
			points: []Point{
				{X: 1, Y: 10},
				{X: 2, Y: 20},
				{X: 3, Y: 30},
				{X: 4, Y: 40},
			},
			// In a purely linear graph, the "furthest from diagonal" logic 
			// usually picks a point in the middle or end depending on floating point,
			// but effectively there is no knee. 
			// Kneedle on a line y=x returns 0 distance for all.
			// Our impl initializes maxDist = -1, so it should pick the first point that satisfies dist > -1.
			// Actually, let's trace: dist will be 0.0 for all points. 0 > -1 is true.
			// It will pick the first point.
			wantX: 1, 
		},
		{
			name: "Plateau",
			points: []Point{
				{X: 1, Y: 100},
				{X: 2, Y: 100},
				{X: 3, Y: 100},
			},
			// Normalized: (0,1), (0.5, 1), (1, 1). 
			// Diagonal is y=x.
			// P1: x=0, y=NaN? No, minY=maxY=100.
			// "if maxY == minY { return last }"
			wantX: 3,
		},
		{
			name: "Step Function",
			points: []Point{
				{X: 1, Y: 0},
				{X: 2, Y: 0},
				{X: 3, Y: 100}, // Jump
				{X: 4, Y: 100},
			},
			// Norm: P1(0,0), P2(0.33,0), P3(0.66,1), P4(1,1)
			// P1: 0-0 = 0
			// P2: 0 - 0.33 = -0.33
			// P3: 1 - 0.66 = 0.33 (Max)
			// P4: 1 - 1 = 0
			wantX: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindKnee(tt.points)
			if got.X != tt.wantX {
				t.Errorf("FindKnee() = %v, want X=%v", got, tt.wantX)
			}
		})
	}
}
