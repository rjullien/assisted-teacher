package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGetProgramme_ValidNiveau(t *testing.T) {
	dir := t.TempDir()
	content := `{"niveau":"Seconde"}`
	os.WriteFile(filepath.Join(dir, "seconde.json"), []byte(content), 0644)

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=seconde", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["niveau"] != "Seconde" {
		t.Fatalf("expected niveau=Seconde, got %q", result["niveau"])
	}
}

func TestGetProgramme_MissingNiveau(t *testing.T) {
	dir := t.TempDir()

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestGetProgramme_InvalidNiveau(t *testing.T) {
	dir := t.TempDir()

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=invalid", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestGetProgramme_FileNotFound(t *testing.T) {
	dir := t.TempDir()

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=terminale", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestGetProgramme_LangEN(t *testing.T) {
	dir := t.TempDir()
	frContent := `{"niveau":"Seconde","lang":"fr"}`
	enContent := `{"niveau":"Seconde","lang":"en"}`
	os.WriteFile(filepath.Join(dir, "seconde.json"), []byte(frContent), 0644)
	os.WriteFile(filepath.Join(dir, "seconde.en.json"), []byte(enContent), 0644)

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=seconde&lang=en", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["lang"] != "en" {
		t.Fatalf("expected lang=en, got %q", result["lang"])
	}
}

func TestGetProgramme_LangEN_FallbackFR(t *testing.T) {
	dir := t.TempDir()
	frContent := `{"niveau":"Seconde","lang":"fr"}`
	os.WriteFile(filepath.Join(dir, "seconde.json"), []byte(frContent), 0644)

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=seconde&lang=en", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["lang"] != "fr" {
		t.Fatalf("expected fallback to FR content, got lang=%q", result["lang"])
	}
}

func TestGetProgramme_DotProgrammesFallback(t *testing.T) {
	dir := t.TempDir()
	progDir := filepath.Join(dir, ".programmes")
	os.MkdirAll(progDir, 0755)
	content := `{"niveau":"Seconde","source":"dotprogrammes"}`
	os.WriteFile(filepath.Join(progDir, "seconde.json"), []byte(content), 0644)

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programme?niveau=seconde", nil)
	w := httptest.NewRecorder()

	handler.GetProgramme(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["source"] != "dotprogrammes" {
		t.Fatalf("expected source=dotprogrammes, got %q", result["source"])
	}
}

func TestListProgrammes(t *testing.T) {
	dir := t.TempDir()

	handler := NewProgrammeHandler(dir)
	req := httptest.NewRequest("GET", "/api/programmes", nil)
	w := httptest.NewRecorder()

	handler.ListProgrammes(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	type niveauInfo struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	var niveaux []niveauInfo
	if err := json.NewDecoder(resp.Body).Decode(&niveaux); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(niveaux) != 3 {
		t.Fatalf("expected 3 niveaux, got %d", len(niveaux))
	}

	expectedIDs := map[string]bool{"seconde": true, "premiere": true, "terminale": true}
	for _, n := range niveaux {
		if !expectedIDs[n.ID] {
			t.Errorf("unexpected niveau ID: %s", n.ID)
		}
	}
}
