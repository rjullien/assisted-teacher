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

// fakePiEnvOut makes the fake pi dump its own environment to a file, so a test
// can assert what the SUBPROCESS really received. Asserting on the bridge's
// intent instead would not catch the key being dropped between the two.
const fakePiEnvOut = "GO_FAKE_PI_ENV_OUT"

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
	// Record the environment for the same reason.
	if out := os.Getenv(fakePiEnvOut); out != "" {
		_ = os.WriteFile(out, []byte(strings.Join(os.Environ(), "\n")), 0o600)
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
	b := NewPiBridge(exe, t.TempDir(), "bifrost", "bifrost-default", nil)
	b.testArgs = []string{"-test.run=TestFakePiHelper"}
	b.testEnv = append([]string{fakePiEnv + "=" + scenario}, extraEnv...)
	return b
}

// newFakePiBridgeWithBrave is newFakePiBridge with web search configured.
func newFakePiBridgeWithBrave(t *testing.T, scenario, key string, extraEnv ...string) *PiBridge {
	t.Helper()
	b := newFakePiBridge(t, scenario, extraEnv...)
	b.brave = NewBraveSearch(key)
	if b.brave == nil {
		t.Fatalf("NewBraveSearch(%q) returned nil", key)
	}
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

// piArgvFromRun returns the argv the bridge really passed to the subprocess.
func piArgvFromRun(t *testing.T, b *PiBridge, argvFile string) []string {
	t.Helper()
	runScenario(t, b)
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake pi did not record argv: %v", err)
	}
	return strings.Split(string(raw), "\n")
}

// The tool list is an explicit allowlist and nothing is implicit about it: this
// asserts the actual argv element by element.
//
// bash must stay OUT: it is what keeps the workspace jail enforced rather than
// advisory. web_search must be IN: --tools is a strict allowlist over extension
// tools too, so omitting it silently drops the tool (measured on 0.84.2).
// The test pins the whole set so that adding a tool is a deliberate act with a
// failing test to update, not a silent widening.
func TestPi_ToolAllowlistIsExact(t *testing.T) {
	argvFile := t.TempDir() + "/argv"
	argv := piArgvFromRun(t, newFakePiBridge(t, "happy", fakePiArgsEnv+"="+argvFile), argvFile)
	joined := strings.Join(argv, " ")

	for _, required := range []string{"--mode", "rpc", "--no-session", "--tools", "--no-approve", "--no-context-files"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing required argument %q in %s", required, joined)
		}
	}

	var toolsArg string
	for i, a := range argv {
		if a == "--tools" && i+1 < len(argv) {
			toolsArg = argv[i+1]
		}
	}
	if toolsArg == "" {
		t.Fatal("--tools had no value")
	}
	if toolsArg != strings.Join(piAllowedTools, ",") {
		t.Fatalf("--tools = %q, want %q", toolsArg, strings.Join(piAllowedTools, ","))
	}

	got := map[string]bool{}
	for _, tool := range strings.Split(toolsArg, ",") {
		got[tool] = true
	}
	// A shell would defeat cmd.Dir and safePath(), which bound the file tools
	// only. Web search comes from the web_search extension tool instead.
	if got["bash"] {
		t.Error("bash is present in --tools: the workspace jail becomes advisory — web search must go through the web_search extension tool")
	}
	// web_search is an extension tool, but --tools filters those too: measured on
	// 0.84.2, omitting it here drops it from the tools declared to the model even
	// though the extension loaded.
	if !got["web_search"] {
		t.Error("web_search is absent from --tools: the extension loads but pi never declares the tool to the model")
	}
	for _, required := range []string{"read", "edit", "write", "grep", "find", "ls"} {
		if !got[required] {
			t.Errorf("file tool %q is missing from --tools", required)
		}
	}
	// Only names pi actually has. `pi --help` on 0.84.2 lists exactly seven
	// built-in tools, and --tools is not validated: a name outside this set is
	// accepted silently and enables nothing, so a typo would be invisible.
	// Names pi actually has: the seven built-ins from `pi --help` on 0.84.2, plus
	// the tools our own extensions register. --tools is not validated, so a name
	// outside this set is accepted silently and enables nothing.
	known := map[string]bool{
		"read": true, "bash": true, "edit": true,
		"write": true, "grep": true, "find": true, "ls": true,
		"web_search": true, // registered by pi-config/extensions/brave-search
	}
	for tool := range got {
		if !known[tool] {
			t.Errorf("%q is neither a pi built-in nor a tool our extensions register — --tools is not validated, so it would silently enable nothing", tool)
		}
	}
}

