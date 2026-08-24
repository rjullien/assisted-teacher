// Package bridge connects the frontend to Hermes (Lya) via the OpenAI-compatible
// streaming API. Uses the same detached-job pattern as TripKit:
//
//   - Frontend sends a prompt → backend starts a Job (background goroutine)
//   - Job calls Hermes POST /v1/chat/completions with stream:true
//   - Job stores ALL SSE events in an append-only log
//   - Frontend subscribes via WebSocket — disconnect/reconnect replays backlog
//   - Frontend disconnect does NOT cancel Hermes — the Job keeps running
//
// This survives Cloudflare 100s timeout: if the WS drops, the frontend reconnects
// and gets the full backlog + live events from where it left off.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// keyFingerprint returns the first 8 hex chars of SHA-256(key).
// Safe to log (non-reversible) and enough to prove whether two sides hold the
// same secret. Compare with, on the cluster:
//
//	kubectl get secret hermes-lya-secret -n openclaw \
//	  -o jsonpath='{.data.API_SERVER_KEY}' | base64 -d | tr -d '\n' | sha256sum
func keyFingerprint(key string) string {
	if key == "" {
		return "empty"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// --- Job infrastructure (same pattern as tripkit-backend/internal/leo/job.go) ---

type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobError   JobStatus = "error"
)

type StreamEvent struct {
	Seq    int    `json:"seq"`
	Type   string `json:"type"`             // delta | tool | done | error | meta
	Text   string `json:"text,omitempty"`   // for delta
	Reply  string `json:"reply,omitempty"`  // full accumulated text (on done)
	Error  string `json:"error,omitempty"`  // for error
	Detail string `json:"detail,omitempty"` // technical detail
	Tool   any    `json:"tool,omitempty"`   // for tool progress
	JobID  string `json:"jobId,omitempty"`
}

type Job struct {
	ID        string
	CreatedAt time.Time

	mu     sync.Mutex
	status JobStatus
	events []StreamEvent
	subs   []chan StreamEvent
	cancel context.CancelFunc
	doneCh chan struct{}
}

func (j *Job) append(ev StreamEvent) {
	j.mu.Lock()
	ev.Seq = len(j.events) + 1
	ev.JobID = j.ID
	j.events = append(j.events, ev)
	if ev.Type == "done" {
		j.status = JobDone
	}
	if ev.Type == "error" {
		j.status = JobError
	}
	subs := append([]chan StreamEvent(nil), j.subs...)
	j.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub <- ev:
		default: // slow subscriber — they'll catch up on reconnect
		}
	}
}

func (j *Job) Subscribe(after int) (backlog []StreamEvent, live <-chan StreamEvent, unsub func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ev := range j.events {
		if ev.Seq > after {
			backlog = append(backlog, ev)
		}
	}
	ch := make(chan StreamEvent, 256)
	j.subs = append(j.subs, ch)
	var once sync.Once
	unsub = func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			out := j.subs[:0]
			for _, s := range j.subs {
				if s != ch {
					out = append(out, s)
				}
			}
			j.subs = out
		})
	}
	return backlog, ch, unsub
}

// --- Hub (in-memory job store) ---

const (
	jobTTL        = 15 * time.Minute
	jobRunTimeout = 10 * time.Minute
)

type Hub struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func NewHub() *Hub {
	return &Hub{jobs: map[string]*Job{}}
}

func (h *Hub) Get(id string) *Job {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.jobs[id]
}

func (h *Hub) gc() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-jobTTL)
	for id, j := range h.jobs {
		j.mu.Lock()
		done := j.status != JobRunning
		created := j.CreatedAt
		j.mu.Unlock()
		if done && created.Before(cutoff) {
			delete(h.jobs, id)
		}
	}
}

// --- HermesBridge ---

type HermesBridge struct {
	hermesURL string
	apiKey    string
	workDir   string
	hub       *Hub
	upgrader  websocket.Upgrader
}

