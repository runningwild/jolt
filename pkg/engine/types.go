package engine

import (
	"time"
)

// Result contains the metrics for a specific test run.
type Result struct {
	IOPS       float64
	Throughput float64 // Bytes per second
	P99Latency time.Duration
	P50Latency time.Duration
	TotalIOs   int64
	Duration   time.Duration
}

// Params defines the parameters for an I/O workload.
type Params struct {
	Path       string        // Path to the device or file
	BlockSize  int           // Size of each I/O in bytes
	Direct     bool          // Use O_DIRECT
	Write      bool          // True for write, false for read
	Rand       bool          // True for random, false for sequential
	Workers    int           // Number of concurrent workers
	QueueDepth int           // Target queue depth per worker (if supported)
	Runtime    time.Duration // How long to run the test
}
