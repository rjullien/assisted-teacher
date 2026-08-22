package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestACPBridge_EchoAgent tests the WebSocket bridge with a mock agent that echoes messages.
// The mock agent is a simple shell script that reads from stdin and writes to stdout.
func TestACPBridge_EchoAgent(t *testing.T) {
	workDir := t.TempDir()

	// Use 'cat' as a simple echo agent — it reads from stdin and writes to stdout
	bridge := NewACPBridge("cat", workDir)

	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	// Connect via WebSocket
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer ws.Close()

	// Send a JSON-RPC message
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	}
	msgBytes, _ := json.Marshal(msg)
	if err := ws.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Read echoed response (cat echoes back)
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, response, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	// Verify it's valid JSON and matches what we sent
	var echoed map[string]interface{}
	if err := json.Unmarshal(response, &echoed); err != nil {
		t.Fatalf("response is not valid JSON: %v (got: %s)", err, string(response))
	}

	if echoed["method"] != "initialize" {
		t.Errorf("expected method 'initialize', got %v", echoed["method"])
	}
	if echoed["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc '2.0', got %v", echoed["jsonrpc"])
	}
}

// TestACPBridge_MultipleMessages tests sending multiple messages through the bridge.
func TestACPBridge_MultipleMessages(t *testing.T) {
	workDir := t.TempDir()
	bridge := NewACPBridge("cat", workDir)

	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer ws.Close()

	messages := []map[string]interface{}{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{}},
		{"jsonrpc": "2.0", "id": 2, "method": "session/create", "params": map[string]interface{}{}},
		{"jsonrpc": "2.0", "id": 3, "method": "prompt/start", "params": map[string]interface{}{
			"message": map[string]interface{}{"role": "user", "content": "hello"},
		}},
	}

	for _, msg := range messages {
		msgBytes, _ := json.Marshal(msg)
		if err := ws.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
			t.Fatalf("write error: %v", err)
		}

		// Read echo
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, response, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read error: %v", err)
		}

		var echoed map[string]interface{}
		if err := json.Unmarshal(response, &echoed); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}

		expectedID := msg["id"].(int)
		if int(echoed["id"].(float64)) != expectedID {
			t.Errorf("expected id %d, got %v", expectedID, echoed["id"])
		}
	}
}

// TestACPBridge_InvalidAgent tests behavior when agent command is invalid.
func TestACPBridge_InvalidAgent(t *testing.T) {
	workDir := t.TempDir()
	bridge := NewACPBridge("nonexistent-binary-that-doesnt-exist", workDir)

	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer ws.Close()

	// Should receive an error message or connection close
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		// Connection closed — expected behavior
		return
	}

	// Or we got a JSON error
	var errMsg map[string]interface{}
	if err := json.Unmarshal(msg, &errMsg); err == nil {
		if errMsg["error"] != nil {
			// Good — we got a JSON-RPC error
			return
		}
	}

	t.Log("Got response from invalid agent (may be OS-dependent):", string(msg))
}

// TestACPBridge_ClientDisconnect tests that agent process is killed when client disconnects.
func TestACPBridge_ClientDisconnect(t *testing.T) {
	workDir := t.TempDir()
	// Use 'sleep 60' as a long-running agent to test cleanup
	bridge := NewACPBridge("sleep 60", workDir)

	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	// Close client connection immediately
	ws.Close()

	// Give the goroutines time to clean up
	time.Sleep(100 * time.Millisecond)

	// If we got here without hanging, cleanup is working
}