func NewHermesBridge(hermesURL, apiKey, workDir string) *HermesBridge {
	// TrimSpace is critical: Infisical/K8s secrets often carry a trailing \n,
	// which makes the Bearer token mismatch on the gateway side.
	key := strings.TrimSpace(apiKey)
	if key != apiKey {
		log.Printf("hermes: API key had %d surrounding whitespace byte(s) — trimmed", len(apiKey)-len(key))
	}
	log.Printf("hermes: bridge configured (url=%s, keyLen=%d, keyFp=%s, workDir=%s)",
		hermesURL, len(key), keyFingerprint(key), workDir)
	return &HermesBridge{
		hermesURL: strings.TrimRight(strings.TrimSpace(hermesURL), "/"),
		apiKey:    key,
		workDir:   workDir,
		hub:       NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// safePath validates and resolves a relative path within the workspace.
// Returns ("", error) if the path escapes the workspace.
func (b *HermesBridge) safePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	abs := filepath.Join(b.workDir, cleaned)
	if !strings.HasPrefix(abs, filepath.Clean(b.workDir)) {
		return "", fmt.Errorf("path escapes workspace: %s", relPath)
	}
	return abs, nil
}

// --- Local file tools (OpenAI-style function calling, mode Desk only) ---

const (
	// maxToolLoops caps the number of Hermes round-trips per job. Without a cap,
	// a model that keeps asking for the same tool would keep the job (and the
	// teacher's chat) spinning until jobRunTimeout.
	maxToolLoops = 8
	// maxReadFileChars mirrors MAX_INLINED_FILE_CHARS in
	// frontend/src/components/Chat.tsx: the same cap on both paths means a file
	// too big to inline in the prompt is also too big to return whole here, and
	// the teacher gets the same behaviour whichever path fed the model.
	maxReadFileChars = 20000
)

// allowedWriteExts restricts writes to the file types the app itself handles
// (see backend/internal/api/files.go and the Markdown editor). Prevents a model
// from dropping a .sh or a .exe into the workspace that is served to the browser.
var allowedWriteExts = map[string]bool{".md": true, ".json": true, ".txt": true}

// hermesFileTools is the tool table declared to Hermes so Lya can ask the
// backend for a file instead of relying on the frontend inlining it (see
// buildDeskPrompt in Chat.tsx, which stays as the fallback).
//
// Why patch_file takes old_string/new_string rather than a unified diff: an
// exact substring replacement is the simplest shape a model produces reliably,
// and it either matches or fails loudly. A unified diff would force us to
// implement fuzzy hunk matching, because the line numbers a model emits are
// stale as soon as the file it saw differs by one line — silently mis-applying
// a hunk into a teacher's course file is a far worse failure than a refusal.
//
// UNVERIFIED: whether the Hermes gateway forwards this `tools` request
// parameter to the upstream model cannot be checked from a development sandbox
// (the gateway is not reachable from there). That is why the loop degrades to
// the previous single-turn behaviour when no tool_calls ever come back, and why
// every job logs one greppable line 'hermes: tool_calls supported=true|false':
// the question gets settled from production logs, not by guessing.
var hermesFileTools = []map[string]interface{}{
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "read_file",
			"description": "Lit un fichier du dossier de travail de l'enseignant et renvoie son contenu. Utilise cet outil avant de modifier un fichier existant, pour travailler sur le contenu réel. Le contenu peut être tronqué s'il est très long : dans ce cas une note explicite le signale à la fin du texte renvoyé.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "write_file",
			"description": "Écrit un fichier dans le dossier de travail de l'enseignant, en le créant s'il n'existe pas. Le contenu fourni remplace intégralement l'ancien : pour une retouche ciblée sur un fichier existant, utilise plutôt patch_file. Extensions autorisées : .md, .json, .txt.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Contenu complet du fichier, en Markdown pour les fichiers .md.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "patch_file",
			"description": "Remplace un extrait exact d'un fichier existant par un nouvel extrait, sans réécrire tout le fichier. old_string doit apparaître exactement une fois dans le fichier : s'il est absent ou présent plusieurs fois, rien n'est écrit et l'erreur t'est renvoyée — allonge alors old_string pour le rendre unique. Extensions autorisées : .md, .json, .txt.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
					"old_string": map[string]interface{}{
						"type":        "string",
						"description": "Extrait exact à remplacer, copié tel quel depuis le fichier (espaces et retours à la ligne compris). Doit apparaître exactement une fois dans le fichier.",
					},
					"new_string": map[string]interface{}{
						"type":        "string",
						"description": "Texte de remplacement. Chaîne vide pour supprimer l'extrait.",
					},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
	},
}

