import React, { useState } from 'react'
import { FolderPlus, X } from 'lucide-react'

interface MkdirModalProps {
  isOpen: boolean
  onClose: () => void
  onSubmit: (folderName: string) => Promise<void>
}

export const MkdirModal: React.FC<MkdirModalProps> = ({ isOpen, onClose, onSubmit }) => {
  const [folderName, setFolderName] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  if (!isOpen) return null

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!folderName.trim()) return

    setLoading(true)
    setError('')
    try {
      await onSubmit(folderName.trim())
      setFolderName('')
      onClose()
    } catch (err: any) {
      setError(err.message || 'Failed to create folder')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-md p-6 shadow-2xl">
        <div className="flex items-center justify-between pb-4 border-b border-slate-800">
          <div className="flex items-center space-x-2 text-slate-100 font-semibold">
            <FolderPlus className="w-5 h-5 text-emerald-400" />
            <span>Create New Folder</span>
          </div>
          <button onClick={onClose} className="text-slate-400 hover:text-slate-200">
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-4 space-y-4">
          {error && <div className="p-3 bg-red-500/10 border border-red-500/20 text-red-400 text-xs rounded-xl">{error}</div>}

          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Folder Name</label>
            <input
              type="text"
              autoFocus
              value={folderName}
              onChange={(e) => setFolderName(e.target.value)}
              placeholder="e.g. Projects, Photos"
              className="w-full px-3.5 py-2.5 bg-slate-950 border border-slate-800 rounded-xl text-sm text-slate-200 focus:outline-none focus:border-brand-500 focus:ring-1 focus:ring-brand-500"
            />
          </div>

          <div className="flex justify-end space-x-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 text-sm font-medium rounded-xl transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || !folderName.trim()}
              className="px-4 py-2 bg-brand-600 hover:bg-brand-500 disabled:opacity-50 text-white text-sm font-medium rounded-xl transition-colors shadow-lg shadow-brand-600/20"
            >
              {loading ? 'Creating...' : 'Create Folder'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
