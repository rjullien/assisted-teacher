package export

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Handler manages document export (Markdown → PDF via Typst, Markdown → DOCX via Pandoc).
type Handler struct {
	workDir string
}

func NewHandler(workDir string) *Handler {
	return &Handler{workDir: workDir}
}

type exportRequest struct {
	Path string `json:"path"` // relative path to the .md file
}

// ExportPDF converts a Markdown file to PDF using Pandoc + Typst engine.
func (h *Handler) ExportPDF(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		httpError(w, http.StatusBadRequest, "missing path")
		return
	}

	absPath, err := h.safePath(req.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	if _, err := os.Stat(absPath); err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}

	// Output path: same name, .pdf extension, in exports/ dir
	baseName := strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
	exportDir := filepath.Join(h.workDir, ".exports")
	os.MkdirAll(exportDir, 0755)
	outPath := filepath.Join(exportDir, baseName+".pdf")

	// Try pandoc with typst engine first, fallback to basic pandoc
	cmd := exec.Command("pandoc", absPath, "-o", outPath, "--pdf-engine=typst")
	cmd.Dir = h.workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		// Fallback: try typst directly if a .typ wrapper exists
		typstPath := filepath.Join(h.workDir, ".exports", baseName+".typ")
		if _, statErr := os.Stat(typstPath); statErr == nil {
			cmd2 := exec.Command("typst", "compile", typstPath, outPath)
			cmd2.Dir = h.workDir
			if output2, err2 := cmd2.CombinedOutput(); err2 != nil {
				httpError(w, http.StatusInternalServerError, "typst failed: %s", string(output2))
				return
			}
		} else {
			httpError(w, http.StatusInternalServerError, "pandoc failed: %s", string(output))
			return
		}
	}

	// Serve the generated PDF
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.pdf"`, baseName))
	http.ServeFile(w, r, outPath)
}

// ExportDOCX converts a Markdown file to DOCX using Pandoc.
func (h *Handler) ExportDOCX(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		httpError(w, http.StatusBadRequest, "missing path")
		return
	}

	absPath, err := h.safePath(req.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	if _, err := os.Stat(absPath); err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}

	baseName := strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
	exportDir := filepath.Join(h.workDir, ".exports")
	os.MkdirAll(exportDir, 0755)
	outPath := filepath.Join(exportDir, baseName+".docx")

	// Check for reference doc template
	args := []string{absPath, "-o", outPath}
	refDoc := filepath.Join(h.workDir, ".templates", "reference.docx")
	if _, err := os.Stat(refDoc); err == nil {
		args = append(args, "--reference-doc="+refDoc)
	}

	cmd := exec.Command("pandoc", args...)
	cmd.Dir = h.workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		httpError(w, http.StatusInternalServerError, "pandoc failed: %s", string(output))
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.docx"`, baseName))
	http.ServeFile(w, r, outPath)
}

func (h *Handler) safePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	abs := filepath.Join(h.workDir, cleaned)
	if !strings.HasPrefix(abs, filepath.Clean(h.workDir)) {
		return "", fmt.Errorf("path escapes workspace: %s", relPath)
	}
	return abs, nil
}

func httpError(w http.ResponseWriter, code int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, args...)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
