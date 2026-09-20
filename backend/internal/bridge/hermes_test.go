package bridge

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// mockHermesServer simulates the Hermes /v1/responses streaming endpoint —
// and accepts the legacy /v1/chat/completions path too, since streamTurn keeps
// parsing both formats after the bridge moved to the Responses API.
func mockHermesServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/chat/completions" {
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

	bridge := NewHermesBridge(hermes.URL, "test-key", "", nil)
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

	bridge := NewHermesBridge(hermes.URL, "test-key", "", nil)
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

	bridge := NewHermesBridge(hermes.URL, "wrong-key", "", nil)
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
		if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/chat/completions" {
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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
	if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/chat/completions" {
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
	// The bridge now POSTs the Responses API body: conversation under "input"
	// (not "messages" as in the legacy chat.completions format). Accept both so
	// the test keeps describing the request that actually went out.
	raw, _ := req["input"].([]interface{})
	if raw == nil {
		raw, _ = req["messages"].([]interface{})
	}
	if raw == nil {
		t.Fatalf("request has no input/messages array: %v", req)
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

// reqMessages returns the conversation array of a recorded request body.
// The bridge POSTs the Responses API body where the conversation lives under
// "input"; the legacy chat.completions format used "messages". Both are read so
// tests keep describing the request that actually went out.
func reqMessages(req map[string]interface{}) []interface{} {
	raw, _ := req["input"].([]interface{})
	if raw == nil {
		raw, _ = req["messages"].([]interface{})
	}
	return raw
}

// toolCallIDs returns the tool_call_id of every role:"tool" message, in order.
func toolCallIDs(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw := reqMessages(req)
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
	raw := reqMessages(req)
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

// assistantToolCallArgs returns the arguments string of every tool call echoed
// in the assistant messages, in order.
func assistantToolCallArgs(t *testing.T, req map[string]interface{}) []string {
	t.Helper()
	raw := reqMessages(req)
	var out []string
	for _, m := range raw {
		msg, ok := m.(map[string]interface{})
		if !ok || msg["role"] != "assistant" {
			continue
		}
		calls, _ := msg["tool_calls"].([]interface{})
		for _, c := range calls {
			call, _ := c.(map[string]interface{})
			fn, _ := call["function"].(map[string]interface{})
			args, _ := fn["arguments"].(string)
			out = append(out, args)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

			b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

			b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runDeskDirectJob(t, b, "Boucle")

	assertNoErrorEvent(t, events)
	done := doneEvent(t, events)
	// Asserted against a literal, NOT against maxToolLoops: comparing the code
	// to its own constant is tautological — raising the constant would raise the
	// expectation with it and the test could never fail. The cap is what stops a
	// looping model from burning tokens, so its value is part of the contract and
	// changing it must break a test deliberately.
	const wantRequests = 8
	if maxToolLoops != wantRequests {
		t.Fatalf("maxToolLoops changed to %d: raising the cap multiplies the tokens a looping "+
			"model can burn. Update wantRequests only if that is intended.", maxToolLoops)
	}
	if got := hermes.requestCount(); got != wantRequests {
		t.Errorf("expected exactly %d Hermes requests, got %d", wantRequests, got)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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
	b := NewHermesBridge("http://example.invalid", "k", "/tmp/workspace", nil)
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
	noWorkDir := NewHermesBridge("http://example.invalid", "k", "", nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

// TestHermesBridge_ToolLoop_BudgetKeepsWriteConfirmations: once the budget is
// spent, replacing "Fichier X écrit" by the budget notice makes a write that
// landed indistinguishable from one that did not, and the model's natural
// recovery is to write the same file again.
func TestHermesBridge_ToolLoop_BudgetKeepsWriteConfirmations(t *testing.T) {
	tmpDir := t.TempDir()
	body := strings.Repeat("x", maxReadFileChars) // exactly at the read cap
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	turns := []scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_a", name: "read_file", arguments: `{"path":"a.md"}`}}},
		{toolCalls: []scriptedToolCall{{id: "call_b", name: "read_file", arguments: `{"path":"b.md"}`}}},
		{toolCalls: []scriptedToolCall{{id: "call_c", name: "read_file", arguments: `{"path":"c.md"}`}}},
		{toolCalls: []scriptedToolCall{{id: "call_w", name: "write_file",
			arguments: `{"path":"d.md","content":"# D"}`}}},
		{text: "Fichier écrit."},
	}
	hermes := newScriptedHermes(turns, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runHermesJobFile(t, b, "Lis trois fichiers puis écris d.md", "desk", deskModeDirect, "d.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	results := toolResultContents(t, hermes.request(t, 4))
	if len(results) != 4 {
		t.Fatalf("expected 4 tool results in the last request, got %d", len(results))
	}
	// The three reads consumed the whole budget, so a further READ would be cut…
	confirmation := results[3]
	if strings.Contains(confirmation, "budget de contexte") {
		t.Errorf("the write confirmation must survive an exhausted budget, got %q", confirmation)
	}
	if !strings.Contains(confirmation, "d.md") || !strings.Contains(confirmation, "écrit") {
		t.Errorf("expected the write confirmation to reach the model, got %q", confirmation)
	}
}

// TestHermesBridge_ToolLoop_EchoedWriteArgumentsAreShrunk: the assistant message
// that must precede the tool results carries the model's own arguments, i.e. the
// whole file on a write_file, and it is resent on every later turn. Echoing it
// verbatim kept the context-overflow path open even though every tool RESULT is
// budgeted.
func TestHermesBridge_ToolLoop_EchoedWriteArgumentsAreShrunk(t *testing.T) {
	tmpDir := t.TempDir()
	longContent := strings.Repeat("é", 10000) // multi-byte on purpose
	args, err := json.Marshal(map[string]string{"path": "gros.md", "content": longContent})
	if err != nil {
		t.Fatal(err)
	}

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file", arguments: string(args)}}},
		{text: "C'est écrit."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runDeskDirectJob(t, b, "Écris gros.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	// The shrink is about the HISTORY only: the file on disk keeps every character.
	data, err := os.ReadFile(filepath.Join(tmpDir, "gros.md"))
	if err != nil || string(data) != longContent {
		t.Fatalf("expected the full content to be written, got %d bytes / %v", len(data), err)
	}

	echoed := assistantToolCallArgs(t, hermes.request(t, 1))
	if len(echoed) != 1 {
		t.Fatalf("expected one echoed tool call, got %d", len(echoed))
	}
	if n := len([]rune(echoed[0])); n > maxEchoedArgsChars {
		t.Errorf("echoed arguments = %d runes, expected at most %d", n, maxEchoedArgsChars)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(echoed[0]), &decoded); err != nil {
		t.Fatalf("echoed arguments must stay valid JSON, got %v (%q)", err, truncateStr(echoed[0], 80))
	}
	if decoded["path"] != "gros.md" {
		t.Errorf("expected the path to survive the shrink, got %q", decoded["path"])
	}
	if !strings.Contains(decoded["content"], "tronqué dans l'historique") {
		t.Errorf("expected the content to be marked as truncated, got %q", truncateStr(decoded["content"], 80))
	}
}

func TestShrinkEchoedArgs(t *testing.T) {
	short := `{"path":"a.md","content":"# A"}`
	if got := shrinkEchoedArgs(short); got != short {
		t.Errorf("short arguments must be echoed verbatim, got %q", got)
	}

	long := `{"path":"a.md","content":"` + strings.Repeat("x", maxEchoedArgsChars+10) + `"}`
	got := shrinkEchoedArgs(long)
	if n := len([]rune(got)); n > maxEchoedArgsChars {
		t.Errorf("shrunk arguments = %d runes, expected at most %d", n, maxEchoedArgsChars)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("shrunk arguments must stay valid JSON: %v", err)
	}
	if decoded["path"] != "a.md" {
		t.Errorf("expected path a.md, got %q", decoded["path"])
	}

	// Arguments that do not decode were already unusable; they must still be
	// bounded, because they are echoed on every later turn.
	broken := `{"path":"a.md","content":"` + strings.Repeat("y", maxEchoedArgsChars+10)
	if n := len([]rune(shrinkEchoedArgs(broken))); n > maxEchoedArgsChars {
		t.Errorf("undecodable arguments = %d runes, expected at most %d", n, maxEchoedArgsChars)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

// TestHermesBridge_ToolLoop_WriteWithNoWorkingFileIsFlagged: with no file open
// the Desk panel shows "⚠️ Aucun fichier ouvert" while the tools stay armed, so
// a write lands in a course file the teacher is not looking at. Reporting it as
// if it were the working file is how a silent edit happens.
func TestHermesBridge_ToolLoop_WriteWithNoWorkingFileIsFlagged(t *testing.T) {
	tmpDir := t.TempDir()

	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "write_file",
			arguments: `{"path":"surprise.md","content":"# Surprise"}`}}},
		{text: "J'ai créé un fichier."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runHermesJobSub(t, b, "Crée un cours", "desk", deskModeDirect)

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if !hasOutsideWorkingFileEvent(events) {
		t.Errorf("expected the write to be flagged when no working file is announced, got %v", toolEventNames(events))
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "aucun fichier ouvert") {
		t.Errorf("expected the model to be told no file was open, got %v", results)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runDeskDirectJob(t, b, "Écris a.md")

	assertNoErrorEvent(t, events)
	doneEvent(t, events)

	if got := hermes.requestCount(); got != 1 {
		t.Errorf("expected the incomplete call to be dropped without a follow-up request, got %d requests", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "a.md")); err == nil {
		t.Error("an incomplete tool call must not write anything")
	}
	// Dropped is not the same as never happened: silently discarded, the teacher
	// got a done event with an empty reply and no clue why nothing came back.
	if names := toolEventNames(events); len(names) != 1 || names[0] != "write_file:error" {
		t.Fatalf("expected one error tool event for the dropped call, got %v", names)
	}
	var reason string
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && ev.Type == "tool" {
			if s, _ := m["error"].(string); s != "" {
				reason = s
			}
		}
	}
	if !strings.Contains(reason, "finish_reason=length") || !strings.Contains(reason, "rien n'a été exécuté") {
		t.Errorf("expected the drop reason in the tool event, got %q", reason)
	}
}

// TestHermesBridge_ToolLoop_DroppedCallStillCountsAsSupported: the per-job
// diagnostic is how production settles whether the gateway forwards `tools`.
// A dropped call left it saying supported=false, i.e. blamed the gateway for a
// truncation the model produced.
func TestHermesBridge_ToolLoop_DroppedCallStillCountsAsSupported(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := newScriptedHermes([]scriptedTurn{
		{
			toolCalls:    []scriptedToolCall{{id: "call_1", name: "write_file", arguments: `{"path":"a.md","content":"# Coup`}},
			finishReason: "length",
		},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	logged := captureLog(t, func() {
		runDeskDirectJob(t, b, "Écris a.md")
	})

	if !strings.Contains(logged, "hermes: tool_calls supported=true") {
		t.Errorf("expected supported=true for a call the model did emit, got:\n%s", logged)
	}
	if !strings.Contains(logged, "toolCalls=1") {
		t.Errorf("expected the dropped call to be counted (toolCalls=1), got:\n%s", logged)
	}
}

// syncBuffer collects log output while the job goroutine is still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureLog redirects the standard logger while fn runs, then waits for the
// per-job diagnostic line.
//
// The line is written by the job goroutine when callHermesStream returns, which
// happens after the done event has already reached the WebSocket: reading the
// sink straight after fn is a race that fails one run in a few.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	sink := &syncBuffer{}
	prevWriter, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(sink)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	}()

	fn()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(sink.String(), "tool_calls supported=") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return sink.String()
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
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

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	events := runDeskDirectJob(t, b, "Boucle d'écriture")

	assertNoErrorEvent(t, events)
	done := doneEvent(t, events)
	if !strings.Contains(done.Reply, "conservées") {
		t.Errorf("expected the cut message to say the writes already applied are kept, got %q", done.Reply)
	}
}

// --- Mirroring the writes Lya makes with her OWN tools ---
//
// Production trace (v1.10.0, mode Desk, sub-mode « Mise à jour directe »,
// working file Test_folders/test_nvx_cours.md): Lya never called the declared
// read_file / write_file / patch_file. She used her own file tools, which write
// to HER filesystem, and her hermes.tool.progress frame therefore reported the
// ABSOLUTE path /opt/data/Test_folders/test_nvx_cours.md. safePath refuses
// absolute paths, so the write was only logged: the teacher was told the file had
// been updated while the editor and the file tree never changed.

// runMirrorWrite streams one hermes.tool.progress write frame reporting
// reportedPath and returns the events plus the captured log of the job.
func runMirrorWrite(t *testing.T, tmpDir, reportedPath, content, mode string) ([]StreamEvent, string) {
	t.Helper()
	hermes := mockHermesFileWriteServer("write_file", reportedPath, content, "done")
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	var events []StreamEvent
	logged := captureLog(t, func() {
		events = runHermesJob(t, b, "Ajoute une analyse de la politique us", mode)
	})
	return events, logged
}

// assertWorkspaceEmpty fails if anything at all was created under root.
func assertWorkspaceEmpty(t *testing.T, root string) {
	t.Helper()
	var found []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("cannot walk the workspace: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("expected an untouched workspace, found %v", found)
	}
}

func TestHermesBridge_MirrorWrite_AbsoluteHermesPath(t *testing.T) {
	tmpDir := t.TempDir()

	// The exact path of the incident, so the regression stays traceable to it.
	events, logged := runMirrorWrite(t, tmpDir,
		"/opt/data/Test_folders/test_nvx_cours.md", "# Test\n\n## US Politics", "desk")

	assertNoErrorEvent(t, events)
	// The frontend reloads the editor by workspace-relative path: with the
	// Hermes-side path it would not match the open file and nothing would refresh.
	if !hasFileChanged(events, "Test_folders/test_nvx_cours.md") {
		t.Errorf("expected file_changed with the relative path, got %v", toolEventNames(events))
	}
	if hasFileChanged(events, "/opt/data/Test_folders/test_nvx_cours.md") {
		t.Error("file_changed must not carry the Hermes-side absolute path")
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "Test_folders", "test_nvx_cours.md"))
	if err != nil {
		t.Fatalf("expected the write to be mirrored into the workspace: %v", err)
	}
	if string(data) != "# Test\n\n## US Politics" {
		t.Errorf("mirrored content differs, got %q", string(data))
	}
	if !strings.Contains(logged, "mirroredWrites=1 skippedWrites=0") {
		t.Errorf("expected the per-job diagnostic to count the mirrored write, got:\n%s", logged)
	}
}

func TestHermesBridge_MirrorWrite_AbsoluteHomePath(t *testing.T) {
	tmpDir := t.TempDir()

	// /opt/data/home/ must be stripped whole: with /opt/data/ tried first this
	// would land in a spurious "home/" folder of the teacher's workspace.
	events, _ := runMirrorWrite(t, tmpDir, "/opt/data/home/x.md", "# X", "desk")

	assertNoErrorEvent(t, events)
	if !hasFileChanged(events, "x.md") {
		t.Errorf("expected file_changed with path x.md, got %v", toolEventNames(events))
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "x.md")); err != nil || string(data) != "# X" {
		t.Errorf("expected x.md mirrored at the workspace root, got %q / %v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "home", "x.md")); err == nil {
		t.Error("the /opt/data/home/ prefix must be stripped whole, not down to home/x.md")
	}
}

func TestHermesBridge_MirrorWrite_RelativePathUnchanged(t *testing.T) {
	tmpDir := t.TempDir()

	// A relative path is what safePath already accepted: the prefix stripping must
	// not change anything for it.
	events, logged := runMirrorWrite(t, tmpDir, "B1/unit5.md", "# Unit 5", "desk")

	assertNoErrorEvent(t, events)
	if !hasFileChanged(events, "B1/unit5.md") {
		t.Errorf("expected file_changed with path B1/unit5.md, got %v", toolEventNames(events))
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "B1", "unit5.md")); err != nil || string(data) != "# Unit 5" {
		t.Errorf("expected B1/unit5.md mirrored unchanged, got %q / %v", string(data), err)
	}
	if !strings.Contains(logged, "mirroredWrites=1 skippedWrites=0") {
		t.Errorf("expected the mirrored write to be counted, got:\n%s", logged)
	}
}

func TestHermesBridge_MirrorWrite_TraversalUnderKnownPrefix(t *testing.T) {
	tmpDir := t.TempDir()

	// Stripping must never become a way out of the workspace: what is left after
	// /opt/data/ is ../../etc/passwd, and safePath is what refuses it.
	events, logged := runMirrorWrite(t, tmpDir, "/opt/data/../../etc/passwd", "malicious", "desk")

	assertNoErrorEvent(t, events)
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("no file_changed must be emitted for a path escaping the workspace, got %v", m)
		}
	}
	assertWorkspaceEmpty(t, tmpDir)
	if _, err := os.Stat(filepath.Join(tmpDir, "..", "..", "etc", "passwd")); err == nil {
		t.Error("the file must not be written outside the workspace")
	}
	if !strings.Contains(logged, "path escapes the workspace") {
		t.Errorf("expected the escape-specific skip reason, got:\n%s", logged)
	}
	if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=1") {
		t.Errorf("expected the refused write to be counted as skipped, got:\n%s", logged)
	}
}

func TestHermesBridge_MirrorWrite_UnknownPrefixRefused(t *testing.T) {
	tmpDir := t.TempDir()

	// An absolute path we know nothing about stays refused: mapping it onto the
	// workspace would be a guess about someone else's filesystem.
	events, logged := runMirrorWrite(t, tmpDir, "/srv/other/x.md", "# X", "desk")

	assertNoErrorEvent(t, events)
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("no file_changed must be emitted for an unknown prefix, got %v", m)
		}
	}
	assertWorkspaceEmpty(t, tmpDir)
	if !strings.Contains(logged, "unknown Hermes-side prefix") {
		t.Errorf("expected the unknown-prefix skip reason, got:\n%s", logged)
	}
	if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=1") {
		t.Errorf("expected the refused write to be counted as skipped, got:\n%s", logged)
	}
}

func TestHermesBridge_MirrorWrite_DisallowedExtension(t *testing.T) {
	tmpDir := t.TempDir()

	// Inside the workspace is not enough: everything written here is served back
	// to the browser and opened in the Markdown editor.
	events, logged := runMirrorWrite(t, tmpDir, "/opt/data/install.sh", "rm -rf /", "desk")

	assertNoErrorEvent(t, events)
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("no file_changed must be emitted for a refused extension, got %v", m)
		}
	}
	assertWorkspaceEmpty(t, tmpDir)
	if !strings.Contains(logged, "extension not allowed") {
		t.Errorf("expected the extension skip reason, got:\n%s", logged)
	}
	if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=1") {
		t.Errorf("expected the refused write to be counted as skipped, got:\n%s", logged)
	}
}

