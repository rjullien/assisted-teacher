import { describe, it, expect } from 'vitest'
import { shouldReloadBuffer } from './editorSync'

describe('shouldReloadBuffer', () => {
  it('reloads the open file when the buffer is clean', () => {
    expect(
      shouldReloadBuffer({
        changedPath: 'B1/unit5.md',
        openFile: 'B1/unit5.md',
        buffer: '# Unit 5',
        lastSaved: '# Unit 5',
      }),
    ).toBe(true)
  })

  // The failure this prevents: Lya patches the working file while the teacher is
  // typing, the buffer is overwritten from disk and the unsaved text is gone with
  // no way to get it back.
  it('never overwrites unsaved edits', () => {
    expect(
      shouldReloadBuffer({
        changedPath: 'B1/unit5.md',
        openFile: 'B1/unit5.md',
        buffer: '# Unit 5\n\nen train de taper…',
        lastSaved: '# Unit 5',
      }),
    ).toBe(false)
  })

  it('ignores a change to another file', () => {
    expect(
      shouldReloadBuffer({
        changedPath: 'B1/autre.md',
        openFile: 'B1/unit5.md',
        buffer: '# Unit 5',
        lastSaved: '# Unit 5',
      }),
    ).toBe(false)
  })

  it('ignores a change when no file is open', () => {
    expect(
      shouldReloadBuffer({
        changedPath: 'B1/unit5.md',
        openFile: null,
        buffer: '',
        lastSaved: '',
      }),
    ).toBe(false)
  })
})
