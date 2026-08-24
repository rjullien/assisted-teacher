// Package bridge connects the frontend to Hermes (Lya) via the OpenAI-compatible
// streaming API. Uses the same detached-job pattern as TripKit:
//
//   - Frontend sends a prompt → backend starts a Job (background goroutine)
//   - Job calls Hermes POST /v1/chat/completions with stream:true
//   - Job stores ALL SSE events in an append-only log
//   - Frontend subscribes via WebSocket — disconnect/reconnect replays backlog
//   - Frontend disconnect does NOT cancel Hermes — the Job keeps running
//
// This survives Cloudflare 100s timeout: if the WS drops, the frontend reconnects
// and gets the full backlog + live events from where it left off.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// keyFingerprint returns the first 8 hex chars of SHA-256(key).
// Safe to log (non-reversible) and enough to prove whether two sides hold the
// same secret. Compare with, on the cluster:
//
//	kubectl get secret hermes-lya-secret -n openclaw \
//	  -o jsonpath='{.data.API_SERVER_KEY}' | base64 -d | tr -d '\n' | sha256sum
func keyFingerprint(key string) string {
	if key == "" {
		return "empty"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// --- Job infrastructure (same pattern as tripkit-backend/internal/leo/job.go) ---

type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobError   JobStatus = "error"
)

type StreamEvent struct {
	Seq    int    `json:"seq"`
	Type   string `json:"type"`             // delta | tool | done | error | meta
	Text   string `json:"text,omitempty"`   // for delta
	Reply  string `json:"reply,omitempty"`  // full accumulated text (on done)
	Error  string `json:"error,omitempty"`  // for error
	Detail string `json:"detail,omitempty"` // technical detail
	Tool   any    `json:"tool,omitempty"`   // for tool progress
	JobID  string `json:"jobId,omitempty"`
}

type Job struct {
	ID        string
	CreatedAt time.Time

	mu     sync.Mutex
	status JobStatus
	events []StreamEvent
	subs   []chan StreamEvent
	cancel context.CancelFunc
	doneCh chan struct{}
}

func (j *Job) append(ev StreamEvent) {
	j.mu.Lock()
	ev.Seq = len(j.events) + 1
	ev.JobID = j.ID
	j.events = append(j.events, ev)
	if ev.Type == "done" {
		j.status = JobDone
	}
	if ev.Type == "error" {
		j.status = JobError
	}
	subs := append([]chan StreamEvent(nil), j.subs...)
	j.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub <- ev:
		default: // slow subscriber — they'll catch up on reconnect
		}
	}
}

func (j *Job) Subscribe(after int) (backlog []StreamEvent, live <-chan StreamEvent, unsub func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ev := range j.events {
		if ev.Seq > after {
			backlog = append(backlog, ev)
		}
	}
	ch := make(chan StreamEvent, 256)
	j.subs = append(j.subs, ch)
	var once sync.Once
	unsub = func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			out := j.subs[:0]
			for _, s := range j.subs {
				if s != ch {
					out = append(out, s)
				}
			}
			j.subs = out
		})
	}
	return backlog, ch, unsub
}

// --- Hub (in-memory job store) ---

const (
	jobTTL        = 15 * time.Minute
	jobRunTimeout = 10 * time.Minute
)

type Hub struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func NewHub() *Hub {
	return &Hub{jobs: map[string]*Job{}}
}

func (h *Hub) Get(id string) *Job {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.jobs[id]
}

func (h *Hub) gc() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-jobTTL)
	for id, j := range h.jobs {
		j.mu.Lock()
		done := j.status != JobRunning
		created := j.CreatedAt
		j.mu.Unlock()
		if done && created.Before(cutoff) {
			delete(h.jobs, id)
		}
	}
}

// --- HermesBridge ---

type HermesBridge struct {
	hermesURL string
	apiKey    string
	workDir   string
	// fsPrefixes are the mount points of Lya's OWN filesystem, stripped from the
	// paths her tool-progress frames report before those paths are jailed into
	// workDir. See stripHermesFSPrefix.
	fsPrefixes []string
	hub        *Hub
	upgrader   websocket.Upgrader
}

func NewHermesBridge(hermesURL, apiKey, workDir string) *HermesBridge {
	// TrimSpace is critical: Infisical/K8s secrets often carry a trailing \n,
	// which makes the Bearer token mismatch on the gateway side.
	key := strings.TrimSpace(apiKey)
	if key != apiKey {
		log.Printf("hermes: API key had %d surrounding whitespace byte(s) — trimmed", len(apiKey)-len(key))
	}
	prefixes := parseHermesFSPrefixes(os.Getenv(hermesFSPrefixesEnv))
	log.Printf("hermes: bridge configured (url=%s, keyLen=%d, keyFp=%s, workDir=%s, fsPrefixes=%s)",
		hermesURL, len(key), keyFingerprint(key), workDir, strings.Join(prefixes, ","))
	return &HermesBridge{
		hermesURL:  strings.TrimRight(strings.TrimSpace(hermesURL), "/"),
		apiKey:     key,
		workDir:    workDir,
		fsPrefixes: prefixes,
		hub:        NewHub(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// hermesFSPrefixesEnv overrides the Hermes-side mount points at deployment time.
// Comma-separated, absolute, longest first. Where Lya's PVC is mounted inside
// HER pod is deployment knowledge, not a constant of this program: a chart that
// moves it must not require a code change here.
const hermesFSPrefixesEnv = "HERMES_FS_PREFIXES"

// defaultHermesFSPrefixes is what the hermes-lya deployment mounts today. Taken
// from a production trace: asked to update "Test_folders/test_nvx_cours.md",
// Lya searched /opt/data and /opt/data/home and finally created
// /opt/data/Test_folders/test_nvx_cours.md — on her own disk.
//
// Longest first, because the list is scanned in order: with "/opt/data/" first,
// "/opt/data/home/x.md" would mirror to "home/x.md" instead of "x.md".
var defaultHermesFSPrefixes = []string{"/opt/data/home/", "/opt/data/"}

// parseHermesFSPrefixes reads the comma-separated prefix list, falling back to
// defaultHermesFSPrefixes when it is unset or contains nothing usable.
//
// Entries are kept in the order given (see defaultHermesFSPrefixes) and get a
// trailing slash if they lack one: without it, stripping "/opt/data" from
// "/opt/data/x.md" leaves "/x.md", still absolute, and safePath refuses it — an
// operator footgun with a silent failure at the other end. A non-absolute entry
// is dropped: it could only ever match by mangling a relative path.
func parseHermesFSPrefixes(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			log.Printf("hermes: ignoring %s entry %q — a Hermes-side prefix must be absolute", hermesFSPrefixesEnv, p)
			continue
		}
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return defaultHermesFSPrefixes
	}
	return out
}

// stripHermesFSPrefix maps a path as Hermes reports it onto a path relative to
// the app's workspace.
//
// Hermes runs in another pod and her file tools work on HER filesystem, so her
// tool-progress frames carry absolute paths like
// "/opt/data/Test_folders/test_nvx_cours.md". safePath refuses absolute paths,
// so every such frame used to be skipped and the teacher saw nothing change in
// the editor while Lya reported success.
//
// ok is false when the path is absolute and matches none of the known prefixes:
// that path is somewhere we know nothing about and stays refused, exactly as
// before. A relative path is returned untouched — it is already what safePath
// expects.
//
// This is NOT a path validator: the remainder still goes through safePath, which
// is what keeps "/opt/data/../../etc/passwd" out (it strips to
// "../../etc/passwd" and is refused there).
func (b *HermesBridge) stripHermesFSPrefix(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return path, true
	}
	for _, prefix := range b.fsPrefixes {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix), true
		}
	}
	return "", false
}

