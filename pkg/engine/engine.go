package engine

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
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
	done := make(chan struct{})
	
	// Atomic counter for live monitoring
	var opsCounter int64

	// Create token bucket for Global Queue Depth
	qd := params.QueueDepth
	if qd <= 0 {
		qd = params.Workers
	}
	tokens := make(chan struct{}, qd)
	for i := 0; i < qd; i++ {
		tokens <- struct{}{}
	}

	start := time.Now()

	for i := 0; i < params.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			defer wg.Done()
			results <- e.runWorker(id, params, tokens, done, &opsCounter)
		}(i)
	}

	// Monitoring Loop
	monitorTicker := time.NewTicker(100 * time.Millisecond)
	defer monitorTicker.Stop()

	var iopsSamples []float64
	var lastOps int64
	var lastTime = start
	var finalRelErr float64

	for {
		select {
		case <-monitorTicker.C:
			now := time.Now()
			elapsed := now.Sub(start)
			
			currOps := atomic.LoadInt64(&opsCounter)
			deltaOps := currOps - lastOps
			deltaTime := now.Sub(lastTime).Seconds()
			
			if deltaTime > 0 {
				sample := float64(deltaOps) / deltaTime
				iopsSamples = append(iopsSamples, sample)
			}
			
			lastOps = currOps
			lastTime = now

			// Check termination conditions
			if elapsed > params.MinRuntime {
				// Calculate stats
				if len(iopsSamples) > 5 {
					mean, stdErr := calculateStats(iopsSamples)
					
					if mean > 0 {
						finalRelErr = stdErr / mean
						
						// If specified confidence target is met
						if params.ConfidenceTarget > 0 {
							if finalRelErr <= params.ConfidenceTarget {
								goto Finished
							}
						}
					}
				}
			}

			if params.MaxRuntime > 0 && elapsed >= params.MaxRuntime {
				goto Finished
			}
		}
	}

Finished:
	close(done)
	wg.Wait()
	close(results)

	duration := time.Since(start)
	return e.aggregate(results, duration, finalRelErr), nil
}

func calculateStats(samples []float64) (mean float64, stdErr float64) {
	sum := 0.0
	for _, x := range samples {
		sum += x
	}
	mean = sum / float64(len(samples))

	variance := 0.0
	for _, x := range samples {
		variance += (x - mean) * (x - mean)
	}
	stdDev := math.Sqrt(variance / float64(len(samples)))
	stdErr = stdDev / math.Sqrt(float64(len(samples)))
	return
}

type workerResult struct {
	ioCount   int64
	latencies []time.Duration
	err       error
}

func (e *Engine) runWorker(id int, params Params, tokens chan struct{}, done chan struct{}, opsCounter *int64) workerResult {
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

	alignedBlock, err := unix.Mmap(-1, 0, params.BlockSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return workerResult{err: fmt.Errorf("failed to allocate aligned memory: %v", err)}
	}
	defer unix.Munmap(alignedBlock)

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
	latencies := make([]time.Duration, 0, 10000)
	
	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for {
		select {
		case <-done:
			return workerResult{ioCount: ioCount, latencies: latencies}
		case <-tokens:
			// Acquired token
		}

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
		
		// Release token
		tokens <- struct{}{}
		
		latencies = append(latencies, time.Since(ioStart))
		
		if err != nil && err != io.EOF {
			return workerResult{err: err}
		}
		if n > 0 {
			ioCount++
			atomic.AddInt64(opsCounter, 1)
		}
	}
}

func (e *Engine) aggregate(results chan workerResult, duration time.Duration, relErr float64) *Result {
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
		return &Result{Duration: duration, MetricConfidence: relErr}
	}

	sort.Slice(allLatencies, func(i, j int) bool {
		return allLatencies[i] < allLatencies[j]
	})

	return &Result{
		IOPS:             float64(totalIOs) / duration.Seconds(),
		Throughput:       0, // Calculate if needed later
		P50Latency:       allLatencies[len(allLatencies)/2],
		P99Latency:       allLatencies[int(float64(len(allLatencies))*0.99)],
		TotalIOs:         totalIOs,
		Duration:         duration,
		MetricConfidence: relErr,
	}
}