func TestHermesBridge_MirrorWrite_LyaModeAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Mode Lya has no file access by design, and the new prefix stripping must not
	// have opened one through the absolute paths she reports.
	events, logged := runMirrorWrite(t, tmpDir,
		"/opt/data/Test_folders/test_nvx_cours.md", "# Test", "lya")

	assertNoErrorEvent(t, events)
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("no file_changed must be emitted in mode lya, got %v", m)
		}
	}
	assertWorkspaceEmpty(t, tmpDir)
	if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=0") {
		t.Errorf("mode lya must not even reach the mirroring code, got:\n%s", logged)
	}
}

// TestHermesBridge_MirrorWrite_SkipReasonsAreDistinguishable: both refusals used
// to log the same "file write skipped (unsafe path …)" line, so production logs
// could not tell "Lya's storage moved, add the prefix" from "something tried to
// leave the workspace".
func TestHermesBridge_MirrorWrite_SkipReasonsAreDistinguishable(t *testing.T) {
	_, unknownLog := runMirrorWrite(t, t.TempDir(), "/srv/other/x.md", "# X", "desk")
	_, escapeLog := runMirrorWrite(t, t.TempDir(), "/opt/data/../../etc/passwd", "malicious", "desk")

	if strings.Contains(unknownLog, "path escapes the workspace") {
		t.Errorf("an unknown prefix must not be reported as an escape attempt, got:\n%s", unknownLog)
	}
	if strings.Contains(escapeLog, "unknown Hermes-side prefix") {
		t.Errorf("an escape attempt must not be reported as an unknown prefix, got:\n%s", escapeLog)
	}
	if !strings.Contains(unknownLog, "unknown Hermes-side prefix") {
		t.Errorf("expected the unknown-prefix reason, got:\n%s", unknownLog)
	}
	if !strings.Contains(escapeLog, "path escapes the workspace") {
		t.Errorf("expected the escape reason, got:\n%s", escapeLog)
	}
}