// safePath validates and resolves a relative path within the workspace.
// Returns ("", error) if the path escapes the workspace.
func (b *HermesBridge) safePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	abs := filepath.Join(b.workDir, cleaned)
	if !strings.HasPrefix(abs, filepath.Clean(b.workDir)) {
		return "", fmt.Errorf("path escapes workspace: %s", relPath)
	}
	return abs, nil
}

// --- Local file tools (OpenAI-style function calling, mode Desk only) ---

const (
	// maxToolLoops caps the number of Hermes round-trips per job. Without a cap,
	// a model that keeps asking for the same tool would keep the job (and the
	// teacher's chat) spinning until jobRunTimeout.
	maxToolLoops = 8
	// maxReadFileChars mirrors MAX_INLINED_FILE_CHARS in
	// frontend/src/components/Chat.tsx: the same cap on both paths means a file
	// too big to inline in the prompt is also too big to return whole here, and
	// the teacher gets the same behaviour whichever path fed the model.
	maxReadFileChars = 20000
	// maxWriteFileChars bounds a single write_file. Reads are capped, writes were
	// not: a runaway generation could fill the workspace PVC (shared with the
	// editor and the exports) with one call. Ten times the read cap leaves room
	// for a whole rewritten course file and still bounds the damage.
	maxWriteFileChars = 200000
	// maxToolResultChars budgets the CUMULATIVE size of the tool results fed back
	// to Hermes over the whole job. Without it, a few large read_file results
	// stack up in `messages` over up to maxToolLoops round-trips, overflow the
	// upstream context window, and the teacher gets a bare "Échec IA. Réessaie."
	// instead of an answer. Cutting the results is recoverable; a 4xx is not.
	maxToolResultChars = 60000
	// maxConfirmationChars hard-caps a message exempted from the budget (a write
	// confirmation, a refusal). Those messages quote a path the model chose, and
	// a model can send a very long one, so "short by construction" is not a
	// guarantee we can rely on.
	maxConfirmationChars = 2000
	// maxEchoedArgsChars bounds the tool-call arguments echoed back in the
	// assistant message that the OpenAI protocol requires before the tool
	// results. Those arguments carry the whole file content on a write_file (up
	// to maxWriteFileChars) and stay in `messages` for every later turn, so two
	// large writes overflow the upstream context window through the echo even
	// though every tool RESULT is budgeted. The overflow arrives as a gateway
	// 4xx, i.e. "Échec IA. Réessaie." for the teacher.
	maxEchoedArgsChars = 4000
	// maxEchoedArgFieldChars is how much of a single long string argument
	// survives in the echo. The model does not need to re-read what it just
	// wrote — the tool result already told it whether the write landed — it only
	// needs the call to still be there, with its id, so the protocol holds.
	maxEchoedArgFieldChars = 1000
)

// allowedFileExts restricts the file types the tools may touch — on READ as well
// as on write. The write side keeps a model from dropping a .sh or a .exe into
// the workspace that is then served to the browser (see
// backend/internal/api/files.go and the Markdown editor); the read side keeps
// anything an operator happened to leave in the PVC (a dotfile, a dump, an
// export) from being shipped to the gateway just because the model guessed a
// name.
var allowedFileExts = map[string]bool{".md": true, ".json": true, ".txt": true}

// hermesFileTools is the tool table declared to Hermes so Lya can ask the
// backend for a file instead of relying on the frontend inlining it (see
// buildDeskPrompt in Chat.tsx, which stays as the fallback).
//
// Why patch_file takes old_string/new_string rather than a unified diff: an
// exact substring replacement is the simplest shape a model produces reliably,
// and it either matches or fails loudly. A unified diff would force us to
// implement fuzzy hunk matching, because the line numbers a model emits are
// stale as soon as the file it saw differs by one line — silently mis-applying
// a hunk into a teacher's course file is a far worse failure than a refusal.
//
// UNVERIFIED: whether the Hermes gateway forwards this `tools` request
// parameter to the upstream model cannot be checked from a development sandbox
// (the gateway is not reachable from there). That is why the loop degrades to
// the previous single-turn behaviour when no tool_calls ever come back, and why
// every job logs one greppable line 'hermes: tool_calls supported=true|false':
// the question gets settled from production logs, not by guessing.
var hermesFileTools = []map[string]interface{}{
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "read_file",
			"description": "Lit un fichier du dossier de travail de l'enseignant et renvoie son contenu. Utilise cet outil avant de modifier un fichier existant, pour travailler sur le contenu réel. Le contenu peut être tronqué s'il est très long : dans ce cas une note explicite le signale à la fin du texte renvoyé. Extensions lisibles : .md, .json, .txt.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "write_file",
			"description": "Écrit un fichier dans le dossier de travail de l'enseignant, en le créant s'il n'existe pas. Le contenu fourni remplace intégralement l'ancien : pour une retouche ciblée sur un fichier existant, utilise plutôt patch_file. Un fichier existant plus long que ce que read_file peut renvoyer en entier ne peut pas être raccourci par cet outil : utilise patch_file. Extensions autorisées : .md, .json, .txt.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Contenu complet du fichier, en Markdown pour les fichiers .md.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "patch_file",
			"description": "Remplace un extrait exact d'un fichier existant par un nouvel extrait, sans réécrire tout le fichier. old_string doit apparaître exactement une fois dans le fichier : s'il est absent ou présent plusieurs fois, rien n'est écrit et l'erreur t'est renvoyée — allonge alors old_string pour le rendre unique. Extensions autorisées : .md, .json, .txt.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Chemin relatif du fichier dans le dossier de travail, par exemple B1/unite1.md.",
					},
					"old_string": map[string]interface{}{
						"type":        "string",
						"description": "Extrait exact à remplacer, copié tel quel depuis le fichier (espaces et retours à la ligne compris). Doit apparaître exactement une fois dans le fichier.",
					},
					"new_string": map[string]interface{}{
						"type":        "string",
						"description": "Texte de remplacement. Chaîne vide pour supprimer l'extrait.",
					},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
	},
}

