import { Allotment } from 'allotment'
import 'allotment/dist/style.css'
import { useState, useEffect, useCallback } from 'react'
import FileTree from './components/FileTree'
import Editor from './components/Editor'
import Chat from './components/Chat'
import Toolbar, { type Niveau } from './components/Toolbar'
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
  const [currentFile, setCurrentFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState<string>('')
  const [refreshKey, setRefreshKey] = useState(0)
  const [niveau, setNiveau] = useState<Niveau>(loadNiveau)
  const [programme, setProgramme] = useState<ProgrammeData | null>(null)

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

  const handleFileSelect = async (path: string) => {
    const res = await getText(`/api/file?path=${encodeURIComponent(path)}`)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.data !== null) {
      setCurrentFile(path)
      setFileContent(res.data)
    }
  }

  const handleSave = async (content: string) => {
    if (!currentFile) return
    const res = await putText(`/api/file?path=${encodeURIComponent(currentFile)}`, content)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      setFileContent(content)
    }
  }

  const handleInsertFromChat = (text: string) => {
    // Append AI-generated content to the current editor content
    const newContent = fileContent ? fileContent + '\n\n' + text : text
    setFileContent(newContent)
    handleSave(newContent)
  }

  const handleExport = async (format: 'pdf' | 'docx') => {
    if (!currentFile) return
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

  return (
    <div className="app">
      <Toolbar
        currentFile={currentFile}
        niveau={niveau}
        onNiveauChange={handleNiveauChange}
        onExportPDF={() => handleExport('pdf')}
        onExportDOCX={() => handleExport('docx')}
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
              onChange={setFileContent}
              onSave={handleSave}
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
    </div>
  )
}
