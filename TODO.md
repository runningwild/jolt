# TODO

## Issues Identified

### 1. Statistical Accuracy of Percentile Aggregation
- **Location:** `pkg/cluster/client.go` (`aggregate`), `pkg/optimize/evaluator.go` (`Evaluate`), `pkg/fio/fio.go` (`ParseOutput`)
- **Issue:** Percentiles (P50, P95, P99) are aggregated using weighted averages. This is mathematically incorrect and only provides an approximation. If distributions differ significantly between nodes/workers, the result will be wrong.
- **Remediation:** Implement true histogram merging. `HdrHistogram` supports merging. The `Result` struct and `TraceMsg` might need to carry serialized histograms instead of just scalar values.

### 2. Potential Cluster Hangs
- **Location:** `pkg/cluster/client.go`
- **Issue:** `ClusterEngine.Run` waits on a `WaitGroup` for all nodes. If a remote node (Agent or FIO) hangs or the network drops, the controller hangs indefinitely. `FioServerNode` uses `exec.Command` without a context/timeout.
- **Remediation:** Add timeouts to `ClusterEngine.Run` (derived from `params.MaxRuntime`) and use `exec.CommandContext`.

### 3. Evaluator Cache Inconsistency
- **Location:** `pkg/optimize/evaluator.go`
- **Issue:** `hashState` only includes `block_size`, `queue_depth`, and `workers`. If new search variables are added to `jolt.yaml` (e.g., `read_pct` optimization), they won't be part of the cache key, leading to collisions where different configs return the same cached result.
- **Remediation:** Update `hashState` to iterate over all keys in the `State` map (sorted) or use a more robust hashing mechanism.

### 4. Agent Statelessness / Overhead
- **Location:** `pkg/agent/server.go`
- **Issue:** The agent creates a new `Engine` for every `POST /run` request. For `io_uring` (and `libaio`), this involves `setup` and `mmap` overhead.
- **Remediation:** Consider caching the engine instance if parameters (like engine type) haven't changed, or accept the overhead for safety.

### 5. Sustain Analyzer Initialization Bug
- **Location:** `pkg/analyze/sustain.go`
- **Issue:** `lastTime` is initialized to 0. The first event (at `time.Now()`) causes a massive delta to be added to the 0-IOPS bin of the histogram. This skews the `stability.csv` output, adding ~50 years of "0 IOPS" data to the profile, which compresses the useful graph area.
- **Remediation:** Initialize `lastTime` to the timestamp of the first processed event.

### 6. Sustain Analysis Memory Usage
- **Location:** `pkg/analyze/sustain.go`
- **Observation:** The `EventPQ` stores all start/end events. For long runs with high IOPS, this can consume gigabytes of memory.
- **Remediation:** Verify if `processEventsUntil` effectively prunes the PQ. If the `safeHorizon` logic works, the PQ should stay small (proportional to `workers * batch_size`). Ensure `workerMinStarts` are updated frequently enough.

### 7. FIO Parser Fragility
- **Location:** `pkg/fio/fio.go`
- **Issue:** Relies on exact string keys "99.000000" in JSON output.
- **Remediation:** Use a fuzzy matcher or iterate the percentile map to find the closest key.
