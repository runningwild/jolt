package engine

import (
	"time"
)

// Result contains the metrics for a specific test run.
type Result struct {
	IOPS             float64
	Throughput       float64 // Bytes per second
	P99Latency       time.Duration
	P50Latency       time.Duration
	TotalIOs         int64
	Duration         time.Duration
	MetricConfidence float64 // The achieved StdErr/Mean (lower is better)
	TerminationReason string // Why the test finished (Timeout, Converged, etc.)
}

// Params defines the parameters for an I/O workload.
type Params struct {
	Path       string        // Path to the device or file
	BlockSize  int           // Size of each I/O in bytes
	Direct     bool          // Use O_DIRECT
	Write      bool          // True for write, false for read
	Rand       bool          // True for random, false for sequential
	Workers    int           // Number of concurrent workers (goroutines)
	QueueDepth int           // Global target queue depth (token bucket size)
	MinRuntime time.Duration // Minimum time to run the test
	MaxRuntime time.Duration // Maximum time to run the test
	ErrorTarget float64      // Target standard error / mean (e.g. 0.01 for 1%)
	
	// Optional callback for real-time progress updates
	Progress func(Result)
}

// Progress reports intermediate status of a running test point.
type Progress struct {
	Elapsed time.Duration
	IOPS    float64
	RelErr  float64
}
