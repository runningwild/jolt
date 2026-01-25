package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration for an optimization run.
type Config struct {
	Target    string      `yaml:"target"`
	Search    []Variable  `yaml:"search"`
	Objectives []Objective `yaml:"objectives"`
	Optimizer string      `yaml:"optimizer"` // "simulated_annealing"
	Settings  Settings    `yaml:"settings"`
}

type Settings struct {
	EngineType       string        `yaml:"engine_type"` // "sync" or "uring"
	Direct           bool          `yaml:"direct"`
	ReadPct          int           `yaml:"read_pct"` // 0-100
	Write_Deprecated bool          `yaml:"write"`    // Deprecated: use read_pct
	Rand             bool          `yaml:"rand"`
	MinRuntime time.Duration `yaml:"min_runtime"`
	MaxRuntime time.Duration `yaml:"max_runtime"`
	ErrorTarget float64      `yaml:"error_target"`

	// Annealing settings
	InitialTemp     float64 `yaml:"initial_temp"`    // Starting temperature; should match the magnitude of score changes (e.g., 1000 for IOPS)
	CoolingRate     float64 `yaml:"cooling_rate"`    // How fast to cool; typical values are 0.9 to 0.99
	MinTemp         float64 `yaml:"min_temp"`        // Temperature at which optimization stops (e.g., 0.01)
	StepsPerTemp    int     `yaml:"steps_per_temp"`   // Number of iterations to run at each temperature level (e.g., 1-10)
	RestartInterval int     `yaml:"restart_interval"` // If > 0, reset to best state after this many steps without improvement
}

// Variable defines a parameter to optimize.
type Variable struct {
	Name   string    `yaml:"variable"` // "block_size", "queue_depth", "workers"
	Values []int     `yaml:"values,omitempty"` // Explicit list (e.g. for block_size)
	Range  []int     `yaml:"range,omitempty"`  // [min, max] (e.g. for workers)
	Step   int       `yaml:"step,omitempty"`   // Step size for range
}

// Objective defines what to maximize/minimize or constrain.
type Objective struct {
	Type   string  `yaml:"type"`   // "maximize", "minimize", "constraint"
	Metric string  `yaml:"metric"` // "iops", "throughput", "p99_latency", "p50_latency"
	Limit  string  `yaml:"limit,omitempty"` // For constraints: "10ms", "50000"
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Set defaults
	if cfg.Settings.MinRuntime == 0 {
		cfg.Settings.MinRuntime = 1 * time.Second
	}
	
	// Handle transition from 'write: bool' to 'read_pct: int'
	// If read_pct is not set (is 0) AND write was true, then read_pct is 0.
	// If read_pct is not set (is 0) AND write was false/unset, then read_pct is 100.
	if cfg.Settings.ReadPct == 0 {
		if cfg.Settings.Write_Deprecated {
			cfg.Settings.ReadPct = 0
		} else {
			cfg.Settings.ReadPct = 100
		}
	}

	if cfg.Settings.MaxRuntime == 0 {
		cfg.Settings.MaxRuntime = 5 * time.Second
	}
	if cfg.Settings.InitialTemp == 0 {
		cfg.Settings.InitialTemp = 1000.0
	}
	if cfg.Settings.CoolingRate == 0 {
		cfg.Settings.CoolingRate = 0.95
	}
	if cfg.Settings.MinTemp == 0 {
		cfg.Settings.MinTemp = 0.01
	}
	if cfg.Settings.StepsPerTemp == 0 {
		cfg.Settings.StepsPerTemp = 1
	}
	return &cfg, nil
}
