import React, { useState } from 'react'
import { Download, Share2, Edit2, Trash2, FolderDown, Link2, CornerLeftUp, ArrowUp, ArrowDown } from 'lucide-react'
import { NodeItem } from '../types'
import { FileIcon } from './FileIcon'

export type SortField = 'name' | 'mtime' | 'size'
export type SortOrder = 'asc' | 'desc'

interface FileListProps {
  currentPath?: string
  items: NodeItem[]
  selectedPaths: Set<string>
  sharedPaths?: Set<string>
  onToggleSelect: (path: string) => void
  onToggleSelectAll: () => void
  onNavigate: (path: string) => void
  onDownload: (item: NodeItem) => void
  onShare: (item: NodeItem) => void
  onRename: (item: NodeItem) => void
  onDelete: (item: NodeItem) => void
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '-'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

function formatDate(dateStr: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function getParentPath(path: string): string {
  if (!path || path === '/') return '/'
  const idx = path.lastIndexOf('/')
  if (idx <= 0) return '/'
  return path.substring(0, idx)
}

export const FileList: React.FC<FileListProps> = ({
  currentPath,
  items,
  selectedPaths,
  sharedPaths,
  onToggleSelect,
  onToggleSelectAll,
  onNavigate,
  onDownload,
  onShare,
  onRename,
  onDelete,
}) => {
  const [sortField, setSortField] = useState<SortField>('name')
  const [sortOrder, setSortOrder] = useState<SortOrder>('asc')

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortOrder((prev) => (prev === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortField(field)
      setSortOrder('asc')
    }
  }

  const isAllSelected = items.length > 0 && items.every((i) => selectedPaths.has(i.path))
  const showParentRow = currentPath && currentPath !== '/'

  const sortedItems = [...items].sort((a, b) => {
    if (a.is_dir && !b.is_dir) return -1
    if (!a.is_dir && b.is_dir) return 1

    let cmp = 0
    if (sortField === 'name') {
      cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
    } else if (sortField === 'mtime') {
      cmp = new Date(a.mtime).getTime() - new Date(b.mtime).getTime()
    } else if (sortField === 'size') {
      cmp = a.size - b.size
    }

    return sortOrder === 'asc' ? cmp : -cmp
  })

  if (items.length === 0 && !showParentRow) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center border border-dashed border-slate-800 rounded-2xl bg-slate-900/40">
        <div className="p-4 bg-slate-800/60 rounded-2xl mb-3 text-slate-500">
          <FolderDown className="w-8 h-8" />
        </div>
        <h3 className="text-base font-semibold text-slate-300">This folder is empty</h3>
        <p className="text-sm text-slate-500 max-w-sm mt-1">
          Upload files or create new folders to start organizing your files.
        </p>
      </div>
    )
  }

  const renderSortIcon = (field: SortField) => {
    if (sortField !== field) return null
    return sortOrder === 'asc' ? (
      <ArrowUp className="w-3.5 h-3.5 text-brand-400 inline ml-1" />
    ) : (
      <ArrowDown className="w-3.5 h-3.5 text-brand-400 inline ml-1" />
    )
  }

