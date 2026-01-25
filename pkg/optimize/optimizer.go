package optimize

import (
	"fmt"
	"time"
	"github.com/runningwild/jolt/pkg/analyze"
	"github.com/runningwild/jolt/pkg/engine"
)

// SearchParams defines the bounds for finding the knee.
type SearchParams struct {
	BaseParams engine.Params
	VarName    string // "workers" or "queuedepth"
	Min        float64
	Max        float64
	Step       float64
}

type Optimizer struct {
	engine   engine.Engine
	detector *analyze.Detector
}

func New(e engine.Engine, d *analyze.Detector) *Optimizer {
	return &Optimizer{
		engine:   e,
		detector: d,
	}
}

func (o *Optimizer) FindKnee(s SearchParams) (analyze.Analysis, float64, error) {
	var points []analyze.Point

	for val := s.Min; val <= s.Max; val += s.Step {
		params := s.BaseParams
		switch s.VarName {
		case "workers":
			params.Workers = int(val)
		case "queuedepth":
			params.QueueDepth = int(val)
		default:
			return analyze.Analysis{}, 0, fmt.Errorf("unknown variable: %s", s.VarName)
		}

		// Setup progress reporting
		fmt.Printf("Testing %s = %v... ", s.VarName, val) // This line is not part of the original code, it's a new addition.
		params.Progress = func(r engine.Result) {
			// Update line in place
			fmt.Printf("\rTesting %s = %v... IOPS: %.2f (conf: %.2f%%) [%v]   ", 
				s.VarName, val, r.IOPS, r.MetricConfidence*100, r.Duration.Round(100*time.Millisecond))
		}

		res, err := o.engine.Run(params)
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			return analyze.Analysis{}, 0, err
		}

		// Final print to lock in the line
		fmt.Printf("\rTesting %s = %v... IOPS: %.2f (conf: %.2f%%) [%v]   \n", 
			s.VarName, val, res.IOPS, res.MetricConfidence*100, res.Duration.Round(100*time.Millisecond))

		p := analyze.Point{X: val, Y: res.IOPS}
		points = append(points, p)
		fmt.Printf("IOPS: %.2f (err: %.2f%%, p50: %v, p95: %v, p99: %v)\n", 
			p.Y, res.MetricConfidence*100, res.P50Latency.Round(time.Microsecond), 
			res.P95Latency.Round(time.Microsecond), res.P99Latency.Round(time.Microsecond))

		analysis := o.detector.Analyze(points)
		// If we've found the saturation point, we can stop early
		if analysis.SaturationPoint.X != 0 {
			return analysis, analyze.CalculateConfidence(points), nil
		}
	}

	return o.detector.Analyze(points), analyze.CalculateConfidence(points), nil
}