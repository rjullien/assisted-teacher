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

  return { name, path, status, error, outsideWorkingFile: t.outsideWorkingFile === true }
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

/** True when the payload describes a file operation worth keeping in the thread. */
export function isFileOp(tool: NormalizedTool): boolean {
  if (!READ_TOOL_NAMES.has(tool.name) && !WRITE_TOOL_NAMES.has(tool.name)) return false
  // A failed call carries no path when the model's arguments could not be decoded
  // at all. It still belongs in the thread: routed to the transient status line
  // instead, the only trace of a failed write flashes by and disappears.
  return tool.path !== '' || tool.status === 'error'
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
  if (tool.path && READ_TOOL_NAMES.has(tool.name)) return t('piChat.toolRead', { path: tool.path })
  if (tool.path && WRITE_TOOL_NAMES.has(tool.name)) {
    return t('piChat.toolWrite', { path: tool.path })
  }
  if (tool.name) return t('piChat.toolOther', { name: tool.name })
  return t('piChat.toolUnknown')
}
