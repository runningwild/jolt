package engine

import (
	"math"
	"os"
	"testing"
	"time"
)

// TestUringConsistency checks if multiple runs of the same config produce stable results.
func TestUringConsistency(t *testing.T) {
	// Skip if not running on Linux (io_uring is Linux-specific)
	// We'll try to Setup a small ring to check for support.
	eng := NewUring()
	
	tmpFile, err := os.CreateTemp("", "jolt-uring-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// 10MB test file
	if err := tmpFile.Truncate(10 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	params := Params{
		EngineType:  "uring",
		Path:        tmpFile.Name(),
		BlockSize:   4096,
		Direct:      false, // Use buffered for generic test environments
		ReadPct:     100,
		Rand:        true,
		Workers:     4,
		QueueDepth:  64,
		MinRuntime:  1 * time.Second,
		MaxRuntime:  2 * time.Second,
		ErrorTarget: 0.05,
	}

	// Run 3 times and check variance
	var results []*Result
	for i := 0; i < 3; i++ {
		res, err := eng.Run(params)
		if err != nil {
			t.Fatalf("Run %d failed: %v", i, err)
		}
		results = append(results, res)
		t.Logf("Run %d: IOPS=%.2f", i, res.IOPS)
	}

	// Calculate Mean and Max deviation
	sum := 0.0
	for _, r := range results {
		sum += r.IOPS
	}
	mean := sum / float64(len(results))

	for i, r := range results {
		diff := math.Abs(r.IOPS - mean)
		pct := (diff / mean) * 100
		if pct > 20 { // Allow 20% variance for CI/noisy environments, but shouldn't be 400%
			t.Errorf("Run %d IOPS (%.2f) deviated from mean (%.2f) by %.1f%%", i, r.IOPS, mean, pct)
		}
	}
}

// TestUringHighQD stresses the engine with high concurrency.
func TestUringHighQD(t *testing.T) {
	eng := NewUring()
	tmpFile, _ := os.CreateTemp("", "jolt-uring-stress")
	defer os.Remove(tmpFile.Name())
	_ = tmpFile.Truncate(10 * 1024 * 1024)
	tmpFile.Close()

	params := Params{
		EngineType:  "uring",
		Path:        tmpFile.Name(),
		BlockSize:   4096,
		Direct:      false,
		ReadPct:     50, // Mixed R/W
		Rand:        true,
		Workers:     1, // 1 worker, deep queue
		QueueDepth:  128,
		MinRuntime:  1 * time.Second,
		MaxRuntime:  2 * time.Second,
		ErrorTarget: 0.1,
	}

	res, err := eng.Run(params)
	if err != nil {
		t.Fatalf("Stress test failed: %v", err)
	}
	if res.IOPS < 100 {
		t.Errorf("Extremely low IOPS (%f) in stress test", res.IOPS)
	}
}
