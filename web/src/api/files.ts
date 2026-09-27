import { apiRequest } from './client'
import { NodeItem } from '../types'

export async function getFiles(path: string): Promise<{ node: NodeItem; items: NodeItem[] }> {
  const encPath = encodeURIComponent(path)
  return apiRequest<{ node: NodeItem; items: NodeItem[] }>(`/api/v1/files?path=${encPath}`, {
    method: 'GET',
  })
}

export async function uploadFile(destPath: string, file: File): Promise<{ node: NodeItem }> {
  const formData = new FormData()
  formData.append('path', destPath)
  formData.append('file', file)

  return apiRequest<{ node: NodeItem }>('/api/v1/files/upload', {
    method: 'POST',
    body: formData,
  })
}

export async function mkdir(path: string): Promise<{ node: NodeItem }> {
  return apiRequest<{ node: NodeItem }>('/api/v1/files/mkdir', {
    method: 'POST',
    body: JSON.stringify({ path }),
  })
}

export async function deletePath(path: string): Promise<void> {
  const encPath = encodeURIComponent(path)
  await apiRequest<{ status: string }>(`/api/v1/files?path=${encPath}`, {
    method: 'DELETE',
  })
}

export async function renamePath(oldPath: string, newPath: string): Promise<void> {
  await apiRequest<{ status: string }>('/api/v1/files/rename', {
    method: 'POST',
    body: JSON.stringify({ old_path: oldPath, new_path: newPath }),
  })
}

export function getDownloadUrl(path: string): string {
  return `/api/v1/files/download?path=${encodeURIComponent(path)}`
}

export function getZipDownloadUrl(path: string): string {
  return `/api/v1/files/download-zip?path=${encodeURIComponent(path)}`
}