// Desk sub-modes. In "insert" Lya only answers in the chat and the teacher
// inserts what they keep by hand; in "direct" Lya updates the working file
// herself through the file tools. "legacy" is neither: it is what an absent or
// unrecognised field resolves to, i.e. a client that predates the selector.
const (
	deskModeInsert = "insert"
	deskModeDirect = "direct"
	deskModeLegacy = "legacy"
)

// normalizeDeskMode resolves the sub-mode sent by the frontend.
//
// Three states, not two, because "absent" and "insert" must not be conflated
// and neither may be treated as an opt-in to the file tools:
//
//   - "insert": the teacher explicitly asked for copie/insertion — nothing is
//     ever written, not even by a v1.9.0 progress frame.
//   - "direct": the teacher explicitly asked Lya to edit the working file — the
//     only value that arms read_file/write_file/patch_file.
//   - anything else (absent, empty, misspelled): "legacy". A deployed frontend
//     that does not know the field yet keeps the exact v1.9.x behaviour (the
//     opportunistic write interception), but does NOT get the much stronger new
//     tools it never opted into. Defaulting a typo into full write access is the
//     kind of fail-open nobody notices until a course file is overwritten.
func normalizeDeskMode(deskMode string) string {
	switch strings.ToLower(strings.TrimSpace(deskMode)) {
	case deskModeInsert:
		return deskModeInsert
	case deskModeDirect:
		return deskModeDirect
	default:
		return deskModeLegacy
	}
}

// fileToolsEnabled is the single gate for declaring AND executing the local file
// tools. Kept in one place on purpose: the Desk sub-mode toggle (copie/insertion
// vs mise à jour directe) narrows the same condition, and a gate scattered
// across the SSE loop is how mode Lya ends up writing files by accident.
//
// Requires an explicit "direct": see normalizeDeskMode for why an absent field
// is not enough.
func (b *HermesBridge) fileToolsEnabled(mode, deskMode string) bool {
	return mode == "desk" && normalizeDeskMode(deskMode) == deskModeDirect && b.workDir != ""
}

// legacyWritesEnabled gates the v1.9.0 opportunistic write interception on
// `event: hermes.tool.progress` frames (see handleToolFileWrite).
//
// Wider than fileToolsEnabled by exactly one case, deskModeLegacy: a frontend
// that never sends deskMode keeps writing files as it did in v1.9.x, so
// upgrading the backend alone changes nothing for it. "insert" still blocks it —
// a teacher who asked for copie/insertion must not have a file mutated by a
// write-flavoured progress frame.
func (b *HermesBridge) legacyWritesEnabled(mode, deskMode string) bool {
	if mode != "desk" || b.workDir == "" {
		return false
	}
	sub := normalizeDeskMode(deskMode)
	return sub == deskModeDirect || sub == deskModeLegacy
}

// fileToolArgs is the union of the arguments of the three tools. A single struct
// keeps the decoding of the model's arguments string in one place.
type fileToolArgs struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// checkFileExt rejects extensions outside allowedFileExts.
func checkFileExt(relPath string) error {
	ext := strings.ToLower(filepath.Ext(relPath))
	if !allowedFileExts[ext] {
		return fmt.Errorf("extension %q non autorisée pour %s : seuls les fichiers .md, .json et .txt sont accessibles", ext, relPath)
	}
	return nil
}

// readFileTool returns the content of a workspace file, truncated to
// maxReadFileChars with an explicit notice so the model never mistakes a cut
// file for a complete one.
func (b *HermesBridge) readFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	// Reads are filtered like writes: staying inside the workspace is not enough,
	// since everything read here leaves the pod for the gateway.
	if err := checkFileExt(args.Path); err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", "", fmt.Errorf("lecture impossible de %s : %v", args.Path, err)
	}
	runes := []rune(string(raw))
	if len(runes) > maxReadFileChars {
		return fmt.Sprintf("%s\n\n[…contenu tronqué : seuls les %d premiers caractères sur %d sont renvoyés. Ne réécris pas ce fichier en entier à partir de cet extrait, utilise patch_file.]",
			string(runes[:maxReadFileChars]), maxReadFileChars, len(runes)), "", nil
	}
	return string(raw), "", nil
}

// writeFileTool writes a whole file inside the workspace and reports the
// relative path as changed so the editor can reload it.
func (b *HermesBridge) writeFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	if err := checkFileExt(args.Path); err != nil {
		return "", "", err
	}
	if n := len([]rune(args.Content)); n > maxWriteFileChars {
		return "", "", fmt.Errorf("contenu trop long pour %s : %d caractères pour un maximum de %d. Découpe la modification et utilise patch_file", args.Path, n, maxWriteFileChars)
	}
	// A file longer than what read_file returns whole can only have been seen
	// truncated, so a shorter rewrite means the tail is being dropped. The
	// truncation notice returned by read_file asks the model not to do this, but a
	// notice is not a guard: refuse, and say what to use instead.
	if existing, err := os.ReadFile(abs); err == nil {
		if oldLen := len([]rune(string(existing))); oldLen > maxReadFileChars && len([]rune(args.Content)) < oldLen {
			return "", "", fmt.Errorf("réécriture refusée pour %s : le fichier fait %d caractères, tu n'en as vu que les %d premiers et le contenu proposé est plus court. Modifie-le avec patch_file", args.Path, oldLen, maxReadFileChars)
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return "", "", fmt.Errorf("création du dossier parent impossible pour %s : %v", args.Path, err)
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0644); err != nil {
		return "", "", fmt.Errorf("écriture impossible de %s : %v", args.Path, err)
	}
	return fmt.Sprintf("Fichier %s écrit (%d octets).", args.Path, len(args.Content)), args.Path, nil
}

