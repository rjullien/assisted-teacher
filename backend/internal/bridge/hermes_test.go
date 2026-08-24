package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockHermesServer simulates the Hermes /v1/chat/completions streaming endpoint.
func mockHermesServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}

		// Verify auth
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", 401)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)

		// Simulate streaming response
		chunks := []string{"Hello ", "from ", "Hermes!"}
		for _, chunk := range chunks {
			data := fmt.Sprintf(`{"choices":[{"delta":{"content":"%s"}}]}`, chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func TestHermesBridge_PromptAndStream(t *testing.T) {
	hermes := mockHermesServer()
	defer hermes.Close()

	bridge := NewHermesBridge(hermes.URL, "test-key", "")
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	// Connect WebSocket
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	// Send prompt
	msg, _ := json.Marshal(map[string]string{
		"type":    "prompt",
		"content": "Hello",
		"system":  "You are a teacher",
	})
	ws.WriteMessage(websocket.TextMessage, msg)

	// Read events
	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	// Verify
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	// First event should be meta
	if events[0].Type != "meta" {
		t.Errorf("first event should be meta, got %s", events[0].Type)
	}

	// Should have delta events
	var deltas []string
	for _, ev := range events {
		if ev.Type == "delta" {
			deltas = append(deltas, ev.Text)
		}
	}
	if len(deltas) != 3 {
		t.Errorf("expected 3 deltas, got %d", len(deltas))
	}
	fullText := strings.Join(deltas, "")
	if fullText != "Hello from Hermes!" {
		t.Errorf("expected 'Hello from Hermes!', got %q", fullText)
	}

	// Last event should be done with full reply
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Errorf("last event should be done, got %s", last.Type)
	}
	if last.Reply != "Hello from Hermes!" {
		t.Errorf("done.Reply should be full text, got %q", last.Reply)
	}
}

func TestHermesBridge_Reconnect(t *testing.T) {
	hermes := mockHermesServer()
	defer hermes.Close()

	bridge := NewHermesBridge(hermes.URL, "test-key", "")
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// First connection: send prompt, read a few events, then disconnect
	ws1, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	msg, _ := json.Marshal(map[string]string{"type": "prompt", "content": "Hi"})
	ws1.WriteMessage(websocket.TextMessage, msg)

	// Read meta event to get jobId
	ws1.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, _ := ws1.ReadMessage()
	var meta StreamEvent
	json.Unmarshal(data, &meta)
	jobID := meta.JobID
	if jobID == "" {
		t.Fatal("expected jobId in meta event")
	}

	// Disconnect immediately (simulate Cloudflare timeout)
	ws1.Close()

	// Wait for job to complete
	time.Sleep(200 * time.Millisecond)

	// Reconnect and subscribe
	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
	defer ws2.Close()

	subMsg, _ := json.Marshal(map[string]interface{}{
		"type":  "subscribe",
		"jobId": jobID,
		"after": 0, // replay everything
	})
	ws2.WriteMessage(websocket.TextMessage, subMsg)

	// Should get full backlog
	var events []StreamEvent
	ws2.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := ws2.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	// Should have meta + deltas + done
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events on reconnect, got %d", len(events))
	}

	last := events[len(events)-1]
	if last.Type != "done" {
		t.Errorf("last event should be done, got %s", last.Type)
	}
	if last.Reply != "Hello from Hermes!" {
		t.Errorf("expected full reply on reconnect, got %q", last.Reply)
	}
}

func TestHermesBridge_BadKey(t *testing.T) {
	hermes := mockHermesServer()
	defer hermes.Close()

	bridge := NewHermesBridge(hermes.URL, "wrong-key", "")
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer ws.Close()

	msg, _ := json.Marshal(map[string]string{"type": "prompt", "content": "Hi"})
	ws.WriteMessage(websocket.TextMessage, msg)

	// Should get meta then error
	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	found := false
	for _, ev := range events {
		if ev.Type == "error" {
			found = true
			if !strings.Contains(ev.Detail, "hermes auth failed") && !strings.Contains(ev.Detail, "401") {
				t.Errorf("expected auth failure detail, got %q", ev.Detail)
			}
			if !strings.Contains(ev.Error, "Clé API Hermes") {
				t.Errorf("expected user-facing Hermes key error, got %q", ev.Error)
			}
			break
		}
	}
	if !found {
		t.Error("expected an error event for bad API key")
	}
}

