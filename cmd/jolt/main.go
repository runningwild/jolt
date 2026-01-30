package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/runningwild/jolt/pkg/agent"
	"github.com/runningwild/jolt/pkg/cluster"
	"github.com/runningwild/jolt/pkg/config"
	"github.com/runningwild/jolt/pkg/engine"
	"github.com/runningwild/jolt/pkg/optimize"
	"github.com/runningwild/jolt/pkg/sweep"
)

func main() {
	// Dispatch subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "optimize":
			runOptimizerCmd() // Explicit 'optimize' subcommand
			return
		case "sweep":
			runSweepCmd() // Explicit 'sweep' subcommand
			return
		case "sustain":
			runSustainCmd()
			return
		case "agent":
			runAgentCmd()
			return
		case "remote":
			runRemoteCmd()
			return
		}
	}

	// Default behavior (flags -> optimize)
	runDefaultOptimize()
}

// Flags holds pointers to all supported CLI flags
type Flags struct {
	// Config File (optional)
	ConfigFile *string
	WriteConfig *string

	// Legacy/Flag-based overrides
	Path        *string
	EngineType  *string
	BS          *int
	Direct      *bool
	ReadPct     *int
	RandIO      *bool
	MinRuntime  *time.Duration
	MaxRuntime  *time.Duration
	ErrorTarget *float64

	// Search Params
	VarName    *string
	MinVal     *int
	MaxVal     *int
	StepVal    *int
	Workers    *int
	QueueDepth *int

	// Reporting
	ReportFile *string
}

func SetupFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.ConfigFile = fs.String("config", "", "Path to configuration file (disables other flags)")
	f.WriteConfig = fs.String("write-config", "", "Save the generated configuration to this YAML file")

	f.Path = fs.String("path", "", "Path to device or file")
	f.EngineType = fs.String("engine", "sync", "I/O engine: 'sync', 'uring', or 'libaio'")
	f.BS = fs.Int("bs", 4096, "Block size")
	f.Direct = fs.Bool("direct", true, "Use O_DIRECT")
			f.ReadPct = fs.Int("read-pct", 100, "Read percentage (0-100)")
			f.RandIO = fs.Bool("rand", true, "Random I/O (default is sequential)")
			
			f.MinRuntime = fs.Duration("min-runtime", 1*time.Second, "Minimum runtime for each test point")
			f.MaxRuntime = fs.Duration("max-runtime", 5*time.Second, "Maximum runtime for each test point")
	f.ErrorTarget = fs.Float64("error", 0.05, "Target relative error (stdErr/mean), e.g., 0.05 for 5%")

	f.VarName = fs.String("var", "workers", "Variable to optimize: 'workers', 'queue_depth', 'block_size'")
	f.MinVal = fs.Int("min", 1, "Minimum value for the variable")
	f.MaxVal = fs.Int("max", 32, "Maximum value for the variable")
	f.StepVal = fs.Int("step", 1, "Step value for the variable")
	
f.Workers = fs.Int("workers", 1, "Fixed number of workers (when not optimizing workers)")
f.QueueDepth = fs.Int("queue-depth", 1, "Fixed Global Queue Depth (when not optimizing queue_depth)")
	
f.ReportFile = fs.String("report", "", "Write results to JSON file")
	return f
}

