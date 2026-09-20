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

// piAllowedTools is the allowlist passed to `pi --tools`.
//
// `bash` stays OUT, and the v1.8.0 decision stands. Web search comes from
// `web_search`, a custom tool registered by the pi extension in
// pi-config/extensions/brave-search. It calls the Brave API from inside pi's own
// process, so the model never gets a shell.
//
// pi DOES have a custom-tool API: `pi.registerTool()`, documented in
// packages/coding-agent/docs/extensions.md and shipping in 0.84.2. An earlier
// revision of this file claimed otherwise (citing earendil-works/pi#190 as an
// unreleased proposal) and paid for it by re-enabling bash. Measured on the real
// image at 0.84.2, the tool list pi declares to the model is:
//
//	edit, find, grep, ls, read, web_search, write
//
// — web_search present, bash absent. TestPi_ToolAllowlistIsExact pins it.
//
// Why this matters beyond tidiness: cmd.Dir and safePath() bound the FILE TOOLS,
// not a subprocess. A shell would make the workspace jail advisory. Keeping bash
// out is what keeps it enforced.
//
// `web_search` MUST be listed here. --tools is a strict allowlist over ALL
// tools, extension tools included: measured on 0.84.2, omitting web_search from
// this list silently drops it from the tools declared to the model, even though
// the extension loaded fine.
//
// The built-ins are read, bash, edit, write, grep, find, ls, verified against
// `pi --help` on 0.84.2. There is no `powershell` tool in pi.
//
// Note that --tools is NOT validated by pi: `--tools read,jenexistepas` is
// accepted silently (measured). So a typo here silently drops a tool instead of
// failing at boot, which is why TestPi_ToolAllowlistIsExact pins the string.
var piAllowedTools = []string{"read", "edit", "write", "grep", "find", "ls", "web_search"}

type PiBridge struct {
	piCmd    string       // path to pi binary
	workDir  string       // workspace directory (jail root)
	provider string       // provider key declared in ~/.pi/agent/models.json
	model    string       // model *name* to match (not its id — see runPi)
	brave    *BraveSearch // Brave Search client, nil if BRAVE_SEARCH_API_KEY unset
	hub      *Hub
	upgrader websocket.Upgrader

	// Test seams: let pi_test.go re-exec the test binary as a fake pi, so the
	// bridge can be tested without pi, without Node and without an LLM.
	testArgs []string
	testEnv  []string
}

