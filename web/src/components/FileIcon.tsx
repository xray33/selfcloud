import React from 'react'
import {
  Folder,
  FileText,
  FileImage,
  FileVideo,
  FileAudio,
  FileCode,
  FileArchive,
  File as GenericFile
} from 'lucide-react'

interface FileIconProps {
  isDir: boolean
  contentType: string
  name: string
}

export const FileIcon: React.FC<FileIconProps> = ({ isDir, contentType, name }) => {
  if (isDir) {
    return <Folder className="w-5 h-5 text-amber-400 fill-amber-400/20 shrink-0" />
  }

  const ext = name.split('.').pop()?.toLowerCase() || ''

  if (contentType.startsWith('image/') || ['jpg', 'jpeg', 'png', 'gif', 'svg', 'webp', 'avif'].includes(ext)) {
    return <FileImage className="w-5 h-5 text-purple-400 shrink-0" />
  }
  if (contentType.startsWith('video/') || ['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(ext)) {
    return <FileVideo className="w-5 h-5 text-rose-400 shrink-0" />
  }
  if (contentType.startsWith('audio/') || ['mp3', 'wav', 'ogg', 'flac'].includes(ext)) {
    return <FileAudio className="w-5 h-5 text-cyan-400 shrink-0" />
  }
  if (['zip', 'tar', 'gz', '7z', 'rar', 'bz2'].includes(ext) || contentType.includes('zip') || contentType.includes('archive')) {
    return <FileArchive className="w-5 h-5 text-amber-500 shrink-0" />
  }
  if (['js', 'ts', 'jsx', 'tsx', 'go', 'py', 'json', 'html', 'css', 'rs', 'sh', 'sql', 'md'].includes(ext)) {
    return <FileCode className="w-5 h-5 text-emerald-400 shrink-0" />
  }
  if (['pdf', 'txt', 'doc', 'docx', 'xls', 'xlsx'].includes(ext) || contentType.startsWith('text/')) {
    return <FileText className="w-5 h-5 text-blue-400 shrink-0" />
  }

  return <GenericFile className="w-5 h-5 text-slate-400 shrink-0" />
}
