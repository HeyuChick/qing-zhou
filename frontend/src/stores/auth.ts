import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { apiPost, apiGet } from '@/api'

export interface User {
  id: number
  username: string
  email: string
  email_verified: boolean
  role: string
  is_admin: boolean
  status: string
  points: number
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem('qz_token') || '')
  const user = ref<User | null>(null)
  const loaded = ref(false)
  let initPromise: Promise<void> | null = null

  const isLoggedIn = computed(() => !!token.value && !!user.value)
  const isAdmin = computed(() => !!user.value?.is_admin)

  async function login(username: string, password: string) {
    const data = await apiPost<{ token: string; user: User }>('/api/auth/login', { username, password })
    token.value = data.token
    user.value = data.user
    localStorage.setItem('qz_token', data.token)
  }

  async function register(username: string, password: string, code?: string, email?: string) {
    const body: any = { username, password }
    if (code) body.code = code
    if (email) body.email = email
    const data = await apiPost<{ token: string; user: User }>('/api/auth/register', body)
    token.value = data.token
    user.value = data.user
    localStorage.setItem('qz_token', data.token)
  }

  async function fetchMe() {
    if (!token.value) return
    try {
      user.value = await apiGet<User>('/api/auth/me')
    } catch (e: any) {
      if (e.status === 401) logout()
    }
  }

  function logout() {
    if (token.value) {
      apiPost('/api/auth/logout').catch(() => {})
    }
    token.value = ''
    user.value = null
    localStorage.removeItem('qz_token')
  }

  /** 初始化：从 localStorage 恢复 token，拉取用户信息。防重复调用。 */
  async function init() {
    if (loaded.value) return
    if (initPromise) return initPromise
    initPromise = (async () => {
      if (token.value) {
        await fetchMe()
      }
      loaded.value = true
    })()
    return initPromise
  }

  return { token, user, loaded, isLoggedIn, isAdmin, login, register, fetchMe, logout, init }
})
