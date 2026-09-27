import React, { useState, useEffect } from 'react'
import { Share2, Copy, Check, X } from 'lucide-react'
import { NodeItem, Share } from '../types'
import { createShare } from '../api/shares'

interface ShareModalProps {
  item: NodeItem | null
  onClose: () => void
}

export const ShareModal: React.FC<ShareModalProps> = ({ item, onClose }) => {
  const [share, setShare] = useState<Share | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (item) {
      setShare(null)
      setError('')
      setCopied(false)
      setLoading(true)

      createShare(item.path)
        .then((res) => setShare(res.share))
        .catch((err) => setError(err.message || 'Failed to create share link'))
        .finally(() => setLoading(false))
    }
  }, [item])

  if (!item) return null

  const handleCopy = () => {
    if (share?.share_url) {
      navigator.clipboard.writeText(share.share_url)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-md p-6 shadow-2xl">
        <div className="flex items-center justify-between pb-4 border-b border-slate-800">
          <div className="flex items-center space-x-2 text-slate-100 font-semibold">
            <Share2 className="w-5 h-5 text-purple-400" />
            <span>Share Link</span>
          </div>
          <button onClick={onClose} className="text-slate-400 hover:text-slate-200">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="mt-4 space-y-4">
          <p className="text-xs text-slate-400">
            Anyone with this link can view and download <span className="font-semibold text-slate-200">{item.name}</span> without logging in.
          </p>

          {loading && (
            <div className="py-6 text-center text-sm text-slate-500 animate-pulse">
              Generating public link...
            </div>
          )}

          {error && <div className="p-3 bg-red-500/10 border border-red-500/20 text-red-400 text-xs rounded-xl">{error}</div>}

          {share && (
            <div className="space-y-2">
              <label className="block text-xs font-medium text-slate-400">Public Share URL</label>
              <div className="flex items-center space-x-2">
                <input
                  type="text"
                  readOnly
                  value={share.share_url}
                  className="flex-1 px-3 py-2 bg-slate-950 border border-slate-800 rounded-xl text-xs font-mono text-purple-300 focus:outline-none"
                />
                <button
                  onClick={handleCopy}
                  className="flex items-center space-x-1 px-3.5 py-2 bg-purple-600 hover:bg-purple-500 text-white text-xs font-medium rounded-xl transition-all shadow-lg shadow-purple-600/20 shrink-0"
                >
                  {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                  <span>{copied ? 'Copied!' : 'Copy'}</span>
                </button>
              </div>
            </div>
          )}

          <div className="flex justify-end pt-2">
            <button
              onClick={onClose}
              className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 text-sm font-medium rounded-xl transition-colors"
            >
              Done
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