// mockHermesFileWriteServer simulates Hermes emitting a hermes.tool.progress event
// with a write_file tool completion.
func mockHermesFileWriteServer(toolName, filePath, content, status string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", 401)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)

		// Emit tool progress event
		toolPayload, _ := json.Marshal(map[string]interface{}{
			"name":    toolName,
			"path":    filePath,
			"content": content,
			"status":  status,
		})
		fmt.Fprintf(w, "event: hermes.tool.progress\ndata: %s\n\n", string(toolPayload))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)

		// Emit done
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func TestHermesBridge_FileWrite(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := mockHermesFileWriteServer("write_file", "test/output.md", "# Hello", "done")
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	server := httptest.NewServer(http.HandlerFunc(b.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	// Send prompt
	msg, _ := json.Marshal(map[string]string{"type": "prompt", "content": "Write a file"})
	ws.WriteMessage(websocket.TextMessage, msg)

	// Collect events
	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	// Verify file_changed event was emitted
	foundFileChanged := false
	for _, ev := range events {
		if ev.Type == "tool" {
			if toolMap, ok := ev.Tool.(map[string]interface{}); ok {
				if toolMap["name"] == "file_changed" && toolMap["path"] == "test/output.md" {
					foundFileChanged = true
				}
			}
		}
	}
	if !foundFileChanged {
		t.Error("expected a tool event with name=file_changed and path=test/output.md")
	}

	// Verify file was actually written
	filePath := filepath.Join(tmpDir, "test", "output.md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("expected file to exist at %s: %v", filePath, err)
	}
	if string(data) != "# Hello" {
		t.Errorf("expected file content '# Hello', got %q", string(data))
	}
}

func TestHermesBridge_FileWrite_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := mockHermesFileWriteServer("write_file", "../../etc/passwd", "malicious", "done")
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	server := httptest.NewServer(http.HandlerFunc(b.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	// Send prompt
	msg, _ := json.Marshal(map[string]string{"type": "prompt", "content": "Write a file"})
	ws.WriteMessage(websocket.TextMessage, msg)

	// Collect events
	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	// Verify NO file_changed event was emitted
	for _, ev := range events {
		if ev.Type == "tool" {
			if toolMap, ok := ev.Tool.(map[string]interface{}); ok {
				if toolMap["name"] == "file_changed" {
					t.Error("file_changed event should NOT be emitted for path traversal attempt")
				}
			}
		}
	}

	// Verify no error event was emitted (graceful skip)
	for _, ev := range events {
		if ev.Type == "error" {
			t.Error("no error event should be emitted for path traversal (graceful skip)")
		}
	}

	// Verify the file was NOT written anywhere outside tmpDir
	if _, err := os.Stat(filepath.Join(tmpDir, "../../etc/passwd")); err == nil {
		t.Error("file should NOT have been written outside workspace")
	}
}

func TestHermesBridge_FileWrite_LyaMode(t *testing.T) {
	tmpDir := t.TempDir()

	// Mock server emits a write_file tool progress event with valid path and content
	hermes := mockHermesFileWriteServer("write_file", "test/output.md", "# Hello from Lya", "done")
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	server := httptest.NewServer(http.HandlerFunc(b.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	// Send prompt with mode="lya" — file writes must be skipped
	msg, _ := json.Marshal(map[string]string{
		"type":    "prompt",
		"content": "Write a file",
		"mode":    "lya",
	})
	ws.WriteMessage(websocket.TextMessage, msg)

	// Collect events
	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}

	// Verify the tool progress event IS still forwarded (Lya sees tool events)
	foundToolProgress := false
	for _, ev := range events {
		if ev.Type == "tool" {
			if toolMap, ok := ev.Tool.(map[string]interface{}); ok {
				if toolMap["name"] == "write_file" {
					foundToolProgress = true
				}
			}
		}
	}
	if !foundToolProgress {
		t.Error("expected the tool progress event to still be forwarded in lya mode")
	}

	// Verify NO file_changed event was emitted
	for _, ev := range events {
		if ev.Type == "tool" {
			if toolMap, ok := ev.Tool.(map[string]interface{}); ok {
				if toolMap["name"] == "file_changed" {
					t.Error("file_changed event should NOT be emitted in lya mode")
				}
			}
		}
	}

	// Verify the file was NOT written to disk
	filePath := filepath.Join(tmpDir, "test", "output.md")
	if _, err := os.Stat(filePath); err == nil {
		t.Errorf("file should NOT have been written in lya mode, but found at %s", filePath)
	}
}

// --- Multi-turn tool loop (read_file / write_file / patch_file) ---

// scriptedToolCall is one tool call a scripted turn asks for.
type scriptedToolCall struct {
	id        string
	name      string
	arguments string
}

// scriptedTurn is one Hermes response: either plain text, or tool calls.
type scriptedTurn struct {
	text      string
	toolCalls []scriptedToolCall
	// finishReason overrides the finish_reason of the closing chunk. Empty means
	// "tool_calls" for a tool turn and "stop" for a text turn. Setting "stop" or
	// "length" on a tool turn mimics a gateway that streamed a tool_calls delta
	// and then gave up in the middle of the arguments.
	finishReason string
	// errorMessage makes the turn stream a {"error":{...}} chunk instead of an
	// answer, the way Hermes reports an upstream failure mid-stream.
	errorMessage string
}

// scriptedHermes serves one scripted turn per request and records every decoded
// request body, so tests can assert both on what the bridge sent (the `tools`
// array, the tool result messages fed back) and on what it did locally.
type scriptedHermes struct {
	*httptest.Server

	mu       sync.Mutex
	turns    []scriptedTurn
	repeat   bool // replay the last turn once the script is exhausted (loop-cap tests)
	requests []map[string]interface{}
}

func newScriptedHermes(turns []scriptedTurn, repeat bool) *scriptedHermes {
	s := &scriptedHermes{turns: turns, repeat: repeat}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *scriptedHermes) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer test-key" {
		http.Error(w, "unauthorized", 401)
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", 400)
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, body)
	idx := len(s.requests) - 1
	turn := scriptedTurn{text: "script épuisé"}
	switch {
	case idx < len(s.turns):
		turn = s.turns[idx]
	case s.repeat && len(s.turns) > 0:
		turn = s.turns[len(s.turns)-1]
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)

	if turn.errorMessage != "" {
		sseData(w, flusher, map[string]interface{}{
			"error": map[string]interface{}{"message": turn.errorMessage},
		})
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	if len(turn.toolCalls) > 0 {
		for i, tc := range turn.toolCalls {
			// Arguments are deliberately split in two fragments: a real gateway
			// streams them piecemeal and the bridge must concatenate them.
			argRunes := []rune(tc.arguments)
			cut := len(argRunes) / 2
			sseData(w, flusher, map[string]interface{}{"choices": []interface{}{map[string]interface{}{
				"delta": map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{
					"index": i, "id": tc.id, "type": "function",
					"function": map[string]interface{}{"name": tc.name, "arguments": string(argRunes[:cut])},
				}}},
			}}})
			sseData(w, flusher, map[string]interface{}{"choices": []interface{}{map[string]interface{}{
				"delta": map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{
					"index":    i,
					"function": map[string]interface{}{"arguments": string(argRunes[cut:])},
				}}},
			}}})
		}
		sseData(w, flusher, map[string]interface{}{"choices": []interface{}{map[string]interface{}{
			"delta": map[string]interface{}{}, "finish_reason": orDefault(turn.finishReason, "tool_calls"),
		}}})
	} else {
		// Two deltas, to prove text is streamed and accumulated as before.
		// Split on runes: cutting mid-UTF-8 would corrupt the accented French text.
		runes := []rune(turn.text)
		cut := len(runes) / 2
		for _, part := range []string{string(runes[:cut]), string(runes[cut:])} {
			sseData(w, flusher, map[string]interface{}{"choices": []interface{}{map[string]interface{}{
				"delta": map[string]interface{}{"content": part},
			}}})
		}
		sseData(w, flusher, map[string]interface{}{"choices": []interface{}{map[string]interface{}{
			"delta": map[string]interface{}{}, "finish_reason": orDefault(turn.finishReason, "stop"),
		}}})
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func sseData(w http.ResponseWriter, flusher http.Flusher, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func (s *scriptedHermes) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *scriptedHermes) request(t *testing.T, i int) map[string]interface{} {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.requests) {
		t.Fatalf("expected at least %d request(s), got %d", i+1, len(s.requests))
	}
	return s.requests[i]
}

