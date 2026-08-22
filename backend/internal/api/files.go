package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FileHandler handles file CRUD operations on the workspace.
type FileHandler struct {
	workDir string
}

func NewFileHandler(workDir string) *FileHandler {
	return &FileHandler{workDir: workDir}
}

// FileNode represents a file or directory in the tree.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Children []FileNode `json:"children,omitempty"`
}

// ListTree returns the full file tree of the workspace.
func (h *FileHandler) ListTree(w http.ResponseWriter, r *http.Request) {
	tree, err := buildTree(h.workDir, "")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to list files: %v", err)
		return
	}
	jsonResponse(w, tree)
}

func buildTree(root, rel string) ([]FileNode, error) {
	absPath := filepath.Join(root, rel)
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	var nodes []FileNode
	for _, e := range entries {
		// Skip hidden files and directories
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		node := FileNode{
			Name:  e.Name(),
			Path:  filepath.Join(rel, e.Name()),
			IsDir: e.IsDir(),
		}
		if e.IsDir() {
			children, err := buildTree(root, node.Path)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ReadFile returns the content of a file.
func (h *FileHandler) ReadFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		httpError(w, http.StatusBadRequest, "missing path parameter")
		return
	}

	absPath, err := h.safePath(path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		httpError(w, http.StatusBadRequest, "path is a directory")
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "cannot read file: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

// WriteFile creates or overwrites a file.
func (h *FileHandler) WriteFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		httpError(w, http.StatusBadRequest, "missing path parameter")
		return
	}

	absPath, err := h.safePath(path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot create parent dir: %v", err)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "cannot read body: %v", err)
		return
	}

	if err := os.WriteFile(absPath, body, 0644); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot write file: %v", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	jsonResponse(w, map[string]string{"status": "ok", "path": path})
}

// DeleteFile removes a file or empty directory.
func (h *FileHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		httpError(w, http.StatusBadRequest, "missing path parameter")
		return
	}

	absPath, err := h.safePath(path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	if err := os.RemoveAll(absPath); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot delete: %v", err)
		return
	}

	jsonResponse(w, map[string]string{"status": "ok"})
}

// MkDir creates a directory.
func (h *FileHandler) MkDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		httpError(w, http.StatusBadRequest, "missing path in body")
		return
	}

	absPath, err := h.safePath(req.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	if err := os.MkdirAll(absPath, 0755); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot create directory: %v", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "ok", "path": req.Path})
}

// Rename renames/moves a file or directory.
func (h *FileHandler) Rename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.From == "" || req.To == "" {
		httpError(w, http.StatusBadRequest, "missing from/to in body")
		return
	}

	absFrom, err := h.safePath(req.From)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}
	absTo, err := h.safePath(req.To)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)
		return
	}

	// Ensure target parent exists
	if err := os.MkdirAll(filepath.Dir(absTo), 0755); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot create target dir: %v", err)
		return
	}

	if err := os.Rename(absFrom, absTo); err != nil {
		httpError(w, http.StatusInternalServerError, "cannot rename: %v", err)
		return
	}

	jsonResponse(w, map[string]string{"status": "ok", "from": req.From, "to": req.To})
}

// safePath validates and resolves a relative path within the workspace.
func (h *FileHandler) safePath(relPath string) (string, error) {
	// Clean the path to prevent traversal
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	abs := filepath.Join(h.workDir, cleaned)
	// Double check it's still within workDir
	if !strings.HasPrefix(abs, filepath.Clean(h.workDir)) {
		return "", fmt.Errorf("path escapes workspace: %s", relPath)
	}
	return abs, nil
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, code int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, args...)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
