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

	numWorkers := params.Workers
	if numWorkers <= 0 {
		numWorkers = 1
	}
	qdPerWorker := params.QueueDepth / numWorkers
	if qdPerWorker <= 0 {
		qdPerWorker = 1
	}

	var wg sync.WaitGroup
	done := make(chan struct{})
	var opsCounter int64
	results := make(chan workerResult, numWorkers)

	start := time.Now()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			results <- e.runUringWorker(id, params, qdPerWorker, done, &opsCounter)
		}(i)
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
	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	var ioCount int64
	latencies := make([]time.Duration, 0, 10000)

	startTimes := make([]time.Time, qd)
	inFlight := 0

	for {
		queued := 0
		for inFlight < qd {
			idx := inFlight 

			offset := int64(0)
			if params.Rand {
				offset = r.Int63n(maxBlocks) * int64(params.BlockSize)
			} else {
				offset = (r.Int63n(maxBlocks)) * int64(params.BlockSize)
			}

			isRead := true
			if params.ReadPct < 100 {
				if params.ReadPct == 0 || r.Intn(100) >= params.ReadPct {
					isRead = false
				}
			}

			blockBuf := alignedBlock[idx*params.BlockSize : (idx+1)*params.BlockSize]
			
			var op uring.Operation
			if isRead {
				op = uring.Read(f.Fd(), blockBuf, uint64(offset))
			} else {
				op = uring.Write(f.Fd(), blockBuf, uint64(offset))
			}
			
			err := ring.QueueSQE(op, 0, uint64(idx))
			if err != nil {
				break
			}
			startTimes[idx] = time.Now()
			inFlight++
			queued++
		}

		if queued > 0 {
			for {
				_, err := ring.Submit()
				if err == nil || !isEINTR(err) {
					if err != nil {
						return workerResult{err: err}
					}
					break
				}
			}
		}

		var cqe *uring.CQEvent
		for {
			cqe, err = ring.WaitCQEvents(1)
			if err == nil || !isEINTR(err) {
				break
			}
		}
		if err != nil {
			return workerResult{err: err}
		}

		for cqe != nil {
			idx := int(cqe.UserData)
			if cqe.Res < 0 {
				return workerResult{err: syscall.Errno(-cqe.Res)}
			}
			
			latencies = append(latencies, time.Since(startTimes[idx]))
			ioCount++
			atomic.AddInt64(opsCounter, 1)
			inFlight--
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
	if errors.Is(err, syscall.EINTR) {
		return true
	}
	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		return sysErr.Err == syscall.EINTR
	}
	return false
}