// fileToolsEnabled is the single gate for declaring AND executing the local file
// tools. Kept in one place on purpose: the Desk sub-mode toggle (copie/insertion
// vs mise à jour directe) has to narrow the same condition, and a gate scattered
// across the SSE loop is how mode Lya ends up writing files by accident.
func (b *HermesBridge) fileToolsEnabled(mode string) bool {
	return mode == "desk" && b.workDir != ""
}

// fileToolArgs is the union of the arguments of the three tools. A single struct
// keeps the decoding of the model's arguments string in one place.
type fileToolArgs struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// checkWriteExt rejects extensions outside allowedWriteExts.
func checkWriteExt(relPath string) error {
	ext := strings.ToLower(filepath.Ext(relPath))
	if !allowedWriteExts[ext] {
		return fmt.Errorf("extension %q non autorisée pour %s : seuls .md, .json et .txt peuvent être écrits", ext, relPath)
	}
	return nil
}

// readFileTool returns the content of a workspace file, truncated to
// maxReadFileChars with an explicit notice so the model never mistakes a cut
// file for a complete one.
func (b *HermesBridge) readFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", "", fmt.Errorf("lecture impossible de %s : %v", args.Path, err)
	}
	runes := []rune(string(raw))
	if len(runes) > maxReadFileChars {
		return fmt.Sprintf("%s\n\n[…contenu tronqué : seuls les %d premiers caractères sur %d sont renvoyés. Ne réécris pas ce fichier en entier à partir de cet extrait, utilise patch_file.]",
			string(runes[:maxReadFileChars]), maxReadFileChars, len(runes)), "", nil
	}
	return string(raw), "", nil
}

// writeFileTool writes a whole file inside the workspace and reports the
// relative path as changed so the editor can reload it.
func (b *HermesBridge) writeFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	if err := checkWriteExt(args.Path); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return "", "", fmt.Errorf("création du dossier parent impossible pour %s : %v", args.Path, err)
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0644); err != nil {
		return "", "", fmt.Errorf("écriture impossible de %s : %v", args.Path, err)
	}
	return fmt.Sprintf("Fichier %s écrit (%d octets).", args.Path, len(args.Content)), args.Path, nil
}

// patchFileTool replaces old_string by new_string exactly once. A missing or
// ambiguous old_string is an error result handed back to the model: never a
// silent no-op (the teacher would believe the edit landed) and never a partial
// write.
func (b *HermesBridge) patchFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	if err := checkWriteExt(args.Path); err != nil {
		return "", "", err
	}
	if args.OldString == "" {
		return "", "", fmt.Errorf("old_string vide : fournis l'extrait exact à remplacer dans %s", args.Path)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", "", fmt.Errorf("lecture impossible de %s : %v", args.Path, err)
	}
	text := string(raw)
	switch n := strings.Count(text, args.OldString); {
	case n == 0:
		return "", "", fmt.Errorf("old_string introuvable dans %s : le fichier n'a pas été modifié. Relis-le avec read_file et recopie l'extrait exact", args.Path)
	case n > 1:
		return "", "", fmt.Errorf("old_string apparaît %d fois dans %s : le fichier n'a pas été modifié. Allonge old_string pour qu'il soit unique", n, args.Path)
	}
	patched := strings.Replace(text, args.OldString, args.NewString, 1)
	if err := os.WriteFile(abs, []byte(patched), 0644); err != nil {
		return "", "", fmt.Errorf("écriture impossible de %s : %v", args.Path, err)
	}
	return fmt.Sprintf("Fichier %s modifié (%d octets).", args.Path, len(patched)), args.Path, nil
}

// execFileTool decodes the model's arguments string and dispatches to the
// matching executor. Every failure (bad JSON, unknown tool, refused path) comes
// back as an error the model can act on, never as a job-level error event.
func (b *HermesBridge) execFileTool(name, rawArgs string) (string, string, error) {
	var args fileToolArgs
	if rawArgs == "" {
		rawArgs = "{}"
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", "", fmt.Errorf("arguments JSON invalides pour %s : %v", name, err)
	}
	switch name {
	case "read_file":
		return b.readFileTool(args)
	case "write_file":
		return b.writeFileTool(args)
	case "patch_file":
		return b.patchFileTool(args)
	default:
		return "", "", fmt.Errorf("outil inconnu : %s (disponibles : read_file, write_file, patch_file)", name)
	}
}