// TestHermesBridge_MirrorWrite_PrefixesFromEnv: where Lya's PVC is mounted is
// deployment knowledge, so a chart that moves it must not need a code change.
func TestHermesBridge_MirrorWrite_PrefixesFromEnv(t *testing.T) {
	t.Setenv(hermesFSPrefixesEnv, "/srv/lya/data/")

	tmpDir := t.TempDir()
	events, _ := runMirrorWrite(t, tmpDir, "/srv/lya/data/B1/unit5.md", "# Unit 5", "desk")
	if !hasFileChanged(events, "B1/unit5.md") {
		t.Errorf("expected the configured prefix to be stripped, got %v", toolEventNames(events))
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "B1", "unit5.md")); err != nil || string(data) != "# Unit 5" {
		t.Errorf("expected B1/unit5.md mirrored, got %q / %v", string(data), err)
	}

	// The env value REPLACES the defaults: an operator who moved the mount and
	// forgot a path must see it refused, not silently written somewhere else.
	otherDir := t.TempDir()
	events, logged := runMirrorWrite(t, otherDir, "/opt/data/B1/unit5.md", "# Unit 5", "desk")
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("the default prefixes must not survive an explicit %s, got %v", hermesFSPrefixesEnv, m)
		}
	}
	assertWorkspaceEmpty(t, otherDir)
	if !strings.Contains(logged, "unknown Hermes-side prefix") {
		t.Errorf("expected the unknown-prefix reason, got:\n%s", logged)
	}
}

