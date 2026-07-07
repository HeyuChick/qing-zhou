import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiGet } from '@/api'

export interface SiteConfig {
  site_name: string
  site_description: string
  register_mode: string
  registration_open: boolean
  email_verify_required: boolean
  points_per_cny: number
  homepage_mode: string
  homepage_url: string
}

export const useConfigStore = defineStore('config', () => {
  const config = ref<SiteConfig>({
    site_name: '轻舟',
    site_description: '',
    register_mode: 'open',
    registration_open: true,
    email_verify_required: true,
    points_per_cny: 10,
    homepage_mode: 'monitor',
    homepage_url: '',
  })

  async function fetchConfig() {
    try {
      const data = await apiGet<SiteConfig>('/api/config')
      if (data) Object.assign(config.value, data)
    } catch {}
  }

  return { config, fetchConfig }
})
