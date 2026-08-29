import type { Router } from 'vue-router'
import type { SiteConfig } from '@/stores/config'

export function externalHelpURL(config: Pick<SiteConfig, 'help_docs_mode' | 'help_docs_url'>): string {
  if (config.help_docs_mode !== 'external') return ''
  try {
    const url = new URL((config.help_docs_url || '').trim())
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : ''
  } catch {
    return ''
  }
}

export function openHelp(config: SiteConfig, router: Router) {
  const url = externalHelpURL(config)
  if (url) {
    window.open(url, '_blank', 'noopener,noreferrer')
    return
  }
  router.push('/help')
}