// patchFileTool replaces old_string by new_string exactly once. A missing or
// ambiguous old_string is an error result handed back to the model: never a
// silent no-op (the teacher would believe the edit landed) and never a partial
// write.
func (b *HermesBridge) patchFileTool(args fileToolArgs) (string, string, error) {
	if args.Path == "" {
		return "", "", fmt.Errorf("paramètre path manquant")
	}
	abs, err := b.safePath(args.Path)
	if err != nil {
		return "", "", fmt.Errorf("chemin refusé : %s est en dehors du dossier de travail", args.Path)
	}
	if err := checkFileExt(args.Path); err != nil {
		return "", "", err
	}
	if args.OldString == "" {
		return "", "", fmt.Errorf("old_string vide : fournis l'extrait exact à remplacer dans %s", args.Path)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", "", fmt.Errorf("lecture impossible de %s : %v", args.Path, err)
	}
	text := string(raw)
	switch n := strings.Count(text, args.OldString); {
	case n == 0:
		return "", "", fmt.Errorf("old_string introuvable dans %s : le fichier n'a pas été modifié. Relis-le avec read_file et recopie l'extrait exact", args.Path)
	case n > 1:
		return "", "", fmt.Errorf("old_string apparaît %d fois dans %s : le fichier n'a pas été modifié. Allonge old_string pour qu'il soit unique", n, args.Path)
	}
	patched := strings.Replace(text, args.OldString, args.NewString, 1)
	if err := os.WriteFile(abs, []byte(patched), 0644); err != nil {
		return "", "", fmt.Errorf("écriture impossible de %s : %v", args.Path, err)
	}
	return fmt.Sprintf("Fichier %s modifié (%d octets).", args.Path, len(patched)), args.Path, nil
}

// execFileTool decodes the model's arguments string and dispatches to the
// matching executor. Every failure (bad JSON, unknown tool, refused path) comes
// back as an error the model can act on, never as a job-level error event.
func (b *HermesBridge) execFileTool(name, rawArgs string) (string, string, error) {
	var args fileToolArgs
	if rawArgs == "" {
		rawArgs = "{}"
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", "", fmt.Errorf("arguments JSON invalides pour %s : %v", name, err)
	}
	switch name {
	case "read_file":
		return b.readFileTool(args)
	case "write_file":
		return b.writeFileTool(args)
	case "patch_file":
		return b.patchFileTool(args)
	default:
		return "", "", fmt.Errorf("outil inconnu : %s (disponibles : read_file, write_file, patch_file)", name)
	}
}

// writeToolNames lists tool names that represent a file-write operation.
var writeToolNames = map[string]bool{
	"write":       true,
	"write_file":  true,
	"create_file": true,
	"edit":        true,
	"edit_file":   true,
}

// writeMirrorResult is what a tool-progress frame did to the workspace.
//
// The two zero-value cases must not be confused in the per-job diagnostic:
// "this frame was not a completed file write" (nothing to mirror) and "it was,
// and we refused it" (something the teacher expected to land did not).
type writeMirrorResult struct {
	// path is the WORKSPACE-RELATIVE path written, "" if nothing was written.
	// Relative because the frontend reloads the editor by relative path.
	path string
	// skipped marks a completed file write that was refused (unknown Hermes-side
	// prefix, path escaping the workspace, extension not allowed).
	skipped bool
}

// handleToolFileWrite inspects a tool-progress payload and, if it represents a
// completed file-write operation, mirrors the file into the workspace and
// returns its relative path.
//
// Lya's own tools write to HER filesystem (see stripHermesFSPrefix), so this is
// the only path by which such a write reaches the teacher's workspace.
func (b *HermesBridge) handleToolFileWrite(payload map[string]interface{}) writeMirrorResult {
	if b.workDir == "" {
		return writeMirrorResult{}
	}

	// Normalize tool name (same keys as frontend/src/tools.ts)
	name := strFromMap(payload, "name")
	if name == "" {
		name = strFromMap(payload, "tool")
	}
	if name == "" {
		name = strFromMap(payload, "toolName")
	}
	if name == "" {
		name = strFromMap(payload, "label")
	}

	if !writeToolNames[name] {
		return writeMirrorResult{}
	}

	// Check status is "done"
	status := strFromMap(payload, "status")
	if status == "" {
		status = strFromMap(payload, "state")
	}
	if status != "done" {
		return writeMirrorResult{}
	}

	// Normalize path
	path := strFromMap(payload, "path")
	if path == "" {
		path = strFromMap(payload, "file")
	}
	if path == "" {
		path = strFromArgs(payload, "path")
	}
	if path == "" {
		path = strFromArgs(payload, "file")
	}
	if path == "" {
		return writeMirrorResult{}
	}

	// Normalize content
	content := strFromMap(payload, "content")
	if content == "" {
		content = strFromMap(payload, "output")
	}
	if content == "" {
		content = strFromMap(payload, "result")
	}
	if content == "" {
		content = strFromArgs(payload, "content")
	}
	if content == "" {
		return writeMirrorResult{}
	}

	// Map Lya's filesystem onto the workspace before jailing: she reports absolute
	// paths on her own PVC and safePath only accepts relative ones.
	relPath, known := b.stripHermesFSPrefix(path)
	if !known {
		// Distinct from the escape message below on purpose: this one is an
		// operator problem (Lya's storage moved, set HERMES_FS_PREFIXES), the other
		// one is a path trying to leave the workspace. One shared "unsafe path"
		// line made the two indistinguishable in `kubectl logs`.
		log.Printf("hermes: file write skipped (unknown Hermes-side prefix, not under %s): %q",
			strings.Join(b.fsPrefixes, " or "), path)
		return writeMirrorResult{skipped: true}
	}
	if strings.TrimSpace(relPath) == "" {
		log.Printf("hermes: file write skipped (path names a Hermes mount root, not a file): %q", path)
		return writeMirrorResult{skipped: true}
	}

	// Validate path — safePath stays the ONLY path validator, applied to what is
	// left after stripping, so "/opt/data/../../etc/passwd" is refused here.
	absPath, err := b.safePath(relPath)
	if err != nil {
		log.Printf("hermes: file write skipped (path escapes the workspace): reported=%q relative=%q: %v", path, relPath, err)
		return writeMirrorResult{skipped: true}
	}
	// Same allowlist as the file tools: staying inside the workspace is not
	// enough, since everything written here is served back to the browser and
	// opened in the Markdown editor.
	if err := checkFileExt(relPath); err != nil {
		log.Printf("hermes: file write skipped (extension not allowed): reported=%q: %v", path, err)
		return writeMirrorResult{skipped: true}
	}

	// Create parent directories and write file
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		log.Printf("hermes: cannot create parent dir for %q: %v", relPath, err)
		return writeMirrorResult{skipped: true}
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		log.Printf("hermes: cannot write file %q: %v", relPath, err)
		return writeMirrorResult{skipped: true}
	}

	log.Printf("hermes: mirrored file write reported=%q into %s (%d bytes)", path, relPath, len(content))
	return writeMirrorResult{path: relPath}
}

