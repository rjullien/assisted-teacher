import { describe, it, expect } from 'vitest'
import { normalizeTool, isFileOp, isSearchOp, isToolRunning, toolLabel } from './tools'
import { t as tFn } from './i18n'

const t = (key: string, params?: Record<string, string | number>) => tFn('fr', key, params)

describe('normalizeTool', () => {
  it('reads the canonical shape', () => {
    expect(normalizeTool({ name: 'read', path: 'B1/unit5.md', status: 'started' })).toEqual({
      name: 'read',
      path: 'B1/unit5.md',
      status: 'started',
      error: '',
      outsideWorkingFile: false,
      query: '',
    })
  })

  // Refusals are a normal outcome of the Hermes tool loop, and the only thing
  // that distinguishes them from a successful write is these two fields.
  it('reads the failure fields of the Hermes tool loop', () => {
    const tool = normalizeTool({
      name: 'patch_file',
      path: 'a.md',
      status: 'error',
      error: 'old_string introuvable',
      outsideWorkingFile: true,
    })
    expect(tool.status).toBe('error')
    expect(tool.error).toBe('old_string introuvable')
    expect(tool.outsideWorkingFile).toBe(true)
  })

  it('does not invent a working-file deviation', () => {
    expect(normalizeTool({ name: 'write_file', path: 'a.md' }).outsideWorkingFile).toBe(false)
    expect(
      normalizeTool({ name: 'write_file', path: 'a.md', outsideWorkingFile: 'oui' }).outsideWorkingFile,
    ).toBe(false)
  })

  it('accepts the alternate name keys Hermes uses', () => {
    expect(normalizeTool({ tool: 'search_files' }).name).toBe('search_files')
    expect(normalizeTool({ toolName: 'cronjob' }).name).toBe('cronjob')
    expect(normalizeTool({ label: 'web_search' }).name).toBe('web_search')
  })

  it('accepts a bare string payload', () => {
    expect(normalizeTool('terminal').name).toBe('terminal')
  })

  it('finds a path nested under args', () => {
    expect(normalizeTool({ name: 'write', args: { path: 'Vocabulaire/animals.md' } }).path).toBe(
      'Vocabulaire/animals.md',
    )
  })

  it('never returns undefined for an unusable payload', () => {
    const empty = {
      name: '',
      path: '',
      status: '',
      error: '',
      outsideWorkingFile: false,
      query: '',
    }
    expect(normalizeTool(undefined)).toEqual(empty)
    expect(normalizeTool(null)).toEqual(empty)
    expect(normalizeTool({})).toEqual(empty)
    expect(normalizeTool({ status: 'started' })).toEqual({ ...empty, status: 'started' })
  })
})

