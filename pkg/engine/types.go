package engine

import (
	"time"
)

// Result contains the metrics for a specific test run.
type Result struct {
	IOPS              float64
	Throughput        float64 // Bytes per second
	MeanLatency       time.Duration
	P50Latency        time.Duration
	P95Latency        time.Duration
	P99Latency        time.Duration
	P999Latency       time.Duration
	TotalIOs          int64
	Duration          time.Duration
	MetricConfidence  float64 // The achieved StdErr/Mean (lower is better)
	TerminationReason string  // Why the test finished (Timeout, Converged, etc.)
}

// Engine defines the interface for different I/O execution strategies.
type Engine interface {
	Run(params Params) (*Result, error)
}

// Params defines the parameters for an I/O workload.
type Params struct {
	EngineType string        // "sync" or "uring"
	Path       string        // Path to the device or file
	BlockSize  int           // Size of each I/O in bytes
	Direct     bool          // Use O_DIRECT
	ReadPct    int           // Percentage of operations that are reads (0-100)
	Rand       bool          // True for random, false for sequential
	Distribute bool          // If true, workers/QD are split among nodes (Cluster only)
	Workers    int           // Number of concurrent workers (goroutines or async loops)
	QueueDepth int           // Global target queue depth (token bucket size)
	MinRuntime time.Duration // Minimum time to run the test
	MaxRuntime time.Duration // Maximum time to run the test
	ErrorTarget float64      `json:"error_target"`      // Target standard error / mean (e.g. 0.01 for 1%)
	
	// Optional callback for real-time progress updates
	Progress func(Result) `json:"-"`
}

// Progress reports intermediate status of a running test point.
type Progress struct {
	Elapsed time.Duration
	IOPS    float64
	RelErr  float64
}
