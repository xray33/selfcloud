import React, { useState, useEffect } from 'react'
import { User } from './types'
import { getMe, login as apiLogin, logout as apiLogout } from './api/auth'
import { LoginPage } from './pages/LoginPage'
import { FilesPage } from './pages/FilesPage'
import { PublicSharePage } from './pages/PublicSharePage'

export function App() {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  // Check if current URL is a public share link: e.g. /s/{token}
  const pathname = window.location.pathname
  const shareMatch = pathname.match(/^\/s\/([^\/]+)/)
  const shareToken = shareMatch ? shareMatch[1] : null

  useEffect(() => {
    if (!shareToken) {
      getMe()
        .then((res) => setUser(res.user))
        .catch(() => setUser(null))
        .finally(() => setLoading(false))
    } else {
      setLoading(false)
    }
  }, [shareToken])

  const handleLogin = async (username: string, pass: string) => {
    const res = await apiLogin(username, pass)
    setUser(res.user)
  }

  const handleLogout = async () => {
    await apiLogout()
    setUser(null)
  }

  if (shareToken) {
    return <PublicSharePage token={shareToken} />
  }

  if (loading) {
    return (
      <div className="min-h-screen bg-slate-950 flex items-center justify-center text-slate-500 text-sm animate-pulse">
        Initializing SelfCloud...
      </div>
    )
  }

  if (!user) {
    return <LoginPage onLogin={handleLogin} />
  }

  return <FilesPage user={user} onLogout={handleLogout} />
}