func TestParseHermesFSPrefixes(t *testing.T) {
	if got := parseHermesFSPrefixes(""); len(got) != 2 || got[0] != "/opt/data/home/" || got[1] != "/opt/data/" {
		t.Errorf("expected the deployed defaults, longest first, got %v", got)
	}
	if got := parseHermesFSPrefixes("   "); len(got) != 2 || got[0] != "/opt/data/home/" {
		t.Errorf("a blank value must fall back to the defaults, got %v", got)
	}
	// A missing trailing slash would leave an absolute remainder ("/x.md") that
	// safePath refuses, i.e. a silent no-op at the other end of the pipe.
	if got := parseHermesFSPrefixes("/srv/lya, /srv/other/"); len(got) != 2 ||
		got[0] != "/srv/lya/" || got[1] != "/srv/other/" {
		t.Errorf("expected trimmed prefixes with a trailing slash, got %v", got)
	}
	// A relative entry could only match by mangling a relative path.
	if got := parseHermesFSPrefixes("opt/data/,/srv/lya/"); len(got) != 1 || got[0] != "/srv/lya/" {
		t.Errorf("expected the non-absolute entry to be dropped, got %v", got)
	}
	if got := parseHermesFSPrefixes("opt/data/"); len(got) != 2 || got[0] != "/opt/data/home/" {
		t.Errorf("a list with nothing usable must fall back to the defaults, got %v", got)
	}
}

// --- SSE instrumentation (HERMES_TRACE_EVENTS + always-on per-job summary) ---
//
// Why this exists: v1.9.0 (write interception), v1.10.0 (declared tools) and
// v1.10.1 (Hermes-side path mapping) were all written against an ASSUMED
// tool-frame shape, and all three failed in production the same way — Lya says
// "c'est fait", nothing appears in the workspace, and the logs cannot say whether
// a tool frame ever arrived. These tests cover the instrumentation that makes the
// next fix evidence-based, not a fourth guess.

// mockHermesEventServer streams ONE SSE frame under eventName carrying payload,
// then data: [DONE].
//
// Unlike mockHermesFileWriteServer it fixes neither the event name nor the
// payload keys: that is what lets a test describe a frame whose shape the bridge
// does NOT expect, which is the case production keeps hitting.
func mockHermesEventServer(eventName string, payload map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/chat/completions" {
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
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, string(data))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// runFrameJob drives one job whose Hermes answer is a single frame under
// eventName, and returns the captured log plus the events.
func runFrameJob(t *testing.T, tmpDir, eventName, mode string, payload map[string]interface{}) (string, []StreamEvent) {
	t.Helper()
	hermes := mockHermesEventServer(eventName, payload)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	var events []StreamEvent
	logged := captureLog(t, func() {
		events = runHermesJob(t, b, "Ajoute une ligne dans Test_folders/test_nvx_cours.md", mode)
	})
	return logged, events
}

// captureLogSync redirects the standard logger while fn runs and returns what was
// written. Unlike captureLog it waits for nothing, so it is for code that logs
// synchronously (a parser), not for a job goroutine.
func captureLogSync(t *testing.T, fn func()) string {
	t.Helper()
	sink := &syncBuffer{}
	prevWriter, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(sink)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	}()
	fn()
	return sink.String()
}

// writeFramePayload is the shape v1.9.0 assumed and the only one that mirrors
// today: it is used as the baseline the rejection cases deviate from.
func writeFramePayload() map[string]interface{} {
	return map[string]interface{}{
		"name":    "write_file",
		"path":    "/opt/data/Test_folders/test_nvx_cours.md",
		"content": "# Test\n\nLigne ajoutée pour tester l'écriture dans le fichier.",
		"status":  "done",
	}
}

// TestHermesTraceEnvVarName: the flag name is a contract with .env.example and
// with the README command an operator copies into a terminal.
func TestHermesTraceEnvVarName(t *testing.T) {
	if hermesTraceEventsEnv != "HERMES_TRACE_EVENTS" {
		t.Errorf("the documented flag is HERMES_TRACE_EVENTS, got %q — .env.example and the README point at the old name", hermesTraceEventsEnv)
	}
	if hermesToolProgressEvent != "hermes.tool.progress" {
		t.Errorf("expected the acted-upon event name to stay hermes.tool.progress, got %q", hermesToolProgressEvent)
	}
}

func TestParseHermesTraceEvents(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{" true ", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"", false},
		{"false", false},
		{"0", false},
		{"off", false},
	} {
		if got := parseHermesTraceEvents(tc.raw); got != tc.want {
			t.Errorf("parseHermesTraceEvents(%q) = %t, want %t", tc.raw, got, tc.want)
		}
	}
	// An unrecognised value must not switch tracing on AND must not be silent: an
	// operator who mistyped it would otherwise wait for frames that never come.
	var got bool
	logged := captureLogSync(t, func() { got = parseHermesTraceEvents("yes-please") })
	if got {
		t.Error("an unrecognised value must leave tracing off")
	}
	if !strings.Contains(logged, `hermes: ignoring HERMES_TRACE_EVENTS="yes-please"`) {
		t.Errorf("expected the ignored value to be reported, got:\n%s", logged)
	}
}

