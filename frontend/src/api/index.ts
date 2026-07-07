/* ---------- API 封装 ---------- */

import { useAuthStore } from '@/stores/auth'

export interface ApiError extends Error {
  status: number
}

async function request<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const auth = useAuthStore()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(opts.headers as Record<string, string> || {}),
  }
  if (auth.token) {
    headers['Authorization'] = 'Bearer ' + auth.token
  }

  const res = await fetch(path, {
    ...opts,
    headers,
    credentials: 'include',
  })

  let body: any = null
  try { body = await res.json() } catch {}

  if (!res.ok) {
    if (res.status === 401) {
      auth.logout()
    }
    const err = new Error((body && body.msg) || `请求失败 ${res.status}`) as ApiError
    err.status = res.status
    throw err
  }
  return body?.data ?? null
}

/** GET，返回列表时保证是数组 */
export async function apiList<T = any>(path: string): Promise<T[]> {
  const data = await request<T[]>(path)
  return Array.isArray(data) ? data : []
}

/** GET，返回单个对象 */
export function apiGet<T = any>(path: string): Promise<T> {
  return request<T>(path)
}

export function apiPost<T = any>(path: string, body?: any): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    body: body ? JSON.stringify(body) : undefined,
  })
}

export function apiPut<T = any>(path: string, body?: any): Promise<T> {
  return request<T>(path, {
    method: 'PUT',
    body: body ? JSON.stringify(body) : undefined,
  })
}

export function apiDelete<T = any>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' })
}