// runHermesJob drives one prompt through the WebSocket and returns every event.
// It sends NO deskMode field, which is exactly what a frontend predating the
// sub-mode selector does: these tests therefore guard the "legacy" sub-mode,
// where the v1.9.0 write interception still applies but no file tool is armed.
func runHermesJob(t *testing.T, b *HermesBridge, prompt, mode string) []StreamEvent {
	t.Helper()
	return runHermesJobSub(t, b, prompt, mode, "")
}

// runDeskDirectJob drives one prompt in mode desk, sub-mode direct: the only
// combination in which the file tools are declared and executed.
func runDeskDirectJob(t *testing.T, b *HermesBridge, prompt string) []StreamEvent {
	t.Helper()
	return runHermesJobSub(t, b, prompt, "desk", deskModeDirect)
}

// runHermesJobSub is runHermesJob with an explicit Desk sub-mode. An empty
// deskMode omits the field from the payload rather than sending "".
func runHermesJobSub(t *testing.T, b *HermesBridge, prompt, mode, deskMode string) []StreamEvent {
	t.Helper()
	return runHermesJobFile(t, b, prompt, mode, deskMode, "")
}

// runHermesJobFile is runHermesJobSub with the working file the Desk panel
// announces (the currentFile field of the prompt message).
func runHermesJobFile(t *testing.T, b *HermesBridge, prompt, mode, deskMode, currentFile string) []StreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(b.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	payload := map[string]string{"type": "prompt", "content": prompt, "mode": mode}
	if deskMode != "" {
		payload["deskMode"] = deskMode
	}
	if currentFile != "" {
		payload["currentFile"] = currentFile
	}
	msg, _ := json.Marshal(payload)
	ws.WriteMessage(websocket.TextMessage, msg)

	var events []StreamEvent
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var ev StreamEvent
		json.Unmarshal(data, &ev)
		events = append(events, ev)
		if ev.Type == "done" || ev.Type == "error" {
			break
		}
	}
	return events
}

func doneEvent(t *testing.T, events []StreamEvent) StreamEvent {
	t.Helper()
	for _, ev := range events {
		if ev.Type == "done" {
			return ev
		}
	}
	t.Fatalf("expected a done event, got %d events (last type %q)", len(events), lastType(events))
	return StreamEvent{}
}

func lastType(events []StreamEvent) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Type
}

func assertNoErrorEvent(t *testing.T, events []StreamEvent) {
	t.Helper()
	for _, ev := range events {
		if ev.Type == "error" {
			t.Fatalf("unexpected error event: %s / %s", ev.Error, ev.Detail)
		}
	}
}

// toolResultContents returns the content of every role:"tool" message in a
// recorded request body, in order.
func toolResultContents(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw, ok := req["messages"].([]interface{})
	if !ok {
		t.Fatalf("request has no messages array: %v", req)
	}
	var out []string
	for _, m := range raw {
		msg, ok := m.(map[string]interface{})
		if !ok || msg["role"] != "tool" {
			continue
		}
		content, _ := msg["content"].(string)
		out = append(out, content)
	}
	return out
}

func toolEventNames(events []StreamEvent) []string {
	var names []string
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		if m, ok := ev.Tool.(map[string]interface{}); ok {
			name, _ := m["name"].(string)
			status, _ := m["status"].(string)
			if status != "" {
				name += ":" + status
			}
			names = append(names, name)
		}
	}
	return names
}

// hasOutsideWorkingFileEvent reports whether any tool event was flagged as
// touching a file other than the one the Desk panel announces.
func hasOutsideWorkingFileEvent(events []StreamEvent) bool {
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		if m, ok := ev.Tool.(map[string]interface{}); ok {
			if flagged, _ := m["outsideWorkingFile"].(bool); flagged {
				return true
			}
		}
	}
	return false
}

// toolCallIDs returns the tool_call_id of every role:"tool" message, in order.
func toolCallIDs(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw, _ := req["messages"].([]interface{})
	var out []string
	for _, m := range raw {
		msg, ok := m.(map[string]interface{})
		if !ok || msg["role"] != "tool" {
			continue
		}
		id, _ := msg["tool_call_id"].(string)
		out = append(out, id)
	}
	return out
}

// assistantToolCallIDs returns the ids declared in the assistant tool_calls
// messages, in order.
func assistantToolCallIDs(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw, _ := req["messages"].([]interface{})
	var out []string
	for _, m := range raw {
		msg, ok := m.(map[string]interface{})
		if !ok || msg["role"] != "assistant" {
			continue
		}
		calls, _ := msg["tool_calls"].([]interface{})
		for _, c := range calls {
			call, _ := c.(map[string]interface{})
			id, _ := call["id"].(string)
			out = append(out, id)
		}
	}
	return out
}

