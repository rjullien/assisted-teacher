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
			Mode    string `json:"mode,omitempty"`
			JobID   string `json:"jobId,omitempty"`
			After   int    `json:"after,omitempty"`
		}
		if err := json.Unmarshal(message, &msg); err != nil {
			sendWSJSON(conn, StreamEvent{Type: "error", Error: "invalid JSON"})
			continue
		}

		switch msg.Type {
		case "prompt":
			// Start a new job and immediately subscribe
			job := b.startJob(msg.Content, msg.System, msg.Mode)
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
func (b *HermesBridge) startJob(content, systemPrompt, mode string) *Job {
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
		log.Printf("agent_usage agent=lya mode=%s jobId=%s promptLen=%d durationMs=%d status=%s",
			mode, job.ID, len(content), durationMs, status)
	}()
	return job
}

// callHermesStream does POST /v1/chat/completions stream:true to Lya.
// mode controls file-write behavior: only "desk" mode writes files to disk.
func (b *HermesBridge) callHermesStream(ctx context.Context, job *Job, content, systemPrompt, mode string) error {
	messages := []map[string]string{}
	if systemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": content})

	body, _ := json.Marshal(map[string]interface{}{
		"messages": messages,
		"provider": "custom",
		"stream":   true,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", b.hermesURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "assisted-teacher-backend")

	client := &http.Client{Timeout: 0} // No timeout — stream runs for minutes
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("hermes unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
			return fmt.Errorf("hermes auth failed (HTTP %d): %s — vérifie HERMES_API_KEY (= API_SERVER_KEY de Lya, y compris dans /opt/data/.env du PVC)", resp.StatusCode, bodySnippet)
		}
		return fmt.Errorf("hermes HTTP %d: %s", resp.StatusCode, bodySnippet)
	}

	// Consume SSE stream
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var full strings.Builder
	var eventName string

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
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			return nil
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
					Content string `json:"content"`
				} `json:"delta"`
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
			return nil
		}
		if len(chunk.Choices) > 0 {
			text := chunk.Choices[0].Delta.Content
			if text != "" {
				full.WriteString(text)
				job.append(StreamEvent{Type: "delta", Text: text})
			}
		}
	}
	return scanner.Err()
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
