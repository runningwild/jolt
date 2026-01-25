package engine

import (
	"os"
	"testing"
	"time"
)

func TestEngineRun(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "jolt-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write some data so we can read it
	size := int64(1024 * 1024) // 1MB
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	eng := New()
	params := Params{
		Path:       tmpFile.Name(),
		BlockSize:  4096,
		Direct:     false, // O_DIRECT might not work on tmpfs
		Write:      false,
		Rand:       true,
		Workers:    2,
		Runtime:    100 * time.Millisecond,
	}

	result, err := eng.Run(params)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.IOPS <= 0 {
		t.Errorf("Expected positive IOPS, got %f", result.IOPS)
	}
	if result.TotalIOs <= 0 {
		t.Errorf("Expected positive TotalIOs, got %d", result.TotalIOs)
	}
	t.Logf("IOPS: %f, P99 Latency: %v", result.IOPS, result.P99Latency)
}
