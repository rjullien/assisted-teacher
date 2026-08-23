package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	bridge := NewHermesBridge(hermes.URL, "test-key")
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

	bridge := NewHermesBridge(hermes.URL, "test-key")
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

	bridge := NewHermesBridge(hermes.URL, "wrong-key")
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
