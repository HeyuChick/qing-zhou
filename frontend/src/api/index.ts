/* ---------- API 封装 ---------- */

import { useAuthStore } from '@/stores/auth'

export interface ApiError extends Error {
  status: number
}

async function request<T = any>(path: string, opts: RequestInit = {}, raw = false): Promise<T> {
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
      // Session died mid-use — bounce to the public page with the login prompt so
      // the user isn't stranded on a dead screen of failing calls. Skip when already
      // on the public page (its own authless calls must not cause a redirect loop).
      const h = window.location.hash
      if (h && h !== '#/' && !h.startsWith('#/?')) {
        window.location.hash = '/?login=1'
      }
    }
    const err = new Error((body && body.msg) || `请求失败 ${res.status}`) as ApiError
    err.status = res.status
    throw err
  }
  // Most endpoints reply with the {code,data,msg} envelope, so we unwrap .data.
  // A few (e.g. sing-box config preview) write a raw JSON document straight to
  // the body — those have no .data, so unwrapping would yield null. `raw` returns
  // the whole parsed body for those callers.
  return raw ? (body as T) : (body?.data ?? null)
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

/**
 * GET，返回未拆封的原始响应体（用于直接返回 JSON 文档、而非 {code,data,msg}
 * 信封的接口，如 sing-box 配置预览）。
 */
export function apiGetRaw<T = any>(path: string): Promise<T> {
  return request<T>(path, {}, true)
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