// The extension reads process.env.BRAVE_SEARCH_API_KEY. One name across the whole
// cluster: it is the one the third-party hermes-agent image imposes, and that
// image is not ours to change.
func TestPi_PassesBraveKeyToSubprocess(t *testing.T) {
	envFile := t.TempDir() + "/env"
	b := newFakePiBridgeWithBrave(t, "happy", "  brave-secret\n", fakePiEnvOut+"="+envFile)
	runScenario(t, b)

	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("fake pi did not record its environment: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}

	v, ok := env["BRAVE_SEARCH_API_KEY"]
	if !ok {
		t.Fatal("BRAVE_SEARCH_API_KEY was not exported to the pi subprocess")
	}
	// Trimmed: an Infisical secret carries a trailing newline, and "\n" is truthy,
	// so an un-trimmed empty secret would send the extension into a Brave call
	// that answers 422 instead of reporting a missing key.
	if v != "brave-secret" {
		t.Errorf("BRAVE_SEARCH_API_KEY = %q, want the trimmed key %q", v, "brave-secret")
	}
	// A second name would mean two Infisical entries to rotate in step.
	if _, leaked := env["BRAVE_API_KEY"]; leaked {
		t.Error("BRAVE_API_KEY was exported too: the cluster standardised on BRAVE_SEARCH_API_KEY alone")
	}
}

// Without a key, nothing Brave-related reaches the subprocess: web_search called
// with an empty token answers 422 in the middle of a lesson.
func TestPi_NoBraveKeyNoBraveEnv(t *testing.T) {
	envFile := t.TempDir() + "/env"
	b := newFakePiBridge(t, "happy", fakePiEnvOut+"="+envFile)
	runScenario(t, b)

	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("fake pi did not record its environment: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "BRAVE_SEARCH_API_KEY=") || strings.HasPrefix(line, "BRAVE_API_KEY=") {
			t.Fatalf("a Brave variable reached the subprocess with no key configured: %q", line)
		}
	}
}

// The capability has to be NAMED in the prompt: pi gets no conversation history,
// so even a declared tool is one it may never consider using.
func TestPi_PromptAnnouncesWebSearchOnlyWhenConfigured(t *testing.T) {
	with := buildPiPrompt("systeme", "B1/unit5.md", "ajoute un exercice", true)
	if !strings.Contains(with, "web_search") {
		t.Errorf("expected the prompt to name the web_search tool, got:\n%s", with)
	}

	without := buildPiPrompt("systeme", "B1/unit5.md", "ajoute un exercice", false)
	if strings.Contains(without, "web_search") {
		t.Errorf("web search must not be announced without a key, got:\n%s", without)
	}

	// The teacher's own text stays last in both cases: the hint must not push it
	// away from the end of the prompt.
	for name, prompt := range map[string]string{"with": with, "without": without} {
		if !strings.HasSuffix(strings.TrimSpace(prompt), "ajoute un exercice") {
			t.Errorf("%s: the teacher's text must end the prompt, got:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "B1/unit5.md") {
			t.Errorf("%s: the open file was dropped from the prompt", name)
		}
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
	built := buildPiPrompt("prompt systeme", "B1/unit5.md", "bonjour", false)
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

	bare := buildPiPrompt("", "", "bonjour", false)
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