// TestHermesBridge_Trace_FrameLoggedWhenEnabled: the raw payload of every frame
// must be readable in the logs, since its KEYS are what three releases guessed.
func TestHermesBridge_Trace_FrameLoggedWhenEnabled(t *testing.T) {
	t.Setenv(hermesTraceEventsEnv, "true")

	logged, _ := runFrameJob(t, t.TempDir(), hermesToolProgressEvent, "desk", writeFramePayload())

	if !strings.Contains(logged, "hermes_trace: event=hermes.tool.progress data=") {
		t.Errorf("expected the frame traced under its event name, got:\n%s", logged)
	}
	// The payload itself, not a summary of it: the path Lya reports is the fact
	// v1.10.1 was built on.
	if !strings.Contains(logged, `"path":"/opt/data/Test_folders/test_nvx_cours.md"`) {
		t.Errorf("expected the raw data payload in the trace, got:\n%s", logged)
	}
	// A frame with no event: line is traced as <none>, so "the stream ended" is
	// visible too — a job that produced nothing at all still leaves a trace.
	if !strings.Contains(logged, "hermes_trace: event=<none> data=[DONE]") {
		t.Errorf("expected the [DONE] frame traced with event=<none>, got:\n%s", logged)
	}
}

// TestHermesBridge_Trace_OffByDefault: the trace logs the teacher's course
// content, so it must stay off until an operator asks for it.
func TestHermesBridge_Trace_OffByDefault(t *testing.T) {
	t.Setenv(hermesTraceEventsEnv, "")

	logged, _ := runFrameJob(t, t.TempDir(), hermesToolProgressEvent, "desk", writeFramePayload())

	if strings.Contains(logged, "hermes_trace:") {
		t.Errorf("no frame must be traced without %s, got:\n%s", hermesTraceEventsEnv, logged)
	}
	// Not a vacuous assertion: the job DID run and the always-on lines are there.
	if !strings.Contains(logged, "hermes: tool frame seen (name=\"write_file\"") {
		t.Errorf("expected the always-on tool frame line, got:\n%s", logged)
	}
	if !strings.Contains(logged, "toolProgressFrames=1") {
		t.Errorf("expected the always-on summary to count the frame, got:\n%s", logged)
	}
}

// mockHermesResponsesServer streams a realistic Responses-API turn: one
// function_call carrying COMPLETE arguments (path+content), a text delta and
// the terminal response.completed — exactly what production Lya sends on
// POST /v1/responses (and what hermes.tool.progress NEVER carried).
func mockHermesResponsesServer(filePath, content string, pathFound bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
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

		// Responses payload structs per the gateway writer (api_server.py).
		type respItem struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Arguments string `json:"arguments"`
		}
		argsMap := map[string]interface{}{"content": content}
		if pathFound {
			argsMap["path"] = filePath
		}
		argsJSON, _ := json.Marshal(argsMap)

		// 1. function_call added (in_progress) — mirrors the WRITE carrying args
		added := map[string]interface{}{
			"type": "response.output_item.added",
			"item": respItem{Type: "function_call", Name: "write_file", Status: "in_progress", Arguments: string(argsJSON)},
		}
		b, _ := json.Marshal(added)
		fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(b))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)

		// 2. text delta
		delta := map[string]interface{}{"type": "response.output_text.delta", "delta": "Ligne ajoutée. ✅"}
		b, _ = json.Marshal(delta)
		fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", string(b))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)

		// 3. function_call done (completed) — same args, must NOT double-mirror
		done := map[string]interface{}{
			"type": "response.output_item.done",
			"item": respItem{Type: "function_call", Name: "write_file", Status: "completed", Arguments: string(argsJSON)},
		}
		b, _ = json.Marshal(done)
		fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(b))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)

		// 4. terminal — the Responses API has no data: [DONE]
		completed := map[string]interface{}{"type": "response.completed"}
		b, _ = json.Marshal(completed)
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(b))
		flusher.Flush()
	}))
}

// TestHermesBridge_ResponsesAPI_MirrorsWrite is the regression test for the bug
// that survived v1.9.0–v1.10.1: Lya says she wrote the file, nothing changes.
// chat.completions frames never carried path/content; the Responses API does,
// in response.output_item.added.arguments — and that is what must land in the
// workspace.
func TestHermesBridge_ResponsesAPI_MirrorsWrite(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := mockHermesResponsesServer("/opt/data/Test_folders/test_nvx_cours.md", "# Test\n\nLigne ajoutée.", true)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	logged := captureLog(t, func() {
		runHermesJob(t, b, "Ajoute une ligne dans Test_folders/test_nvx_cours.md", "desk")
	})

	// The write must have been mirrored into the workspace, stripped of the
	// /opt/data/ prefix (stripHermesFSPrefix + safePath).
	rel := filepath.Join("Test_folders", "test_nvx_cours.md")
	data, err := os.ReadFile(filepath.Join(tmpDir, rel))
	if err != nil {
		t.Fatalf("expected the mirrored file at %s: %v\nlogs:\n%s", rel, err, logged)
	}
	if string(data) != "# Test\n\nLigne ajoutée." {
		t.Errorf("mirrored content mismatch: %q", string(data))
	}
	if !strings.Contains(logged, "mirroredWrites=1") {
		t.Errorf("expected the diagnostic to count one mirrored write, got:\n%s", logged)
	}
	// The "done" event must NOT mirror a second copy.
	if strings.Contains(logged, "mirroredWrites=2") {
		t.Errorf("the completed function_call must not double-mirror, got:\n%s", logged)
	}
	// The terminal event must be visible as a clean end (no [DONE] in Responses).
	if !strings.Contains(logged, "sawDone") && !strings.Contains(logged, "status=done") {
		t.Logf("note: sawDone appears in per-turn state, not the summary — log tail:\n%s", logged)
	}
}

// TestHermesBridge_ResponsesAPI_RejectsFramelessWrite: if the function_call
// arguments carry no path, the mirror must be refused loudly, not silently.
func TestHermesBridge_ResponsesAPI_RejectsFramelessWrite(t *testing.T) {
	tmpDir := t.TempDir()
	hermes := mockHermesResponsesServer("", "contenu sans chemin", false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, nil)
	logged := captureLog(t, func() {
		runHermesJob(t, b, "Écris un fichier", "desk")
	})

	assertWorkspaceEmpty(t, tmpDir)
	if !strings.Contains(logged, "reported no path") {
		t.Errorf("expected the no-path rejection logged with the reason, got:\n%s", logged)
	}
}

// TestHermesBridge_Summary_NoToolFrameAtAll: the production case. Lya answered in
// text, no tool frame was ever sent, and the log must say so with a number
// instead of leaving the absence invisible.
func TestHermesBridge_Summary_NoToolFrameAtAll(t *testing.T) {
	hermes := mockHermesServer() // three text deltas, then [DONE]
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), nil)
	logged := captureLog(t, func() {
		runHermesJob(t, b, "Ajoute une ligne dans Test_folders/test_nvx_cours.md", "desk")
	})

	if !strings.Contains(logged, "sseEventFrames=0 sseEventNames=[] toolProgressFrames=0 sseChunkFrames=3") {
		t.Errorf("expected the summary to report zero event frames and the three text deltas, got:\n%s", logged)
	}
	if strings.Contains(logged, "hermes: tool frame") {
		t.Errorf("no tool frame arrived, so no tool frame line must be logged, got:\n%s", logged)
	}
}

