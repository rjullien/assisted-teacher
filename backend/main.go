package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/api"
	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/bridge"
	"github.com/rjullien/opencode-usage-tracker/cours-ia/backend/internal/export"
)

//go:embed static/*
var staticFS embed.FS

func main() {
	port := flag.String("port", envOr("PORT", "9847"), "HTTP port")
	workDir := flag.String("workdir", envOr("WORKSPACE_DIR", "./workspace"), "Workspace directory for course files")
	agentCmd := flag.String("agent", envOr("ACP_AGENT_CMD", ""), "ACP agent command (e.g. 'opencode-ai acp' or 'openclaw acp')")
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

	// ACP WebSocket bridge
	if *agentCmd != "" {
		acpBridge := bridge.NewACPBridge(*agentCmd, *workDir)
		mux.HandleFunc("/ws/acp", acpBridge.HandleWebSocket)
		log.Printf("ACP agent: %s", *agentCmd)
	} else {
		log.Println("WARNING: No ACP agent configured (set ACP_AGENT_CMD)")
	}

	// Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Serve frontend SPA (embedded or from disk)
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("cannot access embedded static: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle("/", spaHandler(fileServer))

	addr := fmt.Sprintf(":%s", *port)
	log.Printf("cours-ia server listening on %s (workspace: %s)", addr, *workDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// spaHandler serves static files, falling back to index.html for SPA routing
func spaHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't intercept API or WS routes
		if len(r.URL.Path) > 4 && (r.URL.Path[:4] == "/api" || r.URL.Path[:3] == "/ws") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
