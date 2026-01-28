package optimize

import (
	"testing"
	"time"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
)

type mockEngine struct {
	runFunc func(params engine.Params) (*engine.Result, error)
}

func (m *mockEngine) Run(params engine.Params) (*engine.Result, error) {
	return m.runFunc(params)
}

func TestEvaluator_Scoring(t *testing.T) {
	cfg := &config.Config{
		Objectives: []config.Objective{
			{Type: "maximize", Metric: "iops"},
			{Type: "constraint", Metric: "p99_latency", Limit: "10ms"},
		},
	}

	mock := &mockEngine{
		runFunc: func(params engine.Params) (*engine.Result, error) {
			// Return a result based on params or static
			return &engine.Result{
				IOPS:       1000,
				P99Latency: 5 * time.Millisecond,
				TotalIOs:   1000,
				Duration:   1 * time.Second,
			}, nil
		},
	}

	eval := NewEvaluator(mock, cfg)
	state := State{"workers": 1}

	_, score, reason, err := eval.Evaluate(state)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if reason != "" {
		t.Errorf("Expected valid run, got reason: %s", reason)
	}
	if score <= 0 {
		t.Errorf("Expected positive score for maximize IOPS, got %f", score)
	}
}

func TestEvaluator_ConstraintFailure(t *testing.T) {
	cfg := &config.Config{
		Objectives: []config.Objective{
			{Type: "maximize", Metric: "iops"},
			{Type: "constraint", Metric: "p99_latency", Limit: "10ms"},
		},
	}

	mock := &mockEngine{
		runFunc: func(params engine.Params) (*engine.Result, error) {
			return &engine.Result{
				IOPS:       2000,
				P99Latency: 20 * time.Millisecond, // Violates 10ms limit
				TotalIOs:   1000,
				Duration:   1 * time.Second,
			}, nil
		},
	}

	eval := NewEvaluator(mock, cfg)
	state := State{"workers": 2}

	_, score, reason, err := eval.Evaluate(state)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if reason == "" {
		t.Error("Expected failure reason for constraint violation, got empty")
	}
	if score != -1000.0 {
		t.Errorf("Expected failure score -1000, got %f", score)
	}
}

func TestEvaluator_Caching(t *testing.T) {
	cfg := &config.Config{
		Objectives: []config.Objective{{Type: "maximize", Metric: "iops"}},
	}

	callCount := 0
	mock := &mockEngine{
		runFunc: func(params engine.Params) (*engine.Result, error) {
			callCount++
			return &engine.Result{
				IOPS:     1000,
				TotalIOs: 100,
				Duration: 1 * time.Second,
			}, nil
		},
	}

	eval := NewEvaluator(mock, cfg)
	state := State{"workers": 1}

	// First call
	eval.Evaluate(state)
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// Second call with same state
	// The Evaluator implementation ALWAYS calls engine.Run(), 
	// but it merges the result with the cache. 
	// Wait, checking the implementation...
	// "res, err := e.eng.Run(p)" is called unconditionally.
	// "if cached, found := e.Cache[key]; found { ... merge ... }"
	// So callCount WILL increase.
	// But the result in Cache should have doubled TotalIOs.
	
	eval.Evaluate(state)
	if callCount != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount)
	}

	key := eval.hashState(state)
	cached := eval.Cache[key]
	if cached.TotalIOs != 200 { // 100 + 100
		t.Errorf("Expected aggregated TotalIOs=200, got %d", cached.TotalIOs)
	}
}