// LoadConfig determines the config source (file or flags) and returns a Config object.
func (f *Flags) LoadConfig() (*config.Config, error) {
	// 1. If -config is provided, load it
	if *f.ConfigFile != "" {
		cfg, err := config.Load(*f.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load config file: %w", err)
		}
		// Note: We currently don't allow overriding config file values with other flags.
		return cfg, nil
	}

	// 2. Build Config from Flags
	if *f.Path == "" {
		return nil, fmt.Errorf("-path is required when using flags")
	}

	// Normalize variable name (allow queue-depth to match queue_depth)
	normalizedVar := strings.ReplaceAll(*f.VarName, "-", "_")

	cfg := &config.Config{
		Target: *f.Path,
		Settings: config.Settings{
			EngineType:  *f.EngineType,
			Direct:      *f.Direct,
			ReadPct:     *f.ReadPct,
			Rand:        *f.RandIO,
			MinRuntime:  *f.MinRuntime,
			MaxRuntime:  *f.MaxRuntime,
			ErrorTarget: *f.ErrorTarget,
		},
		Objectives: []config.Objective{
			{Type: "maximize", Metric: "iops"},
		},
	}

	// Define the variable to search
	searchVar := config.Variable{
		Name:  normalizedVar,
		Range: []int{*f.MinVal, *f.MaxVal},
		Step:  *f.StepVal,
	}
	cfg.Search = append(cfg.Search, searchVar)

	// Handle Fixed Values
	if normalizedVar != "workers" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "workers", Values: []int{*f.Workers},
		})
	}
	if normalizedVar != "queue_depth" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "queue_depth", Values: []int{*f.QueueDepth},
		})
	}
	if normalizedVar != "block_size" {
		cfg.Search = append(cfg.Search, config.Variable{
			Name: "block_size", Values: []int{*f.BS},
		})
	}

	return cfg, nil
}

func (f *Flags) MaybeWriteConfig(cfg *config.Config) {
	if *f.WriteConfig == "" {
		return
	}
	// Marshal to YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Printf("Warning: Failed to marshal config for writing: %v\n", err)
		return
	}
	if err := os.WriteFile(*f.WriteConfig, data, 0644); err != nil {
		fmt.Printf("Warning: Failed to write config file: %v\n", err)
		return
	}
	fmt.Printf("Configuration written to %s\n", *f.WriteConfig)
}

// runDefaultOptimize handles "jolt [flags]"

func runDefaultOptimize() {

	f := SetupFlags(flag.CommandLine)

	flag.Parse()



	if *f.ConfigFile == "" && *f.Path == "" {

		// If neither config nor path is provided, print help

		flag.Usage()

		os.Exit(1)

	}



	cfg, err := f.LoadConfig()

	if err != nil {

		fmt.Printf("Error: %v\n", err)

		os.Exit(1)

	}

	f.MaybeWriteConfig(cfg)

	eng := engine.New(cfg.Settings.EngineType)

	runOptimizeLogic(f, cfg, eng)

}



// runOptimizerCmd handles "jolt optimize [flags]"

func runOptimizerCmd() {

	fs := flag.NewFlagSet("optimize", flag.ExitOnError)

	f := SetupFlags(fs)

	fs.Parse(os.Args[2:])

	

	cfg, err := f.LoadConfig()

	if err != nil {

		fmt.Printf("Error: %v\n", err)

		os.Exit(1)

	}

	f.MaybeWriteConfig(cfg)

	eng := engine.New(cfg.Settings.EngineType)

	runOptimizeLogic(f, cfg, eng)

}



func runOptimizeLogic(f *Flags, cfg *config.Config, eng engine.Engine) {

	fmt.Printf("Optimizing %s using Coordinate Descent...\n", cfg.Target)

	

	optimizer := optimize.NewCoordinate(eng, cfg)

	

	bestState, bestRes, err := optimizer.Optimize()

	if err != nil {

		fmt.Printf("Optimization failed: %v\n", err)

		os.Exit(1)

	}



	fmt.Printf("\n>>> Optimization Complete <<<\n")

	fmt.Printf("Best State: %v\n", bestState)

	fmt.Printf("Metrics:    IOPS=%.0f, Throughput=%.2f MB/s\n", bestRes.IOPS, bestRes.Throughput/1024/1024)



	if *f.ReportFile != "" {

		writeReport(*f.ReportFile, optimizer.GetHistory())

	}

}



// runSweepCmd handles "jolt sweep [flags]"