// NewPiBridge creates a bridge for the pi agent.
func NewPiBridge(piCmd, workDir, provider, model string, brave *BraveSearch) *PiBridge {
	log.Printf("pi bridge: configured (cmd=%s, workDir=%s, provider=%s, model=%s, tools=%s, brave=%t)",
		piCmd, workDir, provider, model, strings.Join(piAllowedTools, ","), brave != nil)
	return &PiBridge{
		piCmd:    piCmd,
		workDir:  workDir,
		provider: provider,
		model:    model,
		brave:    brave,
		hub:      NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// toolPath extracts the file path a tool event operates on. pi puts it in
// `args`, under `path` for read/write/edit; `file_path` is accepted as a
// defensive fallback.
func toolPath(raw map[string]interface{}) string {
	args, _ := raw["args"].(map[string]interface{})
	if args == nil {
		return ""
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	return ""
}

// tailBuffer keeps the last n lines written to it. Used to attach pi's stderr to
// an error without letting a chatty process grow memory without bound.
type tailBuffer struct {
	mu    sync.Mutex
	n     int
	lines []string
}

func newTailBuffer(n int) *tailBuffer {
	return &tailBuffer{n: n}
}

func (b *tailBuffer) add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.n {
		b.lines = b.lines[len(b.lines)-b.n:]
	}
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, " | ")
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

// piWebSearchHint tells pi that web search exists and when to reach for it.
//
// The tool is already declared in the tool list, so this is not about
// discoverability but about disposition: in a one-shot prompt with no
// conversation history, nothing makes the model consider searching rather than
// answering from memory. The fallback sentence matters as much as the capability
// — a failed search must not end the turn with "je ne peux pas chercher".
const piWebSearchHint = "Tu peux chercher sur le web avec l'outil web_search " +
	"(API Brave Search, déjà configuré). Utilise-le pour " +
	"vérifier un fait, trouver un document authentique ou une référence récente. " +
	"Si la recherche échoue, dis-le et continue avec ce que tu sais."

// buildPiPrompt assembles the single message sent to pi: pedagogical system
// preamble, then the web-search hint, then the open-file context, then the
// teacher's text last.
//
// pi is given no conversation history — each request is one turn — so anything
// that must be known has to be in here.
//
// webSearch is false when BRAVE_SEARCH_API_KEY is unset: announcing a capability
// that is not configured would make pi call web_search and report a Brave error
// to the teacher instead of answering.
func buildPiPrompt(systemPrompt, currentFile, content string, webSearch bool) string {
	var b strings.Builder
	if systemPrompt != "" {
		b.WriteString(systemPrompt)
		b.WriteString("\n\n---\n\n")
	}
	if webSearch {
		b.WriteString(piWebSearchHint)
		b.WriteString("\n\n")
	}
	if currentFile != "" {
		fmt.Fprintf(&b, "[Fichier ouvert : %s]\n\n", currentFile)
	}
	b.WriteString(content)
	return b.String()
}

// piArgs returns the argv for one pi run. Verified against pi 0.84.2: every
// flag here appears in pi's own documentation. An undocumented flag is not
// merely ignored — `--provider custom` is what made pi exit 1 with
// `Unknown provider "custom"` in v1.7.4 through v1.7.7.
func (b *PiBridge) piArgs() []string {
	args := []string{
		"--mode", "rpc",
		"--no-session",
		// Explicit allowlist for ALL tools. bash is in it — see piAllowedTools
		// for why, and for what that costs.
		"--tools", strings.Join(piAllowedTools, ","),
		// Ignore any .pi/ the teacher could have dropped in the workspace via
		// PUT /api/file. Belt and braces with defaultProjectTrust: never.
		"--no-approve",
		// Do NOT remove. Context files (AGENTS.md, AGENTS.override.md,
		// CLAUDE.md) are loaded INDEPENDENTLY of project trust, so
		// defaultProjectTrust: never does not cover them.
		//
		// The workspace is writable by the teacher (PUT /api/file), so anything
		// in it is untrusted input. Measured against pi 0.84.2 with a recording
		// stub LLM: without this flag a marker placed in workspace/AGENTS.md
		// reaches the prompt sent to the model; with it, it does not.
		//
		// The flag is absent from pi's published docs but functional. That makes
		// it fragile across upgrades, which is why TestPi_DisablesWorkspaceContextFiles
		// asserts it on the real argv.
		"--no-context-files",
	}
	if b.provider != "" {
		args = append(args, "--provider", b.provider)
	}
	if b.model != "" {
		// Matched against the model `name` in models.json, not its `id`: the id
		// carries a slash (opencode-go/…) which --model would read as a
		// provider/id pair.
		args = append(args, "--model", b.model)
	}
	if len(b.testArgs) > 0 {
		// The fake pi is this test binary: its own flags must come first, and
		// `--` stops Go's flag parser so pi's flags reach os.Args untouched
		// instead of being rejected as unknown.
		return append(append(append([]string{}, b.testArgs...), "--"), args...)
	}
	return args
}

// runPi spawns `pi --mode rpc --no-session` and streams the response.
func (b *PiBridge) runPi(ctx context.Context, job *Job, content, systemPrompt, currentFile string, toolCount *int, toolsUsed *[]string) error {
	prompt := buildPiPrompt(systemPrompt, currentFile, content, b.brave != nil)

	cmd := exec.CommandContext(ctx, b.piCmd, b.piArgs()...)
	cmd.Dir = b.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// PI_OFFLINE disables every startup network call (update check, package
	// check, install telemetry). Inside the cluster those either hang or fail,
	// and none of them are wanted here.
	cmd.Env = append(cmd.Environ(), "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
	// One name, BRAVE_SEARCH_API_KEY, everywhere: it is the name the third-party
	// nousresearch/hermes-agent image imposes on the Hermès pods, and that image
	// is not ours to change. Everything we do own reads that name, so a single
	// Infisical entry feeds the whole cluster and `grep -r` finds the full chain.
	//
	// Passed explicitly even though the extension would inherit it from the
	// environment, because this value is TRIMMED (see NewBraveSearch) while the
	// raw pod env is not.
	if b.brave != nil {
		cmd.Env = append(cmd.Env, "BRAVE_SEARCH_API_KEY="+b.brave.apiKey)
	}
	cmd.Env = append(cmd.Env, b.testEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	// Without this, pi's own error message was thrown away and all we ever saw
	// was "exit status 1".
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi: %w", err)
	}

	// Tell the UI something is happening before pi has emitted anything.
	job.append(StreamEvent{Type: "status", Text: "starting"})

	stderrLog := newTailBuffer(20)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderrPipe)
		sc.Buffer(make([]byte, 0, 32*1024), 512*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			log.Printf("pi stderr: %s", truncateStr(line, 500))
			stderrLog.add(line)
		}
	}()

	// fail wraps an error with whatever pi wrote on stderr, so the cause reaches
	// both the pod log and the UI instead of a bare exit code.
	fail := func(format string, args ...any) error {
		<-stderrDone
		base := fmt.Errorf(format, args...)
		if tail := stderrLog.String(); tail != "" {
			return fmt.Errorf("%w — pi stderr: %s", base, truncateStr(tail, 600))
		}
		return base
	}

	// Send the prompt command (pi RPC protocol)
	promptCmd := map[string]interface{}{
		"type":    "prompt",
		"message": prompt,
	}
	reqBytes, _ := json.Marshal(promptCmd)
	reqBytes = append(reqBytes, '\n')
	if _, err := stdin.Write(reqBytes); err != nil {
		return fail("write stdin: %v", err)
	}
	// Don't close stdin yet — pi expects the pipe to stay open for potential
	// follow-up commands. We'll let it close when the process exits.

	// Read JSONL events from stdout (pi RPC protocol)
	var full strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var mu sync.Mutex
	filesWritten := map[string]bool{}
	// toolCallId -> path, populated from tool_execution_start.
	toolPaths := map[string]string{}

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
			callID, _ := raw["toolCallId"].(string)
			path := toolPath(raw)
			// Remember the path against the call id. Verified against pi 0.84.2:
			// tool_execution_end carries `args: {}`, so the path is only ever
			// available on the start event.
			if path != "" && callID != "" {
				mu.Lock()
				toolPaths[callID] = path
				mu.Unlock()
			}
			job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
				"name":   toolName,
				"status": "running",
				"path":   path,
			}})

		case "tool_execution_end":
			toolName, _ := raw["toolName"].(string)
			callID, _ := raw["toolCallId"].(string)
			path := toolPath(raw)
			if path == "" && callID != "" {
				mu.Lock()
				path = toolPaths[callID]
				mu.Unlock()
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

		// Lifecycle events exist so the teacher is not staring at a still screen.
		// They carry a stable token, never a translated string: the UI owns the
		// wording and the app is bilingual.
		case "agent_start", "turn_start":
			job.append(StreamEvent{Type: "status", Text: "thinking"})

		case "auto_retry_start":
			job.append(StreamEvent{Type: "status", Text: "retrying"})

		case "compaction_start":
			job.append(StreamEvent{Type: "status", Text: "compacting"})

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
	<-stderrDone

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
		return fail("pi stdout scan: %v", scanErr)
	}
	if cmdErr != nil {
		return fail("pi exited: %v", cmdErr)
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
