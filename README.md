# jolt

Jolt is a performance analysis tool designed to find optimal operating points for block devices and files. Unlike traditional tools like `fio` that require manual parameter sweeping, Jolt uses optimization algorithms (like Coordinate Descent) to automatically find the best configuration for your performance objectives.

## Features

- **High Performance Engines**:
  - `sync`: Standard Go synchronous I/O (portable).
  - `uring`: High-performance Linux `io_uring` backend.
- **Advanced Optimization**:
  - Automatically tunes parameters like `block_size`, `queue_depth`, and `workers`.
  - Supports hard constraints (e.g., "Maximize IOPS while P99 Latency < 5ms").
- **Statistical Confidence**:
  - Adaptive runtime: Tests run only as long as needed to reach a stable measurement (configurable relative error).
- **Mixed Workloads**: Full control over read/write ratios.
- **Structured Reporting**: Export the entire optimization history to JSON for analysis or plotting.

## Installation

```bash
go build -o jolt ./cmd/jolt
```

## Usage

### Simple Mode (Flags)

Run a quick search across a single variable:

```bash
# Find optimal workers for random read IOPS
sudo ./jolt -path /dev/nvme0n1 -var workers -min 1 -max 32 -error 0.01
```

### Advanced Mode (YAML Configuration)

Create a `jolt.yaml` for multi-variable optimization:

```yaml
target: /dev/nvme0n1
optimizer: coordinate_descent
settings:
  engine_type: uring
  direct: true
  read_pct: 70
  min_runtime: 1s
  error_target: 0.05

search:
  - variable: block_size
    values: [4096, 8192, 16384]
  - variable: queue_depth
    range: [1, 128]
  - variable: workers
    range: [1, 32]

objectives:
  - type: maximize
    metric: throughput
  - type: constraint
    metric: p99_latency
    limit: 10ms
```

Run the optimizer:

```bash
sudo ./jolt optimize -config jolt.yaml -report results.json
```

## Subcommands

- `jolt [flags]`: Legacy flag-based single variable search.
- `jolt optimize -config <file>`: Multi-variable optimization based on a configuration file.