// strFromMap extracts a string value from a map by key.
func strFromMap(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// strFromArgs extracts a string from the nested "args" map.
func strFromArgs(m map[string]interface{}, key string) string {
	args, ok := m["args"]
	if !ok {
		return ""
	}
	argsMap, ok := args.(map[string]interface{})
	if !ok {
		return ""
	}
	return strFromMap(argsMap, key)
}

// HandleWebSocket upgrades to WS and handles prompt/subscribe messages.
func (b *HermesBridge) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Keepalive pings (survive idle timeouts)
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
				log.Printf("ws read error: %v", err)
			}
			return
		}

		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content,omitempty"`
			System  string `json:"system,omitempty"`
			// Path of the file the teacher has open, for the agent_usage log.
			// Lya cannot read the workspace, so the frontend inlines the file
			// content into Content itself — this is observability, not context.
			CurrentFile string `json:"currentFile,omitempty"`
			Mode        string `json:"mode,omitempty"`
			// Desk sub-mode: "insert" (Lya answers, the teacher inserts) or
			// "direct" (Lya edits the working file through the file tools).
			// Only read when Mode is "desk"; see normalizeDeskMode for the default.
			DeskMode string `json:"deskMode,omitempty"`
			JobID    string `json:"jobId,omitempty"`
			After    int    `json:"after,omitempty"`
		}
		if err := json.Unmarshal(message, &msg); err != nil {
			sendWSJSON(conn, StreamEvent{Type: "error", Error: "invalid JSON"})
			continue
		}

		switch msg.Type {
		case "prompt":
			// Start a new job and immediately subscribe
			job := b.startJob(msg.Content, msg.System, msg.CurrentFile, msg.Mode, msg.DeskMode)
			sendWSJSON(conn, StreamEvent{Type: "meta", JobID: job.ID})
			b.streamToWS(conn, job, 0)
		case "subscribe":
			// Reconnect to existing job
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

// startJob creates a background job that calls Hermes streaming.
func (b *HermesBridge) startJob(content, systemPrompt, currentFile, mode, deskMode string) *Job {
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
		// Default mode to "desk" if empty
		if mode == "" {
			mode = "desk"
		}
		deskMode = normalizeDeskMode(deskMode)
		err := b.callHermesStream(ctx, job, content, systemPrompt, currentFile, mode, deskMode)
		durationMs := time.Since(start).Milliseconds()
		status := "done"
		if job.status == JobRunning {
			if err != nil {
				// An auth failure is not a transient glitch: telling the teacher
				// to retry would be a lie, and it hides an operator problem.
				userMsg := "Échec IA. Réessaie."
				if strings.Contains(err.Error(), "hermes auth failed") {
					userMsg = "Clé API Hermes invalide (HERMES_API_KEY ≠ API_SERVER_KEY de Lya)."
				}
				job.append(StreamEvent{Type: "error", Error: userMsg, Detail: err.Error()})
				status = "error"
			} else {
				// Should not happen — done is emitted inside callHermesStream
				job.append(StreamEvent{Type: "done"})
			}
		}
		if job.status == JobError {
			status = "error"
		}
		// agent_usage log line
		file := currentFile
		if file == "" {
			file = "-"
		}
		// deskMode is logged even outside mode desk: it is the effective value the
		// gate used, so a "no file was written" report can be traced to the
		// sub-mode the teacher was in rather than guessed.
		log.Printf("agent_usage agent=lya mode=%s deskMode=%s jobId=%s promptLen=%d file=%s durationMs=%d status=%s",
			mode, deskMode, job.ID, len(content), file, durationMs, status)
	}()
	return job
}

