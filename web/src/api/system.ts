import { apiRequest } from './client'

export interface SystemInfo {
  service: {
    version: string
    commit: string
    build_date: string
  }
}

export async function getSystemInfo(): Promise<SystemInfo> {
  return apiRequest<SystemInfo>('/api/v1/system/info', {
    method: 'GET',
  })
}
