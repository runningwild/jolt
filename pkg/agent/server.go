package agent

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/runningwild/jolt/pkg/engine"
)

type Server struct {
	eng  engine.Engine
	path string
}

func NewServer(engType string, path string) *Server {
	return &Server{
		eng:  engine.New(engType),
		path: path,
	}
}

func (s *Server) ListenAndServe(port int) error {
	http.HandleFunc("/run", s.handleRun)
	http.HandleFunc("/health", s.handleHealth)
	
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Jolt Agent listening on %s (Engine: default)\n", addr)
	return http.ListenAndServe(addr, nil)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var params engine.Params
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, fmt.Sprintf("Invalid body: %v", err), http.StatusBadRequest)
		return
	}

	// Override path if configured on the agent
	if s.path != "" {
		params.Path = s.path
	}

	// For the agent, we might want to allow overriding the engine type per request,
	// or stick to the one initialized. 
	// Currently engine.New returns an interface. 
	// The params contain 'EngineType'. We should probably respect that if possible,
	// OR (simpler for now) just use the pre-initialized engine or create a new one per request?
	// Creating a new engine per request is safer if the engine holds state (uring ring).
	// Engine.New() is cheap for 'sync', slightly heavier for 'uring' (ring allocation).
	// But 'uring' needs to be closed. The current engine.Run() usually handles setup/teardown 
	// internally or relies on the struct.
	
	// Let's create a fresh engine for the request to be safe and stateless.
	eng := engine.New(params.EngineType)
	
	res, err := eng.Run(params)
	if err != nil {
		// If the test failed (e.g. disk error), we return 200 OK but with error in JSON?
		// Or 500?
		// Better to return 500 so the controller knows something went wrong at the system level.
		http.Error(w, fmt.Sprintf("Engine execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}
