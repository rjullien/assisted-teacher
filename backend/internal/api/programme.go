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
	"seconde":   true,
	"premiere":  true,
	"terminale": true,
}

// GetProgramme serves GET /api/programme?niveau=seconde|premiere|terminale&lang=fr|en
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

	// Determine language: default to "fr"
	lang := r.URL.Query().Get("lang")
	if lang != "en" {
		lang = "fr"
	}

	// Build filename: seconde.json (fr) or seconde.en.json (en)
	var filename string
	if lang == "en" {
		filename = niveau + ".en.json"
	} else {
		filename = niveau + ".json"
	}

	// Try direct path first (PROGRAMMES_DIR points directly to the folder with JSON files)
	filePath := filepath.Join(h.workDir, filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		// Fallback: try .programmes/ subdirectory (legacy layout where workDir is the workspace root)
		filePath = filepath.Join(h.workDir, ".programmes", filename)
		content, err = os.ReadFile(filePath)
		if err != nil {
			// If EN not found, fallback to FR
			if lang == "en" {
				frFilename := niveau + ".json"
				filePath = filepath.Join(h.workDir, frFilename)
				content, err = os.ReadFile(filePath)
				if err != nil {
					filePath = filepath.Join(h.workDir, ".programmes", frFilename)
					content, err = os.ReadFile(filePath)
				}
			}
			if err != nil {
				httpError(w, http.StatusNotFound, "programme file not found for niveau: %s (lang: %s)", niveau, lang)
				return
			}
		}
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
