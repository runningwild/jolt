package analyze

import (
	"sort"
)

type Point struct {
	X float64
	Y float64
	OriginalX interface{} // Keep track of the original param (e.g. 16 workers)
}

// FindKnee implements the Kneedle algorithm to find the point of maximum curvature.
// It assumes the curve is concave (increasing but flattening out, like a typical saturation curve).
func FindKnee(points []Point) Point {
	if len(points) < 3 {
		if len(points) > 0 {
			return points[len(points)-1]
		}
		return Point{}
	}

	// 1. Sort by X
	sort.Slice(points, func(i, j int) bool {
		return points[i].X < points[j].X
	})

	// 2. Normalize to [0, 1]
	minX, maxX := points[0].X, points[len(points)-1].X
	minY, maxY := points[0].Y, points[0].Y
	for _, p := range points {
		if p.Y < minY { minY = p.Y }
		if p.Y > maxY { maxY = p.Y }
	}

	// Avoid divide by zero
	if maxX == minX || maxY == minY {
		return points[len(points)-1]
	}

	// 3. Calculate Difference Curve
	// D(x) = Y_normalized - X_normalized
	// (Assuming the line connecting start and end is y=x in normalized space)
	maxDist := -1.0
	var knee Point

	for _, p := range points {
		xNorm := (p.X - minX) / (maxX - minX)
		yNorm := (p.Y - minY) / (maxY - minY)
		
		// Distance from diagonal y=x
		// Ideally, we want the point furthest "above" the diagonal line connecting (0,0) and (1,1)
		// Since we normalized, the diagonal is y = x.
		// Distance = y - x
		dist := yNorm - xNorm

		if dist > maxDist {
			maxDist = dist
			knee = p
		}
	}

	return knee
}
