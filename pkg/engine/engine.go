package engine

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/runningwild/jolt/pkg/stats"
	"golang.org/x/sys/unix"
)

// SyncEngine implements Engine using standard synchronous syscalls and goroutines.
type SyncEngine struct {
}

func NewSync() *SyncEngine {
	return &SyncEngine{}
}

// New returns an Engine of the requested type.
func New(engineType string) Engine {
	switch engineType {
	case "uring":
		return NewUring()
	default:
		return NewSync()
	}
}

// Run executes a workload based on the provided params.
func (e *SyncEngine) Run(params Params) (*Result, error) {
	if params.BlockSize <= 0 {
		return nil, fmt.Errorf("invalid block size: %d", params.BlockSize)
	}

	var wg sync.WaitGroup
	results := make(chan workerResult, params.Workers)
	done := make(chan struct{})
	
	// Atomic counter for live monitoring
	var opsCounter int64

	// Create token bucket for Global Queue Depth enforcement.
	// In the SyncEngine, "Queue Depth" effectively limits the maximum number of
	// concurrent I/O operations across all workers. The `tokens` channel acts
	// as a semaphore: a worker must acquire a token before performing I/O
	// and release it afterwards.
	qd := params.QueueDepth
	if qd <= 0 {
		qd = params.Workers
	}
	tokens := make(chan struct{}, qd)
	for i := 0; i < qd; i++ {
		tokens <- struct{}{}
	}

	start := time.Now()
	var reason string

	for i := 0; i < params.Workers; i++ {
		wg.Add(1)
		go func(id int) {
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

			// Calculate intermediate stats
			var mean, stdErr float64
			if len(iopsSamples) > 0 {
				mean, stdErr = calculateStats(iopsSamples)
			}
			
			if mean > 0 {
				finalRelErr = stdErr / mean
			}

			if params.Progress != nil {
				params.Progress(Result{
					IOPS:             mean,
					MetricConfidence: finalRelErr,
					Duration:         elapsed,
					TotalIOs:         currOps,
				})
			}

			// Check termination conditions
			if elapsed > params.MinRuntime {
				if len(iopsSamples) > 5 {
					if mean > 0 {
						if params.ErrorTarget > 0 {
							if finalRelErr <= params.ErrorTarget {
								reason = "Converged"
								goto Finished
							}
						}
					}
				}
			}

			if params.MaxRuntime > 0 && elapsed >= params.MaxRuntime {
				reason = "Timeout"
				goto Finished
			}
		}
	}

Finished:
	close(done)
	wg.Wait()
	close(results)

	duration := time.Since(start)
	res, err := e.aggregate(results, duration, finalRelErr)
	if err != nil {
		return nil, err
	}
	res.Throughput = float64(res.TotalIOs*int64(params.BlockSize)) / duration.Seconds()
	res.TerminationReason = reason
	return res, nil
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
	hist      *stats.Histogram
	err       error
}

func (e *SyncEngine) runWorker(id int, params Params, tokens chan struct{}, done chan struct{}, opsCounter *int64) workerResult {
	flags := os.O_RDONLY
	if params.ReadPct < 100 {
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
	hist := stats.NewHistogram()
	
	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for {
		select {
		case <-done:
			return workerResult{ioCount: ioCount, hist: hist}
		case <-tokens:
			// Acquired token
		}

		offset := int64(0)
		if params.Rand {
			offset = r.Int63n(maxBlocks) * int64(params.BlockSize)
		} else {
			offset = (ioCount * int64(params.BlockSize)) % (maxBlocks * int64(params.BlockSize))
		}

		// Decide Read vs Write
		isRead := true
		if params.ReadPct < 100 {
			if params.ReadPct == 0 || r.Intn(100) >= params.ReadPct {
				isRead = false
			}
		}

		ioStart := time.Now()
		var n int
		if isRead {
			n, err = f.ReadAt(alignedBlock, offset)
		} else {
			n, err = f.WriteAt(alignedBlock, offset)
		}
		
		// Release token
		tokens <- struct{}{}
		
		hist.Record(time.Since(ioStart).Microseconds())
		
		if err != nil && err != io.EOF {
			return workerResult{err: err}
		}
		if n > 0 {
			ioCount++
			atomic.AddInt64(opsCounter, 1)
		}
	}
}

func (e *SyncEngine) aggregate(results chan workerResult, duration time.Duration, relErr float64) (*Result, error) {
	var totalIOs int64
	hist := stats.NewHistogram()
	var firstErr error

	for res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		totalIOs += res.ioCount
		hist.Merge(res.hist)
	}

	if firstErr != nil {
		return nil, firstErr
	}

	if totalIOs == 0 {
		return &Result{Duration: duration, MetricConfidence: relErr}, nil
	}

	return &Result{
		IOPS:             float64(totalIOs) / duration.Seconds(),
		Throughput:       0, // Calculated in Run
		MeanLatency:      time.Duration(hist.Mean() * float64(time.Microsecond)),
		P50Latency:       time.Duration(hist.ValueAtQuantile(0.50)) * time.Microsecond,
		P95Latency:       time.Duration(hist.ValueAtQuantile(0.95)) * time.Microsecond,
		P99Latency:       time.Duration(hist.ValueAtQuantile(0.99)) * time.Microsecond,
		P999Latency:      time.Duration(hist.ValueAtQuantile(0.999)) * time.Microsecond,
		TotalIOs:         totalIOs,
		Duration:         duration,
		MetricConfidence: relErr,
	}, nil
}
