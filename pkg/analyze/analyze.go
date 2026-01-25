package analyze

import "math"

// Point represents a single measurement.
type Point struct {
	X float64
	Y float64
}

// Analysis identifies key transition points in the performance curve.
type Analysis struct {
	LinearLimit     Point // Where the "knee" is
	SaturationPoint Point // Where gains stop entirely
}

type Detector struct {
	LinearThreshold float64 // Slope drop-off to signal knee (e.g. 0.5)
	SatThreshold    float64 // Slope drop-off to signal saturation (e.g. 0.05)
}

func (d *Detector) Analyze(points []Point) Analysis {
	if len(points) < 3 {
		return Analysis{}
	}

	// Calculate initial slope (assumed linear region)
	initialSlope := (points[1].Y - points[0].Y) / (points[1].X - points[0].X)
	
	var analysis Analysis

	for i := 2; i < len(points); i++ {
		// Instantaneous slope
		currentSlope := (points[i].Y - points[i-1].Y) / (points[i].X - points[i-1].X)
		
		// Look for Linear Limit (Knee) - kept sensitive
		if analysis.LinearLimit.X == 0 && currentSlope < initialSlope*d.LinearThreshold {
			analysis.LinearLimit = points[i-1]
		}

		// Look for Saturation Point (Plateau) - smoothed
		// Check average slope of last 3 points (if available) to confirm saturation
		avgSlope := currentSlope
		if i >= 3 {
			prevSlope := (points[i-1].Y - points[i-2].Y) / (points[i-1].X - points[i-2].X)
			avgSlope = (currentSlope + prevSlope) / 2
		}

		if analysis.SaturationPoint.X == 0 && avgSlope < initialSlope*d.SatThreshold {
			analysis.SaturationPoint = points[i-1]
		}
	}

	return analysis
}

// CalculateConfidence returns a value between 0 and 1 representing 
// how "clean" the curve is (low noise).
func CalculateConfidence(points []Point) float64 {
	if len(points) < 3 {
		return 0
	}
	// Simple implementation: check for monotonicity
	violations := 0
	for i := 1; i < len(points); i++ {
		if points[i].Y < points[i-1].Y {
			violations++
		}
	}
	return math.Max(0, 1.0-float64(violations)/float64(len(points)))
}
