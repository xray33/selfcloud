import React, { useState, useEffect, useCallback } from 'react'
import { User, NodeItem } from '../types'
import { Navbar } from '../components/Navbar'
import { Breadcrumb } from '../components/Breadcrumb'
import { ActionToolbar } from '../components/ActionToolbar'
import { FileList } from '../components/FileList'
import { UploadModal } from '../components/UploadModal'
import { MkdirModal } from '../components/MkdirModal'
import { RenameModal } from '../components/RenameModal'
import { ShareModal } from '../components/ShareModal'
import { SharesModal } from '../components/SharesModal'
import { getFiles, mkdir, uploadFile, deletePath, renamePath, getDownloadUrl, getZipDownloadUrl } from '../api/files'
import { getShares } from '../api/shares'
import { getSystemInfo } from '../api/system'

interface FilesPageProps {
  user: User
  onLogout: () => void
}

function getInitialPath(): string {
  const params = new URLSearchParams(window.location.search)
  const p = params.get('path')
  return p ? p : '/'
}

export const FilesPage: React.FC<FilesPageProps> = ({ user, onLogout }) => {
  const [currentPath, setCurrentPath] = useState(getInitialPath)
  const [items, setItems] = useState<NodeItem[]>([])
  const [sharedPaths, setSharedPaths] = useState<Set<string>>(new Set())
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [serviceVersion, setServiceVersion] = useState<string>('')

  // Modals
  const [isUploadOpen, setIsUploadOpen] = useState(false)
  const [isMkdirOpen, setIsMkdirOpen] = useState(false)
  const [isSharesManagerOpen, setIsSharesManagerOpen] = useState(false)
  const [renameTarget, setRenameTarget] = useState<NodeItem | null>(null)
  const [shareTarget, setShareTarget] = useState<NodeItem | null>(null)

  const loadSharesList = useCallback(async () => {
    try {
      const res = await getShares()
      const pathSet = new Set((res.shares || []).map((s) => s.path))
      setSharedPaths(pathSet)
    } catch {
      // Ignore shares fetch error
    }
  }, [])

  const loadDirectory = useCallback(async (path: string) => {
    setLoading(true)
    setError('')
    try {
      const res = await getFiles(path)
      setItems(res.items || [])
      setSelectedPaths(new Set())
    } catch (err: any) {
      setError(err.message || 'Failed to load directory contents')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadDirectory(currentPath)
    loadSharesList()
  }, [currentPath, loadDirectory, loadSharesList])

  useEffect(() => {
    const handlePopState = () => {
      const p = new URLSearchParams(window.location.search).get('path') || '/'
      setCurrentPath(p)
      setSearchQuery('')
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    getSystemInfo()
      .then((info) => setServiceVersion(info.service.version))
      .catch(() => {})
  }, [])

  const handleNavigate = (path: string) => {
    setCurrentPath(path)
    setSearchQuery('')
    const newUrl = path === '/' ? window.location.pathname : `${window.location.pathname}?path=${encodeURIComponent(path)}`
    window.history.pushState({ path }, '', newUrl)
  }

  const handleToggleSelect = (path: string) => {
    setSelectedPaths((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
      }
      return next
    })
  }

  const handleToggleSelectAll = () => {
    if (selectedPaths.size === items.length) {
      setSelectedPaths(new Set())
    } else {
      setSelectedPaths(new Set(items.map((i) => i.path)))
    }
  }

  const handleDownload = (item: NodeItem) => {
    const url = item.is_dir ? getZipDownloadUrl(item.path) : getDownloadUrl(item.path)
    window.open(url, '_blank')
  }

  const handleDownloadCurrentZip = () => {
    window.open(getZipDownloadUrl(currentPath), '_blank')
  }

  const handleUploadSubmit = async (file: File) => {
    await uploadFile(currentPath, file)
  }

  const handleUploadComplete = async () => {
    await loadDirectory(currentPath)
  }

  const handleMkdirSubmit = async (folderName: string) => {
    const targetPath = currentPath === '/' ? `/${folderName}` : `${currentPath}/${folderName}`
    await mkdir(targetPath)
    await loadDirectory(currentPath)
  }

  const handleRenameSubmit = async (oldPath: string, newName: string) => {
    const parentPath = oldPath.substring(0, oldPath.lastIndexOf('/')) || '/'
    const newPath = parentPath === '/' ? `/${newName}` : `${parentPath}/${newName}`
    await renamePath(oldPath, newPath)
    await loadDirectory(currentPath)
  }

  const handleDeleteItem = async (item: NodeItem) => {
    if (confirm(`Are you sure you want to delete ${item.name}?`)) {
      try {
        await deletePath(item.path)
        await loadDirectory(currentPath)
      } catch (err: any) {
        alert(err.message || 'Failed to delete item')
      }
    }
  }

  const handleDeleteSelected = async () => {
    if (selectedPaths.size === 0) return
    if (confirm(`Are you sure you want to delete ${selectedPaths.size} selected item(s)?`)) {
      try {
        for (const p of selectedPaths) {
          await deletePath(p)
        }
        await loadDirectory(currentPath)
      } catch (err: any) {
        alert(err.message || 'Failed to delete selected items')
      }
    }
  }

  const filteredItems = items.filter((item) =>
    item.name.toLowerCase().includes(searchQuery.toLowerCase())
  )

  return (
    <div className="min-h-screen flex flex-col bg-slate-950 text-slate-100">
      <Navbar
        user={user}
        onLogout={onLogout}
        onOpenShares={() => setIsSharesManagerOpen(true)}
        sharesCount={sharedPaths.size}
        serviceVersion={serviceVersion}
      />

      <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <Breadcrumb currentPath={currentPath} onNavigate={handleNavigate} />

        <ActionToolbar
          currentPath={currentPath}
          searchQuery={searchQuery}
          selectedCount={selectedPaths.size}
          onSearchChange={setSearchQuery}
          onUploadClick={() => setIsUploadOpen(true)}
          onMkdirClick={() => setIsMkdirOpen(true)}
          onDownloadZipClick={handleDownloadCurrentZip}
          onDeleteSelectedClick={handleDeleteSelected}
        />

        {error && (
          <div className="mb-4 p-4 bg-red-500/10 border border-red-500/20 text-red-400 text-sm rounded-2xl">
            {error}
          </div>
        )}

        {loading ? (
          <div className="py-20 text-center text-sm text-slate-500 animate-pulse">
            Loading folder contents...
          </div>
        ) : (
          <FileList
            currentPath={currentPath}
            items={filteredItems}
            selectedPaths={selectedPaths}
            sharedPaths={sharedPaths}
            onToggleSelect={handleToggleSelect}
            onToggleSelectAll={handleToggleSelectAll}
            onNavigate={handleNavigate}
            onDownload={handleDownload}
            onShare={(item) => setShareTarget(item)}
            onRename={(item) => setRenameTarget(item)}
            onDelete={handleDeleteItem}
          />
        )}
      </main>

      {/* Modals */}
      <UploadModal
        isOpen={isUploadOpen}
        currentPath={currentPath}
        onClose={() => setIsUploadOpen(false)}
        onUpload={handleUploadSubmit}
        onComplete={handleUploadComplete}
      />

      <MkdirModal
        isOpen={isMkdirOpen}
        onClose={() => setIsMkdirOpen(false)}
        onSubmit={handleMkdirSubmit}
      />

      <RenameModal
        item={renameTarget}
        onClose={() => setRenameTarget(null)}
        onSubmit={handleRenameSubmit}
      />

      <ShareModal
        item={shareTarget}
        onClose={() => {
          setShareTarget(null)
          loadSharesList()
        }}
      />

      <SharesModal
        isOpen={isSharesManagerOpen}
        onClose={() => setIsSharesManagerOpen(false)}
        onSharesChanged={loadSharesList}
      />
    </div>
  )
}
