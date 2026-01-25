package analyze

// Point represents a single measurement in the search space.
type Point struct {
	X float64 // The variable parameter (e.g., Workers, QueueDepth)
	Y float64 // The metric (e.g., IOPS)
}

// KneeDetector identifies the "knee" point in a series of measurements.
type KneeDetector interface {
	FindKnee(points []Point) (Point, bool)
}

// SlopeDetector finds the knee by looking for a significant drop in slope
// compared to the initial linear growth.
type SlopeDetector struct {
	Threshold float64 // e.g., 0.5 means slope dropped to 50% of initial
}

func (d *SlopeDetector) FindKnee(points []Point) (Point, bool) {
	if len(points) < 3 {
		return Point{}, false
	}

	// Calculate initial slope (from first two points)
	initialSlope := (points[1].Y - points[0].Y) / (points[1].X - points[0].X)
	if initialSlope <= 0 {
		return Point{}, false
	}

	for i := 2; i < len(points); i++ {
		currentSlope := (points[i].Y - points[i-1].Y) / (points[i].X - points[i-1].X)
		if currentSlope < initialSlope*d.Threshold {
			// The point before this one was likely the knee
			return points[i-1], true
		}
	}

	return Point{}, false
}
