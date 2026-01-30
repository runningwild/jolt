package analyze

import (
	"math"
	"math/rand"
)

type LinearResult struct {
	Slope       float64
	Intercept   float64
	Coverage    float64 // 0.0 to 1.0 (percentage of points in the region)
	StartX      float64
	EndX        float64
	InlierCount int
}

// FindDominantSlope uses RANSAC to find the longest linear region.
// tolerance: relative error threshold (e.g., 0.05 for 5%)
func FindDominantSlope(points []Point, tolerance float64) LinearResult {
	n := len(points)
	if n < 2 {
		return LinearResult{}
	}

	iterations := 500
	bestInliers := []Point{}

	// RANSAC Loop
	for i := 0; i < iterations; i++ {
		// 1. Pick two random points
		idx1 := rand.Intn(n)
		idx2 := rand.Intn(n)
		if idx1 == idx2 {
			continue
		}
		p1 := points[idx1]
		p2 := points[idx2]

		// 2. Calculate Model (y = mx + c)
		// Avoid vertical lines (divide by zero)
		if math.Abs(p2.X-p1.X) < 1e-9 {
			continue
		}
		m := (p2.Y - p1.Y) / (p2.X - p1.X)
		c := p1.Y - m*p1.X

		// 3. Count Inliers
		currentInliers := make([]Point, 0, n)
		for _, p := range points {
			predictedY := m*p.X + c
			// Relative Error: |Obs - Pred| / Obs
			// Handle Divide by Zero if Obs is 0
			var err float64
			if math.Abs(p.Y) < 1e-9 {
				err = math.Abs(predictedY - p.Y) // Absolute error for near-zero
			} else {
				err = math.Abs(predictedY-p.Y) / math.Abs(p.Y)
			}

			if err <= tolerance {
				currentInliers = append(currentInliers, p)
			}
		}

		// 4. Keep Best
		if len(currentInliers) > len(bestInliers) {
			bestInliers = currentInliers
		}
	}

	// 5. Refine: Least Squares on Best Inliers
	if len(bestInliers) < 2 {
		return LinearResult{}
	}

	m, c := leastSquares(bestInliers)
	
	// Calculate Bounds
	minX, maxX := bestInliers[0].X, bestInliers[0].X
	for _, p := range bestInliers {
		if p.X < minX { minX = p.X }
		if p.X > maxX { maxX = p.X }
	}

	return LinearResult{
		Slope:       m,
		Intercept:   c,
		Coverage:    float64(len(bestInliers)) / float64(n),
		StartX:      minX,
		EndX:        maxX,
		InlierCount: len(bestInliers),
	}
}

// leastSquares performs simple linear regression on a set of points
func leastSquares(points []Point) (m, c float64) {
	var sumX, sumY, sumXY, sumXX float64
	n := float64(len(points))

	for _, p := range points {
		sumX += p.X
		sumY += p.Y
		sumXY += p.X * p.Y
		sumXX += p.X * p.X
	}

	m = (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
	c = (sumY - m*sumX) / n
	return m, c
}
