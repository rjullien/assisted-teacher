import { useState, useRef, useEffect, useCallback } from 'react'
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

interface ChatProps {
  currentFile: string | null
  onInsert: (text: string) => void
}

export default function Chat({ currentFile, onInsert }: ChatProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [connected, setConnected] = useState(false)
  const [authError, setAuthError] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const currentJobId = useRef<string | null>(null)
  const wsRef = useRef<AuthWebSocket | null>(null)

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
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [
                ...prev.slice(0, -1),
                {
                  ...last,
                  content: `⚠️ Erreur : ${errMsg}`,
                  isStreaming: false,
                },
              ]
            }
            return [
              ...prev,
              {
                id: `msg-${Date.now()}`,
                role: 'assistant',
                content: `⚠️ Erreur : ${errMsg}`,
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

    // Send message in the format the backend expects
    wsRef.current.send({
      type: 'prompt',
      content,
      system: 'Tu es un assistant pédagogique spécialisé dans la création de cours et exercices. Réponds en français sauf si on te demande du contenu en anglais.',
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
            <br />• "Reformule ce paragraphe pour des élèves de 3ème"
            <br />• "Crée un quiz à choix multiples sur le vocabulaire des animaux"
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