// callHermesStream drives the conversation with Lya: it posts the messages,
// consumes the streamed answer and, in mode Desk, executes the file tools the
// model asked for and re-posts with their results until the model answers with
// text only (or maxToolLoops is reached).
//
// mode and deskMode control file access: only mode "desk" in the "direct"
// sub-mode declares and executes the file tools. See fileToolsEnabled and
// legacyWritesEnabled.
//
// currentFile is the file the Desk panel announces as the working file. It is
// not a jail (creating a companion file is legitimate), but a write elsewhere is
// flagged to the teacher and to the model, because the UI promises one file.
func (b *HermesBridge) callHermesStream(ctx context.Context, job *Job, content, systemPrompt, currentFile, mode, deskMode string) error {
	messages := []map[string]interface{}{}
	if systemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": content})

	toolsEnabled := b.fileToolsEnabled(mode, deskMode)

	// One greppable diagnostic per job. supported=true means Hermes really did
	// forward the `tools` parameter to the model and streamed tool_calls back —
	// something that can only be observed in production (see hermesFileTools).
	var (
		full          strings.Builder
		loops         int
		toolCallCount int
		sawToolCalls  bool
		// Characters of tool results already fed back, for maxToolResultChars.
		toolResultChars int
		// At least one tool actually modified a file, so an interrupted loop must
		// tell the teacher the workspace is already in an intermediate state.
		wroteFiles bool
		// Writes Lya performed with her OWN tools and that we mirrored into the
		// workspace, and those we refused. See writeMirrorResult.
		mirroredWrites int
		skippedWrites  int
	)
	// deskMode is part of the line so 'no tool calls because Hermes ignores the
	// tools parameter' cannot be confused with 'no tool calls because the teacher
	// was in the copie/insertion sub-mode, where nothing is declared'.
	//
	// mirroredWrites/skippedWrites answer the question this feature failed on in
	// production: Lya reported writing the file, the teacher saw nothing change,
	// and the logs could not say whether anything reached the workspace.
	// supported=false with mirroredWrites>0 is the normal shape today: she ignores
	// the declared tools and uses her own.
	defer func() {
		log.Printf("hermes: tool_calls supported=%t (mode=%s deskMode=%s loops=%d toolCalls=%d mirroredWrites=%d skippedWrites=%d)",
			sawToolCalls, mode, deskMode, loops, toolCallCount, mirroredWrites, skippedWrites)
	}()

	for loops = 1; loops <= maxToolLoops; loops++ {
		payload := map[string]interface{}{
			"messages": messages,
			"provider": "custom",
			"stream":   true,
		}
		if toolsEnabled {
			payload["tools"] = hermesFileTools
			payload["tool_choice"] = "auto"
		}
		body, _ := json.Marshal(payload)

		resp, err := b.postHermesStream(ctx, body)
		if err != nil {
			return err
		}
		turn, err := b.streamTurn(job, resp.Body, mode, deskMode, &full)
		resp.Body.Close()
		// Counted before any early return: a turn that mirrored a write and then
		// failed still changed the teacher's workspace.
		mirroredWrites += turn.mirroredWrites
		skippedWrites += turn.skippedWrites
		if turn.mirroredWrites > 0 {
			wroteFiles = true
		}
		if err != nil {
			return err
		}
		if turn.stopped {
			return nil // chunk.error already surfaced as an error event
		}

		// A call dropped by the finish_reason cross-check still proves the gateway
		// forwarded `tools` and the model tried to use them. Leaving it out of the
		// diagnostic logged supported=false for a gateway that does support tool
		// calls — the exact misattribution that line exists to prevent.
		if turn.droppedCalls > 0 {
			sawToolCalls = true
			toolCallCount += turn.droppedCalls
		}

		if len(turn.toolCalls) == 0 {
			// Plain text answer: exactly the pre-tool-loop behaviour.
			if turn.sawDone {
				job.append(StreamEvent{Type: "done", Reply: full.String()})
			}
			return nil
		}

		sawToolCalls = true
		if !toolsEnabled {
			// No tools were declared, so tool_calls here are unsolicited. Executing
			// them would let mode Lya (or a job without workspace) touch files.
			log.Printf("hermes: refusing %d unsolicited tool call(s) — file tools are only enabled in mode desk, sub-mode direct (mode=%s deskMode=%s workDir=%q)",
				len(turn.toolCalls), mode, deskMode, b.workDir)
			job.append(StreamEvent{Type: "done", Reply: full.String()})
			return nil
		}

		// Feed the assistant turn back verbatim: the OpenAI protocol requires the
		// assistant message carrying tool_calls before the matching tool results.
		calls := make([]map[string]interface{}, 0, len(turn.toolCalls))
		for _, tc := range turn.toolCalls {
			calls = append(calls, map[string]interface{}{
				"id":   tc.id,
				"type": "function",
				"function": map[string]interface{}{
					"name": tc.name,
					// Shrunk, not verbatim: see shrinkEchoedArgs. The call still has to
					// be echoed (the protocol requires it before the tool results), but
					// a whole rewritten course file does not have to be echoed with it.
					"arguments": shrinkEchoedArgs(tc.args.String()),
				},
			})
		}
		messages = append(messages, map[string]interface{}{
			"role":       "assistant",
			"content":    turn.text,
			"tool_calls": calls,
		})

		for _, tc := range turn.toolCalls {
			toolCallCount++
			rawArgs := tc.args.String()
			path := pathFromRawArgs(rawArgs)
			// A write outside the file the Desk panel names. Not refused — asking
			// Lya to create a companion file is legitimate — but never silent: the
			// UI promises one working file, so the teacher gets the deviation in the
			// thread and the model gets it in the tool result.
			outside := outsideWorkingFile(tc.name, path, currentFile)
			// Report before executing so the teacher sees the action live, even if
			// the tool is slow or fails.
			job.append(StreamEvent{Type: "tool", Tool: toolEventPayload(tc.name, path, "running", "", outside)})

			result, changedPath, execErr := b.execFileTool(tc.name, rawArgs)
			if execErr != nil {
				log.Printf("hermes: tool %s path=%q failed: %v", tc.name, path, execErr)
				job.append(StreamEvent{Type: "tool", Tool: toolEventPayload(tc.name, path, "error", execErr.Error(), outside)})
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": tc.id,
					// A refusal is exempt from the budget: it is short, and a model told
					// "Résultat non transmis" instead of "old_string introuvable" retries
					// the very call that cannot work.
					"content": budgetToolResult("Erreur : "+execErr.Error(), &toolResultChars, true),
				})
				continue
			}

			log.Printf("hermes: tool %s path=%q ok (%d chars returned)", tc.name, path, len(result))
			if outside {
				log.Printf("hermes: tool %s wrote %q, outside the announced working file %q", tc.name, path, currentFile)
				if currentFile == "" {
					result += fmt.Sprintf("\n\nAttention : l'enseignant n'a aucun fichier ouvert dans son éditeur, et tu viens de modifier %s. Dis-le explicitement dans ta réponse.", path)
				} else {
					result += fmt.Sprintf("\n\nAttention : le fichier de travail de l'enseignant est %s, et tu viens de modifier %s. Dis-le explicitement dans ta réponse.", currentFile, path)
				}
			}
			job.append(StreamEvent{Type: "tool", Tool: toolEventPayload(tc.name, path, "done", "", outside)})
			if changedPath != "" {
				wroteFiles = true
				// Same synthetic shape as the v1.9.0 write interception, so
				// Chat.tsx reloads the editor without any frontend change.
				job.append(StreamEvent{Type: "tool", Tool: map[string]interface{}{
					"name": "file_changed",
					"path": changedPath,
				}})
			}
			messages = append(messages, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tc.id,
				// Only read results can be large enough to be worth cutting, and only
				// they can be cut without lying: a write confirmation replaced by the
				// budget notice makes a landed write indistinguishable from a suppressed
				// one, and the model then rewrites the file it just wrote.
				"content": budgetToolResult(result, &toolResultChars, tc.name != "read_file"),
			})
		}
	}

	// Loop cap reached and the model still wants tools: stop here rather than let
	// the job spin until jobRunTimeout with nothing shown to the teacher.
	loops = maxToolLoops
	// The tools of the last iteration DID run, so files may already be modified.
	// Saying only "interrompue" reads like an abort and sent teachers looking for
	// an unchanged file that had in fact been rewritten.
	cut := fmt.Sprintf("\n\n_(Boucle d'outils interrompue après %d échanges. Relance la demande en la découpant en étapes plus petites.)_", maxToolLoops)
	if wroteFiles {
		cut = fmt.Sprintf("\n\n_(Boucle d'outils interrompue après %d échanges. Les modifications déjà écrites dans tes fichiers sont conservées : vérifie ton fichier de travail avant de relancer la demande, découpée en étapes plus petites.)_", maxToolLoops)
	}
	full.WriteString(cut)
	job.append(StreamEvent{Type: "delta", Text: cut})
	job.append(StreamEvent{Type: "done", Reply: full.String()})
	log.Printf("hermes: tool loop cut short after %d iterations (mode=%s)", maxToolLoops, mode)
	return nil
}

