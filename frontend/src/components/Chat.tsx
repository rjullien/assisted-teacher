import { useState, useRef, useEffect } from 'react'
import ReactMarkdown from 'react-markdown'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  isStreaming?: boolean
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
  const messageIdCounter = useRef(0)

  // Connect to ACP WebSocket
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/acp`
    const socket = new WebSocket(wsUrl)

    socket.onopen = () => {
      console.log('ACP WebSocket connected')
      // Send initialize request
      const initMsg = {
        jsonrpc: '2.0',
        id: nextId(),
        method: 'initialize',
        params: {
          protocolVersion: '1',
          capabilities: {},
          clientInfo: {
            name: 'cours-ia',
            version: '0.1.0',
          },
        },
      }
      socket.send(JSON.stringify(initMsg))
    }

    socket.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data)
        handleACPMessage(msg)
      } catch (e) {
        console.error('Failed to parse ACP message:', e)
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
  }, [])

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const nextId = () => {
    messageIdCounter.current += 1
    return messageIdCounter.current
  }

  const handleACPMessage = (msg: Record<string, unknown>) => {
    // Handle streaming progress notifications
    if (msg.method === 'notification/progress' || msg.method === 'notifications/progress') {
      const params = msg.params as Record<string, unknown>
      const content = (params?.content as string) || (params?.text as string) || ''
      if (content) {
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              { ...last, content: last.content + content },
            ]
          }
          return [
            ...prev,
            {
              id: `msg-${Date.now()}`,
              role: 'assistant',
              content,
              isStreaming: true,
            },
          ]
        })
      }
    }

    // Handle prompt turn result
    if (msg.result && typeof msg.result === 'object') {
      const result = msg.result as Record<string, unknown>
      if (result.text || result.content) {
        const text = (result.text || result.content) as string
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              { ...last, content: text || last.content, isStreaming: false },
            ]
          }
          return [
            ...prev,
            {
              id: `msg-${Date.now()}`,
              role: 'assistant',
              content: text,
              isStreaming: false,
            },
          ]
        })
        setIsLoading(false)
      }
    }

    // Handle permission requests (auto-accept for MVP0)
    if (msg.method === 'client/requestPermission') {
      const response = {
        jsonrpc: '2.0',
        id: msg.id,
        result: { granted: true },
      }
      ws?.send(JSON.stringify(response))
    }
  }

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
    let prompt = input.trim()
    if (currentFile) {
      prompt = `[Contexte: je travaille sur le fichier "${currentFile}"]\n\n${prompt}`
    }

    // Send ACP prompt/start
    const acpMsg = {
      jsonrpc: '2.0',
      id: nextId(),
      method: 'prompt/start',
      params: {
        message: {
          role: 'user',
          content: prompt,
        },
      },
    }
    ws.send(JSON.stringify(acpMsg))
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