func hasFileChanged(events []StreamEvent, path string) bool {
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		if m, ok := ev.Tool.(map[string]interface{}); ok {
			if m["name"] == "file_changed" && m["path"] == path {
				return true
			}
		}
	}
	return false
}

func declaredToolNames(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw, ok := req["tools"].([]interface{})
	if !ok {
		return nil
	}
	var names []string
	for _, entry := range raw {
		tool, _ := entry.(map[string]interface{})
		fn, _ := tool["function"].(map[string]interface{})
		name, _ := fn["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestHermesBridge_ToolLoop_ReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	fileContent := "# Unité 1\n\nTexte original de l'enseignant.\n"
	if err := os.MkdirAll(filepath.Join(tmpDir, "B1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "B1", "unite1.md"), []byte(fileContent), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "read_file", arguments: `{"path":"B1/unite1.md"}`}}},
		{text: "Le fichier parle de la routine quotidienne."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis B1/unite1.md")

	assertNoErrorEvent(t, events)
	if got := hermes.requestCount(); got != 2 {
		t.Fatalf("expected 2 Hermes requests (tool call + follow-up), got %d", got)
	}

	// First request must declare the three tools with tool_choice auto.
	names := declaredToolNames(t, hermes.request(t, 0))
	if len(names) != 3 || names[0] != "read_file" || names[1] != "write_file" || names[2] != "patch_file" {
		t.Errorf("expected tools [read_file write_file patch_file], got %v", names)
	}
	if hermes.request(t, 0)["tool_choice"] != "auto" {
		t.Errorf("expected tool_choice=auto, got %v", hermes.request(t, 0)["tool_choice"])
	}

	// Second request must carry the file content back as a tool message.
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result message in the follow-up request, got %d", len(results))
	}
	if results[0] != fileContent {
		t.Errorf("expected the file content fed back, got %q", results[0])
	}

	if reply := doneEvent(t, events).Reply; reply != "Le fichier parle de la routine quotidienne." {
		t.Errorf("unexpected done.Reply: %q", reply)
	}
	var deltas []string
	for _, ev := range events {
		if ev.Type == "delta" {
			deltas = append(deltas, ev.Text)
		}
	}
	if strings.Join(deltas, "") != "Le fichier parle de la routine quotidienne." {
		t.Errorf("expected the second turn text to reach the client as deltas, got %v", deltas)
	}
	if got := toolEventNames(events); len(got) != 2 || got[0] != "read_file:running" || got[1] != "read_file:done" {
		t.Errorf("expected running+done tool events for read_file, got %v", got)
	}
}

func TestHermesBridge_ToolLoop_ReadFileTruncated(t *testing.T) {
	tmpDir := t.TempDir()
	long := strings.Repeat("é", maxReadFileChars+500) // multi-byte on purpose
	if err := os.WriteFile(filepath.Join(tmpDir, "long.md"), []byte(long), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "read_file", arguments: `{"path":"long.md"}`}}},
		{text: "Fichier lu."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis long.md")
	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(results))
	}
	if !strings.Contains(results[0], "contenu tronqué") {
		t.Errorf("expected an explicit truncation notice, got %q", truncateStr(results[0], 120))
	}
	if runes := len([]rune(results[0])); runes > maxReadFileChars+400 {
		t.Errorf("expected the content to be capped at %d chars (+notice), got %d runes", maxReadFileChars, runes)
	}
}

func TestHermesBridge_ToolLoop_WriteFile(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"B1/nouveau.md","content":"# Nouveau\n\nContenu."}`}}},
		{text: "C'est écrit."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Crée B1/nouveau.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	data, err := os.ReadFile(filepath.Join(tmpDir, "B1", "nouveau.md"))
	if err != nil {
		t.Fatalf("expected the file to exist: %v", err)
	}
	if string(data) != "# Nouveau\n\nContenu." {
		t.Errorf("unexpected file content: %q", string(data))
	}
	if !hasFileChanged(events, "B1/nouveau.md") {
		t.Errorf("expected a file_changed tool event for B1/nouveau.md, got %v", toolEventNames(events))
	}
}

func TestHermesBridge_ToolLoop_PatchFile(t *testing.T) {
	tmpDir := t.TempDir()
	before := "# Unité 1\n\nVocabulaire : maison.\n\nGrammaire : présent simple.\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "unite1.md"), []byte(before), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "patch_file",
			arguments: `{"path":"unite1.md","old_string":"Vocabulaire : maison.","new_string":"Vocabulaire : maison, jardin."}`}}},
		{text: "Vocabulaire complété."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Complète le vocabulaire")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	data, err := os.ReadFile(filepath.Join(tmpDir, "unite1.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Unité 1\n\nVocabulaire : maison, jardin.\n\nGrammaire : présent simple.\n"
	if string(data) != want {
		t.Errorf("expected only the targeted substring to change.\ngot:  %q\nwant: %q", string(data), want)
	}
	if !hasFileChanged(events, "unite1.md") {
		t.Errorf("expected a file_changed tool event, got %v", toolEventNames(events))
	}
}

func TestHermesBridge_ToolLoop_PatchFileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	before := "# Unité 1\n\nRien à changer.\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "unite1.md"), []byte(before), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "patch_file",
			arguments: `{"path":"unite1.md","old_string":"texte absent","new_string":"peu importe"}`}}},
		{text: "Je n'ai pas trouvé cet extrait."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Patch impossible")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	data, _ := os.ReadFile(filepath.Join(tmpDir, "unite1.md"))
	if string(data) != before {
		t.Errorf("file must be left untouched, got %q", string(data))
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "introuvable") {
		t.Errorf("expected the failure to be fed back to the model, got %v", results)
	}
	if hasFileChanged(events, "unite1.md") {
		t.Error("no file_changed event must be emitted when the patch is refused")
	}
}

func TestHermesBridge_ToolLoop_PatchFileAmbiguous(t *testing.T) {
	tmpDir := t.TempDir()
	before := "# Unité 1\n\nTODO\n\nGrammaire\n\nTODO\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "unite1.md"), []byte(before), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "patch_file",
			arguments: `{"path":"unite1.md","old_string":"TODO","new_string":"À compléter"}`}}},
		{text: "Précise lequel."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Remplace TODO")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	data, _ := os.ReadFile(filepath.Join(tmpDir, "unite1.md"))
	if string(data) != before {
		t.Errorf("file must be left untouched on an ambiguous old_string, got %q", string(data))
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "2 fois") {
		t.Errorf("expected the occurrence count in the tool result, got %v", results)
	}
}

func TestHermesBridge_ToolLoop_PathTraversalRejected(t *testing.T) {
	// The escaping paths for write/patch keep an allowed extension on purpose:
	// otherwise the extension check would mask a missing path guard.
	cases := []struct {
		label string
		tool  string
		args  string
		probe string // path (relative to the workspace) that must not exist afterwards
	}{
		{"read_file", "read_file", `{"path":"../../etc/passwd"}`, "../../etc/passwd"},
		{"write_file", "write_file", `{"path":"../../etc/passwd","content":"malicious"}`, "../../etc/passwd"},
		{"write_file_allowed_ext", "write_file", `{"path":"../evasion.md","content":"malicious"}`, "../evasion.md"},
		{"patch_file", "patch_file", `{"path":"../evasion.md","old_string":"a","new_string":"b"}`, "../evasion.md"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			tmpDir := t.TempDir()
			hermes := newScriptedHermes([]scriptedTurn{
				{toolCalls: []scriptedToolCall{{id: "call_1", name: tc.tool, arguments: tc.args}}},
				{text: "Chemin refusé."},
			}, false)
			defer hermes.Close()

			b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
			events := runDeskDirectJob(t, b, "Sors du dossier")

			assertNoErrorEvent(t, events)
			doneEvent(t, events)

			results := toolResultContents(t, hermes.request(t, 1))
			if len(results) != 1 || !strings.Contains(results[0], "chemin refusé") {
				t.Errorf("expected the path to be refused in the tool result, got %v", results)
			}
			if _, err := os.Stat(filepath.Join(tmpDir, tc.probe)); err == nil {
				t.Errorf("nothing must be written outside the workspace, found %s", tc.probe)
			}
			if hasFileChanged(events, tc.probe) {
				t.Error("no file_changed event must be emitted for a refused path")
			}
		})
	}
}

func TestHermesBridge_ToolLoop_ExtensionRejected(t *testing.T) {
	cases := []struct {
		tool string
		args string
		file string
	}{
		{"write_file", `{"path":"notes.exe","content":"MZ"}`, "notes.exe"},
		{"patch_file", `{"path":"x.sh","old_string":"a","new_string":"b"}`, "x.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			tmpDir := t.TempDir()
			hermes := newScriptedHermes([]scriptedTurn{
				{toolCalls: []scriptedToolCall{{id: "call_1", name: tc.tool, arguments: tc.args}}},
				{text: "Extension refusée."},
			}, false)
			defer hermes.Close()

			b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
			events := runDeskDirectJob(t, b, "Écris un script")

			assertNoErrorEvent(t, events)
			doneEvent(t, events)

			results := toolResultContents(t, hermes.request(t, 1))
			if len(results) != 1 || !strings.Contains(results[0], "non autorisée") {
				t.Errorf("expected the extension to be refused in the tool result, got %v", results)
			}
			if _, err := os.Stat(filepath.Join(tmpDir, tc.file)); err == nil {
				t.Errorf("%s must not be created", tc.file)
			}
		})
	}
}

func TestHermesBridge_ToolLoop_LoopCap(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.md"), []byte("contenu"), 0644); err != nil {
		t.Fatal(err)
	}

	// A model stuck in a loop: it asks for the same tool forever.
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "read_file", arguments: `{"path":"a.md"}`}}},
	}, true)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Boucle")

	assertNoErrorEvent(t, events)
	done := doneEvent(t, events)
	if got := hermes.requestCount(); got != maxToolLoops {
		t.Errorf("expected exactly %d Hermes requests, got %d", maxToolLoops, got)
	}
	if !strings.Contains(done.Reply, "Boucle d'outils interrompue") {
		t.Errorf("expected the French terminal message in done.Reply, got %q", done.Reply)
	}
	foundDelta := false
	for _, ev := range events {
		if ev.Type == "delta" && strings.Contains(ev.Text, "Boucle d'outils interrompue") {
			foundDelta = true
		}
	}
	if !foundDelta {
		t.Error("expected the terminal message to also be streamed as a delta")
	}
}

func TestHermesBridge_ToolLoop_LyaModeDeclaresNoTools(t *testing.T) {
	tmpDir := t.TempDir()

	// The fake asks for a write anyway: an undeclared tool call must never run.
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"interdit.md","content":"non"}`}}},
		{text: "Ne devrait pas arriver."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJob(t, b, "Écris un fichier", "lya")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if _, ok := hermes.request(t, 0)["tools"]; ok {
		t.Error("mode lya must not declare any tools")
	}
	if _, ok := hermes.request(t, 0)["tool_choice"]; ok {
		t.Error("mode lya must not send tool_choice")
	}
	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected a single request in mode lya, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "interdit.md")); err == nil {
		t.Error("no file must be written in mode lya")
	}
}

