import React from 'react'
import { Cloud, LogOut, User as UserIcon, Share2 } from 'lucide-react'
import { User } from '../types'

const staticVersion = import.meta.env.VITE_BUILD_VERSION || 'dev'

interface NavbarProps {
  user: User | null
  onLogout: () => void
  onOpenShares?: () => void
  sharesCount?: number
  serviceVersion?: string
}

export const Navbar: React.FC<NavbarProps> = ({ user, onLogout, onOpenShares, sharesCount, serviceVersion }) => {
  const svcVer = serviceVersion || 'dev'
  const versionsMatch = svcVer === staticVersion

  return (
    <header className="sticky top-0 z-30 bg-slate-900/80 backdrop-blur-md border-b border-slate-800">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex items-center justify-between h-16">
          {/* Logo & Brand */}
          <div className="flex items-center space-x-3">
            <div className="p-2 bg-brand-600/20 text-brand-500 rounded-xl border border-brand-500/30">
              <Cloud className="w-6 h-6" />
            </div>
            <div>
              <span className="text-lg font-bold text-white tracking-tight">SelfCloud</span>
              {versionsMatch ? (
                <span
                  className="ml-2 text-xs font-semibold px-2 py-0.5 bg-slate-800 text-slate-400 rounded-full border border-slate-700"
                  title={`service: ${svcVer} · static: ${staticVersion}`}
                >
                  {svcVer}
                </span>
              ) : (
                <span className="ml-2 inline-flex items-center space-x-1" title={`service: ${svcVer} · static: ${staticVersion}`}>
                  <span className="text-xs font-semibold px-2 py-0.5 bg-slate-800 text-slate-400 rounded-full border border-slate-700">
                    svc:{svcVer}
                  </span>
                  <span className="text-xs font-semibold px-2 py-0.5 bg-slate-800 text-slate-400 rounded-full border border-slate-700">
                    web:{staticVersion}
                  </span>
                </span>
              )}
            </div>
          </div>

          {/* User Profile & Actions */}
          {user && (
            <div className="flex items-center space-x-3">
              {onOpenShares && (
                <button
                  onClick={onOpenShares}
                  className="flex items-center space-x-1.5 text-sm font-medium text-slate-300 hover:text-purple-300 bg-purple-500/10 hover:bg-purple-500/20 px-3 py-1.5 rounded-lg border border-purple-500/30 transition-all"
                  title="Manage active shared links"
                >
                  <Share2 className="w-4 h-4 text-purple-400" />
                  <span className="hidden sm:inline">Shared Links</span>
                  {sharesCount !== undefined && sharesCount > 0 && (
                    <span className="ml-1 px-1.5 py-0.2 bg-purple-500 text-white text-[10px] font-bold rounded-full">
                      {sharesCount}
                    </span>
                  )}
                </button>
              )}

              {/* Google SSO Linking / Status */}
              {user.sso_enabled && (
                user.google_linked ? (
                  <div
                    className="flex items-center space-x-1.5 text-xs text-emerald-400 bg-emerald-500/10 px-2.5 py-1.5 rounded-lg border border-emerald-500/20 select-none"
                    title="Google account linked"
                  >
                    <svg className="w-3.5 h-3.5 shrink-0" viewBox="0 0 24 24">
                      <path
                        fill="#4285F4"
                        d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
                      />
                      <path
                        fill="#34A853"
                        d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
                      />
                      <path
                        fill="#FBBC05"
                        d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.06H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.94l2.85-2.22.81-.63z"
                      />
                      <path
                        fill="#EA4335"
                        d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.06l3.66 2.84c.87-2.6 3.3-4.52 6.16-4.52z"
                      />
                    </svg>
                    <span className="hidden md:inline font-medium">Google Linked</span>
                  </div>
                ) : (
                  <button
                    onClick={() => {
                      window.location.href = '/api/v1/auth/sso/google/login'
                    }}
                    className="flex items-center space-x-1.5 text-sm font-medium text-slate-300 hover:text-white bg-slate-800/80 hover:bg-slate-700/80 px-3 py-1.5 rounded-lg border border-slate-700/60 transition-all cursor-pointer"
                    title="Link your Google account"
                  >
                    <svg className="w-4 h-4 shrink-0" viewBox="0 0 24 24">
                      <path
                        fill="#4285F4"
                        d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
                      />
                      <path
                        fill="#34A853"
                        d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
                      />
                      <path
                        fill="#FBBC05"
                        d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.06H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.94l2.85-2.22.81-.63z"
                      />
                      <path
                        fill="#EA4335"
                        d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.06l3.66 2.84c.87-2.6 3.3-4.52 6.16-4.52z"
                      />
                    </svg>
                    <span className="hidden sm:inline">Link Google</span>
                  </button>
                )
              )}

              <div className="flex items-center space-x-2 text-sm text-slate-300 bg-slate-800/80 px-3 py-1.5 rounded-lg border border-slate-700/60">
                <UserIcon className="w-4 h-4 text-brand-500" />
                <span className="font-medium">{user.username}</span>
              </div>
              <button
                onClick={onLogout}
                className="flex items-center space-x-1.5 text-sm font-medium text-slate-400 hover:text-red-400 bg-slate-800/40 hover:bg-red-500/10 px-3 py-1.5 rounded-lg border border-slate-700/40 hover:border-red-500/30 transition-all"
                title="Log out"
              >
                <LogOut className="w-4 h-4" />
                <span className="hidden sm:inline">Logout</span>
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  )
}
