package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/runningwild/jolt/pkg/analyze"
	"github.com/runningwild/jolt/pkg/engine"
	"github.com/runningwild/jolt/pkg/optimize"
)

func main() {
	path := flag.String("path", "", "Path to device or file")
	minRuntime := flag.Duration("min-runtime", 1*time.Second, "Minimum runtime for each test point")
	maxRuntime := flag.Duration("max-runtime", 0, "Maximum runtime for each test point (0 = unlimited)")
	errorTarget := flag.Float64("error", 0.05, "Target relative error (stdErr/mean), e.g., 0.05 for 5%")
	
	bs := flag.Int("bs", 4096, "Block size")
	direct := flag.Bool("direct", true, "Use O_DIRECT")
	write := flag.Bool("write", false, "Write workload (default is read)")
	randIO := flag.Bool("rand", true, "Random I/O (default is sequential)")
	
	minWorkers := flag.Int("min-workers", 1, "Minimum number of workers")
	maxWorkers := flag.Int("max-workers", 32, "Maximum number of workers")
	stepWorkers := flag.Int("step-workers", 1, "Step for workers")

	queueDepth := flag.Int("queue-depth", 0, "Fixed Global Queue Depth (0 = num workers)")
	varName := flag.String("var", "workers", "Variable to optimize: 'workers' or 'queuedepth'")
	
	minVal := flag.Int("min", 1, "Minimum value for the variable")
	maxVal := flag.Int("max", 32, "Maximum value for the variable")
	stepVal := flag.Int("step", 1, "Step value for the variable")

	flag.Parse()

	if *path == "" {
		fmt.Println("Error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	eng := engine.New()
	detector := &analyze.Detector{
		LinearThreshold: 0.5,
		SatThreshold:    0.1,
	}
	opt := optimize.New(eng, detector)

	// Determine ranges based on legacy worker flags or new generic flags
	start, end, step := float64(*minVal), float64(*maxVal), float64(*stepVal)
	if *varName == "workers" {
		// If using legacy worker flags and generic flags are default, use legacy
		if *minWorkers != 1 || *maxWorkers != 32 || *stepWorkers != 1 {
			start, end, step = float64(*minWorkers), float64(*maxWorkers), float64(*stepWorkers)
		}
	}

	searchParams := optimize.SearchParams{
		BaseParams: engine.Params{
			Path:          *path,
			BlockSize:     *bs,
			Direct:        *direct,
			Write:         *write,
			Rand:          *randIO,
			Workers:       *maxWorkers, // Default workers if not varying workers
			QueueDepth:    *queueDepth,
			MinRuntime:    *minRuntime,
			MaxRuntime:    *maxRuntime,
			ErrorTarget:   *errorTarget,
		},
		VarName: *varName,
		Min:     start,
		Max:     end,
		Step:    step,
	}
	
	// If varying workers, BaseParams.Workers is ignored/overwritten loop-by-loop.
	// If varying queuedepth, BaseParams.Workers acts as the fixed pool size.
	// We should ensure that if varying workers, the queue depth (if not set) scales or is fixed?
	// Existing logic: if QueueDepth is 0, engine sets it to Workers.
	// So if varying workers and QD=0, QD scales with Workers (natural behavior).
	
	fmt.Printf("Starting jolt search on %s varying %s...\n", *path, *varName)
	analysis, confScore, err := opt.FindKnee(searchParams)
	if err != nil {
		fmt.Printf("Search error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n--- Analysis Results ---\n")
	if analysis.LinearLimit.X != 0 {
		fmt.Printf("Linear Limit (Knee): %s = %.0f (IOPS: %.2f)\n", searchParams.VarName, analysis.LinearLimit.X, analysis.LinearLimit.Y)
	} else {
		fmt.Printf("Linear Limit (Knee): Not detected in range\n")
	}

	if analysis.SaturationPoint.X != 0 {
		fmt.Printf("Saturation Point:    %s = %.0f (IOPS: %.2f)\n", searchParams.VarName, analysis.SaturationPoint.X, analysis.SaturationPoint.Y)
	} else {
		fmt.Printf("Saturation Point:    Not detected in range\n")
	}
	
	fmt.Printf("Curve Consistency:   %.1f%%\n", confScore*100)
}
