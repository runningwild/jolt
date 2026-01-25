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
	engine   *engine.Engine
	detector *analyze.Detector
}

func New(e *engine.Engine, d *analyze.Detector) *Optimizer {
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

		fmt.Printf("Testing %s = %v... ", s.VarName, val)
		
		// Progress callback
		onProgress := func(p engine.Progress) {
			fmt.Printf("\rTesting %s = %v... [%.1fs, IOPS: %.2f, err: %.2f%%]   ", 
				s.VarName, val, p.Elapsed.Seconds(), p.IOPS, p.RelErr*100)
		}

		res, err := o.engine.Run(params, onProgress)
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			return analyze.Analysis{}, 0, err
		}

		// Clear progress line and print final
		fmt.Print("\r\033[K") // Clear line
		fmt.Printf("Testing %s = %v... ", s.VarName, val)
		
		p := analyze.Point{X: val, Y: res.IOPS}
		points = append(points, p)
		fmt.Printf("IOPS: %.2f (err: %.2f%%, dur: %v, %s)\n", p.Y, res.MetricConfidence*100, res.Duration.Round(time.Millisecond), res.TerminationReason)

		analysis := o.detector.Analyze(points)
		// If we've found the saturation point, we can stop early
		if analysis.SaturationPoint.X != 0 {
			return analysis, analyze.CalculateConfidence(points), nil
		}
	}

	return o.detector.Analyze(points), analyze.CalculateConfidence(points), nil
}