// pathFromRawArgs best-effort extracts the "path" argument for logging and tool
// events. A malformed payload must not stop us from reporting the tool call.
func pathFromRawArgs(rawArgs string) string {
	var args fileToolArgs
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	return args.Path
}

// toolEventPayload builds the tool event sent to the frontend.
//
// status and error are both carried: a refused write is a normal outcome here
// (missing old_string, rejected extension, path outside the workspace), and
// without the error the chat would show it with the same "✏️ Écriture de …"
// label as a successful one. Optional fields are omitted rather than sent empty,
// so normalizeTool in frontend/src/tools.ts sees the same shape as before.
func toolEventPayload(name, path, status, errMsg string, outsideWorkingFile bool) map[string]interface{} {
	payload := map[string]interface{}{
		"name":   name,
		"path":   path,
		"status": status,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	if outsideWorkingFile {
		payload["outsideWorkingFile"] = true
	}
	return payload
}

// outsideWorkingFile reports a write landing somewhere else than the file the
// Desk panel announces as the working file.
//
// Only meaningful for writes: reads are harmless anywhere in the workspace.
// With no working file announced every write is flagged, because the panel is
// then showing "⚠️ Aucun fichier ouvert" while the tools stay armed — returning
// false there reported a write in an arbitrary course file as if it were the
// file the teacher was looking at.
func outsideWorkingFile(toolName, path, currentFile string) bool {
	if path == "" {
		return false
	}
	if toolName != "write_file" && toolName != "patch_file" {
		return false
	}
	if currentFile == "" {
		return true
	}
	return filepath.Clean(path) != filepath.Clean(currentFile)
}

// shrinkEchoedArgs shortens the long string arguments of a tool call before it
// is echoed back in the assistant message.
//
// The echo is mandatory (the OpenAI protocol wants the assistant tool_calls
// message before the matching tool results) but its content is not: a
// write_file carries the whole file, up to maxWriteFileChars, and it is resent
// on every following turn. Budgeting the results while echoing the arguments
// verbatim left the context-overflow path wide open on writes.
//
// The result stays valid JSON with the same keys, so a gateway that parses it
// still sees a well-formed call. Arguments that do not decode are truncated as
// an opaque string — they were already unusable.
func shrinkEchoedArgs(rawArgs string) string {
	runes := []rune(rawArgs)
	if len(runes) <= maxEchoedArgsChars {
		return rawArgs
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &decoded); err != nil {
		return string(runes[:maxEchoedArgsChars])
	}
	for key, value := range decoded {
		s, ok := value.(string)
		if !ok {
			continue
		}
		if sr := []rune(s); len(sr) > maxEchoedArgFieldChars {
			decoded[key] = string(sr[:maxEchoedArgFieldChars]) +
				"…[tronqué dans l'historique : cet argument a déjà été transmis à l'outil]"
		}
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return string(runes[:maxEchoedArgsChars])
	}
	return string(out)
}

// budgetToolResult caps the CUMULATIVE size of the tool results fed back to
// Hermes at maxToolResultChars, updating used in place.
//
// Each result stays in `messages` for the rest of the job, so over up to
// maxToolLoops round-trips a couple of large read_file results are enough to
// overflow the upstream context window. That arrives as a gateway 4xx, i.e. as
// "Échec IA. Réessaie." for the teacher, with the work of the previous turns
// lost. A cut result is something the model can still work with, and the notice
// tells it what to do instead of asking for the file again.
//
// alwaysDeliver marks a terminal message — a write confirmation or a refusal —
// which must reach the model whatever the budget: those are the only way it can
// tell a landed write from a suppressed one, and once told "Résultat non
// transmis" the natural recovery is to write the same file again. They are still
// counted against the budget (so the reads that follow shrink accordingly) and
// hard-capped at maxConfirmationChars, since they quote a path the model chose.
func budgetToolResult(result string, used *int, alwaysDeliver bool) string {
	if alwaysDeliver {
		runes := []rune(result)
		if len(runes) > maxConfirmationChars {
			result = string(runes[:maxConfirmationChars]) + "…"
			runes = []rune(result)
		}
		*used += len(runes)
		return result
	}
	remaining := maxToolResultChars - *used
	if remaining <= 0 {
		return "[Résultat non transmis : le budget de contexte des outils est épuisé pour cette conversation. Termine avec ce que tu as déjà lu, ou demande à l'enseignant de relancer la demande découpée en étapes.]"
	}
	runes := []rune(result)
	if len(runes) <= remaining {
		*used += len(runes)
		return result
	}
	*used = maxToolResultChars
	return string(runes[:remaining]) +
		"\n\n[…coupé : budget de contexte des outils atteint. Ne réécris pas ce fichier en entier à partir de cet extrait, utilise patch_file.]"
}

// postHermesStream does POST /v1/chat/completions with stream:true and maps
// non-2xx responses to the teacher-facing errors startJob knows about.
func (b *HermesBridge) postHermesStream(ctx context.Context, body []byte) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", b.hermesURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "assisted-teacher-backend")

	client := &http.Client{Timeout: 0} // No timeout — stream runs for minutes
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hermes unreachable: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		bodySnippet := truncateStr(string(raw), 200)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			// Hermes rejected the Bearer key — this is NOT Authelia, and not the
			// teacher's session. Typical body: "Invalid gateway API key".
			//
			// Log the key fingerprint so the key actually sent can be compared
			// with the one Hermes holds, without ever logging the secret:
			//   kubectl exec -n openclaw deploy/hermes-lya -- \
			//     sh -c 'grep API_SERVER_KEY /opt/data/.env | cut -d= -f2 | tr -d "\n" | sha256sum'
			//
			// Beware: Hermes validates API_SERVER_KEY from the PVC file
			// /opt/data/.env, NOT from the K8s/Infisical env of the pod. The two
			// diverge after an Infisical rotation, so matching secrets in
			// Kubernetes are not proof of a matching key. See README.
			log.Printf("hermes: auth rejected (HTTP %d) with keyLen=%d keyFp=%s url=%s",
				resp.StatusCode, len(b.apiKey), keyFingerprint(b.apiKey), b.hermesURL)
			return nil, fmt.Errorf("hermes auth failed (HTTP %d): %s — vérifie HERMES_API_KEY (= API_SERVER_KEY de Lya, y compris dans /opt/data/.env du PVC)", resp.StatusCode, bodySnippet)
		}
		return nil, fmt.Errorf("hermes HTTP %d: %s", resp.StatusCode, bodySnippet)
	}
	return resp, nil
}

