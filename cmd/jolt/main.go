package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
	"github.com/runningwild/jolt/pkg/optimize"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "optimize" {
		runOptimizer()
		return
	}

	runLegacyFlags()
}

func runLegacyFlags() {
	// Flags
	path := flag.String("path", "", "Path to device or file")
	engineType := flag.String("engine", "sync", "I/O engine: 'sync' or 'uring'")
	bs := flag.Int("bs", 4096, "Block size")
	direct := flag.Bool("direct", true, "Use O_DIRECT")
	readPct := flag.Int("read-pct", 100, "Read percentage (0-100)")
	randIO := flag.Bool("rand", true, "Random I/O (default is sequential)")
	
	minRuntime := flag.Duration("min-runtime", 1*time.Second, "Minimum runtime for each test point")
	maxRuntime := flag.Duration("max-runtime", 5*time.Second, "Maximum runtime for each test point")
	errorTarget := flag.Float64("error", 0.05, "Target relative error (stdErr/mean), e.g., 0.05 for 5%")

	// Search Params
	varName := flag.String("var", "workers", "Variable to optimize: 'workers', 'queue_depth', 'block_size'")
	minVal := flag.Int("min", 1, "Minimum value for the variable")
	maxVal := flag.Int("max", 32, "Maximum value for the variable")
	stepVal := flag.Int("step", 1, "Step value for the variable")
	
	// Legacy worker flags override if set
	minWorkers := flag.Int("min-workers", 1, "Minimum number of workers")
	maxWorkers := flag.Int("max-workers", 32, "Maximum number of workers")
	stepWorkers := flag.Int("step-workers", 1, "Step for workers")
	
	queueDepth := flag.Int("queue-depth", 1, "Fixed Global Queue Depth")
	reportFile := flag.String("report", "", "Write optimization history to JSON file")

	flag.Parse()

	if *path == "" {
		fmt.Println("Error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	// 1. Build Config from Flags
	cfg := &config.Config{
		Target: *path,
		Settings: config.Settings{
			EngineType:  *engineType,
			Direct:      *direct,
			ReadPct:     *readPct,
			Rand:        *randIO,
			MinRuntime:  *minRuntime,
			MaxRuntime:  *maxRuntime,
			ErrorTarget: *errorTarget,
		},
		Objectives: []config.Objective{
			{Type: "maximize", Metric: "iops"},
		},
	}

	// 2. Map Legacy Flags to Search Space
	// Handle backward compat for -min-workers etc
	start, end, step := *minVal, *maxVal, *stepVal
	if *varName == "workers" {
		if *minWorkers != 1 || *maxWorkers != 32 || *stepWorkers != 1 {
			start, end, step = *minWorkers, *maxWorkers, *stepWorkers
		}
	}

	// Define the variable to search
	searchVar := config.Variable{
		Name:  *varName,
		Range: []int{start, end},
		Step:  step,
	}
	cfg.Search = append(cfg.Search, searchVar)

	// 3. Handle Fixed Values (Defaults)
	// If we are NOT searching "workers", we fix workers to -max-workers (or -queue-depth for legacy reasons?)
	// Actually, the legacy engine used -max-workers as the fixed count when searching queue_depth.
	// But `CoordinateOptimizer` defaults to the midpoint of a range or we can inject a fixed variable.
	// We can add fixed variables as single-value ranges.
	
	if *varName != "workers" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "workers", Values: []int{*maxWorkers},
		})
	}
	if *varName != "queue_depth" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "queue_depth", Values: []int{*queueDepth},
		})
	}
	if *varName != "block_size" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "block_size", Values: []int{*bs},
		})
	}

	fmt.Printf("Starting jolt optimization on %s varying %s...\n", *path, *varName)
	
	// 4. Run Optimization
	eng := engine.New(cfg.Settings.EngineType)
	optimizer := optimize.NewCoordinate(eng, cfg)
	
	bestState, bestRes, err := optimizer.Optimize()
	if err != nil {
		fmt.Printf("Optimization failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n>>> Optimization Complete <<<\n")
	fmt.Printf("Best State: %v\n", bestState)
	fmt.Printf("Metrics:    IOPS=%.0f, Throughput=%.2f MB/s\n", bestRes.IOPS, bestRes.Throughput/1024/1024)

	if *reportFile != "" {
		writeReport(*reportFile, optimizer.GetHistory())
	}
}

func runOptimizer() {
	optimizeCmd := flag.NewFlagSet("optimize", flag.ExitOnError)
	configFile := optimizeCmd.String("config", "jolt.yaml", "Path to configuration file")
	reportFile := optimizeCmd.String("report", "", "Write optimization history to JSON file")
	optimizeCmd.Parse(os.Args[2:])

	cfg, err := config.Load(*configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Optimizing %s using Coordinate Descent...\n", cfg.Target)
	
	eng := engine.New(cfg.Settings.EngineType)
	optimizer := optimize.NewCoordinate(eng, cfg)
	
	bestState, bestRes, err := optimizer.Optimize()
	if err != nil {
		fmt.Printf("Optimization failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n>>> Optimization Complete <<<\n")
	fmt.Printf("Best State: %v\n", bestState)
	fmt.Printf("Metrics:    IOPS=%.0f, Throughput=%.2f MB/s\n", bestRes.IOPS, bestRes.Throughput/1024/1024)

	if *reportFile != "" {
		writeReport(*reportFile, optimizer.GetHistory())
	}
}

func writeReport(path string, history []optimize.HistoryEntry) {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		fmt.Printf("Failed to marshal report: %v\n", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Printf("Failed to write report: %v\n", err)
		return
	}
	fmt.Printf("Report written to %s\n", path)
}
