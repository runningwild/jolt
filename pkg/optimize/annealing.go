package optimize

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"time"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
)

type AnnealingOptimizer struct {
	eng *engine.Engine
	cfg *config.Config
	rnd *rand.Rand
}

func NewAnnealing(eng *engine.Engine, cfg *config.Config) *AnnealingOptimizer {
	return &AnnealingOptimizer{
		eng: eng,
		cfg: cfg,
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// State represents a specific configuration of variables.
type State map[string]int

func (ao *AnnealingOptimizer) Optimize() (State, engine.Result, error) {
	// 1. Initial State
	current := ao.randomState()
	currentRes, err := ao.evaluate(current)
	if err != nil {
		return nil, engine.Result{}, err
	}
	currentScore := ao.calculateScore(currentRes)

	best := current
	bestRes := currentRes
	bestScore := currentScore

	// Annealing Parameters
	temp := 1.0
	coolingRate := 0.95
	minTemp := 0.01
	
	fmt.Printf("Initial State: %v, Score: %.2f (IOPS: %.0f)\n", current, currentScore, currentRes.IOPS)

	step := 0
	for temp > minTemp {
		step++
		// 2. Neighbor Selection
		neighbor := ao.neighbor(current)
		
		// 3. Evaluation
		res, err := ao.evaluate(neighbor)
		if err != nil {
			fmt.Printf("Evaluation failed: %v\n", err)
			continue
		}
		score := ao.calculateScore(res)

		// 4. Acceptance Probability
		delta := score - currentScore
		acceptance := 0.0
		if delta > 0 {
			acceptance = 1.0
		} else {
			acceptance = math.Exp(delta / temp) // delta is negative
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
			}
		}

		fmt.Printf("[Step %3d] T=%.3f %v => Score: %.2f (IOPS: %.0f) [%s]\n", 
			step, temp, neighbor, score, res.IOPS, status)

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
			// Range [min, max]
			span := v.Range[1] - v.Range[0]
			val := v.Range[0] + ao.rnd.Intn(span+1)
			s[v.Name] = val
		}
	}
	return s
}

func (ao *AnnealingOptimizer) neighbor(s State) State {
	// Copy state
	next := make(State)
	for k, v := range s {
		next[k] = v
	}

	// Pick one variable to change
	idx := ao.rnd.Intn(len(ao.cfg.Search))
	v := ao.cfg.Search[idx]

	if len(v.Values) > 0 {
		// Pick random value from list
		next[v.Name] = v.Values[ao.rnd.Intn(len(v.Values))]
	} else {
		// Perturb within range (small step or big jump?)
		// For SA, usually small step.
		step := v.Step
		if step == 0 { step = 1 }
		
		// 50% chance to go up or down
		change := step
		if ao.rnd.Intn(2) == 0 {
			change = -step
		}
		
		newVal := next[v.Name] + change
		if newVal < v.Range[0] { newVal = v.Range[0] }
		if newVal > v.Range[1] { newVal = v.Range[1] }
		next[v.Name] = newVal
	}
	return next
}

func (ao *AnnealingOptimizer) evaluate(s State) (engine.Result, error) {
	// Construct Params from Settings + State
	p := engine.Params{
		Path:        ao.cfg.Target,
		Direct:      ao.cfg.Settings.Direct,
		Write:       ao.cfg.Settings.Write,
		Rand:        ao.cfg.Settings.Rand,
		MinRuntime:  ao.cfg.Settings.MinRuntime,
		MaxRuntime:  ao.cfg.Settings.MaxRuntime,
		ErrorTarget: ao.cfg.Settings.ErrorTarget,
		
		// Defaults (will be overwritten if in State)
		BlockSize:  4096,
		Workers:    1,
		QueueDepth: 1,
	}

	// Apply State
	if v, ok := s["block_size"]; ok { p.BlockSize = v }
	if v, ok := s["workers"]; ok { p.Workers = v }
	if v, ok := s["queue_depth"]; ok { p.QueueDepth = v }

	// Safety: QD must be >= Workers for the Token Bucket to work well?
	// Actually, our engine handles QD < Workers (workers just fight for fewer tokens).
	// But logically, if we search separately, we treat them as independent.
	
	// Real-time progress (optional, maybe suppress for batch mode?)
	// p.Progress = ... 

	res, err := ao.eng.Run(p)
	if err != nil {
		return engine.Result{}, err
	}
	return *res, nil
}

func (ao *AnnealingOptimizer) calculateScore(res engine.Result) float64 {
	score := 0.0

	// 1. Check Constraints (Hard penalties)
	for _, obj := range ao.cfg.Objectives {
		if obj.Type == "constraint" {
			passed := true
			limitVal := parseLimit(obj.Limit) // e.g., 10ms -> time.Duration
			
			switch obj.Metric {
			case "p99_latency":
				if res.P99Latency > limitVal { passed = false }
			case "p50_latency":
				if res.P50Latency > limitVal { passed = false }
			}
			
			if !passed {
				return -1e9 // Huge penalty
			}
		}
	}

	// 2. Add Objective Score
	// Assume single objective for now or sum normalized values
	for _, obj := range ao.cfg.Objectives {
		val := 0.0
		switch obj.Metric {
		case "iops": val = res.IOPS
		case "throughput": val = res.Throughput
		case "p99_latency": val = -float64(res.P99Latency) // Negate because lower is better
		}

		if obj.Type == "maximize" {
			score += val
		} else if obj.Type == "minimize" {
			score -= val
		}
	}
	
	return score
}

func parseLimit(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err == nil { return d }
	// Try parsing as number?
	f, err := strconv.ParseFloat(s, 64)
	if err == nil { return time.Duration(f) } // Treat as ns?
	return 0
}