describe('toolLabel', () => {
  it('labels file reads and writes with their path', () => {
    expect(toolLabel(normalizeTool({ name: 'read', path: 'a.md' }), t)).toBe('📄 Lecture de a.md')
    expect(toolLabel(normalizeTool({ name: 'write', path: 'a.md' }), t)).toBe('✏️ Écriture de a.md')
    expect(toolLabel(normalizeTool({ name: 'edit', path: 'a.md' }), t)).toBe('✏️ Écriture de a.md')
  })

  // The Hermes tool loop in mode Desk names its tools read_file / write_file /
  // patch_file. Without them in the name sets they reached the generic
  // t('piChat.toolOther') branch and only flashed in the transient status line.
  it('labels the Hermes file tools like the pi ones', () => {
    expect(toolLabel(normalizeTool({ name: 'read_file', path: 'a.md' }), t)).toBe(
      '📄 Lecture de a.md',
    )
    expect(toolLabel(normalizeTool({ name: 'write_file', path: 'a.md' }), t)).toBe(
      '✏️ Écriture de a.md',
    )
    expect(toolLabel(normalizeTool({ name: 'patch_file', path: 'a.md' }), t)).toBe(
      '✏️ Écriture de a.md',
    )
  })

  it('labels the Hermes file tools in en too', () => {
    const tEn = (key: string, params?: Record<string, string | number>) => tFn('en', key, params)
    expect(toolLabel(normalizeTool({ name: 'read_file', path: 'a.md' }), tEn)).toBe('📄 Reading a.md')
    expect(toolLabel(normalizeTool({ name: 'write_file', path: 'a.md' }), tEn)).toBe('✏️ Writing a.md')
    expect(toolLabel(normalizeTool({ name: 'patch_file', path: 'a.md' }), tEn)).toBe('✏️ Writing a.md')
  })

  it('labels a named tool with its name', () => {
    expect(toolLabel(normalizeTool({ name: 'web_search' }), t)).toBe('🔧 web_search')
  })

  // A refused write used to be rendered with the very same "✏️ Écriture de …"
  // label as a successful one, so the teacher trusted a file that never moved.
  it('labels a failed tool as a failure, with its reason', () => {
    const failed = normalizeTool({
      name: 'patch_file',
      path: 'a.md',
      status: 'error',
      error: 'old_string introuvable',
    })
    expect(toolLabel(failed, t)).toBe('⚠️ Échec sur a.md : old_string introuvable')
    expect(toolLabel(failed, t)).not.toContain('Écriture')
  })

  it('labels a failed tool that has no usable path', () => {
    const failed = normalizeTool({
      name: 'write_file',
      status: 'error',
      error: 'arguments JSON invalides',
    })
    expect(toolLabel(failed, t)).toBe("⚠️ Échec de l'outil write_file : arguments JSON invalides")
  })

  it('labels a failure with no reason without leaking a placeholder', () => {
    const label = toolLabel(normalizeTool({ name: 'write_file', path: 'a.md', status: 'error' }), t)
    expect(label).toBe('⚠️ Échec sur a.md : raison inconnue')
    expect(label).not.toContain('{')
  })

  it('labels failures in en too', () => {
    const tEn = (key: string, params?: Record<string, string | number>) => tFn('en', key, params)
    expect(
      toolLabel(normalizeTool({ name: 'write_file', path: 'a.md', status: 'error', error: 'nope' }), tEn),
    ).toBe('⚠️ Failed on a.md: nope')
  })

  // This is the regression: a payload carrying no name used to reach
  // t('piChat.toolOther', { name: undefined }), and the i18n interpolation
  // echoed the placeholder, rendering the literal "🔧 {name}" in the UI.
  it('never renders a raw {name} placeholder when the payload has no name', () => {
    const cases: unknown[] = [undefined, null, {}, { status: 'started' }, { preview: 'x' }]
    for (const raw of cases) {
      const label = toolLabel(normalizeTool(raw), t)
      expect(label).not.toContain('{name}')
      expect(label).toBe('🔧 outil…')
    }
  })
})

describe('isFileOp', () => {
  it('is true only for file operations that carry a path', () => {
    expect(isFileOp(normalizeTool({ name: 'read', path: 'a.md' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'write', path: 'a.md' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'read' }))).toBe(false)
    expect(isFileOp(normalizeTool({ name: 'web_search' }))).toBe(false)
    expect(isFileOp(normalizeTool({}))).toBe(false)
  })

  it('is true for the Hermes file tools when they carry a path', () => {
    expect(isFileOp(normalizeTool({ name: 'read_file', path: 'a.md' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'write_file', path: 'a.md' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'patch_file', path: 'a.md' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'read_file', path: '' }))).toBe(false)
    expect(isFileOp(normalizeTool({ name: 'write_file' }))).toBe(false)
    expect(isFileOp(normalizeTool({ name: 'patch_file', path: '  ' }))).toBe(false)
  })

  // A tool call whose arguments could not be decoded carries no path. Sending it
  // to the transient status line left the failed write with no trace at all.
  it('keeps a failed file tool in the thread even without a path', () => {
    expect(isFileOp(normalizeTool({ name: 'write_file', status: 'error', error: 'boom' }))).toBe(true)
    expect(isFileOp(normalizeTool({ name: 'read_file', status: 'error' }))).toBe(true)
    // Still not a file op: an unrelated tool failing stays a status line.
    expect(isFileOp(normalizeTool({ name: 'web_search', status: 'error' }))).toBe(false)
  })
})

