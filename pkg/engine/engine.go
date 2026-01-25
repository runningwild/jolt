package engine

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Engine manages the execution of I/O workloads.
type Engine struct {
}

func New() *Engine {
	return &Engine{}
}

// Run executes a workload based on the provided params.
func (e *Engine) Run(params Params) (*Result, error) {
	if params.BlockSize <= 0 {
		return nil, fmt.Errorf("invalid block size: %d", params.BlockSize)
	}

	var wg sync.WaitGroup
	results := make(chan workerResult, params.Workers)
	start := time.Now()

	for i := 0; i < params.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			runtime.LockOSThread() // Ensure we stay on this thread for consistent I/O
			defer runtime.UnlockOSThread() // Good practice, though goroutine exits anyway
			defer wg.Done()
			results <- e.runWorker(id, params)
		}(i)
	}

	wg.Wait()
	close(results)

	duration := time.Since(start)
	return e.aggregate(results, duration), nil
}

type workerResult struct {
	ioCount   int64
	latencies []time.Duration
	err       error
}

func (e *Engine) runWorker(id int, params Params) workerResult {
	flags := os.O_RDONLY
	if params.Write {
		flags = os.O_RDWR
	}
	if params.Direct {
		flags |= syscall.O_DIRECT
	}

	f, err := os.OpenFile(params.Path, flags, 0666)
	if err != nil {
		return workerResult{err: err}
	}
	defer f.Close()

	// For O_DIRECT, we need aligned memory.
	// Using Mmap to get page-aligned memory.
	
	alignedBlock, err := unix.Mmap(-1, 0, params.BlockSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return workerResult{err: fmt.Errorf("failed to allocate aligned memory: %v", err)}
	}
	defer unix.Munmap(alignedBlock)

	// Determine file size for random I/O
	// os.Stat returns 0 for block devices, so we use Seek to find the end.
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return workerResult{err: err}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return workerResult{err: err}
	}
	
	maxBlocks := size / int64(params.BlockSize)

	if maxBlocks <= 0 {
		return workerResult{err: fmt.Errorf("file too small for block size")}
	}

	var ioCount int64
	// Pre-allocate some space for latencies to reduce allocations
	latencies := make([]time.Duration, 0, 10000)
	stopTime := time.Now().Add(params.Runtime)

	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for time.Now().Before(stopTime) {
		offset := int64(0)
		if params.Rand {
			offset = r.Int63n(maxBlocks) * int64(params.BlockSize)
		} else {
			offset = (ioCount * int64(params.BlockSize)) % (maxBlocks * int64(params.BlockSize))
		}

		ioStart := time.Now()
		var n int
		if params.Write {
			n, err = f.WriteAt(alignedBlock, offset)
		} else {
			n, err = f.ReadAt(alignedBlock, offset)
		}
		latencies = append(latencies, time.Since(ioStart))

		if err != nil && err != io.EOF {
			return workerResult{err: err}
		}
		if n > 0 {
			ioCount++
		}
	}

	return workerResult{ioCount: ioCount, latencies: latencies}
}

func (e *Engine) aggregate(results chan workerResult, duration time.Duration) *Result {
	var totalIOs int64
	var allLatencies []time.Duration

	for res := range results {
		if res.err != nil {
			// In a real tool, we'd handle errors better
			continue
		}
		totalIOs += res.ioCount
		allLatencies = append(allLatencies, res.latencies...)
	}

	if len(allLatencies) == 0 {
		return &Result{Duration: duration}
	}

	sort.Slice(allLatencies, func(i, j int) bool {
		return allLatencies[i] < allLatencies[j]
	})

	return &Result{
		IOPS:       float64(totalIOs) / duration.Seconds(),
		Throughput: 0, // Calculate if needed later
		P50Latency: allLatencies[len(allLatencies)/2],
		P99Latency: allLatencies[int(float64(len(allLatencies))*0.99)],
		TotalIOs:   totalIOs,
		Duration:   duration,
	}
}
