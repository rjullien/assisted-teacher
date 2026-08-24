import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import ReactMarkdown from 'react-markdown'
import { AuthWebSocket, handleAuthExpired } from '../api'
import { useI18n } from '../i18n'
import { normalizeTool, isFileOp, toolLabel, WRITE_TOOL_NAMES } from '../tools'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  isStreaming?: boolean
  piWroteFiles?: boolean
}

interface StreamEvent {
  seq: number
  type: string // delta | tool | done | error | meta
  text?: string
  reply?: string
  error?: string
  detail?: string
  jobId?: string
  tool?: unknown // shape is not guaranteed by Hermes — see tools.ts
}

interface ProgrammeData {
  niveau: string
  cecrl: Record<string, string>
  axes_culturels: Array<{
    numero: number
    titre: string
    description: string
    exemples_objets_etude: string[]
    obligatoire: boolean
  }>
  contraintes: {
    axes_a_traiter: number
    axes_total: number
    axe_obligatoire: number
    note: string
  }
  competences: Record<string, {
    code: string
    descripteur: string
    niveau_attendu_LVA: string
    niveau_attendu_LVB: string
  }>
  grammaire: string[]
  vocabulaire_thematique: Record<string, string[]>
}

/**
 * Everything about a chat that must survive the component being unmounted.
 *
 * Chat is rendered conditionally — `{activeTab === 'chat' && <Chat/>}` in
 * MobileLayout, and behind the workspace/Lya branch in App. Leaving the tab or
 * switching to mode Lya therefore destroys the component and, with it, any
 * local state. Holding these four fields in the parent is what keeps the
 * conversation alive across those unmounts.
 *
 * `jobId` + `lastSeq` are what allow a generation that is still running on the
 * backend to be picked up again on remount: the Job log is append-only and
 * `Subscribe(after)` replays only events with `Seq > after`.
 */
export interface ChatSession {
  messages: ChatMessage[]
  /** Draft text typed but not sent yet. */
  input: string
  /** Backend job currently streaming, or null when idle. */
  jobId: string | null
  /** Highest StreamEvent.seq already applied — the replay cursor. */
  lastSeq: number
  /**
   * Desk sub-mode. Lives in the session rather than in local state so the
   * choice survives the unmounts described above (leaving the AI tab on mobile,
   * switching to mode Lya): a teacher who enabled direct updates would
   * otherwise silently fall back to copie/insertion on the next message.
   * Optional so an older persisted session still loads — see deskSubMode below.
   */
  deskSubMode?: DeskSubMode
}

/**
 * The two ways mode Desk can work.
 *
 * - `insert`: Lya answers in the chat, the teacher inserts what they keep. No
 *   file is ever modified (the backend declares no file tool at all).
 * - `direct`: Lya updates the working file herself through the backend file
 *   tools, and the panel shows which file is at stake.
 */
export type DeskSubMode = 'insert' | 'direct'

/**
 * Default sub-mode: the non-destructive one. A teacher who never saw the
 * selector must not discover it by finding a course file rewritten.
 */
export const DEFAULT_DESK_SUB_MODE: DeskSubMode = 'insert'

/**
 * Progress tokens emitted by the pi bridge, mapped to i18n keys.
 * The backend never sends a translated string — the UI is bilingual and owns
 * the wording. Unknown tokens are dropped, not rendered.
 */
const PI_STATUS_KEYS: Record<string, string> = {
  starting: 'piChat.statusStarting',
  thinking: 'piChat.statusThinking',
  retrying: 'piChat.statusRetrying',
  compacting: 'piChat.statusCompacting',
}

export const emptyChatSession: ChatSession = {
  messages: [],
  input: '',
  jobId: null,
  lastSeq: 0,
  deskSubMode: DEFAULT_DESK_SUB_MODE,
}

interface ChatProps {
  currentFile: string | null
  /**
   * Current text of `currentFile`, as loaded in the editor. Inlined into the
   * prompt in mode Desk — see buildDeskPrompt.
   */
  fileContent?: string
  onInsert: (text: string) => void
  programme: ProgrammeData | null
  agent?: 'lya' | 'pi'
  onFileChanged?: (path: string) => void
  /** Lifted state. When omitted, Chat keeps its own (used by tests). */
  session?: ChatSession
  onSessionChange?: (session: ChatSession) => void
  /** Display name from Authelia, so the assistant knows who it is helping. */
  userName?: string
}

