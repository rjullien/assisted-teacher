package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setupTestWorkspace creates a temporary workspace with sample files.
func setupTestWorkspace(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()

	// Create sample structure
	os.MkdirAll(filepath.Join(dir, "B1"), 0755)
	os.MkdirAll(filepath.Join(dir, "Vocab"), 0755)
	os.WriteFile(filepath.Join(dir, "B1", "unit5.md"), []byte("# Unit 5\n\nContent here."), 0644)
	os.WriteFile(filepath.Join(dir, "B1", "unit6.md"), []byte("# Unit 6\n\nMore content."), 0644)
	os.WriteFile(filepath.Join(dir, "Vocab", "animals.md"), []byte("# Animals\n\nDog, cat."), 0644)

	return dir, func() {} // t.TempDir() auto-cleans
}

func TestListTree(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()

	handler.ListTree(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tree []FileNode
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have 2 top-level dirs
	if len(tree) != 2 {
		t.Fatalf("expected 2 top-level entries, got %d", len(tree))
	}

	// Find B1 dir
	var b1 *FileNode
	for i := range tree {
		if tree[i].Name == "B1" {
			b1 = &tree[i]
			break
		}
	}
	if b1 == nil {
		t.Fatal("B1 directory not found in tree")
	}
	if !b1.IsDir {
		t.Fatal("B1 should be a directory")
	}
	if len(b1.Children) != 2 {
		t.Fatalf("expected 2 files in B1, got %d", len(b1.Children))
	}
}

func TestListTree_HiddenFilesExcluded(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	// Create hidden file and dir
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0644)
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()

	handler.ListTree(w, req)

	var tree []FileNode
	json.NewDecoder(w.Result().Body).Decode(&tree)

	for _, node := range tree {
		if node.Name[0] == '.' {
			t.Errorf("hidden file/dir should be excluded: %s", node.Name)
		}
	}
}

func TestReadFile(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/file?path=B1/unit5.md", nil)
	w := httptest.NewRecorder()

	handler.ReadFile(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "# Unit 5\n\nContent here." {
		t.Fatalf("unexpected content: %q", string(body))
	}
}

func TestReadFile_NotFound(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/file?path=nonexistent.md", nil)
	w := httptest.NewRecorder()

	handler.ReadFile(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestReadFile_Directory(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/file?path=B1", nil)
	w := httptest.NewRecorder()

	handler.ReadFile(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for directory, got %d", w.Result().StatusCode)
	}
}

func TestReadFile_MissingPath(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("GET", "/api/file", nil)
	w := httptest.NewRecorder()

	handler.ReadFile(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestWriteFile_New(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body := "# New Course\n\nHello world."
	req := httptest.NewRequest("PUT", "/api/file?path=B1/unit7.md", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler.WriteFile(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	// Verify file exists on disk
	content, err := os.ReadFile(filepath.Join(dir, "B1", "unit7.md"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(content) != body {
		t.Fatalf("content mismatch: got %q", string(content))
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body := "# Deep file"
	req := httptest.NewRequest("PUT", "/api/file?path=deep/nested/dir/file.md", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler.WriteFile(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	content, err := os.ReadFile(filepath.Join(dir, "deep", "nested", "dir", "file.md"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(content) != body {
		t.Fatalf("content mismatch")
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body := "# Updated Unit 5"
	req := httptest.NewRequest("PUT", "/api/file?path=B1/unit5.md", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler.WriteFile(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	content, _ := os.ReadFile(filepath.Join(dir, "B1", "unit5.md"))
	if string(content) != body {
		t.Fatalf("content not updated")
	}
}

func TestDeleteFile(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("DELETE", "/api/file?path=B1/unit6.md", nil)
	w := httptest.NewRecorder()

	handler.DeleteFile(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dir, "B1", "unit6.md")); !os.IsNotExist(err) {
		t.Fatal("file should have been deleted")
	}
}

func TestDeleteFile_Directory(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("DELETE", "/api/file?path=Vocab", nil)
	w := httptest.NewRecorder()

	handler.DeleteFile(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dir, "Vocab")); !os.IsNotExist(err) {
		t.Fatal("directory should have been deleted")
	}
}

func TestMkDir(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body, _ := json.Marshal(map[string]string{"path": "B2/grammar"})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.MkDir(w, req)

	if w.Result().StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Result().StatusCode)
	}

	info, err := os.Stat(filepath.Join(dir, "B2", "grammar"))
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected a directory")
	}
}

func TestRename(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body, _ := json.Marshal(map[string]string{
		"from": "B1/unit5.md",
		"to":   "B1/unit5-renamed.md",
	})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Rename(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dir, "B1", "unit5.md")); !os.IsNotExist(err) {
		t.Fatal("old file should not exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "B1", "unit5-renamed.md")); err != nil {
		t.Fatal("new file should exist")
	}
}

// --- Security: path traversal tests ---

func TestReadFile_PathTraversal(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	// Create a file outside workspace
	os.WriteFile(filepath.Join(dir, "..", "secret.txt"), []byte("top secret"), 0644)

	handler := NewFileHandler(dir)

	cases := []string{
		"../secret.txt",
		"../../etc/passwd",
		"B1/../../secret.txt",
		"/etc/passwd",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/file?path="+path, nil)
			w := httptest.NewRecorder()
			handler.ReadFile(w, req)
			if w.Result().StatusCode != http.StatusBadRequest {
				t.Errorf("path %q: expected 400, got %d", path, w.Result().StatusCode)
			}
		})
	}
}

func TestWriteFile_PathTraversal(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)

	cases := []string{
		"../escape.md",
		"../../etc/crontab",
		"/tmp/evil.md",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/api/file?path="+path, bytes.NewBufferString("evil"))
			w := httptest.NewRecorder()
			handler.WriteFile(w, req)
			if w.Result().StatusCode != http.StatusBadRequest {
				t.Errorf("path %q: expected 400, got %d", path, w.Result().StatusCode)
			}
		})
	}
}

func TestDeleteFile_PathTraversal(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	req := httptest.NewRequest("DELETE", "/api/file?path=../something", nil)
	w := httptest.NewRecorder()

	handler.DeleteFile(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestMkDir_PathTraversal(t *testing.T) {
	dir, cleanup := setupTestWorkspace(t)
	defer cleanup()

	handler := NewFileHandler(dir)
	body, _ := json.Marshal(map[string]string{"path": "../../evil"})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.MkDir(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}