// writeToolNames lists tool names that represent a file-write operation.
var writeToolNames = map[string]bool{
	"write":       true,
	"write_file":  true,
	"create_file": true,
	"edit":        true,
	"edit_file":   true,
}

// handleToolFileWrite inspects a tool-progress payload and, if it represents a
// completed file-write operation, writes the file to disk and returns the
// relative path. Returns "" if the event should not trigger a file write.
func (b *HermesBridge) handleToolFileWrite(payload map[string]interface{}) string {
	if b.workDir == "" {
		return ""
	}

	// Normalize tool name (same keys as frontend/src/tools.ts)
	name := strFromMap(payload, "name")
	if name == "" {
		name = strFromMap(payload, "tool")
	}
	if name == "" {
		name = strFromMap(payload, "toolName")
	}
	if name == "" {
		name = strFromMap(payload, "label")
	}

	if !writeToolNames[name] {
		return ""
	}

	// Check status is "done"
	status := strFromMap(payload, "status")
	if status == "" {
		status = strFromMap(payload, "state")
	}
	if status != "done" {
		return ""
	}

	// Normalize path
	path := strFromMap(payload, "path")
	if path == "" {
		path = strFromMap(payload, "file")
	}
	if path == "" {
		path = strFromArgs(payload, "path")
	}
	if path == "" {
		path = strFromArgs(payload, "file")
	}
	if path == "" {
		return ""
	}

	// Normalize content
	content := strFromMap(payload, "content")
	if content == "" {
		content = strFromMap(payload, "output")
	}
	if content == "" {
		content = strFromMap(payload, "result")
	}
	if content == "" {
		content = strFromArgs(payload, "content")
	}
	if content == "" {
		return ""
	}

	// Validate path
	absPath, err := b.safePath(path)
	if err != nil {
		log.Printf("hermes: file write skipped (unsafe path %q): %v", path, err)
		return ""
	}

	// Create parent directories and write file
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		log.Printf("hermes: cannot create parent dir for %q: %v", path, err)
		return ""
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		log.Printf("hermes: cannot write file %q: %v", path, err)
		return ""
	}

	log.Printf("hermes: wrote file %s (%d bytes)", path, len(content))
	return path
}

// strFromMap extracts a string value from a map by key.
func strFromMap(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// strFromArgs extracts a string from the nested "args" map.
func strFromArgs(m map[string]interface{}, key string) string {
	args, ok := m["args"]
	if !ok {
		return ""
	}
	argsMap, ok := args.(map[string]interface{})
	if !ok {
		return ""
	}
	return strFromMap(argsMap, key)
}

// HandleWebSocket upgrades to WS and handles prompt/subscribe messages.
func (b *HermesBridge) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Keepalive pings (survive idle timeouts)
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read error: %v", err)
			}
			return
		}

		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content,omitempty"`
			System  string `json:"system,omitempty"`
			// Path of the file the teacher has open, for the agent_usage log.
			// Lya cannot read the workspace, so the frontend inlines the file
			// content into Content itself — this is observability, not context.
			CurrentFile string `json:"currentFile,omitempty"`
			Mode        string `json:"mode,omitempty"`
			JobID       string `json:"jobId,omitempty"`
			After       int    `json:"after,omitempty"`
		}
		if err := json.Unmarshal(message, &msg); err != nil {
			sendWSJSON(conn, StreamEvent{Type: "error", Error: "invalid JSON"})
			continue
		}

		switch msg.Type {
		case "prompt":
			// Start a new job and immediately subscribe
			job := b.startJob(msg.Content, msg.System, msg.CurrentFile, msg.Mode)
			sendWSJSON(conn, StreamEvent{Type: "meta", JobID: job.ID})
			b.streamToWS(conn, job, 0)
		case "subscribe":
			// Reconnect to existing job
			job := b.hub.Get(msg.JobID)
			if job == nil {
				sendWSJSON(conn, StreamEvent{Type: "error", Error: "job not found"})
				continue
			}
			b.streamToWS(conn, job, msg.After)
		default:
			sendWSJSON(conn, StreamEvent{Type: "error", Error: "unknown type: " + msg.Type})
		}
	}
}

