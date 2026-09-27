import { apiRequest } from './client'
import { User } from '../types'

export async function login(username: string, password: string): Promise<{ user: User }> {
  return apiRequest<{ user: User }>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
}

export async function logout(): Promise<void> {
  await apiRequest<{ status: string }>('/api/v1/auth/logout', {
    method: 'POST',
  })
}

export async function getMe(): Promise<{ user: User }> {
  return apiRequest<{ user: User }>('/api/v1/auth/me', {
    method: 'GET',
  })
}