  return (
    <div className="bg-slate-900 border border-slate-800 rounded-2xl overflow-hidden shadow-xl">
      <div className="overflow-x-auto">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr className="border-b border-slate-800 bg-slate-900/60 text-xs font-semibold text-slate-400 uppercase tracking-wider select-none">
              <th className="py-3.5 px-4 w-10 text-center">
                <input
                  type="checkbox"
                  checked={isAllSelected}
                  onChange={onToggleSelectAll}
                  className="rounded border-slate-700 bg-slate-800 text-brand-500 focus:ring-brand-500/30"
                />
              </th>
              <th className="py-3.5 px-4 cursor-pointer hover:text-slate-200" onClick={() => handleSort('name')}>
                <span>Name</span>
                {renderSortIcon('name')}
              </th>
              <th className="py-3.5 px-4 hidden sm:table-cell w-48 cursor-pointer hover:text-slate-200" onClick={() => handleSort('mtime')}>
                <span>Date Modified</span>
                {renderSortIcon('mtime')}
              </th>
              <th className="py-3.5 px-4 hidden md:table-cell w-32 cursor-pointer hover:text-slate-200" onClick={() => handleSort('size')}>
                <span>Size</span>
                {renderSortIcon('size')}
              </th>
              <th className="py-3.5 px-4 text-right w-36">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/60 text-sm">
            {/* Parent Directory Row (..) */}
            {showParentRow && (
              <tr
                onClick={() => onNavigate(getParentPath(currentPath))}
                className="hover:bg-slate-800/60 cursor-pointer transition-colors border-b border-slate-800/80 group"
              >
                <td className="py-3 px-4 text-center"></td>
                <td className="py-3 px-4" colSpan={4}>
                  <div className="flex items-center space-x-3 text-slate-300 group-hover:text-brand-400 font-medium">
                    <CornerLeftUp className="w-5 h-5 text-brand-400" />
                    <span>..</span>
                  </div>
                </td>
              </tr>
            )}

            {items.length === 0 && showParentRow ? (
              <tr>
                <td colSpan={5} className="py-12 text-center text-slate-500">
                  This folder is empty
                </td>
              </tr>
            ) : (
              sortedItems.map((item) => {
              const isSelected = selectedPaths.has(item.path)
              const isShared = sharedPaths?.has(item.path)

              return (
                <tr
                  key={item.id}
                  className={`group transition-colors ${
                    isSelected ? 'bg-brand-500/10' : 'hover:bg-slate-800/50'
                  }`}
                >
                  {/* Checkbox */}
                  <td className="py-3 px-4 text-center">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => onToggleSelect(item.path)}
                      className="rounded border-slate-700 bg-slate-800 text-brand-500 focus:ring-brand-500/30"
                    />
                  </td>

                  {/* Name + Icon */}
                  <td className="py-3 px-4">
                    <div className="flex items-center space-x-2.5">
                      <button
                        onClick={() => (item.is_dir ? onNavigate(item.path) : onDownload(item))}
                        className="flex items-center space-x-3 text-slate-200 hover:text-brand-400 font-medium text-left truncate max-w-xs sm:max-w-md md:max-w-lg transition-colors"
                      >
                        <FileIcon isDir={item.is_dir} contentType={item.content_type} name={item.name} />
                        <span className="truncate">{item.name}</span>
                      </button>

                      {isShared && (
                        <span className="inline-flex items-center space-x-1 px-2 py-0.5 bg-purple-500/10 text-purple-400 text-[11px] font-semibold rounded-full border border-purple-500/20 shadow-sm" title="Publicly Shared">
                          <Link2 className="w-3 h-3" />
                          <span>Shared</span>
                        </span>
                      )}
                    </div>
                  </td>

                  {/* Date Modified */}
                  <td className="py-3 px-4 hidden sm:table-cell text-slate-400 text-xs">
                    {formatDate(item.mtime)}
                  </td>

                  {/* Size */}
                  <td className="py-3 px-4 hidden md:table-cell text-slate-400 text-xs font-mono">
                    {item.is_dir ? (item.size > 0 ? formatBytes(item.size) : '-') : formatBytes(item.size)}
                  </td>

                  {/* Actions */}
                  <td className="py-3 px-4 text-right">
                    <div className="flex items-center justify-end space-x-1 opacity-90 sm:opacity-0 group-hover:opacity-100 transition-opacity">
                      <button
                        onClick={() => onDownload(item)}
                        className="p-1.5 text-slate-400 hover:text-brand-400 hover:bg-slate-800 rounded-lg transition-colors"
                        title={item.is_dir ? 'Download ZIP' : 'Download file'}
                      >
                        <Download className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => onShare(item)}
                        className="p-1.5 text-slate-400 hover:text-purple-400 hover:bg-slate-800 rounded-lg transition-colors"
                        title="Share link"
                      >
                        <Share2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => onRename(item)}
                        className="p-1.5 text-slate-400 hover:text-amber-400 hover:bg-slate-800 rounded-lg transition-colors"
                        title="Rename"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => onDelete(item)}
                        className="p-1.5 text-slate-400 hover:text-rose-400 hover:bg-slate-800 rounded-lg transition-colors"
                        title="Delete"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              )
            }))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
