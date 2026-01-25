package optimize

import (
	"fmt"
	"github.com/runningwild/jolt/pkg/analyze"
	"github.com/runningwild/jolt/pkg/engine"
)

// SearchParams defines the bounds for finding the knee.
type SearchParams struct {
	BaseParams engine.Params
	VarName    string  // "workers" or "queuedepth"
	Min        float64
	Max        float64
	Step       float64
}

type Optimizer struct {
	engine   *engine.Engine
	detector analyze.KneeDetector
}

func New(e *engine.Engine, d analyze.KneeDetector) *Optimizer {
	return &Optimizer{
		engine:   e,
		detector: d,
	}
}

func (o *Optimizer) FindKnee(s SearchParams) (analyze.Point, error) {
	var points []analyze.Point

	for val := s.Min; val <= s.Max; val += s.Step {
		params := s.BaseParams
		switch s.VarName {
		case "workers":
			params.Workers = int(val)
		case "queuedepth":
			params.QueueDepth = int(val)
		default:
			return analyze.Point{}, fmt.Errorf("unknown variable: %s", s.VarName)
		}

		fmt.Printf("Testing %s = %v...\n", s.VarName, val)
		res, err := o.engine.Run(params)
		if err != nil {
			return analyze.Point{}, err
		}

		p := analyze.Point{X: val, Y: res.IOPS}
		points = append(points, p)
		fmt.Printf("  -> IOPS: %.2f\n", p.Y)

		if knee, found := o.detector.FindKnee(points); found {
			return knee, nil
		}
	}

	return analyze.Point{}, fmt.Errorf("knee not found in range")
}
