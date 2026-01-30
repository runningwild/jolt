package main

import (
	"encoding/csv"
			"flag"
			"fmt"
			"math"
			"os"
		
		"time"
	
		"github.com/runningwild/jolt/pkg/analyze"
	
	"github.com/runningwild/jolt/pkg/engine"
)

// runSustainCmd handles "jolt sustain [flags]"
func runSustainCmd() {
	fs := flag.NewFlagSet("sustain", flag.ExitOnError)
	f := SetupFlags(fs)
	durFlag := fs.Duration("duration", 60*time.Second, "Duration to run")
	resFlag := fs.Duration("resolution", 1*time.Millisecond, "Time resolution for output (bin size)")
	tolFlag := fs.Float64("tolerance", 0.05, "Relative error tolerance for linearity analysis (e.g. 0.05 for 5%)")
	outFlag := fs.String("output", "stability.csv", "Output CSV file")

	fs.Parse(os.Args[2:])

	cfg, err := f.LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Construct Params
	params := engine.Params{
		EngineType: cfg.Settings.EngineType,
		Path:       cfg.Target,
		Direct:     cfg.Settings.Direct,
		ReadPct:    cfg.Settings.ReadPct,
		Rand:       cfg.Settings.Rand,
		MinRuntime: *durFlag,
		MaxRuntime: *durFlag,
	}

	// Resolve Variables from Config (taking first value/min of range if not overridden by flags)
	// Flags are already merged into Config by LoadConfig logic? 
	// LoadConfig uses f.VarName overrides but puts them into Search.
	// We need to extract them.
	
	// Helper to get value
	getValue := func(name string, def int) int {
		for _, v := range cfg.Search {
			if v.Name == name {
				if len(v.Values) > 0 { return v.Values[0] }
				if len(v.Range) > 0 { return v.Range[0] }
			}
		}
		return def
	}

	if *f.ConfigFile == "" {
		params.Workers = *f.Workers
		params.QueueDepth = *f.QueueDepth
		params.BlockSize = *f.BS
	} else {
		params.Workers = getValue("workers", 1)
		params.QueueDepth = getValue("queue_depth", 1)
		params.BlockSize = getValue("block_size", 4096)
	}

	fmt.Printf("Running Sustain Analysis for %s...\n", *durFlag)
	fmt.Printf("Configuration: Workers=%d, QD=%d, BS=%d, Engine=%s\n", params.Workers, params.QueueDepth, params.BlockSize, params.EngineType)

	// Analyzer setup
	traceCh := make(chan engine.TraceMsg, 1024)
	analyzer := analyze.NewSustainAnalyzer(traceCh, params.Workers)
	
	doneCh := make(chan struct{})
	go func() {
		analyzer.Run()
		close(doneCh)
	}()

	params.TraceChannel = traceCh
	params.Progress = func(r engine.Result) {
		fmt.Printf("\rElapsed: %v | IOPS: %.0f | Conf: %.4f", r.Duration.Round(time.Second), r.IOPS, r.MetricConfidence)
	}

	eng := engine.New(params.EngineType)
	res, err := eng.Run(params)
	
	fmt.Println() // Newline after progress
	
	close(traceCh) // Signal analyzer to finish
	
	if err != nil {
		fmt.Printf("Run failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Waiting for analysis to complete...")
	<-doneCh

	points := analyzer.GetProfile()
	if len(points) > 0 {
		points = points[:len(points)-1]
	}
	
	finalPoints := downsamplePoints(points, *resFlag)

	if err := writeStabilityCSV(*outFlag, finalPoints); err != nil {
		fmt.Printf("Failed to write output: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Stability profile written to %s\n", *outFlag)
	fmt.Printf("Average IOPS: %.0f\n", res.IOPS)

	if len(finalPoints) > 2 {
		linear := analyze.FindDominantSlope(finalPoints, *tolFlag)
		dur := linear.EndX - linear.StartX
		variation := math.Abs(linear.Slope * dur)

		// Estimate mean IOPS in region
		midX := (linear.StartX + linear.EndX) / 2
		meanIOPS := linear.Intercept + linear.Slope*midX
		relVar := 0.0
		if meanIOPS > 0 {
			relVar = (variation / meanIOPS) * 100
		}

		fmt.Println("\n>>> Stability Analysis <<<")
		fmt.Printf("Linear Region: %.1f%% of the graph (%.2fs - %.2fs)\n", linear.Coverage*100, linear.StartX, linear.EndX)
		fmt.Printf("Slope:         %.4f IOPS/s\n", linear.Slope)
		fmt.Printf("Variation:     %.2f IOPS (%.2f%%) over %.2fs\n", variation, relVar, dur)
	}
}

func downsamplePoints(points []analyze.Point, resolution time.Duration) []analyze.Point {
	if resolution <= 0 || len(points) == 0 {
		return points
	}

	resSec := resolution.Seconds()
	var result []analyze.Point

	var currentBin int64 = -1
	var sumY float64
	var count int

	for _, p := range points {
		bin := int64(p.X / resSec)

		if bin != currentBin {
			if count > 0 {
				avgY := sumY / float64(count)
				result = append(result, analyze.Point{
					X: float64(currentBin+1) * resSec,
					Y: avgY,
				})
			}
			currentBin = bin
			sumY = 0
			count = 0
		}
		sumY += p.Y
		count++
	}

	if count > 0 {
		avgY := sumY / float64(count)
		result = append(result, analyze.Point{
			X: float64(currentBin+1) * resSec,
			Y: avgY,
		})
	}

	return result
}

func writeStabilityCSV(path string, points []analyze.Point) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{"Duration_Seconds", "Min_IOPS"}); err != nil {
		return err
	}

	for _, p := range points {
		if err := w.Write([]string{
			fmt.Sprintf("%.4f", p.X),
			fmt.Sprintf("%.2f", p.Y),
		}); err != nil {
			return err
		}
	}
	return nil
}