// startJob creates a background job that calls Hermes streaming.
func (b *HermesBridge) startJob(content, systemPrompt, currentFile, mode string) *Job {
	b.hub.gc()
	ctx, cancel := context.WithTimeout(context.Background(), jobRunTimeout)
	job := &Job{
		ID:        fmt.Sprintf("job-%d", time.Now().UnixNano()),
		CreatedAt: time.Now(),
		status:    JobRunning,
		cancel:    cancel,
		doneCh:    make(chan struct{}),
	}
	b.hub.mu.Lock()
	b.hub.jobs[job.ID] = job
	b.hub.mu.Unlock()

	go func() {
		defer cancel()
		defer close(job.doneCh)
		start := time.Now()
		// Default mode to "desk" if empty
		if mode == "" {
			mode = "desk"
		}
		err := b.callHermesStream(ctx, job, content, systemPrompt, mode)
		durationMs := time.Since(start).Milliseconds()
		status := "done"
		if job.status == JobRunning {
			if err != nil {
				// An auth failure is not a transient glitch: telling the teacher
				// to retry would be a lie, and it hides an operator problem.
				userMsg := "Échec IA. Réessaie."
				if strings.Contains(err.Error(), "hermes auth failed") {
					userMsg = "Clé API Hermes invalide (HERMES_API_KEY ≠ API_SERVER_KEY de Lya)."
				}
				job.append(StreamEvent{Type: "error", Error: userMsg, Detail: err.Error()})
				status = "error"
			} else {
				// Should not happen — done is emitted inside callHermesStream
				job.append(StreamEvent{Type: "done"})
			}
		}
		if job.status == JobError {
			status = "error"
		}
		// agent_usage log line
		file := currentFile
		if file == "" {
			file = "-"
		}
		log.Printf("agent_usage agent=lya mode=%s jobId=%s promptLen=%d file=%s durationMs=%d status=%s",
			mode, job.ID, len(content), file, durationMs, status)
	}()
	return job
}

