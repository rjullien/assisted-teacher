package export

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupExportWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "B1"), 0755)
	os.WriteFile(filepath.Join(dir, "B1", "unit5.md"), []byte("# Unit 5\n\nPast Perfect exercises."), 0644)
	return dir
}

func TestExportPDF_MissingPath(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest("POST", "/api/export/pdf", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportPDF(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestExportPDF_FileNotFound(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "nonexistent.md"})
	req := httptest.NewRequest("POST", "/api/export/pdf", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportPDF(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestExportPDF_PathTraversal(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "../../etc/passwd"})
	req := httptest.NewRequest("POST", "/api/export/pdf", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportPDF(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestExportPDF_WithPandoc(t *testing.T) {
	// Skip if pandoc is not installed
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not installed, skipping integration test")
	}
	// Skip if typst is not installed (pandoc needs it as pdf-engine)
	if _, err := exec.LookPath("typst"); err != nil {
		// Try without typst — pandoc might still work with other engines
		t.Skip("typst not installed, skipping PDF integration test")
	}

	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "B1/unit5.md"})
	req := httptest.NewRequest("POST", "/api/export/pdf", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportPDF(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Result().StatusCode, w.Body.String())
	}

	// Check PDF header
	if ct := w.Result().Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("expected content-type application/pdf, got %s", ct)
	}
}

func TestExportDOCX_MissingPath(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest("POST", "/api/export/docx", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportDOCX(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestExportDOCX_FileNotFound(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "nope.md"})
	req := httptest.NewRequest("POST", "/api/export/docx", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportDOCX(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestExportDOCX_PathTraversal(t *testing.T) {
	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "../../../etc/shadow"})
	req := httptest.NewRequest("POST", "/api/export/docx", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportDOCX(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestExportDOCX_WithPandoc(t *testing.T) {
	// Skip if pandoc is not installed
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not installed, skipping integration test")
	}

	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	body, _ := json.Marshal(map[string]string{"path": "B1/unit5.md"})
	req := httptest.NewRequest("POST", "/api/export/docx", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportDOCX(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Result().StatusCode, w.Body.String())
	}

	ct := w.Result().Header.Get("Content-Type")
	if ct != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("unexpected content-type: %s", ct)
	}

	// DOCX files start with PK (ZIP format)
	body2 := w.Body.Bytes()
	if len(body2) < 4 || string(body2[:2]) != "PK" {
		t.Error("output doesn't look like a valid DOCX (ZIP) file")
	}
}

func TestExportDOCX_WithReferenceDoc(t *testing.T) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not installed")
	}

	dir := setupExportWorkspace(t)
	handler := NewHandler(dir)

	// Create a fake reference.docx (pandoc accepts any docx as template)
	// We'll skip this test if we can't create a valid one
	templateDir := filepath.Join(dir, ".templates")
	os.MkdirAll(templateDir, 0755)
	// We won't create a real .docx here — just verify the code path doesn't crash
	// when the template doesn't exist (it falls through to no-template)

	body, _ := json.Marshal(map[string]string{"path": "B1/unit5.md"})
	req := httptest.NewRequest("POST", "/api/export/docx", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ExportDOCX(w, req)

	// Should succeed even without reference doc
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}
}
