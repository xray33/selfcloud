import React from 'react'
import { ChevronRight, Home } from 'lucide-react'

interface BreadcrumbProps {
  currentPath: string
  onNavigate: (path: string) => void
}

export const Breadcrumb: React.FC<BreadcrumbProps> = ({ currentPath, onNavigate }) => {
  const parts = currentPath.split('/').filter(Boolean)

  const buildPath = (index: number) => {
    return '/' + parts.slice(0, index + 1).join('/')
  }

  return (
    <nav className="flex items-center space-x-1 text-sm font-medium text-slate-400 overflow-x-auto py-2">
      <button
        onClick={() => onNavigate('/')}
        className={`flex items-center space-x-1.5 px-2.5 py-1 rounded-md transition-colors ${
          currentPath === '/' ? 'text-brand-500 font-semibold bg-brand-500/10' : 'hover:text-slate-200 hover:bg-slate-800'
        }`}
      >
        <Home className="w-4 h-4" />
        <span>Home</span>
      </button>

      {parts.map((part, idx) => {
        const pathTarget = buildPath(idx)
        const isLast = idx === parts.length - 1

        return (
          <React.Fragment key={pathTarget}>
            <ChevronRight className="w-4 h-4 text-slate-600 shrink-0" />
            <button
              onClick={() => onNavigate(pathTarget)}
              className={`px-2.5 py-1 rounded-md truncate max-w-[150px] sm:max-w-[200px] transition-colors ${
                isLast ? 'text-brand-500 font-semibold bg-brand-500/10' : 'hover:text-slate-200 hover:bg-slate-800'
              }`}
            >
              {part}
            </button>
          </React.Fragment>
        )
      })}
    </nav>
  )
}
