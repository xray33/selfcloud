import React, { useState, useEffect } from 'react'
import { Cloud, Download, FileArchive, Folder } from 'lucide-react'
import { PublicShareInfo } from '../types'
import { getPublicShareInfo, getPublicDownloadUrl } from '../api/shares'
import { FileIcon } from '../components/FileIcon'

interface PublicSharePageProps {
  token: string
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '-'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

export const PublicSharePage: React.FC<PublicSharePageProps> = ({ token }) => {
  const [info, setInfo] = useState<PublicShareInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    setLoading(true)
    setError('')
    getPublicShareInfo(token)
      .then((res) => setInfo(res))
      .catch((err) => setError(err.message || 'Share link not found or expired'))
      .finally(() => setLoading(false))
  }, [token])

  const handleDownload = () => {
    window.open(getPublicDownloadUrl(token), '_blank')
  }

  return (
    <div className="min-h-screen flex flex-col justify-center items-center px-4 bg-slate-950 text-slate-100">
      <div className="w-full max-w-md">
        {/* Brand Header */}
        <div className="text-center mb-8">
          <div className="inline-flex p-3 bg-purple-600/20 text-purple-400 rounded-2xl border border-purple-500/30 mb-4 shadow-xl shadow-purple-600/10">
            <Cloud className="w-10 h-10" />
          </div>
          <h1 className="text-2xl font-bold text-white tracking-tight">Shared via SelfCloud</h1>
          <p className="text-sm text-slate-400 mt-1">Public file download link</p>
        </div>

        {/* Public Share Card */}
        <div className="bg-slate-900 border border-slate-800 rounded-3xl p-8 shadow-2xl">
          {loading && (
            <div className="py-12 text-center text-sm text-slate-500 animate-pulse">
              Fetching shared file details...
            </div>
          )}

          {error && (
            <div className="text-center py-6">
              <p className="text-sm text-red-400 font-medium">{error}</p>
              <p className="text-xs text-slate-500 mt-2">The file may have been unshared or deleted by its owner.</p>
            </div>
          )}

          {info && (
            <div className="space-y-6">
              {/* File Info */}
              <div className="flex items-center space-x-4 p-4 bg-slate-950/80 border border-slate-800 rounded-2xl">
                <div className="p-3 bg-slate-900 rounded-xl">
                  <FileIcon isDir={info.is_dir} contentType={info.content_type} name={info.name} />
                </div>
                <div className="flex-1 truncate">
                  <h2 className="text-base font-semibold text-slate-100 truncate">{info.name}</h2>
                  <p className="text-xs text-slate-400 font-mono mt-0.5">
                    {info.is_dir ? 'Directory (ZIP Archive)' : formatBytes(info.size)}
                  </p>
                </div>
              </div>

              {/* Download Button */}
              <button
                onClick={handleDownload}
                className="w-full flex items-center justify-center space-x-2 py-4 px-4 bg-purple-600 hover:bg-purple-500 text-white font-semibold text-sm rounded-xl shadow-lg shadow-purple-600/25 transition-all"
              >
                {info.is_dir ? <FileArchive className="w-5 h-5" /> : <Download className="w-5 h-5" />}
                <span>{info.is_dir ? 'Download Directory (.zip)' : 'Download File'}</span>
              </button>
            </div>
          )}
        </div>

        <div className="mt-6 text-center text-xs text-slate-500">
          Powered by SelfCloud Storage
        </div>
      </div>
    </div>
  )
}