func TestHermesBridge_ToolLoop_DegradesWhenToolsIgnored(t *testing.T) {
	tmpDir := t.TempDir()

	// A Hermes that ignores `tools` and answers with text only: exactly the
	// pre-tool-loop behaviour must be preserved.
	hermes := newScriptedHermes([]scriptedTurn{
		{text: "Bonjour, voici ta séquence."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Bonjour")

	assertNoErrorEvent(t, events)
	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected exactly 1 request when no tool is called, got %d", got)
	}
	var deltas []string
	for _, ev := range events {
		if ev.Type == "delta" {
			deltas = append(deltas, ev.Text)
		}
	}
	if len(deltas) != 2 || strings.Join(deltas, "") != "Bonjour, voici ta séquence." {
		t.Errorf("expected the text to be forwarded as deltas, got %v", deltas)
	}
	if reply := doneEvent(t, events).Reply; reply != "Bonjour, voici ta séquence." {
		t.Errorf("expected the complete reply on done, got %q", reply)
	}
	if names := toolEventNames(events); len(names) != 0 {
		t.Errorf("expected no tool events, got %v", names)
	}
}

// --- Desk sub-modes: copie/insertion vs mise à jour directe ---
//
// The teacher chooses between two sub-modes inside mode Desk. In "insert" Lya
// answers in the chat only and nothing on disk may move, even if the model asks
// for a write; in "direct" she edits the working file through the file tools.

func TestHermesBridge_ToolLoop_InsertSubModeDeclaresNoTools(t *testing.T) {
	tmpDir := t.TempDir()

	// The fake asks for a write anyway: with no tools declared, that call is
	// unsolicited and must never be executed.
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"interdit.md","content":"non"}`}}},
		{text: "Ne devrait pas arriver."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobSub(t, b, "Écris un fichier", "desk", "insert")

	assertNoErrorEvent(t, events)
	doneEvent(t, events) // the job still completes normally for the teacher

	if _, ok := hermes.request(t, 0)["tools"]; ok {
		t.Error("sub-mode insert must not declare any tools")
	}
	if _, ok := hermes.request(t, 0)["tool_choice"]; ok {
		t.Error("sub-mode insert must not send tool_choice")
	}
	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected a single request in sub-mode insert, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "interdit.md")); err == nil {
		t.Error("no file must be written in sub-mode insert")
	}
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Error("no file_changed event must be emitted in sub-mode insert")
		}
	}
}

// TestHermesBridge_FileWrite_InsertSubMode is the mirror of
// TestHermesBridge_FileWrite_LyaMode for the v1.9.0 progress-frame interception:
// the event still reaches the chat, but nothing is written.
func TestHermesBridge_FileWrite_InsertSubMode(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := mockHermesFileWriteServer("write_file", "test/output.md", "# Hello from Lya", "done")
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobSub(t, b, "Écris un fichier", "desk", "insert")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	foundToolProgress := false
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		m, ok := ev.Tool.(map[string]interface{})
		if !ok {
			continue
		}
		if m["name"] == "write_file" {
			foundToolProgress = true
		}
		if m["name"] == "file_changed" {
			t.Error("file_changed event should NOT be emitted in sub-mode insert")
		}
	}
	if !foundToolProgress {
		t.Error("expected the tool progress event to still be forwarded in sub-mode insert")
	}

	filePath := filepath.Join(tmpDir, "test", "output.md")
	if _, err := os.Stat(filePath); err == nil {
		t.Errorf("file should NOT have been written in sub-mode insert, but found at %s", filePath)
	}
}

func TestHermesBridge_ToolLoop_DirectSubModeWrites(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"B1/direct.md","content":"# Direct"}`}}},
		{text: "C'est écrit."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobSub(t, b, "Mets à jour B1/direct.md", "desk", "direct")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	names := declaredToolNames(t, hermes.request(t, 0))
	if len(names) != 3 || names[0] != "read_file" || names[1] != "write_file" || names[2] != "patch_file" {
		t.Errorf("expected tools [read_file write_file patch_file] in sub-mode direct, got %v", names)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "B1", "direct.md"))
	if err != nil {
		t.Fatalf("expected the file to exist in sub-mode direct: %v", err)
	}
	if string(data) != "# Direct" {
		t.Errorf("unexpected file content: %q", string(data))
	}
	if !hasFileChanged(events, "B1/direct.md") {
		t.Errorf("expected a file_changed tool event, got %v", toolEventNames(events))
	}
}

