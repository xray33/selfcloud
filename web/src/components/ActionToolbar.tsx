import React from 'react'
import { Upload, FolderPlus, Download, Search, Trash2 } from 'lucide-react'

interface ActionToolbarProps {
  currentPath: string
  searchQuery: string
  selectedCount: number
  onSearchChange: (query: string) => void
  onUploadClick: () => void
  onMkdirClick: () => void
  onDownloadZipClick: () => void
  onDeleteSelectedClick?: () => void
}

export const ActionToolbar: React.FC<ActionToolbarProps> = ({
  searchQuery,
  selectedCount,
  onSearchChange,
  onUploadClick,
  onMkdirClick,
  onDownloadZipClick,
  onDeleteSelectedClick,
}) => {
  return (
    <div className="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3 mb-6">
      {/* Search Input */}
      <div className="relative flex-1 max-w-md">
        <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
        <input
          type="text"
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Filter files and folders..."
          className="w-full pl-9 pr-4 py-2 bg-slate-900/90 border border-slate-800 rounded-xl text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-brand-500 focus:ring-1 focus:ring-brand-500 transition-all"
        />
      </div>

      {/* Action Buttons */}
      <div className="flex items-center space-x-2">
        {selectedCount > 0 && onDeleteSelectedClick && (
          <button
            onClick={onDeleteSelectedClick}
            className="flex items-center space-x-1.5 px-3.5 py-2 bg-red-500/10 hover:bg-red-500/20 text-red-400 font-medium text-sm rounded-xl border border-red-500/20 transition-all"
          >
            <Trash2 className="w-4 h-4" />
            <span>Delete ({selectedCount})</span>
          </button>
        )}

        <button
          onClick={onDownloadZipClick}
          className="flex items-center space-x-1.5 px-3.5 py-2 bg-slate-800 hover:bg-slate-700 text-slate-200 font-medium text-sm rounded-xl border border-slate-700/60 transition-all"
          title="Download directory as ZIP archive"
        >
          <Download className="w-4 h-4 text-brand-500" />
          <span className="hidden md:inline">Download ZIP</span>
        </button>

        <button
          onClick={onMkdirClick}
          className="flex items-center space-x-1.5 px-3.5 py-2 bg-slate-800 hover:bg-slate-700 text-slate-200 font-medium text-sm rounded-xl border border-slate-700/60 transition-all"
        >
          <FolderPlus className="w-4 h-4 text-emerald-400" />
          <span>New Folder</span>
        </button>

        <button
          onClick={onUploadClick}
          className="flex items-center space-x-1.5 px-4 py-2 bg-brand-600 hover:bg-brand-500 text-white font-medium text-sm rounded-xl shadow-lg shadow-brand-600/20 transition-all"
        >
          <Upload className="w-4 h-4" />
          <span>Upload</span>
        </button>
      </div>
    </div>
  )
}
