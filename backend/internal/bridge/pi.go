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
// It spawns `pi --mode rpc` as a subprocess per request, translates RPC
// events (JSONL on stdout) to StreamEvent, and reuses the same Job/Hub
// infrastructure as Hermes.
//
// Pi RPC protocol docs: https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md
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

// runPi spawns `pi --mode rpc --no-session` and streams the response.
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
		"--no-session",
	}
	if b.modelsJSON != "" {
		args = append(args, "--provider", "custom", "--model", "default")
	}

	cmd := exec.CommandContext(ctx, b.piCmd, args...)
	cmd.Dir = b.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set PI_MODELS_JSON so pi finds the custom provider config
	cmd.Env = append(cmd.Environ(), "PI_MODELS_JSON="+b.modelsJSON)

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

	// Send the prompt command (pi RPC protocol)
	promptCmd := map[string]interface{}{
		"type":    "prompt",
		"message": msgBuilder.String(),
	}
	reqBytes, _ := json.Marshal(promptCmd)
	reqBytes = append(reqBytes, '\n')
	if _, err := stdin.Write(reqBytes); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	// Don't close stdin yet — pi expects the pipe to stay open for potential
	// follow-up commands. We'll let it close when the process exits.

	// Read JSONL events from stdout (pi RPC protocol)
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

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			log.Printf("pi: unparseable line: %s", truncateStr(line, 100))
			continue
		}

		evType, _ := raw["type"].(string)

		switch evType {
		case "response":
			// Command acknowledgment. Check for errors.
			success, _ := raw["success"].(bool)
			if !success {
				errMsg, _ := raw["error"].(string)
				if errMsg == "" {
					errMsg = "pi rejected the command"
				}
				job.append(StreamEvent{Type: "error", Error: errMsg})
				stdin.Close()
				_ = cmd.Wait()
				return nil
			}
			// success:true — prompt accepted, events will follow

		case "message_update":
			// Streaming delta from the assistant
			ame, _ := raw["assistantMessageEvent"].(map[string]interface{})
			if ame == nil {
				continue
			}
			deltaType, _ := ame["type"].(string)
			switch deltaType {
			case "text_delta":
				delta, _ := ame["delta"].(string)
				if delta != "" {
					full.WriteString(delta)
					job.append(StreamEvent{Type: "delta", Text: delta})
				}
			case "toolcall_start":
				toolName, _ := ame["toolName"].(string)
				*toolCount++
				if toolName != "" {
					found := false
					for _, t := range *toolsUsed {
						if t == toolName {
							found = true
							break
						}
					}
					if !found {
						*toolsUsed = append(*toolsUsed, toolName)
					}
				}
			}

		case "tool_execution_start":
			toolName, _ := raw["toolName"].(string)
			args, _ := raw["args"].(map[string]interface{})
			path := ""
			if args != nil {
				if p, ok := args["path"].(string); ok {
					path = p
				} else if p, ok := args["file_path"].(string); ok {
					path = p
				}
			}
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name":   toolName,
				"status": "running",
				"path":   path,
			}})

		case "tool_execution_end":
			toolName, _ := raw["toolName"].(string)
			args, _ := raw["args"].(map[string]interface{})
			path := ""
			if args != nil {
				if p, ok := args["path"].(string); ok {
					path = p
				} else if p, ok := args["file_path"].(string); ok {
					path = p
				}
			}
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name":   toolName,
				"status": "done",
				"path":   path,
			}})
			// Track file writes
			if (toolName == "write" || toolName == "edit") && path != "" {
				mu.Lock()
				filesWritten[path] = true
				mu.Unlock()
			}

		case "agent_end", "agent_settled":
			// Agent finished — emit file_changed for written files, then done
			mu.Lock()
			for path := range filesWritten {
				job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
					"name": "file_changed",
					"path": path,
				}})
			}
			mu.Unlock()
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			stdin.Close()
			_ = cmd.Wait()
			return nil
		}
	}

	// Scanner exited (process ended without agent_end)
	scanErr := scanner.Err()
	stdin.Close()
	cmdErr := cmd.Wait()

	if full.Len() > 0 {
		// Got some text — emit done
		mu.Lock()
		for path := range filesWritten {
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name": "file_changed",
				"path": path,
			}})
		}
		mu.Unlock()
		job.append(StreamEvent{Type: "done", Reply: full.String()})
		return nil
	}

	if scanErr != nil {
		return fmt.Errorf("pi stdout scan: %w", scanErr)
	}
	if cmdErr != nil {
		return fmt.Errorf("pi exited: %w", cmdErr)
	}
	job.append(StreamEvent{Type: "done", Reply: ""})
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
