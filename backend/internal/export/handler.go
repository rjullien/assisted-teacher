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

// ExportPDF converts a Markdown file to PDF via pandoc → typst → PDF.
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

	baseName := strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
	exportDir := filepath.Join(h.workDir, ".exports")
	os.MkdirAll(exportDir, 0755)
	typPath := filepath.Join(exportDir, baseName+".typ")
	outPath := filepath.Join(exportDir, baseName+".pdf")

	// Step 1: Convert Markdown → Typst format via pandoc
	cmd1 := exec.Command("pandoc", absPath, "-t", "typst", "-o", typPath)
	cmd1.Dir = h.workDir
	if output, err := cmd1.CombinedOutput(); err != nil {
		httpError(w, http.StatusInternalServerError, "pandoc md→typst failed: %s", string(output))
		return
	}

	// Step 2: Compile Typst → PDF
	cmd2 := exec.Command("typst", "compile", typPath, outPath)
	cmd2.Dir = h.workDir
	if output, err := cmd2.CombinedOutput(); err != nil {
		httpError(w, http.StatusInternalServerError, "typst compile failed: %s", string(output))
		return
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
