package main

import (
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
	hermesURL := flag.String("hermes-url", envOr("HERMES_URL", "http://hermes-lya.openclaw.svc.cluster.local:8642"), "Hermes API server URL")
	hermesKey := flag.String("hermes-key", strings.TrimSpace(envOr("HERMES_API_KEY", "")), "Hermes API server key")
	flag.Parse()

	// Ensure workspace exists
	if err := os.MkdirAll(*workDir, 0755); err != nil {
		log.Fatalf("cannot create workspace dir: %v", err)
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

	// Export API
	exportHandler := export.NewHandler(*workDir)
	mux.HandleFunc("POST /api/export/pdf", exportHandler.ExportPDF)
	mux.HandleFunc("POST /api/export/docx", exportHandler.ExportDOCX)

	// Hermes bridge (connects to Lya via HTTP Runs API — reconnectable, no timeout issues)
	if *hermesKey != "" {
		hermesBridge := bridge.NewHermesBridge(*hermesURL, *hermesKey)
		mux.HandleFunc("/ws/acp", hermesBridge.HandleWebSocket)
		log.Printf("Hermes bridge: %s", *hermesURL)
	} else {
		log.Println("WARNING: No HERMES_API_KEY set — chat disabled")
	}

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
