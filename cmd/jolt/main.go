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
	runtime := flag.Duration("runtime", 2*time.Second, "Runtime for each test point")
	bs := flag.Int("bs", 4096, "Block size")
	direct := flag.Bool("direct", true, "Use O_DIRECT")
	write := flag.Bool("write", false, "Write workload (default is read)")
	randIO := flag.Bool("rand", true, "Random I/O (default is sequential)")
	
	minWorkers := flag.Int("min-workers", 1, "Minimum number of workers")
	maxWorkers := flag.Int("max-workers", 32, "Maximum number of workers")
	stepWorkers := flag.Int("step-workers", 1, "Step for workers")

	flag.Parse()

	if *path == "" {
		fmt.Println("Error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	eng := engine.New()
	detector := &analyze.SlopeDetector{Threshold: 0.5}
	opt := optimize.New(eng, detector)

	searchParams := optimize.SearchParams{
		BaseParams: engine.Params{
			Path:      *path,
			BlockSize: *bs,
			Direct:    *direct,
			Write:     *write,
			Rand:      *randIO,
			Runtime:   *runtime,
		},
		VarName: "workers",
		Min:     float64(*minWorkers),
		Max:     float64(*maxWorkers),
		Step:    float64(*stepWorkers),
	}

	fmt.Printf("Starting jolt search on %s...\n", *path)
	knee, err := opt.FindKnee(searchParams)
	if err != nil {
		fmt.Printf("Search finished: %v\n", err)
	} else {
		fmt.Printf("\n>>> Found Knee at %s = %.0f (IOPS: %.2f)\n", searchParams.VarName, knee.X, knee.Y)
	}
}
