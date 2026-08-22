// Package main implements a mock ACP agent for integration testing.
// It speaks ACP JSON-RPC over stdin/stdout: responds to initialize, session/create,
// prompt/start with fake streaming responses, and handles basic protocol.
//
// Usage: go run ./internal/bridge/mockagent
// Or build: go build -o mock-acp-agent ./internal/bridge/mockagent
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type jsonrpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(nil, -32700, "Parse error")
			continue
		}

		switch req.Method {
		case "initialize":
			handleInitialize(req)
		case "session/create":
			handleSessionCreate(req)
		case "prompt/start":
			handlePromptStart(req)
		default:
			sendError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func handleInitialize(req jsonrpcRequest) {
	send(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "1",
			"capabilities": map[string]interface{}{
				"streaming": true,
			},
			"serverInfo": map[string]interface{}{
				"name":    "mock-acp-agent",
				"version": "0.1.0",
			},
		},
	})
}

func handleSessionCreate(req jsonrpcRequest) {
	send(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"sessionId": "mock-session-001",
		},
	})
}

func handlePromptStart(req jsonrpcRequest) {
	// Parse the message from params
	var params struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	json.Unmarshal(req.Params, &params)

	// Simulate streaming response with progress notifications
	chunks := []string{
		"Here are ",
		"**3 exercises** ",
		"for your students:\n\n",
		"## Exercise 1\n",
		"Fill in the blanks with the correct form.\n\n",
		"## Exercise 2\n",
		"Rewrite the sentences.\n\n",
		"## Exercise 3\n",
		"Multiple choice quiz.",
	}

	for _, chunk := range chunks {
		send(jsonrpcNotification{
			JSONRPC: "2.0",
			Method:  "notification/progress",
			Params: map[string]interface{}{
				"content": chunk,
			},
		})
		time.Sleep(10 * time.Millisecond) // Simulate typing delay
	}

	// Final result
	fullResponse := ""
	for _, chunk := range chunks {
		fullResponse += chunk
	}

	send(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"text":          fullResponse,
			"filesModified": []string{},
		},
	})
}

func send(msg interface{}) {
	data, _ := json.Marshal(msg)
	fmt.Println(string(data))
}

func sendError(id interface{}, code int, message string) {
	send(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}