describe('isToolRunning', () => {
  // Both bridges announce a call before executing it: hermes.go sends
  // status:"running", pi.go sends "running" too and Hermes progress frames use
  // "started". Treated as terminal, they duplicated every operation in the
  // thread and prefixed every refusal with a false success.
  it('is true only before the terminal event', () => {
    expect(isToolRunning(normalizeTool({ name: 'write_file', path: 'a.md', status: 'running' }))).toBe(true)
    expect(isToolRunning(normalizeTool({ name: 'read', path: 'a.md', status: 'started' }))).toBe(true)
    expect(isToolRunning(normalizeTool({ name: 'write_file', path: 'a.md', status: 'done' }))).toBe(false)
    expect(isToolRunning(normalizeTool({ name: 'write_file', path: 'a.md', status: 'error' }))).toBe(false)
    // No status at all: nothing says it is still going, so it is not pending.
    expect(isToolRunning(normalizeTool({ name: 'write_file', path: 'a.md' }))).toBe(false)
  })

  it('labels a running file tool as in progress, in fr and en', () => {
    const tEn = (key: string, params?: Record<string, string | number>) => tFn('en', key, params)
    const writing = normalizeTool({ name: 'write_file', path: 'a.md', status: 'running' })
    const reading = normalizeTool({ name: 'read_file', path: 'a.md', status: 'running' })
    expect(toolLabel(writing, t)).toBe('✏️ Écriture de a.md en cours…')
    expect(toolLabel(reading, t)).toBe('📄 Lecture de a.md en cours…')
    expect(toolLabel(writing, tEn)).toBe('✏️ Writing a.md…')
    expect(toolLabel(reading, tEn)).toBe('📄 Reading a.md…')
    // A failure keeps the failure label whatever else it carries.
    expect(
      toolLabel(normalizeTool({ name: 'write_file', path: 'a.md', status: 'error', error: 'boom' }), t)
    ).toBe('⚠️ Échec sur a.md : boom')
  })
})


describe('web_search', () => {
  const tEn = (key: string, params?: Record<string, string | number>) => tFn('en', key, params)

  it('reads the query, and its aliases', () => {
    expect(normalizeTool({ name: 'web_search', status: 'done', query: 'present perfect' }).query).toBe(
      'present perfect'
    )
    expect(normalizeTool({ name: 'web_search', q: 'irregular verbs' }).query).toBe('irregular verbs')
    expect(normalizeTool({ name: 'web_search', args: { query: 'phrasal verbs' } }).query).toBe(
      'phrasal verbs'
    )
  })

  // A search carries a query where a file tool carries a path. Keeping the two
  // apart is what stops Chat.tsx treating a search as a file operation: it keys
  // the editor reload and the "workspace touched" flag off path.
  it('never fills path from a query', () => {
    const tool = normalizeTool({ name: 'web_search', status: 'done', query: 'present perfect' })
    expect(tool.path).toBe('')
    expect(isFileOp(tool)).toBe(false)
    expect(isSearchOp(tool)).toBe(true)
  })

  it('is a search op only for search tools with something to show', () => {
    expect(isSearchOp(normalizeTool({ name: 'web_search', query: 'x', status: 'done' }))).toBe(true)
    // A failed call may not even have a decodable query — it still belongs in
    // the thread, or the only trace of the failure flashes past.
    expect(isSearchOp(normalizeTool({ name: 'web_search', status: 'error', error: 'quota' }))).toBe(
      true
    )
    expect(isSearchOp(normalizeTool({ name: 'web_search' }))).toBe(false)
    expect(isSearchOp(normalizeTool({ name: 'read_file', path: 'a.md' }))).toBe(false)
    // pi now searches through its own `web_search` extension tool, so all three
    // modes surface the same name. `bash` must still never read as a search: it
    // is not in pi's allowlist at all, and if it ever came back it should appear
    // as a generic tool rather than be mislabelled. Asserted with a payload that
    // WOULD qualify on every other count — a bare {name:'bash'} passes even when
    // bash is wrongly in the set, so it proves nothing.
    expect(isSearchOp(normalizeTool({ name: 'bash', status: 'error', error: 'boom' }))).toBe(false)
    expect(isSearchOp(normalizeTool({ name: 'bash', status: 'done', query: 'curl …' }))).toBe(false)
  })

  it('labels a search with its terms, in fr and en', () => {
    const running = normalizeTool({ name: 'web_search', query: 'present perfect', status: 'running' })
    const done = normalizeTool({ name: 'web_search', query: 'present perfect', status: 'done' })
    expect(toolLabel(running, t)).toBe('🔍 Recherche web : present perfect…')
    expect(toolLabel(done, t)).toBe('🔍 Recherche web : present perfect')
    expect(toolLabel(running, tEn)).toBe('🔍 Web search: present perfect…')
    expect(toolLabel(done, tEn)).toBe('🔍 Web search: present perfect')
  })

  // The point of the label work: a raw "🔧 web_search" is what the generic
  // branch produced before, and it told the teacher nothing about what was
  // looked up.
  it('does not fall through to the generic tool label', () => {
    const label = toolLabel(
      normalizeTool({ name: 'web_search', query: 'present perfect', status: 'done' }),
      t
    )
    expect(label).not.toBe('🔧 web_search')
  })

  it('keeps the failure label when Brave is unavailable', () => {
    const failed = normalizeTool({
      name: 'web_search',
      status: 'error',
      error: 'quota Brave dépassé',
    })
    expect(toolLabel(failed, t)).toContain('quota Brave dépassé')
  })
})
