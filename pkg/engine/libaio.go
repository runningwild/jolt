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
	"unsafe"

	"github.com/HdrHistogram/hdrhistogram-go"
	"golang.org/x/sys/unix"
)

// Constants for libaio
const (
	IOCB_CMD_PREAD  = 0
	IOCB_CMD_PWRITE = 1
)

// Kernel structures (Standard 64-bit layout for x86_64 and arm64)
type iocb struct {
	Data      uint64
	Key       uint32
	RwFlags   uint32
	OpCode    uint16
	ReqPrio   int16
	Fd        uint32
	Buf       uint64
	NBytes    uint64
	Offset    int64
	Reserved2 uint64
	Flags     uint32
	ResFd     uint32
}

type ioEvent struct {
	Data uint64
	Obj  uint64
	Res  int64
	Res2 int64
}

type LibAIOEngine struct {
}

func NewLibAIO() *LibAIOEngine {
	return &LibAIOEngine{}
}

func (e *LibAIOEngine) NumNodes() int { return 1 }

func (e *LibAIOEngine) Run(params Params) (*Result, error) {
	if params.BlockSize <= 0 {
		return nil, fmt.Errorf("invalid block size: %d", params.BlockSize)
	}

	// 1. Sanitize Inputs
	numWorkers := params.Workers
	if numWorkers <= 0 { numWorkers = 1 }

	if params.QueueDepth <= 0 {
		params.QueueDepth = numWorkers
	}

	if numWorkers > params.QueueDepth {
		numWorkers = params.QueueDepth
	}

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
			results <- e.runAIOWorker(id, params, qd, done, &opsCounter)
		}(i, workerQD)
	}

	// Monitoring Loop (Identical to Uring/Sync)
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

			// Calculate Instantaneous IOPS (Last 10 samples = 1 second) for display
			var instantIOPS float64
			window := 10
			if len(iopsSamples) == 0 {
				instantIOPS = 0
			} else if len(iopsSamples) < window {
				instantIOPS = mean
			} else {
				sum := 0.0
				for k := 0; k < window; k++ {
					sum += iopsSamples[len(iopsSamples)-1-k]
				}
				instantIOPS = sum / float64(window)
			}

			if params.Progress != nil {
				params.Progress(Result{
					IOPS:             instantIOPS,
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

func (e *LibAIOEngine) runAIOWorker(id int, params Params, qd int, done chan struct{}, opsCounter *int64) workerResult {
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

	// Setup AIO Context
	var ctxId uint64
	if _, _, errno := unix.Syscall(unix.SYS_IO_SETUP, uintptr(qd), uintptr(unsafe.Pointer(&ctxId)), 0); errno != 0 {
		return workerResult{err: fmt.Errorf("io_setup failed: %v", errno)}
	}
	defer func() {
		unix.Syscall(unix.SYS_IO_DESTROY, uintptr(ctxId), 0, 0)
	}()

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
	hist := hdrhistogram.New(1, 3600000000, 3)

	freeSlots := make([]int, qd)
	for i := 0; i < qd; i++ {
		freeSlots[i] = i
	}
	nextFreeIdx := qd
	
	startTimes := make([]time.Time, qd)
	inFlight := 0
	lastOffset := r.Int63n(maxBlocks) * int64(params.BlockSize)
	
	events := make([]ioEvent, qd)
	iocbs := make([]iocb, qd)
	iocbPtrs := make([]*iocb, qd)

	var traceSpans []Span
	const traceBatchSize = 1000

	for {
		submitCount := 0
		// Fill slots
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

			cb := &iocbs[slotIdx]
			*cb = iocb{}
			cb.Fd = uint32(f.Fd())
			cb.Data = uint64(slotIdx)
			cb.Buf = uint64(uintptr(unsafe.Pointer(&alignedBlock[slotIdx*params.BlockSize])))
			cb.NBytes = uint64(params.BlockSize)
			cb.Offset = offset
			
			if isRead {
				cb.OpCode = IOCB_CMD_PREAD
			} else {
				cb.OpCode = IOCB_CMD_PWRITE
			}

			iocbPtrs[submitCount] = cb
			startTimes[slotIdx] = time.Now()
			submitCount++
			inFlight++
		}

		if submitCount > 0 {
			nSub, _, errno := unix.Syscall(unix.SYS_IO_SUBMIT, uintptr(ctxId), uintptr(submitCount), uintptr(unsafe.Pointer(&iocbPtrs[0])))
			if errno != 0 {
				return workerResult{err: fmt.Errorf("io_submit failed: %v", errno)}
			}
			if int(nSub) != submitCount {
				return workerResult{err: fmt.Errorf("io_submit submitted %d < %d", nSub, submitCount)}
			}
		}

		minNr := 0
		if inFlight == qd {
			minNr = 1
		}
		
		if inFlight > 0 {
			// io_getevents
			nEvt, _, errno := unix.Syscall6(unix.SYS_IO_GETEVENTS, uintptr(ctxId), uintptr(minNr), uintptr(qd), uintptr(unsafe.Pointer(&events[0])), 0, 0)
			if errno != 0 && errno != syscall.EINTR {
				return workerResult{err: fmt.Errorf("io_getevents failed: %v", errno)}
			}

			for i := 0; i < int(nEvt); i++ {
				evt := events[i]
				slotIdx := int(evt.Data)
				
				if int64(evt.Res) < 0 {
					return workerResult{err: fmt.Errorf("aio IO error: %v", evt.Res)}
				}

				ioEnd := time.Now()
				ioStart := startTimes[slotIdx]
				startTimes[slotIdx] = time.Time{}

				_ = hist.RecordValue(ioEnd.Sub(ioStart).Microseconds())
				ioCount++
				atomic.AddInt64(opsCounter, 1)
				inFlight--

				freeSlots[nextFreeIdx] = slotIdx
				nextFreeIdx++

				if params.TraceChannel != nil {
					traceSpans = append(traceSpans, Span{Start: ioStart.UnixNano(), End: ioEnd.UnixNano()})
				}
			}

			if params.TraceChannel != nil && len(traceSpans) >= traceBatchSize {
				minStart := int64(math.MaxInt64)
				for k := 0; k < qd; k++ {
					if !startTimes[k].IsZero() {
						ts := startTimes[k].UnixNano()
						if ts < minStart {
							minStart = ts
						}
					}
				}
				params.TraceChannel <- TraceMsg{WorkerID: id, Spans: traceSpans, MinStart: minStart}
				traceSpans = nil
			}
		}

		select {
		case <-done:
			if params.TraceChannel != nil && len(traceSpans) > 0 {
				params.TraceChannel <- TraceMsg{WorkerID: id, Spans: traceSpans, MinStart: math.MaxInt64}
			}
			return workerResult{ioCount: ioCount, hist: hist}
		default:
		}
	}
}