// TestHermesBridge_ToolLoop_AbsentSubModeArmsNoTools pins the middle ground for a
// frontend that predates the sub-mode selector: it keeps the v1.9.0 write
// interception (TestHermesBridge_FileWrite, which sends no deskMode either) but
// gets no file tool armed, because it never opted into one.
func TestHermesBridge_ToolLoop_AbsentSubModeArmsNoTools(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"legacy.md","content":"# Legacy"}`}}},
		{text: "Ne devrait pas arriver."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobSub(t, b, "Écris legacy.md", "desk", "") // no deskMode field

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if _, ok := hermes.request(t, 0)["tools"]; ok {
		t.Error("no deskMode field must not declare any tool")
	}
	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected a single request without deskMode, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "legacy.md")); err == nil {
		t.Error("an unsolicited tool call must not write a file without an explicit direct sub-mode")
	}
}

func TestNormalizeDeskMode(t *testing.T) {
	cases := map[string]string{
		"insert":   deskModeInsert,
		"INSERT":   deskModeInsert,
		" insert ": deskModeInsert,
		"direct":   deskModeDirect,
		" DIRECT ": deskModeDirect,
		"":         deskModeLegacy, // absent field: v1.9.x behaviour, no new tools
		"garbage":  deskModeLegacy, // a typo must never arm the write tools
	}
	for in, want := range cases {
		if got := normalizeDeskMode(in); got != want {
			t.Errorf("normalizeDeskMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileToolGates(t *testing.T) {
	b := NewHermesBridge("http://example.invalid", "k", "/tmp/workspace")
	cases := []struct {
		mode, deskMode        string
		wantTools, wantLegacy bool
	}{
		{"desk", "direct", true, true},
		{"desk", "insert", false, false},
		{"desk", "", false, true},        // legacy client: v1.9.0 interception only
		{"desk", "garbage", false, true}, // same, a typo is not an opt-in
		{"lya", "direct", false, false},  // mode Lya never touches files
		{"pi", "direct", false, false},
	}
	for _, tc := range cases {
		if got := b.fileToolsEnabled(tc.mode, tc.deskMode); got != tc.wantTools {
			t.Errorf("fileToolsEnabled(%q, %q) = %v, want %v", tc.mode, tc.deskMode, got, tc.wantTools)
		}
		if got := b.legacyWritesEnabled(tc.mode, tc.deskMode); got != tc.wantLegacy {
			t.Errorf("legacyWritesEnabled(%q, %q) = %v, want %v", tc.mode, tc.deskMode, got, tc.wantLegacy)
		}
	}

	// No workspace mounted: nothing may be written, whatever the sub-mode.
	noWorkDir := NewHermesBridge("http://example.invalid", "k", "")
	if noWorkDir.fileToolsEnabled("desk", "direct") || noWorkDir.legacyWritesEnabled("desk", "direct") {
		t.Error("no workDir must disable both the file tools and the legacy write path")
	}
}

// --- Guards on what the tools may read, write and feed back ---

func TestHermesBridge_ToolLoop_ReadExtensionRejected(t *testing.T) {
	tmpDir := t.TempDir()
	secret := "API_SERVER_KEY=super-secret\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(secret), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "read_file", arguments: `{"path":".env"}`}}},
		{text: "Je ne peux pas lire ce fichier."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis .env")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "non autorisée") {
		t.Fatalf("expected the read to be refused, got %v", results)
	}
	if strings.Contains(results[0], "super-secret") {
		t.Error("the content of a non-allowlisted file must never reach the gateway")
	}
}

func TestHermesBridge_ToolLoop_WriteTooLongRejected(t *testing.T) {
	tmpDir := t.TempDir()
	huge := strings.Repeat("é", maxWriteFileChars+1) // multi-byte on purpose
	args, _ := json.Marshal(map[string]string{"path": "gros.md", "content": huge})

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file", arguments: string(args)}}},
		{text: "Trop long."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Écris un énorme fichier")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "trop long") {
		t.Fatalf("expected the oversized write to be refused, got %v", results)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "gros.md")); err == nil {
		t.Error("an oversized write must not create the file")
	}
}

// TestHermesBridge_ToolLoop_WriteRefusesTruncatedRewrite covers the file that
// read_file could only return truncated: rewriting it whole from the excerpt
// would silently drop everything past the cut.
func TestHermesBridge_ToolLoop_WriteRefusesTruncatedRewrite(t *testing.T) {
	tmpDir := t.TempDir()
	original := strings.Repeat("a", maxReadFileChars+500)
	target := filepath.Join(tmpDir, "long.md")
	if err := os.WriteFile(target, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "long.md", "content": strings.Repeat("a", 100)})

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file", arguments: string(args)}}},
		{text: "J'utilise patch_file."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Réécris long.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "patch_file") {
		t.Fatalf("expected the truncated rewrite to be refused with a patch_file hint, got %v", results)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("the file must be left untouched, got %d bytes instead of %d", len(data), len(original))
	}
}

// TestHermesBridge_ToolLoop_ToolResultBudget: tool results accumulate in the
// conversation for the whole job, so without a cumulative cap a few large reads
// overflow the upstream context window and the teacher only sees "Échec IA".
func TestHermesBridge_ToolLoop_ToolResultBudget(t *testing.T) {
	tmpDir := t.TempDir()
	// Exactly at the read cap, so each result is returned whole and three of them
	// consume the whole budget.
	body := strings.Repeat("x", maxReadFileChars)
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var turns []scriptedTurn
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		turns = append(turns, scriptedTurn{toolCalls: []scriptedToolCall{
			{id: "call_" + name, name: "read_file", arguments: `{"path":"` + name + `"}`},
		}})
	}
	turns = append(turns, scriptedTurn{text: "J'ai tout lu."})

	hermes := newScriptedHermes(turns, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis les quatre fichiers")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 4))
	if len(results) != 4 {
		t.Fatalf("expected 4 tool results in the last request, got %d", len(results))
	}
	total := 0
	for _, r := range results {
		total += len([]rune(r))
	}
	if total > maxToolResultChars+500 {
		t.Errorf("cumulative tool results = %d runes, expected at most %d (+notice)", total, maxToolResultChars)
	}
	if !strings.Contains(results[3], "budget de contexte des outils") {
		t.Errorf("expected the last result to say the budget is exhausted, got %q", truncateStr(results[3], 120))
	}
}

// --- Working file announced by the Desk panel ---

func TestHermesBridge_ToolLoop_WriteOutsideWorkingFileIsFlagged(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "B1"), 0755); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"B1/autre.md","content":"# Ailleurs"}`}}},
		{text: "J'ai écrit dans un autre fichier."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobFile(t, b, "Complète mon cours", "desk", deskModeDirect, "B1/unite1.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	// Flagged, not refused: creating a companion file is legitimate.
	if _, err := os.ReadFile(filepath.Join(tmpDir, "B1", "autre.md")); err != nil {
		t.Fatalf("expected the write to still happen: %v", err)
	}
	if !hasOutsideWorkingFileEvent(events) {
		t.Errorf("expected a tool event flagged outsideWorkingFile, got %v", toolEventNames(events))
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "B1/unite1.md") {
		t.Errorf("expected the model to be told which file was the working file, got %v", results)
	}
}

func TestHermesBridge_ToolLoop_WriteOnWorkingFileIsNotFlagged(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "B1"), 0755); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"B1/unite1.md","content":"# Unité 1"}`}}},
		{text: "C'est fait."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runHermesJobFile(t, b, "Complète mon cours", "desk", deskModeDirect, "B1/unite1.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if hasOutsideWorkingFileEvent(events) {
		t.Error("a write on the announced working file must not be flagged")
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || strings.Contains(results[0], "Attention") {
		t.Errorf("expected a plain success result, got %v", results)
	}
}

// --- Tool-call reassembly and dispatch edge cases ---

func TestHermesBridge_ToolLoop_UnknownToolName(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "delete_file",
			arguments: `{"path":"a.md"}`}}},
		{text: "Cet outil n'existe pas."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Supprime a.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "outil inconnu") {
		t.Fatalf("expected the unknown tool to be reported to the model, got %v", results)
	}
	if names := toolEventNames(events); len(names) == 0 || !strings.Contains(names[len(names)-1], ":error") {
		t.Errorf("expected an error tool event, got %v", names)
	}
}

// TestHermesBridge_ToolLoop_MalformedArguments: the arguments string is the one
// part of a tool call a model routinely gets wrong. The failure must reach both
// the model (so it can retry) and the thread (so the teacher is not left with a
// status line that vanishes).
func TestHermesBridge_ToolLoop_MalformedArguments(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"a.md","content":`}}},
		{text: "Je recommence."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Écris a.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "arguments JSON invalides") {
		t.Fatalf("expected the malformed arguments to be reported to the model, got %v", results)
	}
	found := false
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		m, ok := ev.Tool.(map[string]interface{})
		if ok && m["status"] == "error" && m["name"] == "write_file" && m["error"] != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error tool event carrying the failure, got %v", toolEventNames(events))
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "a.md")); err == nil {
		t.Error("nothing must be written when the arguments cannot be decoded")
	}
}