// TestHermesBridge_Summary_RejectedFrameDiffersFromNoFrame is the heart of this
// change: "no frame arrived" and "a frame arrived and was rejected" produced the
// same silence for three releases, and the teacher's symptom is identical in both
// cases.
func TestHermesBridge_Summary_RejectedFrameDiffersFromNoFrame(t *testing.T) {
	noFrame := mockHermesServer()
	defer noFrame.Close()
	bNoFrame := NewHermesBridge(noFrame.URL, "test-key", t.TempDir(), nil)
	noFrameLog := captureLog(t, func() {
		runHermesJob(t, bNoFrame, "Ajoute une ligne", "desk")
	})

	rejected := writeFramePayload()
	rejected["status"] = "running"
	rejectedLog, _ := runFrameJob(t, t.TempDir(), hermesToolProgressEvent, "desk", rejected)

	// Same outcome for the workspace…
	for _, logged := range []string{noFrameLog, rejectedLog} {
		if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=0") {
			t.Errorf("expected no write in either case, got:\n%s", logged)
		}
	}
	// …and now distinguishable causes.
	if !strings.Contains(noFrameLog, "toolProgressFrames=0") {
		t.Errorf("expected toolProgressFrames=0 when nothing arrived, got:\n%s", noFrameLog)
	}
	if !strings.Contains(rejectedLog, "toolProgressFrames=1") {
		t.Errorf("expected toolProgressFrames=1 for a frame that arrived, got:\n%s", rejectedLog)
	}
	if !strings.Contains(rejectedLog, `reported status "running", not "done"`) {
		t.Errorf("expected the rejecting guard to be named, got:\n%s", rejectedLog)
	}
	if strings.Contains(noFrameLog, "not mirrored") {
		t.Errorf("nothing arrived, so no guard can have rejected anything, got:\n%s", noFrameLog)
	}
}

// TestKnownWriteToolNames pins the allowlist the rejection line advertises. It
// fails on purpose when a name is added: adding one is only legitimate once a
// real frame, captured with HERMES_TRACE_EVENTS, has shown that name.
func TestKnownWriteToolNames(t *testing.T) {
	if got := knownWriteToolNames(); got != "create_file,edit,edit_file,write,write_file" {
		t.Errorf("expected the sorted allowlist, got %q", got)
	}
}

// TestHermesBridge_ToolFrame_RejectionReasonsAreDistinct: each branch of
// handleToolFileWrite that refuses to mirror must log its OWN greppable reason,
// naming the tool as it was seen. Four of these branches used to `return ""` in
// silence — that silence is what hid the bug across three releases.
func TestHermesBridge_ToolFrame_RejectionReasonsAreDistinct(t *testing.T) {
	cases := []struct {
		label   string
		payload map[string]interface{}
		want    string
	}{
		{
			label: "unknown tool name",
			payload: map[string]interface{}{
				"name": "bash", "path": "/opt/data/x.md", "content": "# X", "status": "done",
			},
			want: `hermes: tool frame not mirrored (tool name "bash" is not a known write tool`,
		},
		{
			label: "no tool name at all",
			payload: map[string]interface{}{
				"path": "/opt/data/x.md", "content": "# X", "status": "done",
			},
			want: `hermes: tool frame not mirrored (tool name "" is not a known write tool`,
		},
		{
			label: "status not done",
			payload: map[string]interface{}{
				"name": "write_file", "path": "/opt/data/x.md", "content": "# X", "status": "running",
			},
			want: `hermes: tool frame not mirrored (write tool "write_file" reported status "running", not "done")`,
		},
		{
			label: "missing path",
			payload: map[string]interface{}{
				"name": "write_file", "content": "# X", "status": "done",
			},
			want: `hermes: tool frame not mirrored (write tool "write_file" reported no path, keys seen: [content,name,status])`,
		},
		{
			label: "missing content",
			payload: map[string]interface{}{
				"name": "write_file", "path": "/opt/data/x.md", "status": "done",
			},
			want: `hermes: tool frame not mirrored (write tool "write_file" reported no content for path "/opt/data/x.md", keys seen: [name,path,status])`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			tmpDir := t.TempDir()
			logged, events := runFrameJob(t, tmpDir, hermesToolProgressEvent, "desk", tc.payload)

			assertNoErrorEvent(t, events)
			assertWorkspaceEmpty(t, tmpDir)
			if !strings.Contains(logged, tc.want) {
				t.Errorf("expected the reason %q, got:\n%s", tc.want, logged)
			}
			// The frame arrived: that fact must be in the always-on summary whatever
			// the guard that rejected it.
			if !strings.Contains(logged, "toolProgressFrames=1") {
				t.Errorf("expected the arrived frame to be counted, got:\n%s", logged)
			}
			// Distinct, not interchangeable: one reason per branch.
			for _, other := range cases {
				if other.label == tc.label {
					continue
				}
				if strings.Contains(logged, other.want) {
					t.Errorf("the %q branch also logged the %q reason:\n%s", tc.label, other.label, logged)
				}
			}
		})
	}
}

// TestHermesBridge_ToolFrame_FactsAreReported: what was extracted from the frame,
// on one always-on line, whatever the outcome.
func TestHermesBridge_ToolFrame_FactsAreReported(t *testing.T) {
	logged, _ := runFrameJob(t, t.TempDir(), hermesToolProgressEvent, "desk", map[string]interface{}{
		"name": "bash", "status": "done", "command": "echo hi",
	})

	want := `hermes: tool frame seen (name="bash" status="done" pathFound=false contentFound=false keys=[command,name,status])`
	if !strings.Contains(logged, want) {
		t.Errorf("expected %q, got:\n%s", want, logged)
	}
}

func TestPayloadKeys(t *testing.T) {
	// Keys only, sorted: the value of "content" is the teacher's whole file and
	// this line is always on.
	got := payloadKeys(map[string]interface{}{
		"name":    "write_file",
		"content": "contenu confidentiel du cours",
	})
	if got != "[content,name]" {
		t.Errorf("expected [content,name], got %q", got)
	}
	if strings.Contains(got, "contenu confidentiel") {
		t.Errorf("payloadKeys must never log a value, got %q", got)
	}

	// Bounded: 14 keys k01…k14 give the first 12 plus a count of the rest.
	many := map[string]interface{}{}
	for i := 1; i <= 14; i++ {
		many[fmt.Sprintf("k%02d", i)] = i
	}
	if got := payloadKeys(many); got != "[k01,k02,k03,k04,k05,k06,k07,k08,k09,k10,k11,k12,+2]" {
		t.Errorf("expected the bounded sorted key list, got %q", got)
	}
	if got := payloadKeys(map[string]interface{}{}); got != "[]" {
		t.Errorf("expected [] for an empty payload, got %q", got)
	}
}

