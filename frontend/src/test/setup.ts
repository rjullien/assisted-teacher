import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// Cleanup after each test
afterEach(() => {
  cleanup()
})

// Mock WebSocket globally
class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  readyState = MockWebSocket.OPEN
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null

  url: string
  sentMessages: string[] = []

  constructor(url: string) {
    this.url = url
    // Simulate connection opening
    setTimeout(() => {
      this.onopen?.(new Event('open'))
    }, 0)
  }

  send(data: string) {
    this.sentMessages.push(data)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.(new CloseEvent('close'))
  }

  // Test helper: simulate receiving a message from the server
  simulateMessage(data: unknown) {
    const event = new MessageEvent('message', {
      data: JSON.stringify(data),
    })
    this.onmessage?.(event)
  }
}

// Expose mock on global
;(globalThis as unknown as Record<string, unknown>).MockWebSocket = MockWebSocket
vi.stubGlobal('WebSocket', MockWebSocket)

// Mock fetch globally
globalThis.fetch = vi.fn()

// Mock scrollIntoView (not available in jsdom)
Element.prototype.scrollIntoView = vi.fn()