// callHermesStream drives the conversation with Lya: it posts the messages,
// consumes the streamed answer and, in mode Desk, executes the file tools the
// model asked for and re-posts with their results until the model answers with
// text only (or maxToolLoops is reached).
//
// mode controls file access: only "desk" declares and executes the file tools,
// and only "desk" writes files from hermes.tool.progress frames.
func (b *HermesBridge) callHermesStream(ctx context.Context, job *Job, content, systemPrompt, mode string) error {
	messages := []map[string]interface{}{}
	if systemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": content})

	toolsEnabled := b.fileToolsEnabled(mode)

	// One greppable diagnostic per job. supported=true means Hermes really did
	// forward the `tools` parameter to the model and streamed tool_calls back —
	// something that can only be observed in production (see hermesFileTools).
	var (
		full          strings.Builder
		loops         int
		toolCallCount int
		sawToolCalls  bool
	)
	defer func() {
		log.Printf("hermes: tool_calls supported=%t (mode=%s loops=%d toolCalls=%d)",
			sawToolCalls, mode, loops, toolCallCount)
	}()

	for loops = 1; loops <= maxToolLoops; loops++ {
		payload := map[string]interface{}{
			"messages": messages,
			"provider": "custom",
			"stream":   true,
		}
		if toolsEnabled {
			payload["tools"] = hermesFileTools
			payload["tool_choice"] = "auto"
		}
		body, _ := json.Marshal(payload)

		resp, err := b.postHermesStream(ctx, body)
		if err != nil {
			return err
		}
		turn, err := b.streamTurn(job, resp.Body, mode, &full)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if turn.stopped {
			return nil // chunk.error already surfaced as an error event
		}

		if len(turn.toolCalls) == 0 {
			// Plain text answer: exactly the pre-tool-loop behaviour.
			if turn.sawDone {
				job.append(StreamEvent{Type: "done", Reply: full.String()})
			}
			return nil
		}

		sawToolCalls = true
		if !toolsEnabled {
			// No tools were declared, so tool_calls here are unsolicited. Executing
			// them would let mode Lya (or a job without workspace) touch files.
			log.Printf("hermes: refusing %d unsolicited tool call(s) — file tools are only enabled in mode desk (mode=%s workDir=%q)",
				len(turn.toolCalls), mode, b.workDir)
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			return nil
		}

		// Feed the assistant turn back verbatim: the OpenAI protocol requires the
		// assistant message carrying tool_calls before the matching tool results.
		calls := make([]map[string]interface{}, 0, len(turn.toolCalls))
		for _, tc := range turn.toolCalls {
			calls = append(calls, map[string]interface{}{
				"id":   tc.id,
				"type": "function",
				"function": map[string]interface{}{
					"name":      tc.name,
					"arguments": tc.args.String(),
				},
			})
		}
		messages = append(messages, map[string]interface{}{
			"role":       "assistant",
			"content":    turn.text,
			"tool_calls": calls,
		})

		for _, tc := range turn.toolCalls {
			toolCallCount++
			rawArgs := tc.args.String()
			// Report before executing so the teacher sees the action live, even if
			// the tool is slow or fails.
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name":   tc.name,
				"path":   pathFromRawArgs(rawArgs),
				"status": "running",
			}})

			result, changedPath, execErr := b.execFileTool(tc.name, rawArgs)
			if execErr != nil {
				log.Printf("hermes: tool %s path=%q failed: %v", tc.name, pathFromRawArgs(rawArgs), execErr)
				job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
					"name":   tc.name,
					"path":   pathFromRawArgs(rawArgs),
					"status": "error",
					"error":  execErr.Error(),
				}})
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": tc.id,
					"content":      "Erreur : " + execErr.Error(),
				})
				continue
			}

			log.Printf("hermes: tool %s path=%q ok (%d chars returned)", tc.name, pathFromRawArgs(rawArgs), len(result))
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name":   tc.name,
				"path":   pathFromRawArgs(rawArgs),
				"status": "done",
			}})
			if changedPath != "" {
				// Same synthetic shape as the v1.9.0 write interception, so
				// Chat.tsx reloads the editor without any frontend change.
				job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
					"name": "file_changed",
					"path": changedPath,
				}})
			}
			messages = append(messages, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tc.id,
				"content":      result,
			})
		}
	}

	// Loop cap reached and the model still wants tools: stop here rather than let
	// the job spin until jobRunTimeout with nothing shown to the teacher.
	loops = maxToolLoops
	cut := fmt.Sprintf("\n\n_(Boucle d'outils interrompue après %d échanges. Relance la demande en la découpant en étapes plus petites.)_", maxToolLoops)
	full.WriteString(cut)
	job.append(StreamEvent{Type: "delta", Text: cut})
	job.append(StreamEvent{Type: "done", Reply: full.String()})
	log.Printf("hermes: tool loop cut short after %d iterations (mode=%s)", maxToolLoops, mode)
	return nil
}

// pathFromRawArgs best-effort extracts the "path" argument for logging and tool
// events. A malformed payload must not stop us from reporting the tool call.
func pathFromRawArgs(rawArgs string) string {
	var args fileToolArgs
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	return args.Path
}

// postHermesStream does POST /v1/chat/completions with stream:true and maps
// non-2xx responses to the teacher-facing errors startJob knows about.
func (b *HermesBridge) postHermesStream(ctx context.Context, body []byte) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", b.hermesURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "assisted-teacher-backend")

	client := &http.Client{Timeout: 0} // No timeout — stream runs for minutes
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hermes unreachable: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		bodySnippet := truncateStr(string(raw), 200)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			// Hermes rejected the Bearer key — this is NOT Authelia, and not the
			// teacher's session. Typical body: "Invalid gateway API key".
			//
			// Log the key fingerprint so the key actually sent can be compared
			// with the one Hermes holds, without ever logging the secret:
			//   kubectl exec -n openclaw deploy/hermes-lya -- \
			//     sh -c 'grep API_SERVER_KEY /opt/data/.env | cut -d= -f2 | tr -d "\n" | sha256sum'
			//
			// Beware: Hermes validates API_SERVER_KEY from the PVC file
			// /opt/data/.env, NOT from the K8s/Infisical env of the pod. The two
			// diverge after an Infisical rotation, so matching secrets in
			// Kubernetes are not proof of a matching key. See README.
			log.Printf("hermes: auth rejected (HTTP %d) with keyLen=%d keyFp=%s url=%s",
				resp.StatusCode, len(b.apiKey), keyFingerprint(b.apiKey), b.hermesURL)
			return nil, fmt.Errorf("hermes auth failed (HTTP %d): %s — vérifie HERMES_API_KEY (= API_SERVER_KEY de Lya, y compris dans /opt/data/.env du PVC)", resp.StatusCode, bodySnippet)
		}
		return nil, fmt.Errorf("hermes HTTP %d: %s", resp.StatusCode, bodySnippet)
	}
	return resp, nil
}

