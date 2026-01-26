package optimize

import (
	"fmt"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
)

type CoordinateOptimizer struct {
	eval *Evaluator
	cfg  *config.Config
}

func NewCoordinate(eng engine.Engine, cfg *config.Config) *CoordinateOptimizer {
	return &CoordinateOptimizer{
		eval: NewEvaluator(eng, cfg),
		cfg:  cfg,
	}
}

func (co *CoordinateOptimizer) Optimize() (State, engine.Result, error) {
	// Start with middle-of-the-road values
	current := make(State)
	for _, v := range co.cfg.Search {
		if len(v.Values) > 0 {
			current[v.Name] = v.Values[len(v.Values)/2]
		} else {
			current[v.Name] = (v.Range[0] + v.Range[1]) / 2
		}
	}

	bestRes, bestScore, reason, err := co.eval.Evaluate(current)
	if err != nil {
		return nil, engine.Result{}, err
	}
	fmt.Printf("Initial State: %v, Score: %.2f (%s) %s\n", current, bestScore, co.eval.FormatMetrics(bestRes), reason)

	for {
		improved := false
		for _, v := range co.cfg.Search {
			fmt.Printf("Optimizing variable: %s\n", v.Name)
			
			var localBestVal int
			var localBestRes engine.Result
			var localBestScore float64

			if len(v.Values) > 0 {
				localBestVal, localBestRes, localBestScore, err = co.optimizeList(current, v)
			} else {
				localBestVal, localBestRes, localBestScore, err = co.optimizeRange(current, v)
			}

			if err != nil {
				return nil, engine.Result{}, err
			}

			if localBestScore > bestScore {
				fmt.Printf("  -> Improved %s: %d => %d (Score: %.2f)\n", v.Name, current[v.Name], localBestVal, localBestScore)
				current[v.Name] = localBestVal
				bestScore = localBestScore
				bestRes = localBestRes
				improved = true
			} else {
				fmt.Printf("  -> No improvement for %s (Best remained: %d)\n", v.Name, current[v.Name])
			}
		}

		if !improved {
			break
		}
	}

	return current, bestRes, nil
}

func (co *CoordinateOptimizer) optimizeList(s State, v config.Variable) (int, engine.Result, float64, error) {
	bestVal := s[v.Name]
	var bestRes engine.Result
	var bestScore float64

	tempState := make(State)
	for k, val := range s { tempState[k] = val }

	for _, val := range v.Values {
		tempState[v.Name] = val
		res, score, reason, err := co.eval.Evaluate(tempState)
		if err != nil { return 0, engine.Result{}, 0, err } 
		
		fmt.Printf("  Testing %s=%d... Score: %.2f (%s) %s\n", v.Name, val, score, co.eval.FormatMetrics(res), reason)
		if score > bestScore || bestRes.IOPS == 0 {
			bestScore = score
			bestRes = res
			bestVal = val
		}
	}
	return bestVal, bestRes, bestScore, nil
}

func (co *CoordinateOptimizer) optimizeRange(s State, v config.Variable) (int, engine.Result, float64, error) {
	bestVal := s[v.Name]
	tempState := make(State)
	for k, val := range s { tempState[k] = val }

	res, score, reason, err := co.eval.Evaluate(tempState)
	if err != nil { return 0, engine.Result{}, 0, err }
	bestRes, bestScore := res, score
	_ = reason // silence unused

	step := v.Step
	if step <= 0 { step = (v.Range[1] - v.Range[0]) / 10 }
	if step <= 0 { step = 1 }

	for step >= 1 {
		improved := false
		// Try UP
		if bestVal + step <= v.Range[1] {
			tempState[v.Name] = bestVal + step
			r, s, reason, err := co.eval.Evaluate(tempState)
			if err != nil { return 0, engine.Result{}, 0, err }
			fmt.Printf("  Testing %s=%d... Score: %.2f (%s) %s\n", v.Name, tempState[v.Name], s, co.eval.FormatMetrics(r), reason)
			if s > bestScore {
				bestScore = s
				bestRes = r
				bestVal = tempState[v.Name]
				improved = true
			}
		}
		// Try DOWN
		if !improved && bestVal - step >= v.Range[0] {
			tempState[v.Name] = bestVal - step
			r, s, reason, err := co.eval.Evaluate(tempState)
			if err != nil { return 0, engine.Result{}, 0, err }
			fmt.Printf("  Testing %s=%d... Score: %.2f (%s) %s\n", v.Name, tempState[v.Name], s, co.eval.FormatMetrics(r), reason)
			if s > bestScore {
				bestScore = s
				bestRes = r
				bestVal = tempState[v.Name]
				improved = true
			}
		}

		if improved {
			// If we improved, keep the same step size and try again from new position
			continue
		} else {
			// Refine step size
			step /= 2
		}
	}

	return bestVal, bestRes, bestScore, nil
}
