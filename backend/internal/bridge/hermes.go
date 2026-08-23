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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

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
	hub       *Hub
	upgrader  websocket.Upgrader
}

func NewHermesBridge(hermesURL, apiKey string) *HermesBridge {
	// TrimSpace is critical: Infisical/K8s secrets often carry a trailing \n,
	// which makes the Bearer token mismatch on the gateway side.
	key := strings.TrimSpace(apiKey)
	if key != apiKey {
		log.Printf("hermes: API key had %d surrounding whitespace byte(s) — trimmed", len(apiKey)-len(key))
	}
	log.Printf("hermes: bridge configured (url=%s, keyLen=%d)", hermesURL, len(key))
	return &HermesBridge{
		hermesURL: strings.TrimRight(strings.TrimSpace(hermesURL), "/"),
		apiKey:    key,
		hub:       NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
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
			job := b.startJob(msg.Content, msg.System)
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
func (b *HermesBridge) startJob(content, systemPrompt string) *Job {
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
		err := b.callHermesStream(ctx, job, content, systemPrompt)
		if job.status == JobRunning {
			if err != nil {
				job.append(StreamEvent{Type: "error", Error: "Échec IA. Réessaie.", Detail: err.Error()})
			} else {
				// Should not happen — done is emitted inside callHermesStream
				job.append(StreamEvent{Type: "done"})
			}
		}
	}()
	return job
}

// callHermesStream does POST /v1/chat/completions stream:true to Lya.
func (b *HermesBridge) callHermesStream(ctx context.Context, job *Job, content, systemPrompt string) error {
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
		return fmt.Errorf("hermes HTTP %d: %s", resp.StatusCode, truncateStr(string(raw), 200))
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
			var tool any
			json.Unmarshal([]byte(data), &tool)
			job.append(StreamEvent{Type: "tool", Tool: tool})
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
