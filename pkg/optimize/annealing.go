package optimize

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
)

type AnnealingOptimizer struct {
	eval *Evaluator
	cfg  *config.Config
	rnd  *rand.Rand
}

func NewAnnealing(eng engine.Engine, cfg *config.Config) *AnnealingOptimizer {
	return &AnnealingOptimizer{
		eval: NewEvaluator(eng, cfg),
		cfg:  cfg,
		rnd:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (ao *AnnealingOptimizer) Optimize() (State, engine.Result, error) {
	current := ao.randomState()
	currentRes, currentScore, reason, err := ao.eval.Evaluate(current)
	if err != nil {
		return nil, engine.Result{}, err
	}

	best := current
	bestRes := currentRes
	bestScore := currentScore

	temp := ao.cfg.Settings.InitialTemp

coolingRate := ao.cfg.Settings.CoolingRate
	minTemp := ao.cfg.Settings.MinTemp
	stepsPerTemp := ao.cfg.Settings.StepsPerTemp
	restartInterval := ao.cfg.Settings.RestartInterval
	
	fmt.Printf("Initial State: %v, Score: %.2f (%s), Temp: %.1f %s\n", 
		current, currentScore, ao.eval.FormatMetrics(currentRes), temp, reason)

	step := 0
	stepsSinceImprovement := 0
	for temp > minTemp {
		for i := 0; i < stepsPerTemp; i++ {
			step++
			stepsSinceImprovement++

			neighbor := ao.neighbor(current, temp/ao.cfg.Settings.InitialTemp)
			res, score, reason, err := ao.eval.Evaluate(neighbor)
			if err != nil {
				return nil, engine.Result{}, err
			}

			delta := score - currentScore
			acceptance := 0.0
			if delta > 0 {
				acceptance = 1.0
			} else {
				exponent := delta / temp
				if exponent < -700 {
					acceptance = 0.0
				} else {
					acceptance = math.Exp(exponent)
				}
			}

			status := "Rejected"
			if acceptance > ao.rnd.Float64() {
				current = neighbor
				currentScore = score
				currentRes = res
				status = "Accepted"
				
				if score > bestScore {
					best = neighbor
					bestScore = score
					bestRes = res
					status = "NEW BEST"
					stepsSinceImprovement = 0
				}
			}

			if reason != "" {
				status = reason
			}

			fmt.Printf("[Step %3d] T=%7.2f %v => Score: %8.2f (%s) [%s]\n", 
				step, temp, neighbor, score, ao.eval.FormatMetrics(res), status)

			if restartInterval > 0 && stepsSinceImprovement >= restartInterval {
				current = best
				currentScore = bestScore
				currentRes = bestRes
				stepsSinceImprovement = 0
				fmt.Printf("--- Restarting from Best State: %v ---\n", best)
			}
		}
		temp *= coolingRate
	}

	return best, bestRes, nil
}

func (ao *AnnealingOptimizer) randomState() State {
	s := make(State)
	for _, v := range ao.cfg.Search {
		if len(v.Values) > 0 {
			s[v.Name] = v.Values[ao.rnd.Intn(len(v.Values))]
		} else {
			span := v.Range[1] - v.Range[0]
			val := v.Range[0] + ao.rnd.Intn(span+1)
			s[v.Name] = val
		}
	}
	return s
}

func (ao *AnnealingOptimizer) neighbor(s State, tempRatio float64) State {
	next := make(State)
	for k, v := range s { next[k] = v }
	idx := ao.rnd.Intn(len(ao.cfg.Search))
	v := ao.cfg.Search[idx]

	if len(v.Values) > 0 {
		next[v.Name] = v.Values[ao.rnd.Intn(len(v.Values))]
	} else {
		span := v.Range[1] - v.Range[0]
		maxJump := float64(span) * tempRatio
		if maxJump < 1 { maxJump = 1 }
		jump := int(ao.rnd.NormFloat64() * maxJump)
		if jump == 0 {
			if ao.rnd.Intn(2) == 0 { jump = 1 } else { jump = -1 }
		}
		newVal := next[v.Name] + jump
		if newVal < v.Range[0] { newVal = v.Range[0] }
		if newVal > v.Range[1] { newVal = v.Range[1] }
		next[v.Name] = newVal
	}
	return next
}