func TestSSEObservationSummary(t *testing.T) {
	if got := (&sseObservation{}).summary(); got != "sseEventFrames=0 sseEventNames=[] toolProgressFrames=0 sseChunkFrames=0" {
		t.Errorf("unexpected empty summary: %q", got)
	}

	var o sseObservation
	// "f" twice: a distinct name beyond the cap must be counted once, not once per
	// frame, or the overflow count would report frames instead of names.
	for _, name := range []string{"a", "a", "b", "c", "d", "e", "f", "f", "g"} {
		o.noteEventFrame(name)
	}
	o.noteEventFrame(hermesToolProgressEvent)
	o.chunkFrames = 3

	want := "sseEventFrames=10 sseEventNames=[a,b,c,d,e,+3] toolProgressFrames=1 sseChunkFrames=3"
	if got := o.summary(); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestTruncateTraceData(t *testing.T) {
	if hermesTraceMaxDataChars != 2000 {
		t.Fatalf("the documented trace cap is 2000 characters, got %d — .env.example and the README say 2000", hermesTraceMaxDataChars)
	}
	// Exactly at the cap: kept whole, no marker.
	exact := strings.Repeat("a", 2000)
	if got := truncateTraceData(exact); got != exact {
		t.Errorf("a payload of exactly 2000 chars must be kept whole, got %d chars with suffix %q",
			len([]rune(got)), got[len(got)-40:])
	}
	// Accented text, because the payloads are French course content: cutting on
	// bytes would leave a broken character in the logs.
	long := strings.Repeat("é", 2500)
	got := truncateTraceData(long)
	const marker = "…[truncated: 2000 of 2500 chars]"
	if !strings.HasSuffix(got, marker) {
		t.Errorf("expected the suffix %q, got the last 60 chars %q", marker, string([]rune(got)[len([]rune(got))-60:]))
	}
	if kept := len([]rune(strings.TrimSuffix(got, marker))); kept != 2000 {
		t.Errorf("expected 2000 characters kept, got %d", kept)
	}
	if !utf8.ValidString(got) {
		t.Error("the truncated payload must stay valid UTF-8")
	}
}

// TestHermesBridge_Trace_PayloadTruncated: end to end, the cap really applies to
// what reaches the log — a whole course file must not be dumped in full.
func TestHermesBridge_Trace_PayloadTruncated(t *testing.T) {
	t.Setenv(hermesTraceEventsEnv, "true")

	payload := writeFramePayload()
	payload["content"] = strings.Repeat("A", 2500) + "QUEUE_DU_FICHIER"

	logged, _ := runFrameJob(t, t.TempDir(), hermesToolProgressEvent, "desk", payload)

	if !strings.Contains(logged, "…[truncated: 2000 of ") {
		t.Errorf("expected the truncation marker in the trace, got the first 300 chars:\n%s", logged[:min(len(logged), 300)])
	}
	if strings.Contains(logged, "QUEUE_DU_FICHIER") {
		t.Error("the tail of an oversized payload must not reach the logs")
	}
}

// TestHermesBridge_Trace_LyaModeStillMirrorsNothing: the instrumentation observes,
// it must not open a file path. Mode Lya has no file access by design.
func TestHermesBridge_Trace_LyaModeStillMirrorsNothing(t *testing.T) {
	t.Setenv(hermesTraceEventsEnv, "true")
	tmpDir := t.TempDir()

	logged, events := runFrameJob(t, tmpDir, hermesToolProgressEvent, "lya", writeFramePayload())

	assertNoErrorEvent(t, events)
	assertWorkspaceEmpty(t, tmpDir)
	for _, ev := range events {
		if m, ok := ev.Tool.(map[string]interface{}); ok && m["name"] == "file_changed" {
			t.Errorf("no file_changed must be emitted in mode lya, got %v", m)
		}
	}
	if !strings.Contains(logged, "mirroredWrites=0 skippedWrites=0") {
		t.Errorf("mode lya must write nothing, got:\n%s", logged)
	}
	// The frame is still observed — tracing is deliberately mode-independent, it is
	// the mirroring that is gated.
	if !strings.Contains(logged, "toolProgressFrames=1") {
		t.Errorf("expected the frame to be counted in mode lya too, got:\n%s", logged)
	}
	if strings.Contains(logged, "hermes: tool frame seen") {
		t.Errorf("mode lya must not reach the mirroring code at all, got:\n%s", logged)
	}
}

// TestHermesBridge_Trace_MirroredWriteStillWorks: the added logging must not
// change the one path that does work today.
func TestHermesBridge_Trace_MirroredWriteStillWorks(t *testing.T) {
	t.Setenv(hermesTraceEventsEnv, "true")
	tmpDir := t.TempDir()

	logged, events := runFrameJob(t, tmpDir, hermesToolProgressEvent, "desk", writeFramePayload())

	assertNoErrorEvent(t, events)
	if !hasFileChanged(events, "Test_folders/test_nvx_cours.md") {
		t.Errorf("expected file_changed with the relative path, got %v", toolEventNames(events))
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "Test_folders", "test_nvx_cours.md"))
	if err != nil {
		t.Fatalf("expected the write to be mirrored: %v", err)
	}
	if !strings.Contains(string(data), "Ligne ajoutée pour tester l'écriture dans le fichier.") {
		t.Errorf("unexpected mirrored content: %q", string(data))
	}
	if !strings.Contains(logged, "mirroredWrites=1 skippedWrites=0 sseEventFrames=1 sseEventNames=[hermes.tool.progress] toolProgressFrames=1") {
		t.Errorf("expected the summary to report the mirrored frame, got:\n%s", logged)
	}
}

// --- web_search (Brave) ------------------------------------------------------

// mockBrave serves one canned Brave answer and counts the calls, so a test can
// prove the search ran exactly once rather than per turn.
type mockBrave struct {
	*httptest.Server
	mu      sync.Mutex
	calls   int
	queries []string
	status  int
}

func newMockBrave(status int) *mockBrave {
	m := &mockBrave{status: status}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.calls++
		m.queries = append(m.queries, r.URL.Query().Get("q"))
		status := m.status
		m.mu.Unlock()

		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var resp braveResponse
		resp.Web.Results = []braveWebResult{{
			Title:       "Present perfect exercises",
			URL:         "https://example.com/present-perfect",
			Description: "Exercices gradués sur le present perfect.",
		}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	return m
}

func (m *mockBrave) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockBrave) client() *BraveSearch {
	return &BraveSearch{apiKey: "brave-test-key", timeout: 5 * time.Second, endpoint: m.URL}
}

// searchCall is the scripted tool call a model makes to search the web.
func searchCall(id, query string) scriptedToolCall {
	args, _ := json.Marshal(map[string]string{"query": query})
	return scriptedToolCall{id: id, name: "web_search", arguments: string(args)}
}

