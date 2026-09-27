import React, { useState, useRef } from 'react'
import { Upload, X, File as FileIcon, AlertCircle, Trash2 } from 'lucide-react'

interface UploadModalProps {
  isOpen: boolean
  currentPath: string
  onClose: () => void
  onUpload: (file: File) => Promise<void>
  onComplete?: () => Promise<void>
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

export const UploadModal: React.FC<UploadModalProps> = ({
  isOpen,
  currentPath,
  onClose,
  onUpload,
  onComplete,
}) => {
  const [selectedFiles, setSelectedFiles] = useState<File[]>([])
  const [loading, setLoading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<{
    current: number
    total: number
    currentName: string
  } | null>(null)
  const [error, setError] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)

  if (!isOpen) return null

  const addFiles = (newFiles: FileList | File[]) => {
    const incoming = Array.from(newFiles)
    if (incoming.length === 0) return

    setSelectedFiles((prev) => {
      const existingKeys = new Set(prev.map((f) => `${f.name}_${f.size}_${f.lastModified}`))
      const unique = incoming.filter((f) => !existingKeys.has(`${f.name}_${f.size}_${f.lastModified}`))
      return [...prev, ...unique]
    })
    setError('')
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) {
      addFiles(e.target.files)
      e.target.value = ''
    }
  }

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    if (e.dataTransfer.files) {
      addFiles(e.dataTransfer.files)
    }
  }

  const handleRemoveFile = (index: number) => {
    if (loading) return
    setSelectedFiles((prev) => prev.filter((_, i) => i !== index))
  }

  const handleClearAll = () => {
    if (loading) return
    setSelectedFiles([])
    setError('')
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleClose = () => {
    if (loading) return
    setSelectedFiles([])
    setError('')
    setUploadProgress(null)
    onClose()
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (selectedFiles.length === 0 || loading) return

    setLoading(true)
    setError('')

    const errors: string[] = []
    const failedFiles: File[] = []

    for (let i = 0; i < selectedFiles.length; i++) {
      const file = selectedFiles[i]
      setUploadProgress({
        current: i + 1,
        total: selectedFiles.length,
        currentName: file.name,
      })

      try {
        await onUpload(file)
      } catch (err: any) {
        failedFiles.push(file)
        errors.push(`${file.name}: ${err.message || 'Upload failed'}`)
      }
    }

    setUploadProgress(null)
    setLoading(false)

    if (onComplete) {
      try {
        await onComplete()
      } catch {
        // ignore directory reload errors
      }
    }

    if (errors.length === 0) {
      setSelectedFiles([])
      onClose()
    } else {
      setSelectedFiles(failedFiles)
      setError(`Failed to upload ${errors.length} file(s):\n${errors.join('\n')}`)
    }
  }

  const totalSize = selectedFiles.reduce((acc, f) => acc + f.size, 0)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-lg p-6 shadow-2xl flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-slate-800 shrink-0">
          <div className="flex items-center space-x-2 text-slate-100 font-semibold truncate">
            <Upload className="w-5 h-5 text-brand-500 shrink-0" />
            <span className="truncate">
              {selectedFiles.length > 1
                ? `Upload ${selectedFiles.length} files to ${currentPath}`
                : `Upload to ${currentPath}`}
            </span>
          </div>
          <button
            onClick={handleClose}
            disabled={loading}
            className="text-slate-400 hover:text-slate-200 disabled:opacity-40 transition-colors p-1"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        <form onSubmit={handleSubmit} className="mt-4 space-y-4 flex flex-col flex-1 min-h-0">
          {error && (
            <div className="p-3 bg-red-500/10 border border-red-500/20 text-red-400 text-xs rounded-xl flex items-start space-x-2 whitespace-pre-line shrink-0 max-h-32 overflow-y-auto">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
              <span>{error}</span>
            </div>
          )}

          {/* Dropzone */}
          <div
            onDragOver={(e) => e.preventDefault()}
            onDrop={handleDrop}
            onClick={() => !loading && fileInputRef.current?.click()}
            className={`flex flex-col items-center justify-center py-6 px-4 border-2 border-dashed border-slate-700 hover:border-brand-500 rounded-2xl bg-slate-950/60 transition-colors group shrink-0 ${
              loading ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'
            }`}
          >
            <input
              ref={fileInputRef}
              type="file"
              multiple
              disabled={loading}
              onChange={handleFileChange}
              className="hidden"
            />
            <div className="p-3 bg-slate-800/80 group-hover:bg-brand-500/20 text-slate-400 group-hover:text-brand-400 rounded-xl mb-2 transition-colors">
              <Upload className="w-6 h-6" />
            </div>
            <p className="text-xs font-medium text-slate-300 text-center">
              Drag and drop files here, or <span className="text-brand-400 underline">browse</span>
            </p>
            <p className="text-[11px] text-slate-500 mt-1">Supports multiple files</p>
          </div>

          {/* Selected Files List */}
          {selectedFiles.length > 0 && (
            <div className="flex flex-col min-h-0 flex-1 space-y-2">
              <div className="flex items-center justify-between text-xs text-slate-400 px-1 shrink-0">
                <span className="font-medium">
                  {selectedFiles.length} file{selectedFiles.length > 1 ? 's' : ''} ({formatBytes(totalSize)})
                </span>
                {!loading && (
                  <button
                    type="button"
                    onClick={handleClearAll}
                    className="text-slate-500 hover:text-red-400 transition-colors text-xs flex items-center space-x-1"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                    <span>Clear all</span>
                  </button>
                )}
              </div>

              <div className="overflow-y-auto space-y-2 pr-1 max-h-48 flex-1">
                {selectedFiles.map((file, idx) => (
                  <div
                    key={`${file.name}_${file.size}_${idx}`}
                    className="flex items-center justify-between space-x-3 p-2.5 bg-slate-800/50 border border-slate-700/60 rounded-xl"
                  >
                    <div className="flex items-center space-x-2.5 min-w-0 flex-1">
                      <FileIcon className="w-4 h-4 text-brand-400 shrink-0" />
                      <div className="min-w-0 flex-1">
                        <p className="text-xs font-medium text-slate-200 truncate">{file.name}</p>
                        <p className="text-[10px] text-slate-400 font-mono">{formatBytes(file.size)}</p>
                      </div>
                    </div>
                    {!loading && (
                      <button
                        type="button"
                        onClick={() => handleRemoveFile(idx)}
                        className="text-slate-500 hover:text-red-400 p-1 rounded-lg transition-colors shrink-0"
                        title="Remove file"
                      >
                        <X className="w-4 h-4" />
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Upload Progress Indicator */}
          {loading && uploadProgress && (
            <div className="p-3 bg-slate-800/60 border border-slate-700/60 rounded-xl space-y-2 shrink-0">
              <div className="flex items-center justify-between text-xs text-slate-300">
                <span className="font-medium">
                  Uploading {uploadProgress.current} of {uploadProgress.total}
                </span>
                <span className="text-slate-400 truncate max-w-[200px]" title={uploadProgress.currentName}>
                  {uploadProgress.currentName}
                </span>
              </div>
              <div className="w-full bg-slate-700 rounded-full h-1.5 overflow-hidden">
                <div
                  className="bg-brand-500 h-1.5 rounded-full transition-all duration-300"
                  style={{ width: `${(uploadProgress.current / uploadProgress.total) * 100}%` }}
                />
              </div>
            </div>
          )}

          {/* Action Buttons */}
          <div className="flex justify-end space-x-2 pt-2 border-t border-slate-800/80 shrink-0">
            <button
              type="button"
              onClick={handleClose}
              disabled={loading}
              className="px-4 py-2 bg-slate-800 hover:bg-slate-700 disabled:opacity-50 text-slate-300 text-sm font-medium rounded-xl transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || selectedFiles.length === 0}
              className="px-4 py-2 bg-brand-600 hover:bg-brand-500 disabled:opacity-50 text-white text-sm font-medium rounded-xl transition-colors shadow-lg shadow-brand-600/20 flex items-center space-x-1.5"
            >
              <span>
                {loading
                  ? `Uploading (${uploadProgress ? `${uploadProgress.current}/${uploadProgress.total}` : '...'})`
                  : selectedFiles.length > 1
                  ? `Upload ${selectedFiles.length} Files`
                  : 'Upload'}
              </span>
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