// toolCallAccum stitches back one streamed tool call: id and function.name arrive
// once (usually on the first delta of that index) while function.arguments comes
// as a stream of string fragments that must be concatenated before decoding.
type toolCallAccum struct {
	id   string
	name string
	args strings.Builder
}

// turnResult is what a single Hermes response (one SSE stream) produced.
type turnResult struct {
	text         string           // text streamed during this turn only
	toolCalls    []*toolCallAccum // in tool_call index order, empty for a text answer
	sawDone      bool             // the stream ended with data: [DONE]
	stopped      bool             // chunk.error surfaced: the job must stop, error already emitted
	finishReason string           // last non-empty finish_reason: "tool_calls", "stop", "length"…
	// droppedCalls counts the calls discarded by the finish_reason cross-check.
	// The caller needs it for the per-job diagnostic: a dropped call still means
	// the gateway forwarded `tools` and the model answered with one.
	droppedCalls int
	// mirroredWrites and skippedWrites count the hermes.tool.progress file writes
	// that landed in the workspace and those that were refused. Production showed
	// Lya writing on her own filesystem with the app's workspace left untouched:
	// these two counters are what makes "did anything land" greppable.
	mirroredWrites int
	skippedWrites  int
}

// streamTurn consumes one SSE stream. It forwards deltas and hermes.tool.progress
// frames exactly as before, accumulates the turn text into full (the whole job's
// reply) and collects any tool_calls for the caller to execute.
//
// data: [DONE] ends the TURN, not necessarily the job: the done event is emitted
// by callHermesStream only once a turn came back without tool calls.
func (b *HermesBridge) streamTurn(job *Job, body io.Reader, mode, deskMode string, full *strings.Builder) (turnResult, error) {
	var res turnResult

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var turnText strings.Builder
	var eventName string
	// Tool-call deltas are addressed by index, and indexes may arrive interleaved.
	byIndex := map[int]*toolCallAccum{}
	var order []int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / keepalive
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		if data == "[DONE]" {
			res.sawDone = true
			break
		}

		// Handle tool progress events from Hermes
		if eventName == "hermes.tool.progress" {
			var tool interface{}
			json.Unmarshal([]byte(data), &tool)
			job.append(StreamEvent{Type: "tool", Tool: tool})

			// If this is a file-write tool with status "done", write file to disk
			// and emit a file_changed event (like PiBridge does).
			// mode Lya must never write, and neither must the copie/insertion
			// sub-mode — otherwise a write-flavoured progress frame would mutate a
			// file the teacher asked to keep intact. A client that sends no
			// deskMode at all keeps this v1.9.x path: see legacyWritesEnabled.
			if b.legacyWritesEnabled(mode, deskMode) {
				if toolMap, ok := tool.(map[string]interface{}); ok {
					mirror := b.handleToolFileWrite(toolMap)
					switch {
					case mirror.path != "":
						res.mirroredWrites++
						job.append(StreamEvent{
							Type: "tool",
							Tool: map[string]interface{}{
								"name": "file_changed",
								// Relative: Chat.tsx reloads the editor by
								// workspace-relative path.
								"path": mirror.path,
							},
						})
					case mirror.skipped:
						res.skippedWrites++
					}
				}
			}
			continue
		}

		// Standard chat.completion.chunk
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			job.append(StreamEvent{Type: "error", Error: chunk.Error.Message})
			res.stopped = true
			return res, nil
		}
		if len(chunk.Choices) > 0 {
			if fr := chunk.Choices[0].FinishReason; fr != "" {
				res.finishReason = fr
			}
			text := chunk.Choices[0].Delta.Content
			if text != "" {
				turnText.WriteString(text)
				full.WriteString(text)
				job.append(StreamEvent{Type: "delta", Text: text})
			}
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc, ok := byIndex[idx]
				if !ok {
					acc = &toolCallAccum{}
					byIndex[idx] = acc
					order = append(order, idx)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.args.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return res, err
	}

	res.text = turnText.String()
	for i, idx := range order {
		acc := byIndex[idx]
		if acc.name == "" {
			continue // an arguments-only fragment with no name is not callable
		}
		// Cross-check finish_reason instead of trusting the surviving accumulators
		// alone: a gateway that streams a tool_calls delta and then finishes on
		// "stop" or "length" left us a fragment, and executing a write with
		// half-decoded arguments is worse than not writing at all. Arguments that
		// do decode are still executed whatever the finish reason — some gateways
		// report "stop" even for a complete tool call.
		if res.finishReason != "" && res.finishReason != "tool_calls" && !json.Valid([]byte(acc.args.String())) {
			log.Printf("hermes: dropping incomplete tool call %q (finish_reason=%s, args=%q)",
				acc.name, res.finishReason, truncateStr(acc.args.String(), 120))
			res.droppedCalls++
			// Say it in the thread too. Dropped silently, the teacher got a done
			// event with an empty reply (the cut turn often carries no text at all)
			// and no explanation of why their request produced nothing.
			job.append(StreamEvent{Type: "tool", Tool: toolEventPayload(acc.name, "", "error",
				fmt.Sprintf("appel interrompu par le modèle (finish_reason=%s) : rien n'a été exécuté, relance ta demande", res.finishReason),
				false)})
			continue
		}
		if acc.id == "" {
			// Some gateways omit the id. The tool result message MUST carry a
			// tool_call_id matching the assistant message, so synthesise a stable one.
			acc.id = fmt.Sprintf("call_%d", i)
		}
		res.toolCalls = append(res.toolCalls, acc)
	}
	return res, nil
}

// streamToWS sends backlog + live events to the WebSocket. Returns when job finishes or WS disconnects.
func (b *HermesBridge) streamToWS(conn *websocket.Conn, job *Job, after int) {
	backlog, live, unsub := job.Subscribe(after)
	defer unsub()

	// Send backlog
	for _, ev := range backlog {
		if err := sendWSJSON(conn, ev); err != nil {
			return
		}
	}

	// Stream live
	for ev := range live {
		if err := sendWSJSON(conn, ev); err != nil {
			return // WS disconnected — Job keeps running
		}
		if ev.Type == "done" || ev.Type == "error" {
			return
		}
	}
}

func sendWSJSON(conn *websocket.Conn, data interface{}) error {
	msg, _ := json.Marshal(data)
	return conn.WriteMessage(websocket.TextMessage, msg)
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
