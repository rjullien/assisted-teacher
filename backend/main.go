package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/api"
	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/bridge"
	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/export"
)

func main() {
	port := flag.String("port", envOr("PORT", "9847"), "HTTP port")
	workDir := flag.String("workdir", envOr("WORKSPACE_DIR", "./workspace"), "Workspace directory for course files")
	programmesDir := flag.String("programmes-dir", envOr("PROGRAMMES_DIR", ""), "Directory for programme JSON files (defaults to WORKSPACE_DIR/.programmes)")
	hermesURL := flag.String("hermes-url", envOr("HERMES_URL", "http://hermes-lya.openclaw.svc.cluster.local:8642"), "Hermes API server URL")
	hermesKey := flag.String("hermes-key", strings.TrimSpace(envOr("HERMES_API_KEY", "")), "Hermes API server key")
	flag.Parse()

	// Ensure workspace exists
	if err := os.MkdirAll(*workDir, 0755); err != nil {
		log.Fatalf("cannot create workspace dir: %v", err)
	}

	// Determine programmes directory
	progDir := *programmesDir
	if progDir == "" {
		progDir = *workDir // fallback: handler will look in workDir/.programmes/
	}

	mux := http.NewServeMux()

	// File API
	fileAPI := api.NewFileHandler(*workDir)
	mux.HandleFunc("GET /api/files", fileAPI.ListTree)
	mux.HandleFunc("GET /api/file", fileAPI.ReadFile)
	mux.HandleFunc("PUT /api/file", fileAPI.WriteFile)
	mux.HandleFunc("DELETE /api/file", fileAPI.DeleteFile)
	mux.HandleFunc("POST /api/files/mkdir", fileAPI.MkDir)
	mux.HandleFunc("POST /api/files/rename", fileAPI.Rename)

	// Programme API (official curriculum data)
	progAPI := api.NewProgrammeHandler(progDir)
	mux.HandleFunc("GET /api/programmes", progAPI.ListProgrammes)
	mux.HandleFunc("GET /api/programme", progAPI.GetProgramme)

	// Export API
	exportHandler := export.NewHandler(*workDir)
	mux.HandleFunc("POST /api/export/pdf", exportHandler.ExportPDF)
	mux.HandleFunc("POST /api/export/docx", exportHandler.ExportDOCX)

	// User info (from Authelia forward-auth headers)
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		name := r.Header.Get("Remote-Name")
		user := r.Header.Get("Remote-User")
		email := r.Header.Get("Remote-Email")
		// Use display name if available, fallback to username
		displayName := name
		if displayName == "" {
			displayName = user
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintf(w, `{"name":%q,"user":%q,"email":%q}`, displayName, user, email)
	})

	// Hermes bridge (connects to Lya via HTTP Runs API — reconnectable, no timeout issues)
	hermesEnabled := *hermesKey != ""
	if hermesEnabled {
		hermesBridge := bridge.NewHermesBridge(*hermesURL, *hermesKey)
		mux.HandleFunc("/ws/acp", hermesBridge.HandleWebSocket)
		log.Printf("Hermes bridge: %s", *hermesURL)
	} else {
		log.Println("WARNING: No HERMES_API_KEY set — chat disabled")
	}

	// Pi bridge (agent that can read/write course files)
	piEnabled := envOr("PI_ENABLED", "") == "true"
	if piEnabled {
		piCmd := envOr("PI_CMD", "pi")
		piModels := envOr("PI_MODELS_JSON", "")
		piBridge := bridge.NewPiBridge(piCmd, *workDir, piModels)
		mux.HandleFunc("/ws/agent/pi", piBridge.HandleWebSocket)
		log.Printf("Pi bridge: cmd=%s, workDir=%s", piCmd, *workDir)
	}

	// Agent discovery (frontend hides pi button when not available)
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		type agentInfo struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Default bool   `json:"default"`
		}
		agents := []agentInfo{}
		if hermesEnabled {
			agents = append(agents, agentInfo{ID: "lya", Label: "Lya", Default: true})
		}
		if piEnabled {
			agents = append(agents, agentInfo{ID: "pi", Label: "Pi", Default: !hermesEnabled})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(agents)
	})

	// Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	addr := fmt.Sprintf(":%s", *port)
	log.Printf("assisted-teacher backend listening on %s (workspace: %s)", addr, *workDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
