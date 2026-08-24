package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
