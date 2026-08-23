import { Allotment } from 'allotment'
import 'allotment/dist/style.css'
import { useState, useEffect, useRef, useCallback } from 'react'
import FileTree from './components/FileTree'
import Editor from './components/Editor'
import Chat from './components/Chat'
import MobileLayout from './components/MobileLayout'
import ProgrammeDrawer from './components/ProgrammeDrawer'
import Toolbar, { type Niveau } from './components/Toolbar'
import { useIsMobile } from './hooks/useIsMobile'
import { getText, putText, postBlob, getJSON, handleAuthExpired } from './api'

// --- Programme types ---

interface ProgrammeData {
  niveau: string
  cecrl: Record<string, string>
  axes_culturels: Array<{
    numero: number
    titre: string
    description: string
    exemples_objets_etude: string[]
    obligatoire: boolean
  }>
  contraintes: {
    axes_a_traiter: number
    axes_total: number
    axe_obligatoire: number
    note: string
  }
  competences: Record<string, {
    code: string
    descripteur: string
    niveau_attendu_LVA: string
    niveau_attendu_LVB: string
  }>
  grammaire: string[]
  vocabulaire_thematique: Record<string, string[]>
}

// --- localStorage persistence ---

const STORAGE_KEY = 'assisted-teacher-niveau'

function loadNiveau(): Niveau {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'seconde' || saved === 'premiere' || saved === 'terminale') {
    return saved
  }
  return 'seconde'
}

function saveNiveau(niveau: Niveau) {
  localStorage.setItem(STORAGE_KEY, niveau)
}

// --- App ---

export default function App() {
  const isMobile = useIsMobile()
  const [currentFile, setCurrentFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState<string>('')
  const [lastSavedContent, setLastSavedContent] = useState<string>('')
  const [refreshKey, setRefreshKey] = useState(0)
  const [niveau, setNiveau] = useState<Niveau>(loadNiveau)
  const [programme, setProgramme] = useState<ProgrammeData | null>(null)
  const [programmeDrawerOpen, setProgrammeDrawerOpen] = useState(false)
  const flushRef = useRef<(() => Promise<void>) | null>(null)

  // Fetch programme data when niveau changes
  useEffect(() => {
    let cancelled = false
    async function fetchProgramme() {
      const res = await getJSON<ProgrammeData>(`/api/programme?niveau=${niveau}`)
      if (!cancelled && res.ok && res.data) {
        setProgramme(res.data)
      }
    }
    fetchProgramme()
    return () => { cancelled = true }
  }, [niveau])

  const handleNiveauChange = useCallback((n: Niveau) => {
    setNiveau(n)
    saveNiveau(n)
  }, [])

  const handleSave = useCallback(async (content: string) => {
    if (!currentFile) return
    const res = await putText(`/api/file?path=${encodeURIComponent(currentFile)}`, content)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      setLastSavedContent(content)
    }
  }, [currentFile])

  const handleFileSelect = async (path: string) => {
    // Flush any pending auto-save before switching files
    if (flushRef.current) {
      await flushRef.current()
    }

    const res = await getText(`/api/file?path=${encodeURIComponent(path)}`)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.data !== null) {
      setCurrentFile(path)
      setFileContent(res.data)
      setLastSavedContent(res.data)
    }
  }

  const handleInsertFromChat = (text: string) => {
    // Append AI-generated content to the current editor content
    const newContent = fileContent ? fileContent + '\n\n' + text : text
    setFileContent(newContent)
    // The auto-save will pick up the change automatically
  }

  const handleExport = async (format: 'pdf' | 'docx') => {
    if (!currentFile) return
    // Flush before exporting to ensure latest content is saved
    if (flushRef.current) {
      await flushRef.current()
    }
    const res = await postBlob(`/api/export/${format}`, { path: currentFile })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.blob) {
      const url = URL.createObjectURL(res.blob)
      const a = document.createElement('a')
      a.href = url
      a.download = currentFile.replace(/\.md$/, `.${format}`)
      a.click()
      URL.revokeObjectURL(url)
    }
  }

  const handleFileTreeRefresh = () => setRefreshKey((k) => k + 1)

  if (isMobile) {
    return (
      <div className="app app--mobile">
        <Toolbar
          currentFile={currentFile}
          niveau={niveau}
          onNiveauChange={handleNiveauChange}
          onExportPDF={() => handleExport('pdf')}
          onExportDOCX={() => handleExport('docx')}
        />
        <MobileLayout
          currentFile={currentFile}
          fileContent={fileContent}
          lastSavedContent={lastSavedContent}
          refreshKey={refreshKey}
          programme={programme}
          onFileSelect={handleFileSelect}
          onFileTreeRefresh={handleFileTreeRefresh}
          onFileContentChange={setFileContent}
          onSave={handleSave}
          onFlushRef={flushRef}
          onInsertFromChat={handleInsertFromChat}
        />
      </div>
    )
  }

  return (
    <div className="app">
      <Toolbar
        currentFile={currentFile}
        niveau={niveau}
        onNiveauChange={handleNiveauChange}
        onExportPDF={() => handleExport('pdf')}
        onExportDOCX={() => handleExport('docx')}
        onToggleProgramme={() => setProgrammeDrawerOpen((o) => !o)}
      />
      <div className="workspace">
        <Allotment>
          <Allotment.Pane preferredSize={220} minSize={160} maxSize={400}>
            <FileTree
              onSelect={handleFileSelect}
              onRefresh={handleFileTreeRefresh}
              refreshKey={refreshKey}
            />
          </Allotment.Pane>
          <Allotment.Pane preferredSize="50%">
            <Editor
              content={fileContent}
              lastSavedContent={lastSavedContent}
              onChange={setFileContent}
              onSave={handleSave}
              onFlushRef={flushRef}
              filePath={currentFile}
            />
          </Allotment.Pane>
          <Allotment.Pane preferredSize={360} minSize={280}>
            <Chat
              currentFile={currentFile}
              onInsert={handleInsertFromChat}
              programme={programme}
            />
          </Allotment.Pane>
        </Allotment>
      </div>
      <ProgrammeDrawer
        open={programmeDrawerOpen}
        onClose={() => setProgrammeDrawerOpen(false)}
        programme={programme}
      />
    </div>
  )
}
