package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// PiBridge handles WebSocket connections for the pi agent.
// It spawns `pi --mode rpc` as a subprocess per request, translates JSONL
// events to StreamEvent, and reuses the same Job/Hub infrastructure as Hermes.
type PiBridge struct {
	piCmd      string // path to pi binary
	workDir    string // workspace directory (jail root)
	modelsJSON string // path to rendered models.json
	hub        *Hub
	upgrader   websocket.Upgrader
}

// NewPiBridge creates a bridge for the pi agent.
func NewPiBridge(piCmd, workDir, modelsJSON string) *PiBridge {
	log.Printf("pi bridge: configured (cmd=%s, workDir=%s, models=%s)", piCmd, workDir, modelsJSON)
	return &PiBridge{
		piCmd:      piCmd,
		workDir:    workDir,
		modelsJSON: modelsJSON,
		hub:        NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// HandleWebSocket upgrades to WS and handles prompt/subscribe messages for pi.
func (b *PiBridge) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("pi ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Keepalive pings
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
				log.Printf("pi ws read error: %v", err)
			}
			return
		}

		var msg struct {
			Type        string `json:"type"`
			Content     string `json:"content,omitempty"`
			System      string `json:"system,omitempty"`
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
			job := b.startJob(msg.Content, msg.System, msg.CurrentFile, msg.Mode)
			sendWSJSON(conn, StreamEvent{Type: "meta", JobID: job.ID})
			b.streamToWS(conn, job, 0)
		case "subscribe":
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

// startJob launches pi as a subprocess for the given prompt.
func (b *PiBridge) startJob(content, systemPrompt, currentFile, mode string) *Job {
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
		toolCount := 0
		var toolsUsed []string
		err := b.runPi(ctx, job, content, systemPrompt, currentFile, &toolCount, &toolsUsed)
		durationMs := time.Since(start).Milliseconds()
		status := "done"
		if err != nil {
			status = "error"
			if job.status == JobRunning {
				job.append(StreamEvent{Type: "error", Error: "Échec agent pi. Réessaie.", Detail: err.Error()})
			}
		}
		// agent_usage log line
		file := currentFile
		if file == "" {
			file = "-"
		}
		log.Printf("agent_usage agent=pi mode=%s jobId=%s promptLen=%d file=%s durationMs=%d status=%s tools=%d toolsUsed=%s",
			mode, job.ID, len(content), file, durationMs, status, toolCount, strings.Join(toolsUsed, ","))
	}()
	return job
}

// piRPCRequest is the JSONL message sent to pi's stdin.
type piRPCRequest struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// runPi spawns `pi --mode rpc` and streams the response.
func (b *PiBridge) runPi(ctx context.Context, job *Job, content, systemPrompt, currentFile string, toolCount *int, toolsUsed *[]string) error {
	// Build the message: system prompt as preamble + user content
	var msgBuilder strings.Builder
	if systemPrompt != "" {
		msgBuilder.WriteString(systemPrompt)
		msgBuilder.WriteString("\n\n---\n\n")
	}
	if currentFile != "" {
		msgBuilder.WriteString(fmt.Sprintf("[Fichier ouvert : %s]\n\n", currentFile))
	}
	msgBuilder.WriteString(content)

	args := []string{
		"--mode", "rpc",
		"--no-approve",
		"--no-context-files",
	}
	if b.modelsJSON != "" {
		args = append(args, "--models", b.modelsJSON)
	}

	cmd := exec.CommandContext(ctx, b.piCmd, args...)
	cmd.Dir = b.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi: %w", err)
	}

	// Send the prompt
	req := piRPCRequest{
		ID:      job.ID,
		Type:    "prompt",
		Message: msgBuilder.String(),
	}
	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')
	if _, err := stdin.Write(reqBytes); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	stdin.Close()

	// Read JSONL responses from stdout
	var full strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var mu sync.Mutex
	filesWritten := map[string]bool{}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var ev piEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "text_delta":
			text := ev.Delta
			if text != "" {
				full.WriteString(text)
				job.append(StreamEvent{Type: "delta", Text: text})
			}

		case "tool_start":
			*toolCount++
			name := ev.ToolName
			if name != "" {
				found := false
				for _, t := range *toolsUsed {
					if t == name {
						found = true
						break
					}
				}
				if !found {
					*toolsUsed = append(*toolsUsed, name)
				}
			}
			job.append(StreamEvent{Type: "tool", Tool: map[string]any{
				"name":   name,
				"status": "running",
				"path":   ev.Path,
			}})

		case "tool_end":
			name := ev.ToolName
			path := ev.Path
			job.append(StreamEvent{Type: "tool", Tool: map[string]any{
				"name":   name,
				"status": "done",
				"path":   path,
			}})
			// Track file writes for file_changed events
			if (name == "write" || name == "edit") && path != "" {
				mu.Lock()
				filesWritten[path] = true
				mu.Unlock()
			}

		case "done", "complete":
			// Emit file_changed for each file pi wrote
			mu.Lock()
			for path := range filesWritten {
				job.append(StreamEvent{Type: "tool", Tool: map[string]any{
					"name": "file_changed",
					"path": path,
				}})
			}
			mu.Unlock()
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			_ = cmd.Wait()
			return nil

		case "error":
			job.append(StreamEvent{Type: "error", Error: ev.ErrorMsg, Detail: ev.Detail})
			_ = cmd.Wait()
			return nil
		}
	}

	// If scanner exits without done/error, the process ended
	if err := cmd.Wait(); err != nil {
		// If we got some text, emit done anyway
		if full.Len() > 0 {
			mu.Lock()
			for path := range filesWritten {
				job.append(StreamEvent{Type: "tool", Tool: map[string]any{
					"name": "file_changed",
					"path": path,
				}})
			}
			mu.Unlock()
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			return nil
		}
		return fmt.Errorf("pi exited: %w", err)
	}

	// Clean exit with content
	if full.Len() > 0 {
		mu.Lock()
		for path := range filesWritten {
			job.append(StreamEvent{Type: "tool", Tool: map[string]any{
				"name": "file_changed",
				"path": path,
			}})
		}
		mu.Unlock()
		job.append(StreamEvent{Type: "done", Reply: full.String()})
	} else {
		job.append(StreamEvent{Type: "done", Reply: ""})
	}
	return nil
}

// streamToWS sends backlog + live events to the WebSocket.
func (b *PiBridge) streamToWS(conn *websocket.Conn, job *Job, after int) {
	backlog, live, unsub := job.Subscribe(after)
	defer unsub()

	for _, ev := range backlog {
		if err := sendWSJSON(conn, ev); err != nil {
			return
		}
	}

	for ev := range live {
		if err := sendWSJSON(conn, ev); err != nil {
			return
		}
		if ev.Type == "done" || ev.Type == "error" {
			return
		}
	}
}

// piEvent is a JSONL event from pi --mode rpc stdout.
type piEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta,omitempty"`
	ToolName string `json:"toolName,omitempty"`
	Path     string `json:"path,omitempty"`
	ErrorMsg string `json:"error,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
