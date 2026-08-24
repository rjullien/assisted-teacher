import { describe, it, expect } from 'vitest'
import { normalizeTool, isFileOp, toolLabel } from './tools'
import { t as tFn } from './i18n'

const t = (key: string, params?: Record<string, string | number>) => tFn('fr', key, params)

describe('normalizeTool', () => {
  it('reads the canonical shape', () => {
    expect(normalizeTool({ name: 'read', path: 'B1/unit5.md', status: 'started' })).toEqual({
      name: 'read',
      path: 'B1/unit5.md',
      status: 'started',
    })
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
    expect(normalizeTool(undefined)).toEqual({ name: '', path: '', status: '' })
    expect(normalizeTool(null)).toEqual({ name: '', path: '', status: '' })
    expect(normalizeTool({})).toEqual({ name: '', path: '', status: '' })
    expect(normalizeTool({ status: 'started' })).toEqual({ name: '', path: '', status: 'started' })
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
})
