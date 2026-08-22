package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// ACPBridge manages the WebSocket ↔ stdio bridge between the web client and an ACP agent.
type ACPBridge struct {
	agentCmd string
	workDir  string
	upgrader websocket.Upgrader
}

func NewACPBridge(agentCmd, workDir string) *ACPBridge {
	return &ACPBridge{
		agentCmd: agentCmd,
		workDir:  workDir,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// HandleWebSocket upgrades the HTTP connection and bridges to the ACP agent subprocess.
func (b *ACPBridge) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Parse the agent command
	parts := strings.Fields(b.agentCmd)
	if len(parts) == 0 {
		log.Println("empty agent command")
		return
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = b.workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("cannot get stdin pipe: %v", err)
		return
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("cannot get stdout pipe: %v", err)
		return
	}

	// Start the agent process
	if err := cmd.Start(); err != nil {
		log.Printf("cannot start agent: %v", err)
		sendWSError(conn, fmt.Sprintf("cannot start agent: %v", err))
		return
	}
	log.Printf("ACP agent started (pid %d): %s", cmd.Process.Pid, b.agentCmd)

	var wg sync.WaitGroup

	// Agent stdout → WebSocket (JSON-RPC messages, one per line)
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		// ACP messages can be large
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, line); err != nil {
				log.Printf("ws write error: %v", err)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("agent stdout scan error: %v", err)
		}
	}()

	// WebSocket → Agent stdin (JSON-RPC messages)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdin.Close()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("ws read error: %v", err)
				}
				return
			}
			// Write message + newline to agent stdin
			if _, err := io.WriteString(stdin, string(message)+"\n"); err != nil {
				log.Printf("agent stdin write error: %v", err)
				return
			}
		}
	}()

	// Wait for both goroutines to finish
	wg.Wait()

	// Kill the agent process
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
	cmd.Wait()
	log.Printf("ACP agent process terminated (pid %d)", cmd.Process.Pid)
}

func sendWSError(conn *websocket.Conn, msg string) {
	errMsg := map[string]interface{}{
		"jsonrpc": "2.0",
		"error": map[string]interface{}{
			"code":    -32603,
			"message": msg,
		},
	}
	data, _ := json.Marshal(errMsg)
	conn.WriteMessage(websocket.TextMessage, data)
}