// TestHermesBridge_ToolLoop_IncompleteCallDroppedOnStop: a gateway that streams a
// tool_calls delta and then finishes on "length" left us truncated arguments.
// Executing them would write from half a payload.
func TestHermesBridge_ToolLoop_IncompleteCallDroppedOnStop(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := newScriptedHermes([]scriptedTurn{
		{
			toolCalls:    []scriptedToolCall{{id: "call_1", name: "write_file", arguments: `{"path":"a.md","content":"# Coup`}},
			finishReason: "length",
		},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Écris a.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected the incomplete call to be dropped without a follow-up request, got %d requests", got)
	}
	if names := toolEventNames(events); len(names) != 0 {
		t.Errorf("expected no tool event for a dropped call, got %v", names)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "a.md")); err == nil {
		t.Error("an incomplete tool call must not write anything")
	}
}

func TestHermesBridge_ToolLoop_TwoCallsInOneTurn(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.md"), []byte("contenu de a"), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{
			{id: "call_1", name: "read_file", arguments: `{"path":"a.md"}`},
			{id: "call_2", name: "write_file", arguments: `{"path":"b.md","content":"# B"}`},
		}},
		{text: "Les deux sont faits."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis a.md puis écris b.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if got := hermes.requestCount(); got != 2 {
		t.Fatalf("expected 2 requests, got %d", got)
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 2 {
		t.Fatalf("expected both tool results to be fed back, got %v", results)
	}
	if !strings.Contains(results[0], "contenu de a") {
		t.Errorf("expected the read result first (tool_call order), got %q", results[0])
	}
	if !strings.Contains(results[1], "b.md") {
		t.Errorf("expected the write result second, got %q", results[1])
	}
	if ids := toolCallIDs(t, hermes.request(t, 1)); len(ids) != 2 || ids[0] != "call_1" || ids[1] != "call_2" {
		t.Errorf("expected tool_call_id call_1 then call_2, got %v", ids)
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "b.md")); err != nil || string(data) != "# B" {
		t.Errorf("expected b.md to be written, got %q / %v", string(data), err)
	}
}

