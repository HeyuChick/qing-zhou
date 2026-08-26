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
      // 会话失效 → 独立登录页，带上当前路径登录后回来。已在登录页则跳过
      // （登录接口密码错也回 401，不能引发重复跳转）。
      const h = window.location.hash
      if (!h.startsWith('#/login')) {
        const cur = h.startsWith('#') ? h.slice(1) : '/'
        const safe = cur.startsWith('/') && !cur.startsWith('//') ? cur.split('?')[0] : '/'
        window.location.hash = '/login?redirect=' + encodeURIComponent(safe)
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

/**
 * GET 一个二进制附件并触发浏览器下载。
 *
 * 不能用 <a href> 直接下载：认证走的是 Authorization 头，普通导航带不上，
 * 服务端只会回 401。所以先 fetch 成 blob，再用临时 object URL 触发保存。
 */
export async function apiDownload(path: string, fallbackName: string): Promise<void> {
  const auth = useAuthStore()
  const res = await fetch(path, {
    headers: auth.token ? { Authorization: 'Bearer ' + auth.token } : {},
    credentials: 'include',
  })
  if (!res.ok) {
    // 失败时后端回的是 JSON 信封，读出来当错误信息用。
    let msg = `请求失败 ${res.status}`
    try { const b = await res.json(); if (b?.msg) msg = b.msg } catch {}
    const err = new Error(msg) as ApiError
    err.status = res.status
    throw err
  }
  // 优先用服务端给的文件名（Content-Disposition），它带了生成时间。
  let name = fallbackName
  const cd = res.headers.get('Content-Disposition') || ''
  const m = /filename="?([^"';]+)"?/.exec(cd)
  if (m) name = m[1]

  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  // 立刻撤销会让部分浏览器取消尚未开始的下载，推迟一拍。
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
