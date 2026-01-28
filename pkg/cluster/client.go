package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/runningwild/jolt/pkg/engine"
)

type ClusterEngine struct {
	nodes []string
}

func New(nodes []string) *ClusterEngine {
	return &ClusterEngine{
		nodes: nodes,
	}
}

func (c *ClusterEngine) Run(params engine.Params) (*engine.Result, error) {
	var wg sync.WaitGroup
	results := make([]*engine.Result, len(c.nodes))
	errors := make([]error, len(c.nodes))

	// Fan out
	for i, node := range c.nodes {
		wg.Add(1)
		
		// Calculate per-node params
		nodeParams := params
		
		// Always distribute Workers
		baseW := params.Workers / len(c.nodes)
		remW := params.Workers % len(c.nodes)
		if i < remW {
			nodeParams.Workers = baseW + 1
		} else {
			nodeParams.Workers = baseW
		}
		
		// Distribute QueueDepth only if explicitly set (> 0)
		if params.QueueDepth > 0 {
			baseQD := params.QueueDepth / len(c.nodes)
			remQD := params.QueueDepth % len(c.nodes)
			if i < remQD {
				nodeParams.QueueDepth = baseQD + 1
			} else {
				nodeParams.QueueDepth = baseQD
			}

			// If distributed QD is 0, DO NOT RUN this node.
			// Otherwise the engine will default QD = Workers, causing huge phantom load.
			if nodeParams.QueueDepth == 0 {
				wg.Done() // Skip
				continue
			}
		}

		// If distributed Workers is 0, DO NOT RUN this node.
		if nodeParams.Workers == 0 {
			wg.Done() // Skip
			continue
		}

		go func(idx int, host string, p engine.Params) {
			defer wg.Done()
			res, err := c.runRemote(host, p)
			results[idx] = res
			errors[idx] = err
		}(i, node, nodeParams)
	}
	wg.Wait()

	// Check errors
	for i, err := range errors {
		if err != nil {
			return nil, fmt.Errorf("node %s failed: %v", c.nodes[i], err)
		}
	}

	return c.aggregate(results), nil
}

func (c *ClusterEngine) runRemote(host string, params engine.Params) (*engine.Result, error) {
	url := fmt.Sprintf("http://%s/run", host)
	
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Timeout should be MaxRuntime + Buffer
	timeout := params.MaxRuntime + 5*time.Second
	if timeout < 10*time.Second { timeout = 10 * time.Second }
	client := &http.Client{Timeout: timeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent %s error (%s): %s", host, resp.Status, string(bytes.TrimSpace(body)))
	}

	var res engine.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *ClusterEngine) aggregate(results []*engine.Result) *engine.Result {
	agg := &engine.Result{}
	var totalWeight float64

	for _, r := range results {
		if r == nil { continue }
		
		agg.TotalIOs += r.TotalIOs
		agg.IOPS += r.IOPS
		agg.Throughput += r.Throughput
		
		if r.Duration > agg.Duration {
			agg.Duration = r.Duration
		}
		if r.MetricConfidence > agg.MetricConfidence {
			agg.MetricConfidence = r.MetricConfidence
		}
		agg.TerminationReason = r.TerminationReason // Last one wins?

		// Weighted aggregation for latencies
		weight := float64(r.TotalIOs)
		totalWeight += weight
		
		agg.MeanLatency += time.Duration(float64(r.MeanLatency) * weight)
		agg.P50Latency += time.Duration(float64(r.P50Latency) * weight)
		agg.P95Latency += time.Duration(float64(r.P95Latency) * weight)
		agg.P99Latency += time.Duration(float64(r.P99Latency) * weight)
		agg.P999Latency += time.Duration(float64(r.P999Latency) * weight)
	}

	if totalWeight > 0 {
		agg.MeanLatency = time.Duration(float64(agg.MeanLatency) / totalWeight)
		agg.P50Latency = time.Duration(float64(agg.P50Latency) / totalWeight)
		agg.P95Latency = time.Duration(float64(agg.P95Latency) / totalWeight)
		agg.P99Latency = time.Duration(float64(agg.P99Latency) / totalWeight)
		agg.P999Latency = time.Duration(float64(agg.P999Latency) / totalWeight)
	}

	return agg
}
