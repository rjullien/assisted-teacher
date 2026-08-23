/**
 * api.ts — Centralized API client with Authelia session handling.
 *
 * Pattern borrowed from TripKit frontend:
 * - All HTTP calls go through request() which detects 401/403 (Authelia expired)
 * - On 401: retry once without credentials (stale token scenario)
 * - If still failing: return { ok: false, authExpired: true }
 * - handleAuthExpired() triggers a full-page reload (one-shot) to force
 *   Authelia's 302 → login → return at the reverse-proxy level.
 *
 * For WebSocket: the WS upgrade itself can fail (Authelia returns 302 HTML
 * instead of 101 Upgrade). We detect this via onclose/onerror and expose
 * a reconnect + auth-expired flow.
 */

// ── Types ────────────────────────────────────────────────────────────────────

export interface ApiResponse<T = unknown> {
  ok: boolean
  status: number
  data: T | null
  error: string | null
  authExpired: boolean
}

// ── Auth state ───────────────────────────────────────────────────────────────

let _authRedirectDone = false

/**
 * Handle expired Authelia session: navigate to current page (triggers
 * Authelia 302 at reverse-proxy level → login → return).
 * One redirect per page load to avoid infinite loops.
 * @returns true if redirect was triggered, false if already attempted
 */
export function handleAuthExpired(): boolean {
  if (_authRedirectDone) return false
  _authRedirectDone = true
  window.location.href = window.location.href
  return true
}

// ── Core request ─────────────────────────────────────────────────────────────

/**
 * Centralized fetch wrapper. Detects Authelia session expiry and returns
 * a structured response.
 */
export async function request<T = unknown>(
  path: string,
  options: RequestInit & { timeoutMs?: number } = {},
  _retried = false
): Promise<ApiResponse<T>> {
  try {
    const { timeoutMs, ...fetchOpts } = options
    const ms = typeof timeoutMs === 'number' && timeoutMs > 0 ? timeoutMs : 15000
    const signal = AbortSignal.timeout(ms)

    const res = await fetch(path, {
      ...fetchOpts,
      signal: options.signal || signal,
    })

    // Authelia session expired: 401 or 403
    // First attempt: retry once (handles edge case where a stale cookie was sent)
    if ((res.status === 401 || res.status === 403) && !_retried) {
      return request<T>(path, options, true)
    }

    // Second 401/403 → session is truly expired
    if (res.status === 401 || res.status === 403) {
      return {
        ok: false,
        status: res.status,
        data: null,
        error: 'Session expirée — recharge la page.',
        authExpired: true,
      }
    }

    // Non-auth error
    if (!res.ok) {
      let errorMsg = `HTTP ${res.status}`
      try {
        const text = await res.text()
        if (text) errorMsg = text.slice(0, 200)
      } catch (_) {}
      return { ok: false, status: res.status, data: null, error: errorMsg, authExpired: false }
    }

    // Success — parse based on content-type
    const ct = res.headers.get('content-type') || ''
    let data: T | null = null
    if (ct.includes('application/json')) {
      data = (await res.json()) as T
    } else {
      // Return text as-is (for file content, etc.)
      data = (await res.text()) as unknown as T
    }

    return { ok: true, status: res.status, data, error: null, authExpired: false }
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : 'network error'
    // Timeout / network / abort
    return { ok: false, status: 0, data: null, error: msg, authExpired: false }
  }
}

// ── Convenience methods ──────────────────────────────────────────────────────

/** GET JSON (no cache — always fresh) */
export async function getJSON<T = unknown>(path: string, timeoutMs?: number): Promise<ApiResponse<T>> {
  const res = await request<T>(path, { timeoutMs, cache: 'no-store' })
  // If the response is "ok" but the data is a string (HTML page from Authelia redirect),
  // treat it as an auth expiry. This happens when fetch follows a 302 to the login page.
  if (res.ok && res.data !== null && typeof res.data === 'string') {
    const text = res.data as unknown as string
    if (/<!DOCTYPE|<html|authelia/i.test(text)) {
      return { ok: false, status: 302, data: null, error: 'Session expirée (redirect Authelia)', authExpired: true }
    }
    // Non-HTML text where we expected JSON — treat as error
    return { ok: false, status: res.status, data: null, error: 'Expected JSON, got text', authExpired: false }
  }
  return res
}

/** GET text (file content — no cache) */
export async function getText(path: string): Promise<ApiResponse<string>> {
  return request<string>(path, { cache: 'no-store' })
}

