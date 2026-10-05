import { beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AdminRelayMetering from '@/components/AdminRelayMetering.vue'
import { apiGet, apiPut } from '@/api'
vi.mock('@/api',()=>({apiGet:vi.fn(),apiPut:vi.fn(),apiPost:vi.fn()}))
beforeEach(()=>{vi.resetAllMocks();vi.mocked(apiGet).mockResolvedValue({enabled:false,cumulative_enabled:false,cumulative_started:false,links:[]});vi.mocked(apiPut).mockResolvedValue({started:true})})
it('requires reviewing credential and restart effects before applying',async()=>{
 const w=mount(AdminRelayMetering);await flushPromises();await w.find('form').trigger('submit');expect(apiPut).not.toHaveBeenCalled();expect(w.text()).toContain('生成并加密保存内部中转凭据');expect(w.text()).toContain('可能中断现有连接')
 await w.findAll('button').find(b=>b.text()==='确认变更')!.trigger('click');await flushPromises();expect(apiPut).toHaveBeenCalledWith('/api/admin/relay-metering',{enabled:false,cumulative_enabled:false,per_user_enabled:false,confirm:true});w.unmount()
})
it('does not permit an unsafe cumulative to reset downgrade',async()=>{
 vi.mocked(apiGet).mockResolvedValue({enabled:true,cumulative_enabled:true,cumulative_started:true,links:[{id:1,source_name:'DMIT',target_name:'Verizon',state:'prepared'}]})
 const w=mount(AdminRelayMetering);await flushPromises();expect(w.findAll('input')[1]!.attributes('disabled')).toBeDefined();expect(w.text()).toContain('等待落地接受');w.unmount()
})
it('shows failed load rather than claiming no links and cancellation sends nothing',async()=>{
 vi.mocked(apiGet).mockRejectedValue(new Error('unavailable'));const w=mount(AdminRelayMetering);await flushPromises();expect(w.text()).toContain('unavailable');expect(w.text()).not.toContain('尚无独立链路身份');w.unmount()
})

it('keeps per-user relay accounting explicit and preserves existing user credentials',async()=>{
 const w=mount(AdminRelayMetering);await flushPromises()
 const inputs=w.findAll('input')
 expect(inputs[2]!.attributes('disabled')).toBeDefined()
 await inputs[0]!.setValue(true);await inputs[2]!.setValue(true)
 await w.find('form').trigger('submit')
 expect(w.text()).toContain('用户现有账号、密码和订阅不变')
 expect(w.text()).toContain('所有机器分别实测，不复制入口用量')
 expect(apiPut).not.toHaveBeenCalled()
 await w.findAll('button').find(b=>b.text()==='确认变更')!.trigger('click');await flushPromises()
 expect(apiPut).toHaveBeenCalledWith('/api/admin/relay-metering',{enabled:true,cumulative_enabled:false,per_user_enabled:true,confirm:true})
 w.unmount()
})
it('cancels a per-user change and clears its dependent switch when links are disabled',async()=>{
 vi.mocked(apiGet).mockResolvedValue({enabled:true,cumulative_enabled:false,per_user_enabled:true,links:[]})
 const w=mount(AdminRelayMetering);await flushPromises()
 await w.find('form').trigger('submit')
 await w.findAll('button').find(b=>b.text()==='取消')!.trigger('click')
 expect(apiPut).not.toHaveBeenCalled()
 await w.findAll('input')[0]!.setValue(false)
 expect((w.findAll('input')[2]!.element as HTMLInputElement).checked).toBe(false)
 await w.find('form').trigger('submit')
 expect(w.text()).toContain('关闭逐用户中转后')
 await w.findAll('button').find(b=>b.text()==='确认变更')!.trigger('click');await flushPromises()
 expect(apiPut).toHaveBeenCalledWith('/api/admin/relay-metering',{enabled:false,cumulative_enabled:false,per_user_enabled:false,confirm:true})
 w.unmount()
})
it('cannot overwrite settings after a failed read',async()=>{
 vi.mocked(apiGet).mockRejectedValue(new Error('failed read'))
 const w=mount(AdminRelayMetering);await flushPromises();await w.find('form').trigger('submit')
 expect(w.find('button[type="submit"]').attributes('disabled')).toBeDefined()
 expect(w.text()).not.toContain('确认变更')
 expect(apiPut).not.toHaveBeenCalled();w.unmount()
})
