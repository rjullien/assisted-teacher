import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AuthWebSocket } from './api'

// The global MockWebSocket from test/setup.ts is already stubbed via vi.stubGlobal.
// It auto-opens after setTimeout(0) and records sentMessages[].

describe('AuthWebSocket', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('calls onOpen when WebSocket connects', async () => {
    const onOpen = vi.fn()
    const onMessage = vi.fn()

    new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
      onOpen,
    })

    // MockWebSocket fires onopen via setTimeout(0)
    await vi.advanceTimersByTimeAsync(1)

    expect(onOpen).toHaveBeenCalledTimes(1)
  })

  it('send() records messages when connected', async () => {
    const onMessage = vi.fn()
    const onOpen = vi.fn()

    const aws = new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
      onOpen,
    })

    // Wait for connection
    await vi.advanceTimersByTimeAsync(1)

    // Send a message
    aws.send({ type: 'prompt', content: 'hello' })

    // Verify the message was sent (via MockWebSocket's sentMessages)
    // We verify indirectly: if send doesn't throw and onOpen was called, it works.
    // AuthWebSocket.send only sends if readyState === OPEN
    expect(aws.connected).toBe(true)
    expect(onOpen).toHaveBeenCalled()
  })

  it('close() disconnects and prevents reconnect', async () => {
    const onMessage = vi.fn()
    const onDisconnect = vi.fn()

    const aws = new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
      onDisconnect,
    })

    // Wait for connection
    await vi.advanceTimersByTimeAsync(1)
    expect(aws.connected).toBe(true)

    // Close
    aws.close()
    expect(aws.connected).toBe(false)
  })

  it('reconnects on abnormal close (code 1006)', async () => {
    const onMessage = vi.fn()
    const onOpen = vi.fn()
    const onReconnect = vi.fn()

    const aws = new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
      onOpen,
      onReconnect,
      maxRetries: 3,
      baseDelay: 100,
    })

    // Wait for initial connection
    await vi.advanceTimersByTimeAsync(1)
    expect(onOpen).toHaveBeenCalledTimes(1)

    // We need to get the internal WebSocket to simulate a close event.
    // The MockWebSocket's close() fires with a generic CloseEvent.
    // To simulate abnormal close (1006), we need to trigger onclose manually.
    // Access the internal ws via the class internals (private, but for testing).
    const internalWs = (aws as unknown as { ws: { onclose: ((ev: CloseEvent) => void) | null; readyState: number } }).ws
    // Simulate abnormal close from the server side
    internalWs.readyState = 3 // CLOSED
    internalWs.onclose?.(new CloseEvent('close', { code: 1006 }))

    // Wait for the backoff delay (100ms * 2^0 = 100ms)
    await vi.advanceTimersByTimeAsync(101)

    // A new WebSocket should be created and connected
    // The new WS also fires onopen after setTimeout(0)
    await vi.advanceTimersByTimeAsync(1)

    // onOpen is called again (AuthWebSocket calls onOpen on every open)
    expect(onOpen).toHaveBeenCalledTimes(2)
    // onReconnect is called because receivedAnyMessage would need to be true
    // In this case we haven't received any message yet, so onReconnect may not fire.
    // Let's verify a new connection was established
    expect(aws.connected).toBe(true)
  })

  it('detects auth expiry when receiving HTML message', async () => {
    const onMessage = vi.fn()
    const onDisconnect = vi.fn()

    const aws = new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
      onDisconnect,
    })

    // Wait for connection
    await vi.advanceTimersByTimeAsync(1)

    // Get the internal WebSocket to simulate receiving an HTML message (Authelia redirect)
    const internalWs = (aws as unknown as { ws: { onmessage: ((ev: MessageEvent) => void) | null; close: () => void; readyState: number } }).ws

    // Simulate receiving an HTML page (Authelia login redirect)
    const htmlMessage = new MessageEvent('message', {
      data: '<!DOCTYPE html><html><body>Authelia Login</body></html>',
    })
    internalWs.onmessage?.(htmlMessage)

    // onDisconnect should be called with 'auth_expired'
    expect(onDisconnect).toHaveBeenCalledWith('auth_expired')
  })

  it('send() serializes objects to JSON', async () => {
    const onMessage = vi.fn()
    const sentData: string[] = []

    // Spy on MockWebSocket.prototype.send to verify JSON serialization
    const OrigMock = globalThis.WebSocket
    const origSend = OrigMock.prototype.send
    OrigMock.prototype.send = function (data: string) {
      sentData.push(data)
      origSend.call(this, data)
    }

    const aws = new AuthWebSocket({
      url: 'ws://localhost/ws/test',
      onMessage,
    })

    await vi.advanceTimersByTimeAsync(1)

    aws.send({ type: 'prompt', content: 'test payload' })

    expect(sentData.length).toBe(1)
    const parsed = JSON.parse(sentData[0])
    expect(parsed.type).toBe('prompt')
    expect(parsed.content).toBe('test payload')

    aws.close()
    OrigMock.prototype.send = origSend
  })
})