func runSweepCmd() {

	fs := flag.NewFlagSet("sweep", flag.ExitOnError)

	f := SetupFlags(fs)

	fs.Parse(os.Args[2:])



	cfg, err := f.LoadConfig()

	if err != nil {

		fmt.Printf("Error: %v\n", err)

		os.Exit(1)

	}

	f.MaybeWriteConfig(cfg)

	eng := engine.New(cfg.Settings.EngineType)

	runSweepLogic(f, cfg, eng)

}



func runSweepLogic(f *Flags, cfg *config.Config, eng engine.Engine) {

	s := sweep.New(eng, cfg)



	history, knee, err := s.Run()

	if err != nil {

		fmt.Printf("Sweep failed: %v\n", err)

		os.Exit(1)

	}



	fmt.Printf("\n>>> Sweep Complete <<<\n")

	if knee.OriginalX != nil {

		fmt.Printf("Knee found at: %v (IOPS: %.0f)\n", knee.OriginalX, knee.Y)

	} else {

		fmt.Println("Could not identify a distinct knee.")

	}



	if *f.ReportFile != "" {

		writeReport(*f.ReportFile, history)

	}

}



// runRemoteCmd handles "jolt remote [optimize|sweep] ..."



func runRemoteCmd() {



	if len(os.Args) < 3 {



		fmt.Println("Usage: jolt remote <command> [flags]")



		os.Exit(1)



	}



	subCmd := os.Args[2]



	



	fs := flag.NewFlagSet("remote "+subCmd, flag.ExitOnError)



	f := SetupFlags(fs)



	nodesFlag := fs.String("nodes", "", "Alias for -jolt-nodes (deprecated)")



	joltNodesFlag := fs.String("jolt-nodes", "", "Comma-separated list of jolt agent nodes (e.g. host1:9000)")



	fioNodesFlag := fs.String("fio-nodes", "", "Comma-separated list of fio server nodes (e.g. host1)")



	fs.Parse(os.Args[3:])



	



	joltNodes := []string{}



	if *joltNodesFlag != "" {



		joltNodes = strings.Split(*joltNodesFlag, ",")



	} else if *nodesFlag != "" {



		joltNodes = strings.Split(*nodesFlag, ",")



	}







	fioNodes := []string{}



	if *fioNodesFlag != "" {



		fioNodes = strings.Split(*fioNodesFlag, ",")



	}







	if len(joltNodes) == 0 && len(fioNodes) == 0 {



		fmt.Println("Error: at least one of -jolt-nodes or -fio-nodes is required")



		os.Exit(1)



	}



	



	// In remote mode, path might be handled by agents. Inject dummy if missing to satisfy Config validation.



	if *f.Path == "" && *f.ConfigFile == "" {



		dummy := "REMOTE_MANAGED"



		f.Path = &dummy



	}







	cfg, err := f.LoadConfig()



	if err != nil {



		fmt.Printf("Error: %v\n", err)



		os.Exit(1)



	}



	f.MaybeWriteConfig(cfg)



	



	fmt.Printf("Initializing Cluster Engine with %d Jolt nodes and %d FIO nodes...\n", len(joltNodes), len(fioNodes))



	eng := cluster.New(joltNodes, fioNodes)



	



	switch subCmd {



	case "optimize":



		runOptimizeLogic(f, cfg, eng)



	case "sweep":



		runSweepLogic(f, cfg, eng)



	default:



		fmt.Printf("Unknown remote command '%s'. Use 'optimize' or 'sweep'.\n", subCmd)



        os.Exit(1)



	}



}







func runAgentCmd() {



	agentCmd := flag.NewFlagSet("agent", flag.ExitOnError)



	port := agentCmd.Int("port", 9000, "Port to listen on")



	path := agentCmd.String("path", "", "Target device/file path (overrides remote request)")



	agentCmd.Parse(os.Args[2:])







		srv := agent.NewServer("sync", *path) 







		







		if err := srv.VerifyAccess(); err != nil {







			fmt.Printf("Agent Startup Error: %v\n", err)







			os.Exit(1)







		}







	







		if err := srv.ListenAndServe(*port); err != nil {



		fmt.Printf("Agent failed: %v\n", err)



		os.Exit(1)



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
