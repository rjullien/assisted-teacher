package api

import (
	"net/http"
	"os"
	"path/filepath"
)

// ProgrammeHandler serves official curriculum JSON files from workspace/.programmes/.
type ProgrammeHandler struct {
	workDir string
}

func NewProgrammeHandler(workDir string) *ProgrammeHandler {
	return &ProgrammeHandler{workDir: workDir}
}

// validNiveaux restricts which files can be served.
var validNiveaux = map[string]bool{
	"seconde":  true,
	"premiere": true,
	"terminale": true,
}

// GetProgramme serves GET /api/programme?niveau=seconde|premiere|terminale
func (h *ProgrammeHandler) GetProgramme(w http.ResponseWriter, r *http.Request) {
	niveau := r.URL.Query().Get("niveau")
	if niveau == "" {
		httpError(w, http.StatusBadRequest, "missing 'niveau' query parameter (seconde|premiere|terminale)")
		return
	}

	if !validNiveaux[niveau] {
		httpError(w, http.StatusBadRequest, "invalid niveau: %s (must be seconde, premiere or terminale)", niveau)
		return
	}

	filePath := filepath.Join(h.workDir, ".programmes", niveau+".json")
	content, err := os.ReadFile(filePath)
	if err != nil {
		httpError(w, http.StatusNotFound, "programme file not found for niveau: %s", niveau)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(content)
}

// ListProgrammes serves GET /api/programmes — returns the list of available niveaux.
func (h *ProgrammeHandler) ListProgrammes(w http.ResponseWriter, r *http.Request) {
	type niveauInfo struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	niveaux := []niveauInfo{
		{ID: "seconde", Label: "Seconde"},
		{ID: "premiere", Label: "Première"},
		{ID: "terminale", Label: "Terminale"},
	}
	jsonResponse(w, niveaux)
}
