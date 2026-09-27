import { apiRequest } from './client'
import { Share, PublicShareInfo } from '../types'

export async function createShare(path: string): Promise<{ share: Share }> {
  return apiRequest<{ share: Share }>('/api/v1/shares', {
    method: 'POST',
    body: JSON.stringify({ path }),
  })
}

export async function getShares(): Promise<{ shares: Share[] }> {
  return apiRequest<{ shares: Share[] }>('/api/v1/shares', {
    method: 'GET',
  })
}

export async function revokeShare(token: string): Promise<void> {
  const encToken = encodeURIComponent(token)
  await apiRequest<{ status: string }>(`/api/v1/shares?token=${encToken}`, {
    method: 'DELETE',
  })
}

export async function getPublicShareInfo(token: string): Promise<PublicShareInfo> {
  const encToken = encodeURIComponent(token)
  return apiRequest<PublicShareInfo>(`/api/v1/public/shares?token=${encToken}`, {
    method: 'GET',
  })
}

export function getPublicDownloadUrl(token: string): string {
  return `/api/v1/public/shares/download?token=${encodeURIComponent(token)}`
}