// web_search is read-only, so unlike the file tools it is offered in EVERY mode:
// a teacher asking a question in mode Lya has as much use for a fact-check as one
// editing a file in Desk.
func TestHermesBridge_WebSearch_DeclaredInEveryMode(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		deskMode      string
		wantFileTools bool
	}{
		{"desk direct", "desk", deskModeDirect, true},
		{"desk insert", "desk", deskModeInsert, false},
		{"desk legacy", "desk", "", false},
		{"lya", "lya", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brave := newMockBrave(http.StatusOK)
			defer brave.Close()
			hermes := newScriptedHermes([]scriptedTurn{{text: "Réponse."}}, false)
			defer hermes.Close()

			b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), brave.client())
			events := runHermesJobSub(t, b, "une question", tc.mode, tc.deskMode)
			assertNoErrorEvent(t, events)

			declared := declaredToolNames(t, hermes.request(t, 0))
			if !slicesContains(declared, "web_search") {
				t.Errorf("web_search must be declared in mode %s/%s, got %v", tc.mode, tc.deskMode, declared)
			}
			hasFileTools := slicesContains(declared, "write_file")
			if hasFileTools != tc.wantFileTools {
				t.Errorf("file tools declared = %t, want %t (mode %s/%s): %v",
					hasFileTools, tc.wantFileTools, tc.mode, tc.deskMode, declared)
			}
		})
	}
}

// No key, no tool: declaring web_search without a client would have the model
// call a tool that can only answer "indisponible".
func TestHermesBridge_WebSearch_NotDeclaredWithoutKey(t *testing.T) {
	hermes := newScriptedHermes([]scriptedTurn{{text: "Réponse."}}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), nil)
	assertNoErrorEvent(t, runHermesJobSub(t, b, "une question", "lya", ""))

	req := hermes.request(t, 0)
	if _, ok := req["tools"]; ok {
		t.Errorf("no tool must be declared in mode Lya without a Brave key, got %v", declaredToolNames(t, req))
	}
}

// The whole point: the search result must come back in the tool message of the
// NEXT request, which is the only way the model can use it.
func TestHermesBridge_WebSearch_ResultReachesTheModel(t *testing.T) {
	brave := newMockBrave(http.StatusOK)
	defer brave.Close()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{searchCall("call_1", "present perfect exercises")}},
		{text: "Voici des exercices."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), brave.client())
	events := runDeskDirectJob(t, b, "trouve des exercices")
	assertNoErrorEvent(t, events)

	if got := brave.callCount(); got != 1 {
		t.Errorf("expected exactly 1 Brave call, got %d", got)
	}
	if q := brave.queries[0]; q != "present perfect exercises" {
		t.Errorf("query sent to Brave = %q", q)
	}

	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result fed back, got %d", len(results))
	}
	for _, want := range []string{"Present perfect exercises", "https://example.com/present-perfect"} {
		if !strings.Contains(results[0], want) {
			t.Errorf("expected the tool result to contain %q, got:\n%s", want, results[0])
		}
	}

	// The teacher sees the search happen, like any other tool: running then done.
	names := toolEventNames(events)
	for _, want := range []string{"web_search:running", "web_search:done"} {
		if !slicesContains(names, want) {
			t.Errorf("expected a %q tool event in the thread, got %v", want, names)
		}
	}
	// A search changes no file, so no editor reload must be triggered.
	if slicesContains(names, "file_changed") {
		t.Errorf("web_search must not emit file_changed, got %v", names)
	}

	// The query travels in the event, under "query" and NOT under "path": the UI
	// labels the search with its terms, and Chat.tsx keys the editor reload off
	// path — a query there would make a search look like a file operation.
	var sawQuery bool
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		m, ok := ev.Tool.(map[string]interface{})
		if !ok || m["name"] != "web_search" {
			continue
		}
		if m["query"] == "present perfect exercises" {
			sawQuery = true
		}
		if p, _ := m["path"].(string); p != "" {
			t.Errorf("web_search event must carry no path, got %q", p)
		}
	}
	if !sawQuery {
		t.Error("no web_search event carried the query the model asked for")
	}
}

// In mode Lya the file tools are not declared. A model that asks for one anyway
// must still be refused — while web_search, which IS declared, goes through.
func TestHermesBridge_WebSearch_AllowedInLyaModeButWriteStillRefused(t *testing.T) {
	brave := newMockBrave(http.StatusOK)
	defer brave.Close()
	tmpDir := t.TempDir()

	writeArgs, _ := json.Marshal(map[string]string{"path": "interdit.md", "content": "x"})
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{
			searchCall("call_1", "irregular verbs"),
			{id: "call_2", name: "write_file", arguments: string(writeArgs)},
		}},
		{text: "Voici ce que j'ai trouvé."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", tmpDir, brave.client())
	events := runHermesJobSub(t, b, "cherche puis écris", "lya", "")
	assertNoErrorEvent(t, events)

	if brave.callCount() != 1 {
		t.Errorf("expected the search to run in mode Lya, Brave calls = %d", brave.callCount())
	}
	// The refused write must not have touched the workspace.
	if _, err := os.Stat(filepath.Join(tmpDir, "interdit.md")); !os.IsNotExist(err) {
		t.Error("write_file was executed in mode Lya: the file exists")
	}
	// Exactly one tool result: the search. The write was dropped, not answered.
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 {
		t.Fatalf("expected only the search result to be fed back, got %d: %v", len(results), results)
	}
	if !strings.Contains(results[0], "Present perfect exercises") {
		t.Errorf("the surviving tool result is not the search result: %s", results[0])
	}
}

// A Brave outage is a tool result, never a job error: the teacher must still get
// an answer, and the model must be told to fall back to what it knows.
func TestHermesBridge_WebSearch_BraveFailureIsNotAJobError(t *testing.T) {
	brave := newMockBrave(http.StatusTooManyRequests)
	defer brave.Close()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{searchCall("call_1", "quota")}},
		{text: "Je n'ai pas pu chercher, voici ce que je sais."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), brave.client())
	events := runDeskDirectJob(t, b, "cherche")
	assertNoErrorEvent(t, events)

	if lastType(events) != "done" {
		t.Errorf("a Brave failure must not end the job in error, last event = %s", lastType(events))
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 {
		t.Fatalf("expected the failure to be fed back as a tool result, got %d", len(results))
	}
	if !strings.Contains(results[0], "Cherche par toi-même") {
		t.Errorf("expected the fallback instruction in the tool result, got:\n%s", results[0])
	}
}

// A tool call with no query is a model error, and it must come back as a tool
// error the model can correct — not as a Brave request with an empty q.
func TestHermesBridge_WebSearch_MissingQueryIsAToolError(t *testing.T) {
	brave := newMockBrave(http.StatusOK)
	defer brave.Close()
	hermes := newScriptedHermes([]scriptedTurn{
		{toolCalls: []scriptedToolCall{{id: "call_1", name: "web_search", arguments: `{"q":"mauvaise clé"}`}}},
		{text: "Corrigé."},
	}, false)
	defer hermes.Close()

	b := NewHermesBridge(hermes.URL, "test-key", t.TempDir(), brave.client())
	events := runDeskDirectJob(t, b, "cherche")
	assertNoErrorEvent(t, events)

	if brave.callCount() != 0 {
		t.Errorf("no Brave call must be made without a query, got %d", brave.callCount())
	}
	results := toolResultContents(t, hermes.request(t, 1))
	if len(results) != 1 || !strings.Contains(results[0], "query") {
		t.Errorf("expected a tool error naming the missing parameter, got %v", results)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
