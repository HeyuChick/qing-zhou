import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { NPopover } from 'naive-ui'
import MachineHealthStrip from '@/components/MachineHealthStrip.vue'
import { apiGet } from '@/api'

const { push } = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('@/api', () => ({ apiGet: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('naive-ui', async importOriginal => ({
  ...await importOriginal<typeof import('naive-ui')>(),
  useMessage: () => ({ error: vi.fn() }),
}))

let wrapper: ReturnType<typeof mount> | undefined
beforeEach(() => {
  vi.clearAllMocks()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  vi.mocked(apiGet).mockResolvedValue({
    from: 100, to: 200, bucket_sec: 60, gap_after_sec: 180,
    machines: [{ server_id: 7, name: '美国Verizon静态家宽', local: false,
      cells: [{ ts: 100, state: 'online', known: true, bytes: 405 * 1024 * 1024 }] }],
  })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

async function renderStrip() {
  wrapper = mount(MachineHealthStrip, { attachTo: document.body })
  await flushPromises()
  const canvas = wrapper.find('canvas')
  vi.spyOn(canvas.element, 'getBoundingClientRect').mockReturnValue({
    left: 100, top: 200, width: 600, height: 48,
    right: 700, bottom: 248, x: 100, y: 200, toJSON() {},
  })
  return { wrapper, canvas }
}

it('anchors the floating tooltip to viewport pointer coordinates even at the rightmost cell', async () => {
  const { wrapper, canvas } = await renderStrip()
  await canvas.trigger('mousemove', { clientX: 699, clientY: 240 })
  await flushPromises()
  const popover = wrapper.findComponent(NPopover)
  expect(popover.props()).toMatchObject({ x: 699, y: 240, show: true, placement: 'top', flip: true, overlap: false })
  const tip = document.body.querySelector('[role="tooltip"]')!
  expect(tip.textContent).toContain('美国Verizon静态家宽')
  expect(tip.textContent).toContain('405 MB')
  // The overlay escapes the health card's stacking/overflow context.
  expect(wrapper.element.contains(tip)).toBe(false)
  expect(popover.props('to')).toBe('body')
  expect((tip.closest('.n-popover') as HTMLElement).style.pointerEvents).toBe('none')
})

it.each(['mouseleave', 'scroll', 'resize', 'axis', 'reload'])('clears a stale tooltip on %s', async event => {
  const { wrapper, canvas } = await renderStrip()
  await canvas.trigger('mousemove', { clientX: 300, clientY: 240 })
  expect(wrapper.findComponent(NPopover).props('show')).toBe(true)
  if (event === 'mouseleave') await canvas.trigger('mouseleave')
  else if (event === 'scroll') canvas.element.dispatchEvent(new Event('scroll'))
  else if (event === 'resize') window.dispatchEvent(new Event('resize'))
  else if (event === 'axis') await canvas.trigger('mousemove', { clientX: 300, clientY: 205 })
  else wrapper.findComponent({ name: 'RadioGroup' }).vm.$emit('update:value', '6h')
  await flushPromises()
  expect(wrapper.findComponent(NPopover).props('show')).toBe(false)
  expect(document.body.querySelector('[role="tooltip"]')).toBeNull()
})

it('still opens the selected machine and time range through the tooltip', async () => {
  const { canvas } = await renderStrip()
  await canvas.trigger('mousemove', { clientX: 699, clientY: 240 })
  await canvas.trigger('click', { clientX: 699, clientY: 240 })
  expect(push).toHaveBeenCalledWith({ name: 'admin-monitor-detail', params: { id: 7 },
    hash: '#traffic-status', query: { from: '100', to: '200' } })
})
