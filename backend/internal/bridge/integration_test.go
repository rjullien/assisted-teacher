package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// buildMockAgent compiles the mock ACP agent and returns the path to the binary.
func buildMockAgent(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "mock-acp-agent")
	cmd := exec.Command("go", "build", "-o", binPath, "./mockagent")
	cmd.Dir = filepath.Join(getModuleRoot(t), "internal", "bridge")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build mock agent: %v\n%s", err, string(output))
	}
	return binPath
}

// getModuleRoot returns the root directory of the Go module.
func getModuleRoot(t *testing.T) string {
	t.Helper()
	// We're in internal/bridge, go up two levels
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// wd = .../backend/internal/bridge
	return filepath.Dir(filepath.Dir(wd))
}

// TestIntegration_MockAgent_Initialize tests the full flow: WebSocket → bridge → mock agent → initialize response.
func TestIntegration_MockAgent_Initialize(t *testing.T) {
	agentBin := buildMockAgent(t)
	workDir := t.TempDir()

	bridge := NewACPBridge(agentBin, workDir)
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	ws := dialWS(t, server.URL)
	defer ws.Close()

	// Send initialize
	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	})

	// Read response
	resp := readJSON(t, ws)

	// Verify
	if resp["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
	}
	if int(resp["id"].(float64)) != 1 {
		t.Errorf("expected id 1, got %v", resp["id"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %v", resp["result"])
	}
	if result["protocolVersion"] != "1" {
		t.Errorf("expected protocolVersion '1', got %v", result["protocolVersion"])
	}
	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "mock-acp-agent" {
		t.Errorf("expected server name 'mock-acp-agent', got %v", serverInfo["name"])
	}
}

// TestIntegration_MockAgent_SessionCreate tests session creation via the mock agent.
func TestIntegration_MockAgent_SessionCreate(t *testing.T) {
	agentBin := buildMockAgent(t)
	workDir := t.TempDir()

	bridge := NewACPBridge(agentBin, workDir)
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	ws := dialWS(t, server.URL)
	defer ws.Close()

	// Initialize first
	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{},
	})
	readJSON(t, ws) // consume response

	// Create session
	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "session/create", "params": map[string]interface{}{},
	})

	resp := readJSON(t, ws)
	result := resp["result"].(map[string]interface{})
	if result["sessionId"] != "mock-session-001" {
		t.Errorf("expected sessionId 'mock-session-001', got %v", result["sessionId"])
	}
}

// TestIntegration_MockAgent_PromptStreaming tests prompt/start with streaming progress notifications.
func TestIntegration_MockAgent_PromptStreaming(t *testing.T) {
	agentBin := buildMockAgent(t)
	workDir := t.TempDir()

	bridge := NewACPBridge(agentBin, workDir)
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	ws := dialWS(t, server.URL)
	defer ws.Close()

	// Initialize
	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{},
	})
	readJSON(t, ws)

	// Send prompt
	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "prompt/start",
		"params": map[string]interface{}{
			"message": map[string]interface{}{
				"role":    "user",
				"content": "Generate 3 exercises on the past perfect",
			},
		},
	})

	// Read all messages until we get the final result
	var notifications []map[string]interface{}
	var finalResult map[string]interface{}

	for i := 0; i < 20; i++ { // safety cap
		msg := readJSON(t, ws)
		if msg["method"] == "notification/progress" {
			notifications = append(notifications, msg)
		} else if msg["result"] != nil {
			finalResult = msg
			break
		}
	}

	// We should have received streaming notifications
	if len(notifications) == 0 {
		t.Fatal("expected streaming notifications, got none")
	}
	t.Logf("received %d progress notifications", len(notifications))

	// Verify first notification has content
	params := notifications[0]["params"].(map[string]interface{})
	if params["content"] == nil || params["content"] == "" {
		t.Error("first notification should have content")
	}

	// Verify final result
	if finalResult == nil {
		t.Fatal("never received final result")
	}
	result := finalResult["result"].(map[string]interface{})
	text := result["text"].(string)
	if !strings.Contains(text, "Exercise 1") {
		t.Errorf("expected response to contain 'Exercise 1', got: %s", text[:100])
	}
	if !strings.Contains(text, "Exercise 3") {
		t.Errorf("expected response to contain 'Exercise 3'")
	}
}

// TestIntegration_MockAgent_UnknownMethod tests that unknown methods return an error.
func TestIntegration_MockAgent_UnknownMethod(t *testing.T) {
	agentBin := buildMockAgent(t)
	workDir := t.TempDir()

	bridge := NewACPBridge(agentBin, workDir)
	server := httptest.NewServer(http.HandlerFunc(bridge.HandleWebSocket))
	defer server.Close()

	ws := dialWS(t, server.URL)
	defer ws.Close()

	sendJSON(t, ws, map[string]interface{}{
		"jsonrpc": "2.0", "id": 99, "method": "unknown/method", "params": map[string]interface{}{},
	})

	resp := readJSON(t, ws)
	if resp["error"] == nil {
		t.Fatal("expected error for unknown method")
	}
	errObj := resp["error"].(map[string]interface{})
	if int(errObj["code"].(float64)) != -32601 {
		t.Errorf("expected error code -32601, got %v", errObj["code"])
	}
}

// --- Helpers ---

func dialWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	return ws
}

func sendJSON(t *testing.T, ws *websocket.Conn, msg map[string]interface{}) {
	t.Helper()
	data, _ := json.Marshal(msg)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("ws write error: %v", err)
	}
}

func readJSON(t *testing.T, ws *websocket.Conn) map[string]interface{} {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ws read error: %v", err)
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("invalid JSON: %v (raw: %s)", err, string(data))
	}
	return msg
}
