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
	eng          engine.Engine
	cfg          *config.Config
	rnd          *rand.Rand
	initialScore float64
}

func NewAnnealing(eng engine.Engine, cfg *config.Config) *AnnealingOptimizer {
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
	
	rawScore, reason := ao.calculateScore(currentRes)
	
	// Only establish baseline if the first run was successful
	if reason == "" {
		ao.initialScore = math.Abs(rawScore)
	}
	if ao.initialScore < 1 { ao.initialScore = 1 }
	
	currentScore := ao.scaleScore(rawScore, reason)

	best := current
	bestRes := currentRes
	bestScore := currentScore

	// Annealing Parameters from config
	temp := ao.cfg.Settings.InitialTemp
	coolingRate := ao.cfg.Settings.CoolingRate
	minTemp := ao.cfg.Settings.MinTemp
	stepsPerTemp := ao.cfg.Settings.StepsPerTemp
	restartInterval := ao.cfg.Settings.RestartInterval
	
	fmt.Printf("Initial State: %v, Score: %.2f (%s), Temp: %.1f %s\n", 
		current, currentScore, ao.formatMetrics(currentRes), temp, reason)

	step := 0
	stepsSinceImprovement := 0
	for temp > minTemp {
		for i := 0; i < stepsPerTemp; i++ {
			step++
			stepsSinceImprovement++

			// 2. Neighbor Selection
			neighbor := ao.neighbor(current, temp/ao.cfg.Settings.InitialTemp)
			
			// 3. Evaluation
			res, err := ao.evaluate(neighbor)
			if err != nil {
				return nil, engine.Result{}, fmt.Errorf("evaluation failed at state %v: %w", neighbor, err)
			}
			raw, reason := ao.calculateScore(res)
			
			// If we didn't have a baseline yet and this run is successful, establish it now
			if ao.initialScore <= 1 && reason == "" {
				ao.initialScore = math.Abs(raw)
				if ao.initialScore < 1 { ao.initialScore = 1 }
				currentScore = ao.scaleScore(rawScore, "")
			}

			score := ao.scaleScore(raw, reason)

			// 4. Acceptance Probability
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
				step, temp, neighbor, score, ao.formatMetrics(res), status)

			// Elitist Restart: if we haven't improved in a while, jump back to best
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
			// Range [min, max]
			span := v.Range[1] - v.Range[0]
			val := v.Range[0] + ao.rnd.Intn(span+1)
			s[v.Name] = val
		}
	}
	return s
}

func (ao *AnnealingOptimizer) neighbor(s State, tempRatio float64) State {
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
		// Perturb within range
		// Jump size scales with temperature ratio (1.0 at start, 0.0 at end)
		span := v.Range[1] - v.Range[0]
		
		maxJump := float64(span) * tempRatio
		if maxJump < 1 {
			maxJump = 1
		}
		
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

func (ao *AnnealingOptimizer) evaluate(s State) (engine.Result, error) {
	// Construct Params from Settings + State
	p := engine.Params{
		EngineType:  ao.cfg.Settings.EngineType,
		Path:        ao.cfg.Target,
		Direct:      ao.cfg.Settings.Direct,
		ReadPct:     ao.cfg.Settings.ReadPct,
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

func (ao *AnnealingOptimizer) scaleScore(raw float64, reason string) float64 {
	if reason != "" {
		return -1000.0 // Consistent penalty for failures
	}
	return (raw / ao.initialScore) * 1000.0
}

func (ao *AnnealingOptimizer) calculateScore(res engine.Result) (float64, string) {
	score := 0.0

	// 1. Check Constraints (Hard penalties)
	for _, obj := range ao.cfg.Objectives {
		if obj.Type == "constraint" {
			passed := true
			limitVal := parseLimit(obj.Limit)
			
			var actualDur time.Duration

			switch obj.Metric {
			case "p99_latency":
				actualDur = res.P99Latency
				if res.P99Latency > limitVal { passed = false }
			case "p50_latency":
				actualDur = res.P50Latency
				if res.P50Latency > limitVal { passed = false }
			case "p95_latency":
				// We don't have P95 in Result yet, using P99 as fallback
				actualDur = res.P99Latency 
				if res.P99Latency > limitVal { passed = false }
			}
			
			if !passed {
				return 0, fmt.Sprintf("Constraint Failed: %s (%v > %s)", obj.Metric, actualDur, obj.Limit)
			}
		}
	}

	// 2. Add Objective Score (Scaled to similar magnitudes)
	for _, obj := range ao.cfg.Objectives {
		val := 0.0
		switch obj.Metric {
		case "iops": 
			val = res.IOPS
		case "throughput": 
			val = res.Throughput / 1024 / 1024 // Use MB/s
		case "p99_latency": 
			val = -float64(res.P99Latency.Seconds() * 1000) // Use -ms (lower is better)
		case "p50_latency":
			val = -float64(res.P50Latency.Seconds() * 1000)
		}

		if obj.Type == "maximize" {
			score += val
		} else if obj.Type == "minimize" {
			score -= val
		}
	}
	
	return score, ""
}

func (ao *AnnealingOptimizer) formatMetrics(res engine.Result) string {
	var parts []string
	for _, obj := range ao.cfg.Objectives {
		switch obj.Metric {
		case "iops":
			parts = append(parts, fmt.Sprintf("IOPS: %.0f", res.IOPS))
		case "throughput":
			parts = append(parts, fmt.Sprintf("BW: %.2f MB/s", res.Throughput/1024/1024))
		case "p99_latency":
			parts = append(parts, fmt.Sprintf("P99: %v", res.P99Latency))
		case "p50_latency":
			parts = append(parts, fmt.Sprintf("P50: %v", res.P50Latency))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("IOPS: %.0f", res.IOPS)
	}

	seen := make(map[string]bool)
	var unique []string
	for _, p := range parts {
		if !seen[p] {
			unique = append(unique, p)
			seen[p] = true
		}
	}

	result := ""
	for i, p := range unique {
		if i > 0 { result += ", " }
		result += p
	}
	return result
}

func parseLimit(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err == nil { return d }
	// Try parsing as number?
	f, err := strconv.ParseFloat(s, 64)
	if err == nil { return time.Duration(f) } // Treat as ns?
	return 0
}
