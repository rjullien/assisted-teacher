import { useState, useRef, useEffect, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import { AuthWebSocket, handleAuthExpired } from '../api'
import { useI18n } from '../i18n'
import { normalizeTool, toolLabel } from '../tools'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  isStreaming?: boolean
}

interface StreamEvent {
  seq: number
  type: string
  text?: string
  reply?: string
  error?: string
  detail?: string
  jobId?: string
  tool?: unknown // shape is not guaranteed by Hermes — see tools.ts
}

/**
 * LyaChat — Full-screen chat companion mode.
 * No system prompt, no file context. Pure conversation with Lya.
 * Passes the user's name so Lya knows who she's talking to.
 */

interface LyaChatProps {
  userName?: string
  messages?: ChatMessage[]
  onMessagesChange?: (msgs: ChatMessage[]) => void
}

export type { ChatMessage as LyaChatMessage }

export default function LyaChat({ userName, messages: externalMessages, onMessagesChange }: LyaChatProps) {
  const { t } = useI18n()
  const [internalMessages, setInternalMessages] = useState<ChatMessage[]>([])
  
  // Use external state if provided, otherwise fall back to internal
  const messages = externalMessages ?? internalMessages
  const messagesRef = useRef<ChatMessage[]>(messages)
  messagesRef.current = messages

  const setMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>> = useCallback((update) => {
    if (onMessagesChange) {
      const newVal = typeof update === 'function' ? update(messagesRef.current) : update
      onMessagesChange(newVal)
    } else {
      setInternalMessages(update)
    }
  }, [onMessagesChange])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [connected, setConnected] = useState(false)
  const [authError, setAuthError] = useState(false)
  // Transient "Lya is using a tool" line. Not part of the conversation.
  const [toolStatus, setToolStatus] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const currentJobId = useRef<string | null>(null)
  const wsRef = useRef<AuthWebSocket | null>(null)

  const handleStreamEvent = useCallback((ev: StreamEvent) => {
    switch (ev.type) {
      case 'meta':
        currentJobId.current = ev.jobId || null
        setToolStatus('')
        break

      // Lya is a pure conversation — tool activity is progress, not content.
      // Shown in a transient line, never appended to the thread.
      case 'tool':
        if (ev.tool) setToolStatus(toolLabel(normalizeTool(ev.tool), t))
        break

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
              { id: `msg-${Date.now()}`, role: 'assistant', content: ev.text!, isStreaming: true },
            ]
          })
        }
        break

      case 'done':
        setMessages((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.role === 'assistant' && last.isStreaming) {
            return [
              ...prev.slice(0, -1),
              { ...last, content: ev.reply || last.content, isStreaming: false },
            ]
          }
          if (ev.reply) {
            return [...prev, { id: `msg-${Date.now()}`, role: 'assistant', content: ev.reply, isStreaming: false }]
          }
          return prev
        })
        setIsLoading(false)
        setToolStatus('')
        currentJobId.current = null
        break

      case 'error':
        const errMsg = ev.error || 'erreur inconnue'
        const detail = ev.detail ? `\n\n\`${ev.detail}\`` : ''
        if (/auth|session|expir/i.test(errMsg)) {
          setAuthError(true)
          handleAuthExpired()
        } else {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            const errorContent = `⚠️ **Erreur** : ${errMsg}${detail}`
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [...prev.slice(0, -1), { ...last, content: errorContent, isStreaming: false }]
            }
            return [...prev, { id: `msg-${Date.now()}`, role: 'assistant', content: errorContent, isStreaming: false }]
          })
        }
        setIsLoading(false)
        setToolStatus('')
        currentJobId.current = null
        break
    }
  }, [t, setMessages])

  // WebSocket connection
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/acp`

    const authWs = new AuthWebSocket({
      url: wsUrl,
      onMessage: (data) => handleStreamEvent(data as StreamEvent),
      onOpen: () => { setConnected(true); setAuthError(false) },
      onDisconnect: (reason) => {
        setConnected(false)
        if (reason === 'auth_expired') setAuthError(true)
        if (isLoading) {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (last && last.role === 'assistant' && last.isStreaming) {
              return [...prev.slice(0, -1), { ...last, content: last.content + '\n\n⚠️ Connexion perdue.', isStreaming: false }]
            }
            return prev
          })
          setIsLoading(false)
          setToolStatus('')
        }
      },
      onReconnect: () => {
        setConnected(true)
        setAuthError(false)
        if (currentJobId.current) {
          authWs.send({ type: 'subscribe', jobId: currentJobId.current, after: 0 })
        }
      },
      maxRetries: 5,
      baseDelay: 1000,
    })

    wsRef.current = authWs
    return () => { authWs.close() }
  }, [handleStreamEvent]) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll
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

    // On the first message, include the user's name so Lya knows who she's talking to
    let content = input.trim()
    if (messages.length === 0 && userName) {
      content = `[Je suis ${userName}]\n\n${content}`
    }

    // No system prompt — pure conversation
    wsRef.current.send({
      type: 'prompt',
      content,
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
    <div className="lya-chat">
      <div className="lya-chat-header">
        <span className="lya-chat-title">{t('lya.title')}</span>
        {!connected && !authError && (
          <span className="lya-chat-status">{t('chat.reconnecting')}</span>
        )}
        {authError && (
          <span className="lya-chat-status error">{t('chat.expired')}</span>
        )}
      </div>

      <div className="lya-chat-messages">
        {messages.length === 0 && (
          <div className="lya-chat-empty">
            <div className="lya-chat-empty-icon">💬</div>
            <p>{t('lya.emptyHint')}</p>
          </div>
        )}
        {messages.map((msg) => (
          <div key={msg.id} className={`lya-message ${msg.role}`}>
            <ReactMarkdown>{msg.content}</ReactMarkdown>
            {msg.role === 'assistant' && !msg.isStreaming && (
              <div className="lya-message-actions">
                <button onClick={() => navigator.clipboard.writeText(msg.content)}>
                  {t('chat.copy')}
                </button>
              </div>
            )}
            {msg.isStreaming && (
              <span className="lya-message-streaming">{t('chat.streaming')}</span>
            )}
          </div>
        ))}
        {toolStatus && <div className="chat-tool-status">{toolStatus}</div>}
        <div ref={messagesEndRef} />
      </div>

      <div className="lya-chat-input">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('lya.placeholder')}
          rows={2}
        />
        <button onClick={handleSend} disabled={isLoading || !input.trim() || !connected}>
          {isLoading ? t('chat.sending') : t('chat.send')}
        </button>
      </div>
    </div>
  )
}
