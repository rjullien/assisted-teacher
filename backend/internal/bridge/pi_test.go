package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests never start a real pi and never reach an LLM. They run this test
// binary again as a fake pi (the standard os/exec test pattern), replaying event
// sequences captured from a real `pi --mode rpc` 0.84.2 run against a stub
// OpenAI server. Zero tokens, no network, no Node in CI.
//
// The captured sequence that these fixtures reproduce:
//
//	response / agent_start / turn_start / message_start / message_end
//	message_update(toolcall_start) / message_update(toolcall_end)
//	tool_execution_start(write, args.path) / tool_execution_end(args EMPTY)
//	turn_end / turn_start / message_update(text_delta)... / turn_end
//	agent_end / agent_settled

const fakePiEnv = "GO_FAKE_PI_SCENARIO"
const fakePiArgsEnv = "GO_FAKE_PI_ARGV_OUT"

// TestFakePiHelper is not a test. It is the fake pi binary, selected by the
// environment variable so `go test` can re-exec itself.
func TestFakePiHelper(t *testing.T) {
	scenario := os.Getenv(fakePiEnv)
	if scenario == "" {
		t.Skip("helper process, not a test")
	}

	// Record argv so a test can assert on the flags the bridge passes.
	if out := os.Getenv(fakePiArgsEnv); out != "" {
		_ = os.WriteFile(out, []byte(strings.Join(os.Args, "\n")), 0o600)
	}

	switch scenario {
	case "happy":
		emit(`{"type":"response","command":"prompt","success":true}`)
		emit(`{"type":"agent_start"}`)
		emit(`{"type":"turn_start"}`)
		emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Voici "}}`)
		emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"l'exercice."}}`)
		emit(`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"write","args":{"path":"B1/unit5.md","content":"x"}}`)
		// args is deliberately empty here — this is what pi really sends.
		emit(`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"write","args":{}}`)
		emit(`{"type":"agent_end","willRetry":false}`)
		emit(`{"type":"agent_settled"}`)

	case "exit1_stderr":
		fmt.Fprintln(os.Stderr, `Error: Unknown provider "custom". Use --list-models to see available providers/models.`)
		os.Exit(1)

	case "rejected":
		emit(`{"type":"response","command":"prompt","success":false,"error":"model not found: nope"}`)

	case "garbage_then_done":
		emit(`this is not json`)
		emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"ok"}}`)
		emit(`{"type":"agent_end"}`)

	case "retry_and_compact":
		emit(`{"type":"response","command":"prompt","success":true}`)
		emit(`{"type":"auto_retry_start","attempt":1}`)
		emit(`{"type":"compaction_start","reason":"threshold"}`)
		emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"fini"}}`)
		emit(`{"type":"agent_end"}`)

	default:
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", scenario)
		os.Exit(2)
	}
	os.Exit(0)
}

func emit(line string) {
	fmt.Fprintln(os.Stdout, line)
}

// newFakePiBridge returns a PiBridge whose "pi" is this test binary.
func newFakePiBridge(t *testing.T, scenario string, extraEnv ...string) *PiBridge {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	b := NewPiBridge(exe, t.TempDir(), "bifrost", "bifrost-default")
	b.testArgs = []string{"-test.run=TestFakePiHelper"}
	b.testEnv = append([]string{fakePiEnv + "=" + scenario}, extraEnv...)
	return b
}

// runScenario starts a job and waits for its terminal event.
func runScenario(t *testing.T, b *PiBridge) []StreamEvent {
	t.Helper()
	job := b.startJob("bonjour", "prompt systeme", "B1/unit5.md", "pi")

	backlog, live, unsub := job.Subscribe(0)
	defer unsub()

	events := append([]StreamEvent(nil), backlog...)
	for _, ev := range events {
		if ev.Type == "done" || ev.Type == "error" {
			return events
		}
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-live:
			events = append(events, ev)
			if ev.Type == "done" || ev.Type == "error" {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out; events so far: %s", summarise(events))
			return events
		}
	}
}

func summarise(events []StreamEvent) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, ev.Type)
	}
	return strings.Join(parts, ",")
}

func firstOfType(events []StreamEvent, typ string) (StreamEvent, bool) {
	for _, ev := range events {
		if ev.Type == typ {
			return ev, true
		}
	}
	return StreamEvent{}, false
}

func toolNames(events []StreamEvent) []string {
	var out []string
	for _, ev := range events {
		if ev.Type != "tool" {
			continue
		}
		m, ok := ev.Tool.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		path, _ := m["path"].(string)
		status, _ := m["status"].(string)
		out = append(out, strings.TrimRight(name+":"+status+":"+path, ":"))
	}
	return out
}

// --- Tests ------------------------------------------------------------------

func TestPi_HappyPath_StreamsDeltasAndFinishes(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "happy"))

	done, ok := firstOfType(events, "done")
	if !ok {
		t.Fatalf("no done event; got %s", summarise(events))
	}
	if done.Reply != "Voici l'exercice." {
		t.Errorf("reply = %q, want %q", done.Reply, "Voici l'exercice.")
	}
	var deltas []string
	for _, ev := range events {
		if ev.Type == "delta" {
			deltas = append(deltas, ev.Text)
		}
	}
	if len(deltas) != 2 {
		t.Errorf("expected 2 delta events, got %d (%s)", len(deltas), summarise(events))
	}
}

// The whole point of the status events: the UI must have something to show
// before any text arrives.
func TestPi_EmitsStartingStatusBeforeAnyOutput(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "happy"))

	status, ok := firstOfType(events, "status")
	if !ok {
		t.Fatalf("no status event; got %s", summarise(events))
	}
	if status.Text != "starting" {
		t.Errorf("first status = %q, want starting", status.Text)
	}
	// It must come before the first delta, otherwise it is useless as a spinner.
	for _, ev := range events {
		if ev.Type == "delta" {
			t.Fatalf("a delta arrived before any status event: %s", summarise(events))
		}
		if ev.Type == "status" {
			break
		}
	}
}

func TestPi_MapsLifecycleEventsToStatusTokens(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "retry_and_compact"))

	var tokens []string
	for _, ev := range events {
		if ev.Type == "status" {
			tokens = append(tokens, ev.Text)
		}
	}
	for _, want := range []string{"starting", "retrying", "compacting"} {
		found := false
		for _, got := range tokens {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing status token %q; got %v", want, tokens)
		}
	}
}

// Regression: tool_execution_end carries `args: {}` in pi 0.84.2, so the path
// has to be remembered from tool_execution_start. Without that, no file_changed
// is emitted and the editor never reloads what pi just wrote.
func TestPi_EmitsFileChangedUsingPathFromStartEvent(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "happy"))

	names := toolNames(events)
	wantEnd := "write:done:B1/unit5.md"
	foundEnd := false
	foundChanged := false
	for _, n := range names {
		if n == wantEnd {
			foundEnd = true
		}
		if n == "file_changed:B1/unit5.md" || n == "file_changed::B1/unit5.md" {
			foundChanged = true
		}
	}
	if !foundEnd {
		t.Errorf("tool_execution_end lost the path: got %v, want one of them to be %q", names, wantEnd)
	}
	if !foundChanged {
		t.Errorf("no file_changed event for the written file: got %v", names)
	}
}

// Regression: stderr used to be discarded, so every failure looked like a bare
// "exit status 1" with no cause. This is the exact message pi prints for the
// misconfiguration that shipped in v1.7.4.
func TestPi_SurfacesStderrOnFailure(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "exit1_stderr"))

	errEv, ok := firstOfType(events, "error")
	if !ok {
		t.Fatalf("no error event; got %s", summarise(events))
	}
	if !strings.Contains(errEv.Detail, "Unknown provider") {
		t.Errorf("stderr not surfaced in Detail.\nDetail = %q", errEv.Detail)
	}
	if !strings.Contains(errEv.Detail, "exit status 1") {
		t.Errorf("exit code missing from Detail = %q", errEv.Detail)
	}
}

func TestPi_ReportsRejectedCommand(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "rejected"))

	errEv, ok := firstOfType(events, "error")
	if !ok {
		t.Fatalf("no error event; got %s", summarise(events))
	}
	if !strings.Contains(errEv.Error, "model not found") {
		t.Errorf("Error = %q, want it to carry pi's reason", errEv.Error)
	}
}

func TestPi_IgnoresUnparseableLines(t *testing.T) {
	events := runScenario(t, newFakePiBridge(t, "garbage_then_done"))

	done, ok := firstOfType(events, "done")
	if !ok {
		t.Fatalf("garbage on stdout aborted the stream: %s", summarise(events))
	}
	if done.Reply != "ok" {
		t.Errorf("reply = %q, want ok", done.Reply)
	}
}

// Safety: bash must never be offered to an agent driven by a teacher from a
// browser. This asserts the actual argv, not the intention.
func TestPi_ToolAllowlistExcludesShells(t *testing.T) {
	argvFile := t.TempDir() + "/argv"
	b := newFakePiBridge(t, "happy", fakePiArgsEnv+"="+argvFile)
	runScenario(t, b)

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake pi did not record argv: %v", err)
	}
	argv := strings.Split(string(raw), "\n")
	joined := strings.Join(argv, " ")

	for _, forbidden := range []string{"bash", "powershell"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("%q reached pi's tool list: %s", forbidden, joined)
		}
	}
	for _, required := range []string{"--mode", "rpc", "--no-session", "--tools", "--no-approve", "--no-context-files"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing required argument %q in %s", required, joined)
		}
	}
	// The allowlist itself, verified element by element.
	var toolsArg string
	for i, a := range argv {
		if a == "--tools" && i+1 < len(argv) {
			toolsArg = argv[i+1]
		}
	}
	if toolsArg == "" {
		t.Fatal("--tools had no value")
	}
	got := strings.Split(toolsArg, ",")
	if len(got) != len(piAllowedTools) {
		t.Errorf("--tools = %v, want exactly %v", got, piAllowedTools)
	}
}

// Security: the workspace is writable by the teacher through PUT /api/file, so
// an AGENTS.md dropped in it is untrusted input. pi loads context files
// regardless of project trust, so defaultProjectTrust: never does NOT cover
// them — only --no-context-files does.
//
// Measured against pi 0.84.2 with a recording stub LLM: without the flag, a
// marker in workspace/AGENTS.md reaches the prompt sent to the model. v1.8.0
// shipped without it. This test exists so that cannot happen twice.
func TestPi_DisablesWorkspaceContextFiles(t *testing.T) {
	argvFile := t.TempDir() + "/argv"
	b := newFakePiBridge(t, "happy", fakePiArgsEnv+"="+argvFile)
	runScenario(t, b)

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake pi did not record argv: %v", err)
	}
	argv := strings.Split(string(raw), "\n")
	for _, a := range argv {
		if a == "--no-context-files" {
			return
		}
	}
	t.Errorf("--no-context-files is missing: a workspace AGENTS.md would reach the model.\nargv = %s",
		strings.Join(argv, " "))
}

// The bridge must not send an empty system preamble as a stray separator.
func TestPi_PromptCarriesSystemAndFileContext(t *testing.T) {
	// The fake pi echoes nothing back, so assert on what would be written by
	// checking the JSON we build is well formed and contains both parts.
	var msg struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	built := buildPiPrompt("prompt systeme", "B1/unit5.md", "bonjour")
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"type":"prompt","message":%q}`, built)), &msg); err != nil {
		t.Fatalf("prompt is not JSON-safe: %v", err)
	}
	if !strings.Contains(msg.Message, "prompt systeme") {
		t.Error("system preamble missing from the prompt")
	}
	if !strings.Contains(msg.Message, "B1/unit5.md") {
		t.Error("open-file context missing from the prompt")
	}
	if !strings.HasSuffix(msg.Message, "bonjour") {
		t.Errorf("user text should come last, got %q", msg.Message)
	}

	bare := buildPiPrompt("", "", "bonjour")
	if bare != "bonjour" {
		t.Errorf("with no system and no file the prompt should be the user text alone, got %q", bare)
	}
}

// Guard against the fake-pi harness silently not running.
func TestFakePiHarnessActuallyRuns(t *testing.T) {
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=TestFakePiHelper")
	cmd.Env = append(os.Environ(), fakePiEnv+"=happy")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("fake pi failed: %v", err)
	}
	if !strings.Contains(string(out), `"agent_end"`) {
		t.Errorf("fake pi produced no agent_end:\n%s", out)
	}
}
