import React, { useState, useEffect } from 'react'
import { X, Share2, Copy, Check, Trash2, ExternalLink } from 'lucide-react'
import { Share } from '../types'
import { getShares, revokeShare } from '../api/shares'

interface SharesModalProps {
  isOpen: boolean
  onClose: () => void
  onSharesChanged?: () => void
}

export const SharesModal: React.FC<SharesModalProps> = ({ isOpen, onClose, onSharesChanged }) => {
  const [shares, setShares] = useState<Share[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copiedToken, setCopiedToken] = useState<string | null>(null)
  const [revokingToken, setRevokingToken] = useState<string | null>(null)

  const loadShares = async () => {
    setLoading(true)
    setError('')
    try {
      const res = await getShares()
      setShares(res.shares || [])
    } catch (err: any) {
      setError(err.message || 'Failed to fetch shares')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (isOpen) {
      loadShares()
    }
  }, [isOpen])

  if (!isOpen) return null

  const getFullShareUrl = (token: string) => {
    return `${window.location.origin}/s/${token}`
  }

  const handleCopy = (token: string) => {
    navigator.clipboard.writeText(getFullShareUrl(token))
    setCopiedToken(token)
    setTimeout(() => setCopiedToken(null), 2000)
  }

  const handleRevoke = async (token: string) => {
    setRevokingToken(token)
    try {
      await revokeShare(token)
      setShares((prev) => prev.filter((s) => s.token !== token))
      if (onSharesChanged) onSharesChanged()
    } catch (err: any) {
      alert('Failed to revoke share: ' + err.message)
    } finally {
      setRevokingToken(null)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-fade-in">
      <div className="w-full max-w-2xl bg-slate-900 border border-slate-800 rounded-3xl p-6 shadow-2xl space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-800/80 pb-4">
          <div className="flex items-center space-x-3">
            <div className="p-2.5 bg-purple-600/20 text-purple-400 rounded-xl border border-purple-500/30">
              <Share2 className="w-5 h-5" />
            </div>
            <div>
              <h2 className="text-lg font-bold text-slate-100">Active Shared Links</h2>
              <p className="text-xs text-slate-400">Manage all public download links created for your files and folders</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-xl transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        {loading && (
          <div className="py-12 text-center text-sm text-slate-500 animate-pulse">
            Loading active shares...
          </div>
        )}

        {error && (
          <div className="p-4 bg-red-500/10 border border-red-500/20 rounded-xl text-sm text-red-400">
            {error}
          </div>
        )}

        {!loading && shares.length === 0 && (
          <div className="py-12 text-center text-slate-500">
            <Share2 className="w-10 h-10 mx-auto text-slate-600 mb-3 opacity-40" />
            <p className="text-sm font-medium text-slate-400">No active shared links</p>
            <p className="text-xs text-slate-600 mt-1">
              Select any file or folder and click the Share icon to generate a public link.
            </p>
          </div>
        )}

        {!loading && shares.length > 0 && (
          <div className="max-h-96 overflow-y-auto space-y-3 pr-1">
            {shares.map((share) => {
              const fullUrl = getFullShareUrl(share.token)
              const isCopied = copiedToken === share.token
              const isRevoking = revokingToken === share.token

              return (
                <div
                  key={share.id}
                  className="p-4 bg-slate-950/80 border border-slate-800 rounded-2xl flex flex-col sm:flex-row sm:items-center justify-between gap-3 group"
                >
                  {/* File / Folder Info */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center space-x-2">
                      <span className="text-xs font-semibold px-2 py-0.5 bg-purple-500/10 text-purple-400 border border-purple-500/20 rounded-md">
                        {share.is_dir ? 'Folder' : 'File'}
                      </span>
                      <span className="text-sm font-semibold text-slate-200 truncate">{share.path}</span>
                    </div>
                    <div className="flex items-center space-x-2 mt-1.5 text-xs text-slate-500 font-mono truncate">
                      <span className="truncate text-purple-300/80">{fullUrl}</span>
                    </div>
                  </div>

                  {/* Actions */}
                  <div className="flex items-center space-x-2 self-end sm:self-center">
                    <button
                      onClick={() => window.open(fullUrl, '_blank')}
                      className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-800 rounded-xl transition-colors"
                      title="Open link in new tab"
                    >
                      <ExternalLink className="w-4 h-4" />
                    </button>

                    <button
                      onClick={() => handleCopy(share.token)}
                      className={`flex items-center space-x-1.5 py-1.5 px-3 rounded-xl text-xs font-medium border transition-all ${
                        isCopied
                          ? 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
                          : 'bg-slate-900 text-slate-300 border-slate-700 hover:bg-slate-800'
                      }`}
                    >
                      {isCopied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                      <span>{isCopied ? 'Copied' : 'Copy'}</span>
                    </button>

                    <button
                      onClick={() => handleRevoke(share.token)}
                      disabled={isRevoking}
                      className="p-2 text-slate-400 hover:text-red-400 hover:bg-red-500/10 rounded-xl transition-colors disabled:opacity-50"
                      title="Revoke shared link"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        )}

        {/* Footer */}
        <div className="pt-2 border-t border-slate-800/80 flex justify-end">
          <button
            onClick={onClose}
            className="py-2.5 px-5 bg-slate-800 hover:bg-slate-700 text-slate-200 font-medium text-sm rounded-xl transition-all"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