// toolCallAccum stitches back one streamed tool call: id and function.name arrive
// once (usually on the first delta of that index) while function.arguments comes
// as a stream of string fragments that must be concatenated before decoding.
type toolCallAccum struct {
	id   string
	name string
	args strings.Builder
}

// turnResult is what a single Hermes response (one SSE stream) produced.
type turnResult struct {
	text      string           // text streamed during this turn only
	toolCalls []*toolCallAccum // in tool_call index order, empty for a text answer
	sawDone   bool             // the stream ended with data: [DONE]
	stopped   bool             // chunk.error surfaced: the job must stop, error already emitted
}

// streamTurn consumes one SSE stream. It forwards deltas and hermes.tool.progress
// frames exactly as before, accumulates the turn text into full (the whole job's
// reply) and collects any tool_calls for the caller to execute.
//
// data: [DONE] ends the TURN, not necessarily the job: the done event is emitted
// by callHermesStream only once a turn came back without tool calls.
func (b *HermesBridge) streamTurn(job *Job, body io.Reader, mode string, full *strings.Builder) (turnResult, error) {
	var res turnResult

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var turnText strings.Builder
	var eventName string
	// Tool-call deltas are addressed by index, and indexes may arrive interleaved.
	byIndex := map[int]*toolCallAccum{}
	var order []int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / keepalive
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		if data == "[DONE]" {
			res.sawDone = true
			break
		}

		// Handle tool progress events from Hermes
		if eventName == "hermes.tool.progress" {
			var tool interface{}
			json.Unmarshal([]byte(data), &tool)
			job.append(StreamEvent{Type: "tool", Tool: tool})

			// If this is a file-write tool with status "done", write file to disk
			// and emit a file_changed event (like PiBridge does).
			// Only write files in "desk" mode — Lya mode must never write.
			if mode == "desk" {
				if toolMap, ok := tool.(map[string]interface{}); ok {
					if relPath := b.handleToolFileWrite(toolMap); relPath != "" {
						job.append(StreamEvent{
							Type: "tool",
							Tool: map[string]interface{}{
								"name": "file_changed",
								"path": relPath,
							},
						})
					}
				}
			}
			continue
		}

		// Standard chat.completion.chunk
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			job.append(StreamEvent{Type: "error", Error: chunk.Error.Message})
			res.stopped = true
			return res, nil
		}
		if len(chunk.Choices) > 0 {
			text := chunk.Choices[0].Delta.Content
			if text != "" {
				turnText.WriteString(text)
				full.WriteString(text)
				job.append(StreamEvent{Type: "delta", Text: text})
			}
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc, ok := byIndex[idx]
				if !ok {
					acc = &toolCallAccum{}
					byIndex[idx] = acc
					order = append(order, idx)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.args.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return res, err
	}

	res.text = turnText.String()
	for i, idx := range order {
		acc := byIndex[idx]
		if acc.name == "" {
			continue // an arguments-only fragment with no name is not callable
		}
		if acc.id == "" {
			// Some gateways omit the id. The tool result message MUST carry a
			// tool_call_id matching the assistant message, so synthesise a stable one.
			acc.id = fmt.Sprintf("call_%d", i)
		}
		res.toolCalls = append(res.toolCalls, acc)
	}
	return res, nil
}

// streamToWS sends backlog + live events to the WebSocket. Returns when job finishes or WS disconnects.
func (b *HermesBridge) streamToWS(conn *websocket.Conn, job *Job, after int) {
	backlog, live, unsub := job.Subscribe(after)
	defer unsub()

	// Send backlog
	for _, ev := range backlog {
		if err := sendWSJSON(conn, ev); err != nil {
			return
		}
	}

	// Stream live
	for ev := range live {
		if err := sendWSJSON(conn, ev); err != nil {
			return // WS disconnected — Job keeps running
		}
		if ev.Type == "done" || ev.Type == "error" {
			return
		}
	}
}

func sendWSJSON(conn *websocket.Conn, data interface{}) error {
	msg, _ := json.Marshal(data)
	return conn.WriteMessage(websocket.TextMessage, msg)
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
