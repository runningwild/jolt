package engine

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/godzie44/go-uring/uring"
	"golang.org/x/sys/unix"
)

type UringEngine struct {
}

func NewUring() *UringEngine {
	return &UringEngine{}
}

func (e *UringEngine) Run(params Params) (*Result, error) {
	if params.BlockSize <= 0 {
		return nil, fmt.Errorf("invalid block size: %d", params.BlockSize)
	}

	// 1. Sanitize Inputs
	// Default to 1 worker if not specified
	numWorkers := params.Workers
	if numWorkers <= 0 {
		numWorkers = 1
	}

	// Default QueueDepth to match Workers if not specified.
	// This ensures we have at least 1 slot per worker.
	if params.QueueDepth <= 0 {
		params.QueueDepth = numWorkers
	}

	// If the user requested more workers than queue slots, cap workers.
	// It doesn't make sense to have a worker with 0 queue slots.
	if numWorkers > params.QueueDepth {
		numWorkers = params.QueueDepth
	}

	// 2. Distribute Queue Depth among workers
	// We divide the total QD as evenly as possible.
	// Remainder slots are distributed 1-per-worker until exhausted.
	qdPerWorker := params.QueueDepth / numWorkers
	remainder := params.QueueDepth % numWorkers

	var wg sync.WaitGroup
	done := make(chan struct{})
	var opsCounter int64
	results := make(chan workerResult, numWorkers)

	start := time.Now()

	for i := 0; i < numWorkers; i++ {
		workerQD := qdPerWorker
		if i < remainder {
			workerQD++
		}
		wg.Add(1)
		go func(id int, qd int) {
			defer wg.Done()
			results <- e.runUringWorker(id, params, qd, done, &opsCounter)
		}(i, workerQD)
	}

	monitorTicker := time.NewTicker(100 * time.Millisecond)
	defer monitorTicker.Stop()

	var iopsSamples []float64
	var lastOps int64
	var lastTime = start
	var finalRelErr float64
	var reason string

	for {
		select {
		case <-monitorTicker.C:
			now := time.Now()
			elapsed := now.Sub(start)
			currOps := atomic.LoadInt64(&opsCounter)
			deltaOps := currOps - lastOps
			deltaTime := now.Sub(lastTime).Seconds()
			if deltaTime > 0 {
				iopsSamples = append(iopsSamples, float64(deltaOps)/deltaTime)
			}
			lastOps = currOps
			lastTime = now

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

			if elapsed > params.MinRuntime {
				if len(iopsSamples) > 5 && mean > 0 && params.ErrorTarget > 0 {
					if finalRelErr <= params.ErrorTarget {
						reason = "Converged"
						goto Finished
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
	
	syncEng := &SyncEngine{}
	res, err := syncEng.aggregate(results, duration, finalRelErr)
	if err != nil {
		return nil, err
	}
	res.Throughput = float64(res.TotalIOs*int64(params.BlockSize)) / duration.Seconds()
	res.TerminationReason = reason
	return res, nil
}

func (e *UringEngine) runUringWorker(id int, params Params, qd int, done chan struct{}, opsCounter *int64) workerResult {
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

	ring, err := uring.New(uint32(qd))
	if err != nil {
		return workerResult{err: fmt.Errorf("failed to setup io_uring: %v", err)}
	}
	defer ring.Close()

	totalBufSize := params.BlockSize * qd
	alignedBlock, err := unix.Mmap(-1, 0, totalBufSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return workerResult{err: fmt.Errorf("failed to allocate aligned memory: %v", err)}
	}
	defer unix.Munmap(alignedBlock)

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return workerResult{err: err}
	}
	
	maxBlocks := size / int64(params.BlockSize)
	if maxBlocks <= 0 {
		return workerResult{err: fmt.Errorf("file too small for block size")}
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	var ioCount int64
	latencies := make([]time.Duration, 0, 10000)

	freeSlots := make([]int, qd)
	for i := 0; i < qd; i++ {
		freeSlots[i] = i
	}
	nextFreeIdx := qd
	
	startTimes := make([]time.Time, qd)
	inFlight := 0
	lastOffset := r.Int63n(maxBlocks) * int64(params.BlockSize)

	for {
		for inFlight < qd && nextFreeIdx > 0 {
			nextFreeIdx--
			slotIdx := freeSlots[nextFreeIdx]

			offset := int64(0)
			if params.Rand {
				offset = r.Int63n(maxBlocks) * int64(params.BlockSize)
			} else {
				offset = lastOffset
				lastOffset = (lastOffset + int64(params.BlockSize)) % size
			}

			isRead := true
			if params.ReadPct < 100 {
				if params.ReadPct == 0 || r.Intn(100) >= params.ReadPct {
					isRead = false
				}
			}

			blockBuf := alignedBlock[slotIdx*params.BlockSize : (slotIdx+1)*params.BlockSize]
			var op uring.Operation
			if isRead {
				op = uring.Read(f.Fd(), blockBuf, uint64(offset))
			} else {
				op = uring.Write(f.Fd(), blockBuf, uint64(offset))
			}
			
			err := ring.QueueSQE(op, 0, uint64(slotIdx))
			if err != nil {
				freeSlots[nextFreeIdx] = slotIdx
				nextFreeIdx++
				break
			}
			startTimes[slotIdx] = time.Now()
			inFlight++
		}

		var cqe *uring.CQEvent
		for {
			cqe, err = ring.SubmitAndWaitCQEvents(1)
			if err == nil || !isEINTR(err) {
				break
			}
		}
		if err != nil {
			return workerResult{err: err}
		}

		for cqe != nil {
			slotIdx := int(cqe.UserData)
			if cqe.Res < 0 {
				return workerResult{err: syscall.Errno(-cqe.Res)}
			}
			
			latencies = append(latencies, time.Since(startTimes[slotIdx]))
			ioCount++
			atomic.AddInt64(opsCounter, 1)
			inFlight--
			
			freeSlots[nextFreeIdx] = slotIdx
			nextFreeIdx++
			ring.SeenCQE(cqe)
			cqe, _ = ring.PeekCQE()
		}

		select {
		case <-done:
			return workerResult{ioCount: ioCount, latencies: latencies}
		default:
		}
	}
}

func isEINTR(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EINTR
	}
	return false
}