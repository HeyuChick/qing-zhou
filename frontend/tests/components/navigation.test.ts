import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, DOMWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import axe from 'axe-core'
import { NDrawer } from 'naive-ui'
import DashboardLayout from '@/components/DashboardLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { useConfigStore } from '@/stores/config'

vi.mock('naive-ui', async importOriginal => ({
  ...await importOriginal<typeof import('naive-ui')>(),
  useMessage: () => ({ error: vi.fn(), success: vi.fn(), warning: vi.fn() }),
}))

async function renderNavigation(mobile = false, isAdmin = true) {
  vi.mocked(window.matchMedia).mockImplementation(query => ({
    matches: mobile && query.includes('768px'), media: query, onchange: null,
    addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(),
    removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
  }))
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore()
  useConfigStore().config.site_name = ''
  auth.user = { id: 1, username: 'admin', email: '', email_verified: true,
    role: isAdmin ? 'admin' : 'user', is_admin: isAdmin, status: 'active', points: 0 }
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/:pathMatch(.*)*', component: { template: '<h1>管理页面</h1>' } },
  ] })
  await router.push('/admin'); await router.isReady()
  const wrapper = mount(DashboardLayout, {
    attachTo: document.body,
    global: { plugins: [pinia, router], stubs: { transition: false } },
  })
  await flushPromises()
  return { wrapper, router }
}

afterEach(() => { document.body.innerHTML = ''; vi.restoreAllMocks() })
describe('admin navigation accessibility', () => {
  for (const mobile of [false, true]) {
    it(`passes axe on ${mobile ? 'mobile' : 'desktop'} and keeps account button named`, async () => {
      const { wrapper } = await renderNavigation(mobile)
      const result = await axe.run(wrapper.element, {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] },
        // jsdom has no layout/color computation; contrast needs browser QA.
        rules: { 'color-contrast': { enabled: false } },
      })
      expect(result.violations.map(v => ({ id: v.id, nodes: v.nodes.map(n => n.target) }))).toEqual([])
      expect(wrapper.get('.account-button').attributes('aria-label')).toBe('admin账户菜单')
      wrapper.unmount()
    })
  }
  for (const mobile of [false, true]) {
    it(`places upstream management directly after registration codes on ${mobile ? 'mobile' : 'desktop'}`, async () => {
      const { wrapper } = await renderNavigation(mobile)
      if (mobile) {
        await wrapper.get('button[aria-label="菜单"]').trigger('click')
        await flushPromises()
      }
      const nav = document.querySelector('nav[aria-label="主导航"]')!
      const labels = Array.from(nav.querySelectorAll('.n-menu-item-content-header')).map(el => el.textContent)
      expect(labels.indexOf('注册码')).toBeGreaterThan(-1)
      expect(labels.indexOf('上游管理')).toBe(labels.indexOf('注册码') + 1)
      wrapper.unmount()
    })
  }
  for (const mobile of [false, true]) {
    for (const isAdmin of [false, true]) {
      it(`keeps the ${mobile ? 'mobile' : 'desktop'} brand link accessible and routes ${isAdmin ? 'admins' : 'users'} to the dashboard`, async () => {
        const { wrapper, router } = await renderNavigation(mobile, isAdmin)
        if (mobile) {
          await wrapper.get('button[aria-label="菜单"]').trigger('click')
          await flushPromises()
          expect(wrapper.findComponent(NDrawer).props('show')).toBe(true)
        }
        const brand = new DOMWrapper(document.querySelector<HTMLAnchorElement>('a.sidebar-brand')!)
        expect(brand.attributes('href')).toBe(router.resolve('/dashboard').href)
        expect(brand.text()).toContain('黑羽短腿机场')
        brand.element.focus()
        expect(document.activeElement).toBe(brand.element)
        await brand.trigger('click'); await flushPromises()
        expect(router.currentRoute.value.path).toBe('/dashboard')
        if (mobile) expect(wrapper.findComponent(NDrawer).props('show')).toBe(false)
        wrapper.unmount()
      })
    }
  }
})