// --- File context inlining (mode Desk) ---

/**
 * Maximum number of characters of file content inlined into a Desk prompt.
 *
 * Inlining makes the prompt as large as the file, so a long course document
 * would push the system prompt and the actual question out of the context
 * window. Anything past this cap is cut and flagged, never sent silently.
 */
export const MAX_INLINED_FILE_CHARS = 20000

/**
 * Appended when the file is longer than the cap. Explicit, so Lya treats the
 * extract as incomplete instead of assuming the document ends there.
 */
const TRUNCATION_NOTICE =
  '\n\n[…]\n\n(Contenu tronqué : le fichier est plus long que cet extrait. Demande-moi la suite si tu en as besoin.)'

/**
 * Picks a fence long enough that the file cannot close it early. Course files
 * are Markdown and routinely contain ``` blocks of their own.
 */
function fenceFor(content: string): string {
  const longestRun = (content.match(/`{3,}/g) || []).reduce((n, run) => Math.max(n, run.length), 2)
  return '`'.repeat(longestRun + 1)
}

/**
 * Builds the prompt for mode Desk, with the file content inlined.
 *
 * Mode Desk talks to Lya (Hermes), who runs in a different pod (namespace
 * `openclaw`, PVC mounted on /opt/data) and has no access to this app's
 * workspace. Sending her only the path made her answer "je ne vois pas de
 * fichier récent correspondant dans le dossier de travail" — she has no tool
 * able to open it. The content therefore has to travel inside the prompt.
 *
 * The question comes last so it stays the most recent, most salient part of
 * the message after a potentially long document.
 */
function buildDeskPrompt(currentFile: string, fileContent: string, question: string): string {
  const body =
    fileContent.length > MAX_INLINED_FILE_CHARS
      ? fileContent.slice(0, MAX_INLINED_FILE_CHARS) + TRUNCATION_NOTICE
      : fileContent
  const fence = fenceFor(body)
  return [
    `[Contexte: je travaille sur le fichier "${currentFile}". Son contenu est reproduit ci-dessous : tu n'as pas accès à mon dossier de travail, ne cherche pas à l'ouvrir.]`,
    '',
    `${fence}${currentFile}`,
    body,
    fence,
    '',
    'Ma demande :',
    question,
  ].join('\n')
}

// --- System prompt builder ---

function buildSystemPrompt(programme: ProgrammeData | null): string {
  if (!programme) {
    return `Tu es un assistant pédagogique spécialisé dans la création de cours d'ANGLAIS au lycée.

LANGUE DE TRAVAIL :
- Les consignes, explications pédagogiques et méta-commentaires sont en FRANÇAIS.
- TOUT le contenu pédagogique (exercices, textes, dialogues, vocabulaire, exemples, phrases modèles, gap-fills, QCM) doit être EN ANGLAIS.
- C'est un cours d'anglais : l'élève doit lire, écrire et pratiquer en anglais.

Tu génères du contenu d'enseignement de l'anglais pour des lycéens français.`
  }

  const { niveau, cecrl, axes_culturels, contraintes, competences, grammaire } = programme

  const axesList = axes_culturels
    .map((a) => `  ${a.numero}. ${a.titre}${a.obligatoire ? ' (OBLIGATOIRE)' : ''}`)
    .join('\n')

  const competencesList = Object.values(competences)
    .map((c) => `  - ${c.code} : ${c.descripteur} (LVA ${c.niveau_attendu_LVA}, LVB ${c.niveau_attendu_LVB})`)
    .join('\n')

  const grammaireList = grammaire.map((g) => `  - ${g}`).join('\n')

  return `Tu es un assistant pédagogique pour un cours d'ANGLAIS niveau ${niveau} (programme officiel BO 2025).

LANGUE DE TRAVAIL :
- Les consignes pédagogiques, explications méthodologiques et commentaires pour le prof sont en FRANÇAIS.
- TOUT le contenu destiné aux élèves (exercices, textes, dialogues, vocabulaire, exemples, gap-fills, QCM, phrases modèles) doit être EN ANGLAIS.
- C'est un cours d'anglais : l'élève doit lire, écrire et pratiquer en anglais.

OBJECTIFS CECRL :
- LVA : ${cecrl.LVA}
- LVB : ${cecrl.LVB}
${cecrl.LVC ? `- LVC : ${cecrl.LVC}` : ''}

AXES CULTURELS DU PROGRAMME :
${axesList}
Contrainte : ${contraintes.note}

COMPÉTENCES LANGAGIÈRES ATTENDUES :
${competencesList}

POINTS DE GRAMMAIRE AU PROGRAMME :
${grammaireList}

CONSIGNES :
- Adapte le vocabulaire et la grammaire au niveau CECRL visé (${cecrl.LVA} en LVA, ${cecrl.LVB} en LVB).
- Respecte les axes thématiques du programme officiel 2025.
- Propose des exercices variés (CO, CE, EO, EE, interaction, médiation).
- Ne dépasse PAS le niveau linguistique attendu pour la classe de ${niveau}.
- Quand tu proposes une activité, indique à quel axe culturel et quelle(s) compétence(s) elle se rattache.
- Le contenu pédagogique (exercices, textes, exemples, consignes élève) est TOUJOURS EN ANGLAIS.
- Les explications pour le professeur (objectifs, déroulement, corrections) sont en français.
- Si on te demande de vérifier la conformité d'un cours, analyse-le au regard du programme (axe, niveau, grammaire, vocabulaire).`
}

export default function Chat({
  currentFile,
  fileContent = '',
  onInsert,
  programme,
  agent = 'lya',
  onFileChanged,
  session: externalSession,
  onSessionChange,
  userName,
}: ChatProps) {
  const { t } = useI18n()

  // --- Persisted state (lifted to the parent when props are provided) --------
  const [internalSession, setInternalSession] = useState<ChatSession>(emptyChatSession)
  const session = externalSession ?? internalSession

  // The ref is updated eagerly inside patchSession, not on render: streaming
  // deltas arrive in bursts and each one must see the result of the previous
  // patch, before React has re-rendered.
  const sessionRef = useRef<ChatSession>(session)
  sessionRef.current = session

  const patchSession = useCallback(
    (patch: Partial<ChatSession> | ((prev: ChatSession) => Partial<ChatSession>)) => {
      const prev = sessionRef.current
      const delta = typeof patch === 'function' ? patch(prev) : patch
      const next = { ...prev, ...delta }
      sessionRef.current = next
      if (onSessionChange) onSessionChange(next)
      else setInternalSession(next)
    },
    [onSessionChange]
  )

  const messages = session.messages
  const input = session.input

  const setMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>> = useCallback(
    (update) => {
      patchSession((prev) => ({
        messages:
          typeof update === 'function'
            ? (update as (m: ChatMessage[]) => ChatMessage[])(prev.messages)
            : update,
      }))
    },
    [patchSession]
  )

  const setInput = useCallback((value: string) => patchSession({ input: value }), [patchSession])

  // A session persisted before the selector existed has no deskSubMode: fall
  // back to the safe sub-mode instead of undefined reaching the payload.
  const deskSubMode: DeskSubMode = session.deskSubMode ?? DEFAULT_DESK_SUB_MODE
  const setDeskSubMode = useCallback(
    (value: DeskSubMode) => patchSession({ deskSubMode: value }),
    [patchSession]
  )

  // --- Transient state (intentionally reset on remount) ---------------------
  const [connected, setConnected] = useState(false)
  const [authError, setAuthError] = useState(false)
  // Transient "Hermes is using a tool" line. Not part of the conversation.
  const [toolStatus, setToolStatus] = useState('')
  // Bridges the gap between clicking Send and the 'meta' event that carries the
  // jobId. Without it, isLoading would blink false and allow a double send.
  const [justSent, setJustSent] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<AuthWebSocket | null>(null)

  // Derived from the session, so a generation still in flight keeps the input
  // disabled after a remount instead of looking idle.
  const isLoading = justSent || session.jobId !== null

  // True between sending a `subscribe` and receiving its first event, so a
  // "job not found" (expired job) can be swallowed instead of shown as an error.
  const resubscribing = useRef(false)

  // Track if current job wrote files (for pi)
  const jobWroteFiles = useRef(false)

  // Memoize system prompt so it only changes when programme or user changes.
  // The identity line is appended on every request, not sent once: the backend
  // replays no history (callHermesStream sends a single [system?, user] turn),
  // so anything stated only once is lost on the next message.
  const systemPrompt = useMemo(() => {
    const base = buildSystemPrompt(programme)
    return userName ? `${base}\n\nTu parles avec ${userName}.` : base
  }, [programme, userName])

  const handleStreamEvent = useCallback((ev: StreamEvent) => {
    // Any event means the resubscribe (if any) landed.
    const wasResubscribing = resubscribing.current
    resubscribing.current = false

    // Advance the replay cursor so a reconnect asks only for what we miss.
    if (typeof ev.seq === 'number' && ev.seq > 0) {
      patchSession({ lastSeq: ev.seq })
    }

    switch (ev.type) {
      case 'meta':
        patchSession({ jobId: ev.jobId || null, lastSeq: 0 })
        setJustSent(false)
        jobWroteFiles.current = false
        setToolStatus('')
        break

      // Lifecycle progress from the pi bridge. Stable tokens, translated here.
      // An unknown token is ignored rather than shown raw, so a newer backend
      // can add tokens without an old bundle displaying gibberish.
      case 'status': {
        const key = ev.text || ''
        const label = PI_STATUS_KEYS[key]
        if (label) setToolStatus(t(label))
        break
      }

      case 'tool': {
        if (!ev.tool) break
        const tool = normalizeTool(ev.tool)

        // file_changed is a synthetic event from the pi bridge, not a display event.
        if (tool.name === 'file_changed') {
          if (tool.path && onFileChanged) onFileChanged(tool.path)
          break
        }

        // Covers pi (write/edit) and the Hermes tool loop (write_file/patch_file):
        // the name set lives in tools.ts so a new backend tool only has to be
        // declared once.
        if (WRITE_TOOL_NAMES.has(tool.name) && tool.status === 'done') {
          jobWroteFiles.current = true
        }

        // File operations are an audit trail worth keeping in the thread.
        // Everything else is Hermes working out loud: show it in a transient
        // status line so it does not accumulate in the conversation.
        if (isFileOp(tool)) {
          const toolText = toolLabel(tool, t)
          setMessages((prev) => [
            ...prev,
            { id: `tool-${Date.now()}-${Math.random()}`, role: 'tool', content: toolText },
          ])
        } else {
          setToolStatus(toolLabel(tool, t))
        }
        break
      }

      case 'delta':
        if (ev.text) {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [
                ...prev.slice(0, -1),
                { ...last, content: last.content + ev.text },
              ]
            }
            return [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: ev.text!,
                isStreaming: true,
              },
            ]
          })
        }
        break

      case 'done':
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          const wroteFiles = jobWroteFiles.current
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: ev.reply || last.content,
                isStreaming: false,
                piWroteFiles: wroteFiles || undefined,
              },
            ]
          }
          if (ev.reply) {
            return [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: ev.reply,
                isStreaming: false,
                piWroteFiles: wroteFiles || undefined,
              },
            ]
          }
          return prev
        })
        setJustSent(false)
        setToolStatus('')
        patchSession({ jobId: null, lastSeq: 0 })
        break

      case 'error': {
        const errMsg = ev.error || 'erreur inconnue'

        // A resubscribe to a job the backend has already garbage-collected
        // (jobTTL is 15 min). Nothing went wrong from the user's point of view —
        // they simply came back too late. Close the dangling message quietly.
        if (wasResubscribing && /job not found/i.test(errMsg)) {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [...prev.slice(0, -1), { ...last, isStreaming: false }]
            }
            return prev
          })
          setJustSent(false)
          setToolStatus('')
          patchSession({ jobId: null, lastSeq: 0 })
          break
        }

        const detail = ev.detail ? `\n\n\`${ev.detail}\`` : ''
        if (/auth|session|expir/i.test(errMsg)) {
          setAuthError(true)
          if (!handleAuthExpired()) {
            setMessages((prev) => [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: '⚠️ Session expirée — [recharge la page](javascript:location.reload()) pour te reconnecter.',
                isStreaming: false,
              },
            ])
          }
        } else {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            const errorContent = `⚠️ **Erreur** : ${errMsg}${detail}`
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [
                ...prev.slice(0, -1),
                {
                  ...last,
                  content: errorContent,
                  isStreaming: false,
                },
              ]
            }
            return [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: errorContent,
                isStreaming: false,
              },
            ]
          })
        }
        setJustSent(false)
        setToolStatus('')
        patchSession({ jobId: null, lastSeq: 0 })
        break
      }
    }
  }, [onFileChanged, t, patchSession, setMessages])

  // Connect to backend WebSocket with auth-aware reconnect
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsPath = agent === 'pi' ? '/ws/agent/pi' : '/ws/acp'
    const wsUrl = `${protocol}//${window.location.host}${wsPath}`

    const authWs = new AuthWebSocket({
      url: wsUrl,
      onMessage: (data) => {
        handleStreamEvent(data as StreamEvent)
      },
      onOpen: () => {
        console.log('WebSocket connected')
        setConnected(true)
        setAuthError(false)
        // Coming back to a generation that kept running while this component was
        // unmounted (tab switch, mode switch). The backend Job survives the
        // WebSocket dropping, so ask for everything after our cursor.
        const { jobId, lastSeq } = sessionRef.current
        if (jobId) {
          resubscribing.current = true
          authWs.send({ type: 'subscribe', jobId, after: lastSeq })
        }
      },
      onDisconnect: (reason) => {
        console.log('WebSocket disconnected:', reason)
        setConnected(false)
        if (reason === 'auth_expired') {
          setAuthError(true)
        }
        if (sessionRef.current.jobId !== null) {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [
                ...prev.slice(0, -1),
                {
                  ...last,
                  content: last.content + '\n\n⚠️ Connexion perdue.',
                  isStreaming: false,
                },
              ]
            }
            return prev
          })
          setJustSent(false)
          setToolStatus('')
          patchSession({ jobId: null, lastSeq: 0 })
        }
      },
      onReconnect: () => {
        console.log('WebSocket reconnected')
        setConnected(true)
        setAuthError(false)
        const { jobId, lastSeq } = sessionRef.current
        if (jobId) {
          // after: lastSeq, not 0 — replaying the whole backlog would append
          // every delta a second time and duplicate the answer on screen.
          resubscribing.current = true
          authWs.send({ type: 'subscribe', jobId, after: lastSeq })
        }
      },
      maxRetries: 5,
      baseDelay: 1000,
    })

    wsRef.current = authWs

    return () => {
      authWs.close()
    }
  }, [handleStreamEvent, agent]) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const handleSend = () => {
    if (!input.trim() || !wsRef.current?.connected) return

    const userMessage: ChatMessage = {
      id: `msg-${Date.now()}`,
      role: 'user',
      content: input.trim(),
    }

    let content = input.trim()
    if (currentFile) {
      // Mode Pi stays path-only on purpose: pi runs in this pod, mounts the same
      // workspace and owns a `read` tool, so inlining would duplicate context it
      // can fetch itself. Only Lya, who cannot reach the workspace, needs the text.
      content =
        agent !== 'pi' && fileContent
          ? buildDeskPrompt(currentFile, fileContent, content)
          : `[Contexte: je travaille sur le fichier "${currentFile}"]\n\n${content}`
    }

    // Append the message and clear the draft in one patch, so a burst of
    // updates cannot resurrect the text that was just sent.
    patchSession((prev) => ({ messages: [...prev.messages, userMessage], input: '' }))
    setJustSent(true)

    wsRef.current.send({
      type: 'prompt',
      content,
      system: systemPrompt,
      mode: agent === 'pi' ? 'pi' : 'desk',
      // Sent unconditionally: the backend only reads it in mode desk, and always
      // sending it keeps the payload shape stable for the logs.
      deskMode: deskSubMode,
      currentFile: currentFile || undefined,
    })
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  return (
    <div className="chat-panel">
      <div className="chat-header">
        {t('chat.title')}
        {agent === 'pi' && (
          <span className="chat-pi-badge">{t('piChat.hint')}</span>
        )}
        {programme && (
          <span className="chat-niveau-badge">
            {programme.niveau} — {programme.cecrl.LVA}
          </span>
        )}
        {!connected && !authError && (
          <span style={{ fontSize: '11px', color: 'var(--text-muted)', marginLeft: '8px' }}>
            {t('chat.reconnecting')}
          </span>
        )}
        {authError && (
          <span style={{ fontSize: '11px', color: '#e74c3c', marginLeft: '8px' }}>
            {t('chat.expired')}
          </span>
        )}
      </div>
      {/* Desk only: mode Pi always edits the file, so it has no sub-mode. */}
      {agent !== 'pi' && (
        <>
          <div className="chat-submode">
            <span className="chat-submode-label">{t('chat.subModeLabel')}</span>
            <div className="chat-submode-switcher">
              <button
                type="button"
                className={`chat-submode-btn ${deskSubMode === 'insert' ? 'active' : ''}`}
                aria-pressed={deskSubMode === 'insert'}
                title={t('chat.subModeInsertHint')}
                onClick={() => setDeskSubMode('insert')}
              >
                {t('chat.subModeInsert')}
              </button>
              <button
                type="button"
                className={`chat-submode-btn ${deskSubMode === 'direct' ? 'active' : ''}`}
                aria-pressed={deskSubMode === 'direct'}
                title={t('chat.subModeDirectHint')}
                onClick={() => setDeskSubMode('direct')}
              >
                {t('chat.subModeDirect')}
              </button>
            </div>
          </div>
          {/* In the direct sub-mode Lya rewrites this exact file, so it is named
              before the conversation: an edit landing in an unexpected course
              file is not something a teacher can undo from here. */}
          {deskSubMode === 'direct' && (
            <div className={`chat-workfile ${currentFile ? '' : 'chat-workfile--none'}`}>
              {currentFile ? t('chat.workingFile', { path: currentFile }) : t('chat.noWorkingFile')}
            </div>
          )}
        </>
      )}
      <div className="chat-messages">
        {messages.length === 0 && (
          <div style={{ color: 'var(--text-muted)', fontSize: '13px', padding: '16px' }}>
            {t('chat.emptyHint')}
            <br /><br />
            {t('chat.examples')}
            <br />• {t('chat.example1')}
            <br />• {t('chat.example2', { niveau: programme?.niveau || 'Seconde' })}
            <br />• {t('chat.example3')}
            <br />• {t('chat.example4')}
          </div>
        )}
        {messages.map((msg) => (
          <div key={msg.id} className={`chat-message ${msg.role}`}>
            {msg.role === 'tool' ? (
              <span className="chat-tool-event">{msg.content}</span>
            ) : (
              <ReactMarkdown>{msg.content}</ReactMarkdown>
            )}
            {msg.role === 'assistant' && !msg.isStreaming && (
              <div className="actions">
                {msg.piWroteFiles ? (
                  // Name the agent that actually wrote: in the direct sub-mode it
                  // is Lya, and crediting Pi there sent the teacher looking at the
                  // wrong mode when checking what changed.
                  <span className="chat-pi-updated">
                    {agent === 'pi' ? t('piChat.updated') : t('chat.updated')}
                  </span>
                ) : (
                  <button onClick={() => onInsert(msg.content)}>
                    {t('chat.insert')}
                  </button>
                )}
                <button onClick={() => navigator.clipboard.writeText(msg.content)}>
                  {t('chat.copy')}
                </button>
              </div>
            )}
            {msg.isStreaming && (
              <span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>{t('chat.streaming')}</span>
            )}
          </div>
        ))}
        {toolStatus && <div className="chat-tool-status">{toolStatus}</div>}
        <div ref={messagesEndRef} />
      </div>
      <div className="chat-input">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('chat.placeholder')}
          rows={2}
        />
        <button onClick={handleSend} disabled={isLoading || !input.trim() || !connected}>
          {isLoading ? t('chat.sending') : t('chat.send')}
        </button>
      </div>
    </div>
  )
}
