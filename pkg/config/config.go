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
	Direct     bool          `yaml:"direct"`
	Write      bool          `yaml:"write"`
	Rand       bool          `yaml:"rand"`
	MinRuntime time.Duration `yaml:"min_runtime"`
	MaxRuntime time.Duration `yaml:"max_runtime"`
	ErrorTarget float64      `yaml:"error_target"`
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
	if cfg.Settings.MaxRuntime == 0 {
		cfg.Settings.MaxRuntime = 5 * time.Second
	}
	return &cfg, nil
}
