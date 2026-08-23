import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import ReactMarkdown from 'react-markdown'
import { AuthWebSocket, handleAuthExpired } from '../api'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  isStreaming?: boolean
}

interface StreamEvent {
  seq: number
  type: string // delta | done | error | meta
  text?: string
  reply?: string
  error?: string
  detail?: string
  jobId?: string
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

interface ChatProps {
  currentFile: string | null
  onInsert: (text: string) => void
  programme: ProgrammeData | null
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

export default function Chat({ currentFile, onInsert, programme }: ChatProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [connected, setConnected] = useState(false)
  const [authError, setAuthError] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const currentJobId = useRef<string | null>(null)
  const wsRef = useRef<AuthWebSocket | null>(null)

  // Memoize system prompt so it only changes when programme changes
  const systemPrompt = useMemo(() => buildSystemPrompt(programme), [programme])

  const handleStreamEvent = useCallback((ev: StreamEvent) => {
    switch (ev.type) {
      case 'meta':
        // Job started — store jobId for potential reconnect
        currentJobId.current = ev.jobId || null
        break

      case 'delta':
        // Streaming text chunk — append to current assistant message
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
        // Stream finished — finalize the assistant message
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: ev.reply || last.content,
                isStreaming: false,
              },
            ]
          }
          // If no streaming message exists yet, create a final one
          if (ev.reply) {
            return [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: ev.reply,
                isStreaming: false,
              },
            ]
          }
          return prev
        })
        setIsLoading(false)
        currentJobId.current = null
        break

      case 'error':
        // Error from backend — check if auth-related
        const errMsg = ev.error || 'erreur inconnue'
        const detail = ev.detail ? `\n\n\`${ev.detail}\`` : ''
        if (/auth|session|expir/i.test(errMsg)) {
          setAuthError(true)
          if (!handleAuthExpired()) {
            // Already tried redirect — show message
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
        setIsLoading(false)
        currentJobId.current = null
        break
    }
  }, [])

  // Connect to backend WebSocket with auth-aware reconnect
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/acp`

    const authWs = new AuthWebSocket({
      url: wsUrl,
      onMessage: (data) => {
        handleStreamEvent(data as StreamEvent)
      },
      onOpen: () => {
        console.log('WebSocket connected')
        setConnected(true)
        setAuthError(false)
      },
      onDisconnect: (reason) => {
        console.log('WebSocket disconnected:', reason)
        setConnected(false)
        if (reason === 'auth_expired') {
          setAuthError(true)
          // AuthWebSocket already calls handleAuthExpired() internally
        }
        // If we were mid-stream, show a reconnection message
        if (isLoading) {
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
          setIsLoading(false)
        }
      },
      onReconnect: () => {
        console.log('WebSocket reconnected')
        setConnected(true)
        setAuthError(false)
        // If there was an active job, try to resubscribe
        if (currentJobId.current) {
          authWs.send({
            type: 'subscribe',
            jobId: currentJobId.current,
            after: 0,
          })
        }
      },
      maxRetries: 5,
      baseDelay: 1000,
    })

    wsRef.current = authWs

    return () => {
      authWs.close()
    }
  }, [handleStreamEvent]) // eslint-disable-line react-hooks/exhaustive-deps

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
    setMessages((prev) => [...prev, userMessage])
    setIsLoading(true)

    // Build context with current file info
    let content = input.trim()
    if (currentFile) {
      content = `[Contexte: je travaille sur le fichier "${currentFile}"]\n\n${content}`
    }

    // Send message with dynamic system prompt based on selected niveau
    wsRef.current.send({
      type: 'prompt',
      content,
      system: systemPrompt,
    })
    setInput('')
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
        💬 Assistant IA
        {programme && (
          <span className="chat-niveau-badge">
            {programme.niveau} — {programme.cecrl.LVA}
          </span>
        )}
        {!connected && !authError && (
          <span style={{ fontSize: '11px', color: 'var(--text-muted)', marginLeft: '8px' }}>
            (reconnexion…)
          </span>
        )}
        {authError && (
          <span style={{ fontSize: '11px', color: '#e74c3c', marginLeft: '8px' }}>
            (session expirée)
          </span>
        )}
      </div>
      <div className="chat-messages">
        {messages.length === 0 && (
          <div style={{ color: 'var(--text-muted)', fontSize: '13px', padding: '16px' }}>
            Posez une question ou demandez à l'IA de générer du contenu pour votre cours.
            <br /><br />
            Exemples :
            <br />• "Génère 5 exercices de gap-fill sur le present perfect, niveau B1"
            <br />• "Reformule ce paragraphe pour des élèves de {programme?.niveau || 'Seconde'}"
            <br />• "Ce cours respecte-t-il l'axe 4 du programme ?"
            <br />• "Propose un plan de séquence sur l'axe Commonwealth"
          </div>
        )}
        {messages.map((msg) => (
          <div key={msg.id} className={`chat-message ${msg.role}`}>
            <ReactMarkdown>{msg.content}</ReactMarkdown>
            {msg.role === 'assistant' && !msg.isStreaming && (
              <div className="actions">
                <button onClick={() => onInsert(msg.content)}>
                  📝 Insérer dans le cours
                </button>
                <button onClick={() => navigator.clipboard.writeText(msg.content)}>
                  📋 Copier
                </button>
              </div>
            )}
            {msg.isStreaming && (
              <span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>⏳ en cours...</span>
            )}
          </div>
        ))}
        <div ref={messagesEndRef} />
      </div>
      <div className="chat-input">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Demandez à l'IA..."
          rows={2}
        />
        <button onClick={handleSend} disabled={isLoading || !input.trim() || !connected}>
          {isLoading ? '...' : 'Envoyer'}
        </button>
      </div>
    </div>
  )
}
