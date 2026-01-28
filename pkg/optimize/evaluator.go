package optimize

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
)

type Evaluator struct {
	eng          engine.Engine
	cfg          *config.Config
	initialScore float64
	History      []HistoryEntry
	Cache        map[string]engine.Result // Cache of aggregated results
}

type HistoryEntry struct {
	State  State         `json:"state"`
	Result engine.Result `json:"result"`
	Score  float64       `json:"score"`
	Reason string        `json:"reason,omitempty"`
}

// State represents a specific configuration of variables.
type State map[string]int

func NewEvaluator(eng engine.Engine, cfg *config.Config) *Evaluator {
	return &Evaluator{
		eng: eng,
		cfg: cfg,
		History: make([]HistoryEntry, 0),
		Cache:   make(map[string]engine.Result),
	}
}

func (e *Evaluator) NumNodes() int {
	return e.eng.NumNodes()
}

func (e *Evaluator) Evaluate(s State) (engine.Result, float64, string, error) {
	p := engine.Params{
		EngineType:  e.cfg.Settings.EngineType,
		Path:        e.cfg.Target,
		Direct:      e.cfg.Settings.Direct,
		ReadPct:     e.cfg.Settings.ReadPct,
		Rand:        e.cfg.Settings.Rand,
		MinRuntime:  e.cfg.Settings.MinRuntime,
		MaxRuntime:  e.cfg.Settings.MaxRuntime,
		ErrorTarget: e.cfg.Settings.ErrorTarget,
		BlockSize:   4096,
		Workers:     1,
		QueueDepth:  1,
	}

	key := e.hashState(s)

	if v, ok := s["block_size"]; ok { p.BlockSize = v }
	if v, ok := s["workers"]; ok { p.Workers = v }
	if v, ok := s["queue_depth"]; ok { p.QueueDepth = v }

	res, err := e.eng.Run(p)
	if err != nil {
		return engine.Result{}, 0, "", err
	}

	// Aggregate with cached result
	if cached, found := e.Cache[key]; found {
		// Merge logic
		totalDuration := cached.Duration + res.Duration
		totalIOs := cached.TotalIOs + res.TotalIOs
		
		// Weighted average for latencies
		// NOTE: Averaging percentiles is a statistical approximation and not mathematically correct.
		// A true merge would require the raw histograms or full latency lists from both results.
		// For the purpose of this heuristic optimizer, we accept this error to save memory/complexity,
		// but be aware that merged P99 values may be slightly off compared to a fresh run of the same duration.
		wOld := float64(cached.TotalIOs)
		wNew := float64(res.TotalIOs)
		totalW := wOld + wNew
		
		mergedP50 := time.Duration((float64(cached.P50Latency)*wOld + float64(res.P50Latency)*wNew) / totalW)
		mergedP99 := time.Duration((float64(cached.P99Latency)*wOld + float64(res.P99Latency)*wNew) / totalW)
		
		// Recalculate metrics
		mergedRes := engine.Result{
			TotalIOs:         totalIOs,
			Duration:         totalDuration,
			IOPS:             float64(totalIOs) / totalDuration.Seconds(),
			Throughput:       float64(totalIOs*int64(p.BlockSize)) / totalDuration.Seconds(),
			P50Latency:       mergedP50,
			P99Latency:       mergedP99,
			MetricConfidence: (cached.MetricConfidence + res.MetricConfidence) / 2, // Approximate
			TerminationReason: res.TerminationReason, // Keep latest reason
		}
		*res = mergedRes
	}
	e.Cache[key] = *res

	raw, reason := e.calculateScore(*res)
	
	if e.initialScore <= 1 && reason == "" {
		e.initialScore = math.Abs(raw)
		if e.initialScore < 1 { e.initialScore = 1 }
	}

	score := e.scaleScore(raw, reason)
	
	// Record history
	// Copy state to avoid reference issues
	stateCopy := make(State)
	for k, v := range s { stateCopy[k] = v }
	
	e.History = append(e.History, HistoryEntry{
		State:  stateCopy,
		Result: *res,
		Score:  score,
		Reason: reason,
	})

	return *res, score, reason, nil
}

func (e *Evaluator) hashState(s State) string {
	// deterministic key
	// Map iteration is random, so we must sort keys or hardcode known keys
	// We only have 3 known keys
	// NOTE: This explicitly ignores any other keys in the State map.
	// If new tunable parameters are added to State, they MUST be added here
	// or they will be ignored for caching purposes.
	return fmt.Sprintf("bs=%d:qd=%d:w=%d", s["block_size"], s["queue_depth"], s["workers"])
}

func (e *Evaluator) scaleScore(raw float64, reason string) float64 {
	if reason != "" {
		return -1000.0
	}
	return (raw / e.initialScore) * 1000.0
}

func (e *Evaluator) calculateScore(res engine.Result) (float64, string) {
	for _, obj := range e.cfg.Objectives {
		if obj.Type == "constraint" {
			limitVal := parseLimit(obj.Limit)
			var actualDur time.Duration
			passed := true
			switch obj.Metric {
			case "p99_latency":
				actualDur = res.P99Latency
				if res.P99Latency > limitVal { passed = false }
			case "p50_latency":
				actualDur = res.P50Latency
				if res.P50Latency > limitVal { passed = false }
			case "p95_latency":
				actualDur = res.P95Latency
				if res.P95Latency > limitVal { passed = false }
			}
			if !passed {
				return 0, fmt.Sprintf("Constraint Failed: %s (%v > %s)", obj.Metric, actualDur, obj.Limit)
			}
		}
	}

	score := 0.0
	for _, obj := range e.cfg.Objectives {
		val := 0.0
		switch obj.Metric {
		case "iops": val = res.IOPS
		case "throughput": val = res.Throughput / 1024 / 1024
		case "p99_latency": val = -float64(res.P99Latency.Seconds() * 1000)
		case "p50_latency": val = -float64(res.P50Latency.Seconds() * 1000)
		}
		if obj.Type == "maximize" { score += val } else if obj.Type == "minimize" { score -= val }
	}
	return score, ""
}

func (e *Evaluator) FormatMetrics(res engine.Result) string {
	var parts []string
	for _, obj := range e.cfg.Objectives {
		switch obj.Metric {
		case "iops": parts = append(parts, fmt.Sprintf("IOPS: %.0f", res.IOPS))
		case "throughput": parts = append(parts, fmt.Sprintf("BW: %.2f MB/s", res.Throughput/1024/1024))
		case "p99_latency": parts = append(parts, fmt.Sprintf("P99: %v", res.P99Latency))
		case "p50_latency": parts = append(parts, fmt.Sprintf("P50: %v", res.P50Latency))
		}
	}
	if len(parts) == 0 { return fmt.Sprintf("IOPS: %.0f", res.IOPS) }
	
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
	f, err := strconv.ParseFloat(s, 64)
	if err == nil { return time.Duration(f) }
	return 0
}
