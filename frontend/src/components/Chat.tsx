import { useState, useRef, useEffect, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'

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
  const [ws, setWs] = useState<WebSocket | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const currentJobId = useRef<string | null>(null)

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
        // Error from backend
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: `⚠️ Erreur : ${ev.error || 'erreur inconnue'}`,
                isStreaming: false,
              },
            ]
          }
          return [
            ...prev,
            {
              id: `msg-${Date.now()}`,
              role: 'assistant',
              content: `⚠️ Erreur : ${ev.error || 'erreur inconnue'}`,
              isStreaming: false,
            },
          ]
        })
        setIsLoading(false)
        currentJobId.current = null
        break
    }
  }, [])

  // Connect to backend WebSocket
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/acp`
    const socket = new WebSocket(wsUrl)

    socket.onopen = () => {
      console.log('WebSocket connected')
    }

    socket.onmessage = (event) => {
      try {
        const ev: StreamEvent = JSON.parse(event.data)
        handleStreamEvent(ev)
      } catch (e) {
        console.error('Failed to parse message:', e)
      }
    }

    socket.onerror = (err) => {
      console.error('WebSocket error:', err)
    }

    socket.onclose = () => {
      console.log('WebSocket closed')
    }

    setWs(socket)

    return () => {
      socket.close()
    }
  }, [handleStreamEvent])

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const handleSend = () => {
    if (!input.trim() || !ws || ws.readyState !== WebSocket.OPEN) return

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
    const msg = {
      type: 'prompt',
      content,
      system: 'Tu es un assistant pédagogique spécialisé dans la création de cours et exercices. Réponds en français sauf si on te demande du contenu en anglais.',
    }
    ws.send(JSON.stringify(msg))
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
      <div className="chat-header">💬 Assistant IA</div>
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
        <button onClick={handleSend} disabled={isLoading || !input.trim()}>
          {isLoading ? '...' : 'Envoyer'}
        </button>
      </div>
    </div>
  )
}
