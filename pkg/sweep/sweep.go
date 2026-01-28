package sweep

import (
	"fmt"

	"github.com/runningwild/jolt/pkg/analyze"
	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
	"github.com/runningwild/jolt/pkg/optimize"
)

type Sweeper struct {
	eval *optimize.Evaluator
	cfg  *config.Config
}

func New(eng engine.Engine, cfg *config.Config) *Sweeper {
	return &Sweeper{
		eval: optimize.NewEvaluator(eng, cfg),
		cfg:  cfg,
	}
}

func (s *Sweeper) Run() ([]optimize.HistoryEntry, analyze.Point, error) {
	// Identify the sweep variable
	// We expect exactly one variable to have a Range or Values with > 1 item.
	// If multiple, we might default to the first one or error.
	var sweepVar *config.Variable
	
	// Prepare base state
	state := make(optimize.State)
	
	for i := range s.cfg.Search {
		v := &s.cfg.Search[i]
		// Default to first value
		val := 0
		if len(v.Values) > 0 {
			val = v.Values[0]
		} else {
			val = v.Range[0]
		}
		state[v.Name] = val
		
		// Check if this is the one to sweep
		isSweep := false
		if len(v.Values) > 1 {
			isSweep = true
		} else if len(v.Values) == 0 && v.Range[1] > v.Range[0] {
			isSweep = true
		}
		
		if isSweep {
			if sweepVar == nil {
				sweepVar = v
			} else {
				// We already found a sweep var. Having two is a "Grid Search", 
				// but for "Knee Finding" we usually want 2D plot.
				// For now, let's warn or just stick to the first one found.
				fmt.Printf("Warning: Multiple sweep variables detected. Sweeping '%s', fixing '%s' to %d.\n", 
					sweepVar.Name, v.Name, val)
			}
		}
	}

	if sweepVar == nil {
		return nil, analyze.Point{}, fmt.Errorf("no variable defined with a range or multiple values to sweep")
	}

	fmt.Printf("Sweeping variable '%s' to find the knee...\n", sweepVar.Name)

	// Generate steps
	var steps []int
	if len(sweepVar.Values) > 0 {
		steps = sweepVar.Values
	} else {
		step := sweepVar.Step
		if step <= 0 { step = 1 }
		for i := sweepVar.Range[0]; i <= sweepVar.Range[1]; i += step {
			steps = append(steps, i)
		}
	}

	var results []optimize.HistoryEntry
	var points []analyze.Point

	for i, val := range steps {
		// Update State
		state[sweepVar.Name] = val

		// Run
		res, score, _, err := s.eval.Evaluate(state)
		if err != nil {
			return nil, analyze.Point{}, err
		}

		fmt.Printf("[%d/%d] %s=%d -> IOPS: %.0f, Latency: %v\n", 
			i+1, len(steps), sweepVar.Name, val, res.IOPS, res.P99Latency)

		results = append(results, optimize.HistoryEntry{
			State: copyState(state),
			Result: res,
			Score: score,
		})

		// For Knee calculation, we usually care about Throughput/IOPS vs Cost (Value)
		// Assuming "maximize iops" is the goal.
		// X = Parameter Value (e.g. Workers)
		// Y = Metric (IOPS)
		points = append(points, analyze.Point{
			X: float64(val),
			Y: res.IOPS, // Default to IOPS for knee finding. Make configurable?
			OriginalX: val,
		})
	}

	knee := analyze.FindKnee(points)
	return results, knee, nil
}

func copyState(s optimize.State) optimize.State {
	c := make(optimize.State)
	for k, v := range s { c[k] = v }
	return c
}
