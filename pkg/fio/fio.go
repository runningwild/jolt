package fio

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/runningwild/jolt/pkg/engine"
)

// GenerateJob creates a FIO job file content based on Jolt params.
func GenerateJob(p engine.Params) string {
	var sb strings.Builder

	sb.WriteString("[global]\n")
	
	// Engine mapping
	switch p.EngineType {
	case "uring":
		sb.WriteString("ioengine=io_uring\n")
	case "libaio":
		sb.WriteString("ioengine=libaio\n")
	case "sync":
		sb.WriteString("ioengine=sync\n")
	default:
		sb.WriteString("ioengine=libaio\n") // Default fallback
	}

	sb.WriteString(fmt.Sprintf("filename=%s\n", p.Path))
	sb.WriteString(fmt.Sprintf("bs=%d\n", p.BlockSize))
	
	if p.Direct {
		sb.WriteString("direct=1\n")
	} else {
		sb.WriteString("direct=0\n")
	}

	// Read/Write Mix
	if p.ReadPct == 100 {
		if p.Rand {
			sb.WriteString("rw=randread\n")
		} else {
			sb.WriteString("rw=read\n")
		}
	} else if p.ReadPct == 0 {
		if p.Rand {
			sb.WriteString("rw=randwrite\n")
		} else {
			sb.WriteString("rw=write\n")
		}
	} else {
		if p.Rand {
			sb.WriteString("rw=randrw\n")
		} else {
			sb.WriteString("rw=rw\n")
		}
		sb.WriteString(fmt.Sprintf("rwmixread=%d\n", p.ReadPct))
	}

	// Concurrency
	// Jolt "Workers" -> FIO "numjobs"
	// Jolt "QueueDepth" -> Total slots per node.
	// FIO "iodepth" -> Slots per job.
	// iodepth = QueueDepth / Workers
	
	iodepth := 1
	if p.Workers > 0 {
		if p.QueueDepth > 0 {
			iodepth = p.QueueDepth / p.Workers
			if iodepth < 1 { iodepth = 1 }
		} else {
			// Default behavior: QD matches Workers (1 slot per worker)
			iodepth = 1
		}
	}

	sb.WriteString(fmt.Sprintf("numjobs=%d\n", p.Workers))
	sb.WriteString(fmt.Sprintf("iodepth=%d\n", iodepth))
	
	// FIO needs separate threads if numjobs > 1
	if p.Workers > 1 {
		sb.WriteString("group_reporting\n")
	}

	// Runtime
	// FIO time_based requires a runtime
	// We use MaxRuntime from params
	dur := p.MaxRuntime
	if dur == 0 { dur = 10 * time.Second }
	
	sb.WriteString("time_based\n")
	sb.WriteString(fmt.Sprintf("runtime=%ds\n", int(dur.Seconds())))

	// To get JSON output matching our needs
	sb.WriteString("\n[jolt_job]\n")
	return sb.String()
}

// Structures for parsing FIO JSON output
type FioOutput struct {
	Jobs        []FioJob `json:"jobs"`
	ClientStats []FioJob `json:"client_stats"`
}

type FioJob struct {
	Read  FioStats `json:"read"`
	Write FioStats `json:"write"`
}

type FioStats struct {
	IOPS      float64     `json:"iops"`
	TotalIOS  int64       `json:"total_ios"`
	ClatNs    FioLatStats `json:"clat_ns"` // Completion latency
}

type FioLatStats struct {
	Mean       float64           `json:"mean"`
	Percentile map[string]uint64 `json:"percentile"` // e.g. "99.000000": 1234
}

func ParseOutput(jsonData []byte, duration time.Duration) (*engine.Result, error) {
	var out FioOutput
	if err := json.Unmarshal(jsonData, &out); err != nil {
		return nil, err
	}

	jobs := out.Jobs
	if len(jobs) == 0 {
		jobs = out.ClientStats
	}

	res := &engine.Result{
		Duration: duration,
	}

	// FIO with group_reporting should return 1 job summarizing everything.
	// If multiple jobs, we sum/average.
	
	var totalReadIOs, totalWriteIOs int64
	var totalReadIOPS, totalWriteIOPS float64
	
	// Helper to parse percentile map
	getPerc := func(m map[string]uint64, target string) time.Duration {
		// FIO keys are strings like "99.000000"
		// We try exact match or close match? FIO usually consistent.
		if v, ok := m[target]; ok {
			return time.Duration(v) * time.Nanosecond
		}
		// Try trim trailing zeros?
		// For now assume standard FIO output keys
		return 0
	}

	for _, j := range jobs {
		totalReadIOs += j.Read.TotalIOS
		totalWriteIOs += j.Write.TotalIOS
		
		totalReadIOPS += j.Read.IOPS
		totalWriteIOPS += j.Write.IOPS
		
		// This aggregation is naive if there are multiple reported groups,
		// but with group_reporting=1 there is only one.
		// We'll assume the latencies in the first job block are representative 
		// (or the only ones).
		
		// If both read and write exist, we need to weight them.
		rCount := float64(j.Read.TotalIOS)
		wCount := float64(j.Write.TotalIOS)
		total := rCount + wCount
		
		if total > 0 {
			// Mean
			rMean := j.Read.ClatNs.Mean
			wMean := j.Write.ClatNs.Mean
			avgMean := (rMean*rCount + wMean*wCount) / total
			res.MeanLatency = time.Duration(avgMean) * time.Nanosecond

			// P99 (Approximation)
			rP99 := getPerc(j.Read.ClatNs.Percentile, "99.000000")
			wP99 := getPerc(j.Write.ClatNs.Percentile, "99.000000")
			res.P99Latency = time.Duration((float64(rP99)*rCount + float64(wP99)*wCount) / total)
			
			// P50
			rP50 := getPerc(j.Read.ClatNs.Percentile, "50.000000")
			wP50 := getPerc(j.Write.ClatNs.Percentile, "50.000000")
			res.P50Latency = time.Duration((float64(rP50)*rCount + float64(wP50)*wCount) / total)
		}
	}
	
	res.TotalIOs = totalReadIOs + totalWriteIOs
	res.IOPS = totalReadIOPS + totalWriteIOPS
	
	// Calculate Throughput? FIO provides it but we can derive from IOPS * BS if needed, 
	// or parse BW field. Result struct has Throughput.
	// Let's assume caller sets Throughput or we add BW parsing later if needed.
	// For knee finding, IOPS is primary.
	
	return res, nil
}
