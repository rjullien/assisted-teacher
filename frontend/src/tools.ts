/**
 * Normalization of Hermes tool-progress payloads.
 *
 * The backend forwards `event: hermes.tool.progress` frames verbatim
 * (`json.Unmarshal` into `any` in bridge/hermes.go), so the frontend receives
 * whatever shape Hermes happens to send. That shape is NOT stable: the tool
 * name has been observed under `name`, `tool`, `toolName` and `label`, and the
 * path under `path`, `file` or nested in `args`.
 *
 * Reading `ev.tool.name` directly and passing it straight to `t()` is what
 * produced the literal `🔧 {name}` in the UI: when `name` is undefined the i18n
 * interpolation falls back to echoing the placeholder
 * (`params[k] ?? "{" + k + "}"` in i18n/index.ts).
 *
 * TripKit's `toolLabel()` in leo-chat-stream.js never hits this because it tries
 * several keys and ends on a literal fallback. This module is the same idea.
 */

export interface NormalizedTool {
  /** Tool name, or '' when the payload carries none. */
  name: string
  /** File path when the tool operates on one, else ''. */
  path: string
  /** Lifecycle status ('started' | 'done' | 'error' | …), or ''. */
  status: string
  /**
   * Why the tool failed, when `status` is 'error', else ''.
   *
   * The Hermes tool loop reports refusals (missing old_string, rejected
   * extension, path outside the workspace) as `status: 'error'` events. Ignoring
   * this field is what showed a refused write with the very same
   * "✏️ Écriture de …" label as a successful one, leaving the teacher believing
   * the file had changed.
   */
  error: string
  /**
   * True when the backend flagged the write as landing somewhere else than the
   * working file named at the top of the Desk panel.
   */
  outsideWorkingFile: boolean
  /**
   * Search terms, for `web_search`. Empty for every other tool.
   *
   * Carried separately from `path` on purpose: Chat.tsx keys the editor reload
   * and the "this job touched the workspace" decision off `path`, so putting a
   * query there would make a web search look like a file operation.
   */
  query: string
}

function str(v: unknown): string {
  return typeof v === 'string' ? v.trim() : ''
}

/**
 * Coerce an arbitrary tool-progress payload into a predictable shape.
 * Always returns strings — never undefined — so callers can branch safely and
 * never interpolate `undefined` into a translation.
 */
export function normalizeTool(raw: unknown): NormalizedTool {
  const empty: NormalizedTool = {
    name: '',
    path: '',
    status: '',
    error: '',
    outsideWorkingFile: false,
    query: '',
  }
  if (!raw) return empty

  // Hermes sometimes sends a bare string instead of an object.
  if (typeof raw === 'string') {
    return { ...empty, name: raw.trim() }
  }

  if (typeof raw !== 'object') return empty

  const t = raw as Record<string, unknown>
  const args = (t.args && typeof t.args === 'object' ? t.args : {}) as Record<string, unknown>

  const name = str(t.name) || str(t.tool) || str(t.toolName) || str(t.label)
  const path = str(t.path) || str(t.file) || str(args.path) || str(args.file)
  const status = str(t.status) || str(t.state)
  const error = str(t.error) || str(t.message)
  // `q` and args.query are accepted alongside `query` for the same reason every
  // other field here has aliases: the payload shape is not ours to control.
  const query = str(t.query) || str(t.q) || str(args.query) || str(args.q)

  return {
    name,
    path,
    status,
    error,
    outsideWorkingFile: t.outsideWorkingFile === true,
    query,
  }
}

/**
 * Tool names that read a file.
 *
 * `read` comes from the pi bridge; `read_file` from the Hermes tool loop in
 * mode Desk. Both must map to the same label, otherwise a `read_file` event
 * falls through to the generic transient "🔧 read_file" status line instead of
 * staying in the thread as an audit trail.
 */
export const READ_TOOL_NAMES: ReadonlySet<string> = new Set(['read', 'read_file'])

/**
 * Tool names that modify a file.
 *
 * `write` / `edit` come from the pi bridge, `write_file` / `patch_file` from the
 * Hermes tool loop. Chat.tsx also reuses this set to decide whether a job
 * touched the workspace, so the literals live here only.
 */
export const WRITE_TOOL_NAMES: ReadonlySet<string> = new Set([
  'write',
  'edit',
  'write_file',
  'patch_file',
])

/**
 * Tool names that search the web.
 *
 * `web_search` is the Hermes tool loop (Desk and Lya). pi searches through its
 * own `bash` tool running the web-search skill, so its events arrive as `bash`
 * and stay in the generic branch — there is no reliable way to tell a search
 * from any other command it runs.
 */
export const SEARCH_TOOL_NAMES: ReadonlySet<string> = new Set(['web_search'])

/** True when the payload describes a file operation worth keeping in the thread. */
export function isFileOp(tool: NormalizedTool): boolean {
  if (!READ_TOOL_NAMES.has(tool.name) && !WRITE_TOOL_NAMES.has(tool.name)) return false
  // A failed call carries no path when the model's arguments could not be decoded
  // at all. It still belongs in the thread: routed to the transient status line
  // instead, the only trace of a failed write flashes by and disappears.
  return tool.path !== '' || tool.status === 'error'
}

/**
 * True when the payload describes a web search worth keeping in the thread.
 *
 * Kept out of isFileOp deliberately: a search touches no file, and Chat.tsx uses
 * isFileOp to decide whether the workspace changed.
 */
export function isSearchOp(tool: NormalizedTool): boolean {
  if (!SEARCH_TOOL_NAMES.has(tool.name)) return false
  return tool.query !== '' || tool.status === 'error'
}

/**
 * True while the tool is still running, i.e. before its terminal event.
 *
 * Both bridges announce a call before executing it (`status: 'running'` in
 * bridge/hermes.go and bridge/pi.go). Treating that event like a finished one is
 * what showed "✏️ Écriture de X" for a write that had not happened yet — twice
 * for a successful write, and immediately before the error line for a refused
 * one.
 */
export function isToolRunning(tool: NormalizedTool): boolean {
  return tool.status === 'running' || tool.status === 'started'
}

type Translate = (key: string, params?: Record<string, string | number>) => string

/**
 * Human label for a tool event. Falls back to a generic label when the payload
 * carries no usable name, so the UI can never surface a raw `{name}`.
 *
 * A failed tool gets its own label: a refusal that reads like a success is worse
 * than no line at all, because the teacher then trusts a file that never moved.
 */
export function toolLabel(tool: NormalizedTool, t: Translate): string {
  if (tool.status === 'error') {
    const reason = tool.error || t('piChat.toolErrorGeneric')
    return tool.path
      ? t('piChat.toolFailed', { path: tool.path, error: reason })
      : t('piChat.toolFailedNoPath', { name: tool.name || '?', error: reason })
  }
  if (tool.path && READ_TOOL_NAMES.has(tool.name)) {
    return isToolRunning(tool)
      ? t('piChat.toolReading', { path: tool.path })
      : t('piChat.toolRead', { path: tool.path })
  }
  if (tool.path && WRITE_TOOL_NAMES.has(tool.name)) {
    return isToolRunning(tool)
      ? t('piChat.toolWriting', { path: tool.path })
      : t('piChat.toolWrite', { path: tool.path })
  }
  if (tool.query && SEARCH_TOOL_NAMES.has(tool.name)) {
    return isToolRunning(tool)
      ? t('piChat.toolSearching', { query: tool.query })
      : t('piChat.toolSearched', { query: tool.query })
  }
  if (tool.name) return t('piChat.toolOther', { name: tool.name })
  return t('piChat.toolUnknown')
}