// TestHermesBridge_ToolLoop_MissingCallID: some gateways omit the tool-call id,
// but the tool result message MUST carry a tool_call_id matching the assistant
// message or the follow-up request is rejected upstream.
func TestHermesBridge_ToolLoop_MissingCallID(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.md"), []byte("contenu de a"), 0644); err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "", name: "read_file", arguments: `{"path":"a.md"}`}}},
		{text: "Lu."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Lis a.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	req := hermes.request(t, 1)
	ids := toolCallIDs(t, req)
	if len(ids) != 1 || ids[0] != "call_0" {
		t.Fatalf("expected the synthesised id call_0 on the tool result, got %v", ids)
	}
	if assistantIDs := assistantToolCallIDs(t, req); len(assistantIDs) != 1 || assistantIDs[0] != ids[0] {
		t.Errorf("assistant tool_calls ids %v must match the tool result ids %v", assistantIDs, ids)
	}
}

// TestHermesBridge_ToolLoop_ChunkErrorAfterWrite: the gateway failing mid-loop
// must surface as an error, and the file already written stays written — the
// teacher's editor was reloaded from it.
func TestHermesBridge_ToolLoop_ChunkErrorAfterWrite(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"a.md","content":"# A"}`}}},
		{errorMessage: "upstream model overloaded"},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Écris a.md")

	var sawError bool
	for _, ev := range events {
		if ev.Type == "error" && strings.Contains(ev.Error, "overloaded") {
			sawError = true
		}
		if ev.Type == "done" {
			t.Error("no done event must follow a chunk error")
		}
	}
	if !sawError {
		t.Fatalf("expected the chunk error to reach the teacher, got %v", events)
	}
	if !hasFileChanged(events, "a.md") {
		t.Errorf("expected the file_changed event of the completed write, got %v", toolEventNames(events))
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "a.md")); err != nil || string(data) != "# A" {
		t.Errorf("the write that already succeeded must not be rolled back, got %q / %v", string(data), err)
	}
}

func TestHermesBridge_ToolLoop_LoopCapMentionsWrites(t *testing.T) {
	tmpDir := t.TempDir()

	// A model stuck writing the same file forever: when the loop is cut, the file
	// on disk has already been modified and the message must say so.
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"a.md","content":"# encore"}`}}},
	}, true)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir)
	events := runDeskDirectJob(t, b, "Boucle d'écriture")

	assertNoErrorEvent(t, events)
	done := doneEvent(t, events)
	if !strings.Contains(done.Reply, "conservées") {
		t.Errorf("expected the cut message to say the writes already applied are kept, got %q", done.Reply)
	}
}
