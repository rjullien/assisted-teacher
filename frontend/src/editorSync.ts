/**
 * When the editor buffer may be replaced by what the agents wrote on disk.
 *
 * A `file_changed` event reaches App whenever pi or Lya writes a file (in mode
 * Desk, sub-mode « Mise à jour directe », that is now every request that touches
 * the working file). Reloading unconditionally overwrote the open buffer AND
 * `lastSavedContent`, so a teacher typing while the agent wrote lost the text
 * they had not saved yet, with nothing to undo it: the buffer they were editing
 * simply disappeared.
 *
 * Keeping the buffer when it is dirty means the editor and the disk diverge for
 * a moment, and the next auto-save writes the teacher's version over the
 * agent's. That is the deliberate trade-off: the teacher's own unsaved work is
 * the only content that exists nowhere else, and the agent's version can always
 * be asked for again.
 */
export function shouldReloadBuffer(args: {
  /** Path reported as changed by the backend. */
  changedPath: string
  /** File currently open in the editor, or null. */
  openFile: string | null
  /** Current editor buffer. */
  buffer: string
  /** Content of the last successful save — buffer !== lastSaved means dirty. */
  lastSaved: string
}): boolean {
  const { changedPath, openFile, buffer, lastSaved } = args
  if (!openFile || changedPath !== openFile) return false
  return buffer === lastSaved
}