/** PUT text (save file) */
export async function putText(path: string, body: string): Promise<ApiResponse<unknown>> {
  return request(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain' },
    body,
  })
}

/** POST JSON */
export async function postJSON<T = unknown>(
  path: string,
  body: unknown,
  opts?: { timeoutMs?: number }
): Promise<ApiResponse<T>> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    timeoutMs: opts?.timeoutMs,
  })
}

/** POST and get blob back (for export) */
export async function postBlob(
  path: string,
  body: unknown
): Promise<{ ok: boolean; blob: Blob | null; authExpired: boolean; error: string | null }> {
  try {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(30000),
    })

    if (res.status === 401 || res.status === 403) {
      return { ok: false, blob: null, authExpired: true, error: 'Session expirée' }
    }

    if (!res.ok) {
      return { ok: false, blob: null, authExpired: false, error: `HTTP ${res.status}` }
    }

    const blob = await res.blob()
    return { ok: true, blob, authExpired: false, error: null }
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : 'network error'
    return { ok: false, blob: null, authExpired: false, error: msg }
  }
}

// ── WebSocket with auth handling ─────────────────────────────────────────────

export interface AuthWebSocketOptions {
  /** URL to connect to (ws:// or wss://) */
  url: string
  /** Called on each message */
  onMessage: (data: unknown) => void
  /** Called when WS is open and ready */
  onOpen?: () => void
  /** Called when connection lost (with reason) */
  onDisconnect?: (reason: 'auth_expired' | 'error' | 'closed') => void
  /** Called when reconnect succeeds */
  onReconnect?: () => void
  /** Max reconnect attempts before giving up (default: 5) */
  maxRetries?: number
  /** Base delay between reconnects in ms (default: 1000, exponential backoff) */
  baseDelay?: number
}

/**
 * Managed WebSocket with auto-reconnect and Authelia session detection.
 *
 * Authelia detection: if the WS closes immediately (code 1006 with no frames
 * received) it likely means the upgrade was intercepted by Authelia (302 → HTML).
 * After a few failed reconnects we call handleAuthExpired().
 */
export class AuthWebSocket {
  private ws: WebSocket | null = null
  private opts: Required<AuthWebSocketOptions>
  private retryCount = 0
  private closed = false
  private receivedAnyMessage = false
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null

  constructor(options: AuthWebSocketOptions) {
    this.opts = {
      maxRetries: 5,
      baseDelay: 1000,
      onOpen: () => {},
      onDisconnect: () => {},
      onReconnect: () => {},
      ...options,
    }
    this.connect()
  }

  private connect() {
    if (this.closed) return
    this.ws = new WebSocket(this.opts.url)

    this.ws.onopen = () => {
      this.retryCount = 0
      if (this.receivedAnyMessage) {
        // This is a reconnect
        this.opts.onReconnect!()
      }
      this.opts.onOpen!()
    }

    this.ws.onmessage = (event) => {
      this.receivedAnyMessage = true
      try {
        const data = JSON.parse(event.data)
        this.opts.onMessage(data)
      } catch (_) {
        // Non-JSON message — might be Authelia HTML redirect page
        const text = String(event.data)
        if (/<!DOCTYPE|<html|authelia/i.test(text)) {
          // Authelia intercepted the WS — session expired
          this.ws?.close()
          this.opts.onDisconnect!('auth_expired')
          handleAuthExpired()
          return
        }
      }
    }

    this.ws.onerror = () => {
      // Error alone doesn't tell us much — onclose will fire next
    }

    this.ws.onclose = (event) => {
      if (this.closed) return

      // Code 1006 = abnormal close (no close frame). If we never received a
      // message, this likely means Authelia intercepted the upgrade request.
      const possibleAuth = event.code === 1006 && !this.receivedAnyMessage

      if (this.retryCount >= this.opts.maxRetries) {
        // Give up — if it looks like auth, trigger the redirect
        if (possibleAuth) {
          this.opts.onDisconnect!('auth_expired')
          handleAuthExpired()
        } else {
          this.opts.onDisconnect!('error')
        }
        return
      }

      // Exponential backoff reconnect
      const delay = this.opts.baseDelay * Math.pow(2, this.retryCount)
      this.retryCount++
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    }
  }

  /** Send a message (JSON-serialized) */
  send(data: unknown) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(data))
    }
  }

  /** Check if connected */
  get connected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }

  /** Gracefully close — no reconnect */
  close() {
    this.closed = true
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
  }
}
