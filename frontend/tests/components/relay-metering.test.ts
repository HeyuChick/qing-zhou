import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AdminRelayMetering from '@/components/AdminRelayMetering.vue'
import { apiGet, apiPut, apiPost } from '@/api'
vi.mock('@/api',()=>({apiGet:vi.fn(),apiPut:vi.fn(),apiPost:vi.fn()}))
const node=(id=0,name='面板本机')=>({server_id:id,name,version:'1.14.2',has_v2ray_api:true,has_vision_framing_fix:false,vision_required:false,checked_at:1700000000,requires_reinstall:false,requires_check:false,reasons:[] as string[]})
const initial=()=>({enabled:false,per_user_enabled:false,cumulative_enabled:false,cumulative_started:false,links:[] as any[],credentials:[] as any[],nodes:[node()],sync_epoch:'instance-a',sync:{} as Record<string,any>})
let current:ReturnType<typeof initial>, preflight:any, wrappers:VueWrapper[]
const button=(w:VueWrapper,text:string)=>w.findAll('button').find(b=>b.text()===text)!
async function open(w:VueWrapper){(w.find('details').element as HTMLDetailsElement).open=true;await w.find('details').trigger('toggle');await flushPromises()}
async function close(w:VueWrapper){(w.find('details').element as HTMLDetailsElement).open=false;await w.find('details').trigger('toggle');await flushPromises()}
async function render(opened=true){const w=mount(AdminRelayMetering);wrappers.push(w);await flushPromises();if(opened)await open(w);return w}
async function inspect(w:VueWrapper){await w.find('form').trigger('submit');await flushPromises()}
async function save(w:VueWrapper){await inspect(w);await button(w,'确认保存并下发').trigger('click');await flushPromises()}
function deferred<T>(){let resolve!:(value:T)=>void;let reject!:(reason?:any)=>void;const promise=new Promise<T>((res,rej)=>{resolve=res;reject=rej});return {promise,resolve,reject}}
beforeEach(()=>{
 vi.useFakeTimers();vi.resetAllMocks();wrappers=[];current=initial()
 preflight={valid:true,errors:[],nodes:[node()],scope_note:'只读取已有记录，不进行节点连接'}
 vi.mocked(apiGet).mockImplementation(async path=>path.includes('/preflight?')?preflight:structuredClone(current))
 vi.mocked(apiPut).mockResolvedValue({started:true,sync_ticket:{epoch:'instance-a',revision:10}})
 vi.mocked(apiPost).mockResolvedValue({started:true,sync_ticket:{epoch:'instance-a',revision:10}})
})
afterEach(()=>{for(const w of wrappers)w.unmount();vi.useRealTimers()})
it('preflights without saving, preserves defaults and asks for credential/restart confirmation',async()=>{
 const w=await render();expect(w.findAll('input').every(i=>!(i.element as HTMLInputElement).checked)).toBe(true)
 await inspect(w);expect(apiPut).not.toHaveBeenCalled()
 expect(apiGet).toHaveBeenCalledWith('/api/admin/relay-metering/preflight?enabled=false&cumulative_enabled=false&per_user_enabled=false',expect.objectContaining({signal:expect.any(AbortSignal)}))
 expect(w.text()).toContain('生成并加密保存内部中转凭据');expect(w.text()).toContain('可能中断现有连接');expect(w.text()).toContain('受影响节点（1 台）')
 await button(w,'确认保存并下发').trigger('click');await flushPromises()
 expect(apiPut).toHaveBeenCalledWith('/api/admin/relay-metering',{enabled:false,cumulative_enabled:false,per_user_enabled:false,confirm:true})
 expect(w.text()).toContain('设置已保存，正在等待节点下发');expect(w.text()).not.toContain('本轮节点下发已成功')
})
it('does not permit an unsafe cumulative to reset downgrade',async()=>{
 current={...current,enabled:true,cumulative_enabled:true,cumulative_started:true,links:[{id:1,source_name:'DMIT',target_name:'Verizon',state:'prepared'}]}
 const w=await render();expect(w.findAll('input')[1]!.attributes('disabled')).toBeDefined();expect(w.text()).toContain('等待落地接受')
})
it('keeps per-user metering explicit and preserves the account and subscription',async()=>{
 const w=await render(), inputs=w.findAll('input')
 expect(inputs[2]!.attributes('disabled')).toBeDefined();await inputs[0]!.setValue(true);await inputs[2]!.setValue(true);await inspect(w)
 expect(w.text()).toContain('用户现有账号、密码和订阅不变');expect(w.text()).toContain('所有机器分别实测，不复制入口用量')
 await button(w,'确认保存并下发').trigger('click');await flushPromises()
 expect(apiPut).toHaveBeenCalledWith('/api/admin/relay-metering',{enabled:true,cumulative_enabled:false,per_user_enabled:true,confirm:true})
})
it('cancels without writing and clears the dependent per-user switch when links are disabled',async()=>{
 current={...current,enabled:true,per_user_enabled:true};const w=await render();await inspect(w);await button(w,'取消').trigger('click')
 expect(apiPut).not.toHaveBeenCalled();await w.findAll('input')[0]!.setValue(false);expect((w.findAll('input')[2]!.element as HTMLInputElement).checked).toBe(false)
 await inspect(w);expect(w.text()).toContain('关闭逐用户中转后')
})
it('cannot overwrite settings or claim no links after a failed read',async()=>{
 vi.mocked(apiGet).mockRejectedValue(new Error('failed read'));const w=await render();await w.find('form').trigger('submit')
 expect(w.text()).toContain('failed read');expect(w.text()).not.toContain('尚无独立链路身份');expect(w.find('button[type="submit"]').attributes('disabled')).toBeDefined();expect(apiPut).not.toHaveBeenCalled()
})
it('shows every preflight blocker and specific core requirements without silently installing anything',async()=>{
 preflight={valid:false,errors:['入口 A 存在环路','入口 B 落地已禁用'],scope_note:'只读取已有记录',nodes:[{...node(1,'香港入口'),requires_reinstall:true,vision_required:true,reasons:['需要 with_v2ray_api','需要 Vision 修复内核']},{...node(2,'美国落地'),requires_check:true,error:'SSH unavailable',reasons:['重新检测 SSH unavailable']}]}
 const w=await render();await inspect(w)
 for(const value of ['入口 A 存在环路','入口 B 落地已禁用','香港入口','美国落地','需要 with_v2ray_api','需要 Vision 修复内核','SSH unavailable'])expect(w.text()).toContain(value)
 expect(button(w,'确认保存并下发').attributes('disabled')).toBeDefined();expect(apiPut).not.toHaveBeenCalled();expect(apiPost).not.toHaveBeenCalled()
})
it('blocks submission on preflight transport or malformed response failure',async()=>{
 vi.mocked(apiGet).mockImplementation(async path=>{if(path.includes('/preflight?'))throw new Error('preflight unavailable');return current})
 const w=await render();await inspect(w);expect(w.text()).toContain('preflight unavailable');expect(button(w,'确认保存并下发').attributes('disabled')).toBeDefined()
 await button(w,'取消').trigger('click');preflight={valid:true};vi.mocked(apiGet).mockImplementation(async path=>path.includes('/preflight?')?preflight:current)
 await inspect(w);expect(w.text()).toContain('预检结果不完整');expect(apiPut).not.toHaveBeenCalled()
})
it('disables repeated clicks while preflighting and saving, preserving one mutation',async()=>{
 const check=deferred<any>(), write=deferred<any>();vi.mocked(apiGet).mockImplementation(path=>path.includes('/preflight?')?check.promise:Promise.resolve(current));vi.mocked(apiPut).mockReturnValue(write.promise)
 const w=await render();await w.find('form').trigger('submit');await w.find('form').trigger('submit')
 expect(apiGet.mock.calls.filter(([path])=>path.includes('/preflight?'))).toHaveLength(1)
 check.resolve(preflight);await flushPromises();await button(w,'确认保存并下发').trigger('click');await button(w,'正在保存…').trigger('click')
 expect(apiPut).toHaveBeenCalledTimes(1);write.resolve({sync_ticket:{epoch:'instance-a',revision:10}});await flushPromises()
})
it('renders every failed node, with names and concrete reasons, rather than only the first',async()=>{
 current.nodes=[node(1,'入口 A'),node(2,'落地 B')];current.sync={'-1':{state:'failed',error:'全局配置失败'},'1':{state:'failed',error:'A: SSH connection refused'},'2':{state:'failed',error:'B: sing-box check failed\nunsupported protocol'}}
 const w=await render();for(const value of ['全局配置失败','入口 A','落地 B','A: SSH connection refused','B: sing-box check failed','unsupported protocol'])expect(w.text()).toContain(value)
})
it('never treats the old success or an older in-flight pass as completion for a newer ticket',async()=>{
 current.sync={'-1':{state:'ok',revision:15,request_revision:5,started_revision:6},'0':{state:'ok',revision:14}}
 const w=await render();await save(w);expect(w.text()).toContain('等待本次确认');expect(w.text()).not.toContain('本轮节点下发已成功')
 current.sync={'-1':{state:'running',revision:16,request_revision:10,started_revision:16},'0':{state:'ok',revision:14}}
 await vi.advanceTimersByTimeAsync(2000);expect(w.text()).toContain('等待本次确认')
 current.sync={'-1':{state:'ok',revision:18,request_revision:10,started_revision:16},'0':{state:'ok',revision:17}}
 await vi.advanceTimersByTimeAsync(2000);expect(w.text()).toContain('本轮节点下发已成功');expect(w.text()).toContain('链路是否已切换')
 const calls=apiGet.mock.calls.length;await vi.advanceTimersByTimeAsync(10000);expect(apiGet).toHaveBeenCalledTimes(calls)
})
it('stops on terminal failure and never labels saved settings as success',async()=>{
 current.nodes=[node(1,'入口'),node(2,'落地')];current.sync={'-1':{state:'failed',error:'无法准备配置',request_revision:10,started_revision:11,revision:14},'1':{state:'failed',error:'SSH refused',revision:12},'2':{state:'failed',error:'core missing',revision:13}}
 const w=await render();await save(w);expect(w.text()).toContain('设置已保存，但下发有失败');expect(w.text()).toContain('SSH refused');expect(w.text()).toContain('core missing');expect(w.text()).not.toContain('本轮节点下发已成功')
 const calls=apiGet.mock.calls.length;await vi.advanceTimersByTimeAsync(10000);expect(apiGet).toHaveBeenCalledTimes(calls)
})
it('rejects reused revision numbers after the controller restarts',async()=>{
 const w=await render();current.sync_epoch='instance-b';current.sync={'-1':{state:'ok',revision:13,request_revision:10,started_revision:11},'0':{state:'ok',revision:12}};await save(w)
 expect(w.text()).toContain('面板已重启');expect(w.text()).not.toContain('本轮节点下发已成功');expect(w.text()).toContain('等待本次确认')
})
it('does not claim success when save fails or status refresh fails after save',async()=>{
 const w=await render();vi.mocked(apiPut).mockRejectedValueOnce(new Error('connection lost'));await save(w);expect(w.text()).toContain('尚未确认是否保存');expect(w.text()).not.toContain('设置已保存，正在等待')
 await button(w,'取消').trigger('click');await inspect(w);vi.mocked(apiGet).mockRejectedValue(new Error('status unavailable'));await button(w,'确认保存并下发').trigger('click');await flushPromises()
 expect(w.text()).toContain('status unavailable');expect(w.text()).toContain('自动刷新已暂停');expect(w.text()).not.toContain('本轮节点下发已成功')
})
it('bounds automatic polling and leaves an explicit manual continuation for waiting nodes',async()=>{
 const w=await render();current.sync={'-1':{state:'pending',revision:10,request_revision:10}};await save(w)
 const before=apiGet.mock.calls.length;await vi.advanceTimersByTimeAsync(122000);expect(apiGet.mock.calls.length-before).toBe(60)
 expect(w.text()).toContain('自动刷新已暂停');expect(button(w,'继续检查结果')).toBeTruthy()
 const stopped=apiGet.mock.calls.length;await vi.advanceTimersByTimeAsync(20000);expect(apiGet).toHaveBeenCalledTimes(stopped)
 await button(w,'继续检查结果').trigger('click');await flushPromises();await vi.advanceTimersByTimeAsync(2000);expect(apiGet.mock.calls.length).toBeGreaterThan(stopped)
})
it('closing aborts polling requests and stale responses cannot overwrite a newly reopened panel',async()=>{
 const w=await render();await save(w);const old=deferred<any>();let signal:AbortSignal|undefined
 vi.mocked(apiGet).mockImplementation((_path,options)=>{signal=options?.signal as AbortSignal;return old.promise})
 await vi.advanceTimersByTimeAsync(2000);await close(w);expect(signal?.aborted).toBe(true)
 current={...current,enabled:true,per_user_enabled:true};vi.mocked(apiGet).mockResolvedValue(current);await open(w)
 old.resolve(initial());await flushPromises();expect((w.findAll('input')[0]!.element as HTMLInputElement).checked).toBe(true)
 const calls=apiGet.mock.calls.length;await close(w);await vi.advanceTimersByTimeAsync(10000);expect(apiGet).toHaveBeenCalledTimes(calls)
})
it('cancelling preflight ignores its late result and unmount aborts reads',async()=>{
 const w=await render();const old=deferred<any>();let signal:AbortSignal|undefined
 vi.mocked(apiGet).mockImplementation((_path,options)=>{signal=options?.signal as AbortSignal;return old.promise});await w.find('form').trigger('submit');await button(w,'取消').trigger('click');expect(signal?.aborted).toBe(true)
 old.resolve(preflight);await flushPromises();expect(w.find('[aria-label="变更预检"]').exists()).toBe(false);expect(apiPut).not.toHaveBeenCalled()
 const pending=deferred<any>();vi.mocked(apiGet).mockImplementation((_path,options)=>{signal=options?.signal as AbortSignal;return pending.promise});await button(w,'刷新状态').trigger('click');w.unmount();expect(signal?.aborted).toBe(true)
})
it('closing during save keeps the write lock and ignores its stale UI completion',async()=>{
 const w=await render(), write=deferred<any>();vi.mocked(apiPut).mockReturnValue(write.promise);await inspect(w);await button(w,'确认保存并下发').trigger('click');await close(w);await open(w)
 expect(w.find('button[type="submit"]').attributes('disabled')).toBeDefined();write.resolve({sync_ticket:{epoch:'instance-a',revision:10}});await flushPromises()
 expect(w.text()).not.toContain('本轮节点下发已成功');expect(apiPut).toHaveBeenCalledTimes(1)
})
it('retains individual old-generation user identity in keys, confirmation and request',async()=>{
 current.credentials=[{kind:'user_generation',link_id:1,server_id:2,inbound_id:3,user_id:44,generation:1,state:'active',name:'用户44旧代',can_retire:true,reason:'来源与落地已静默'}]
 const w=await render();await button(w,'停用旧凭据').trigger('click');expect(w.text()).toContain('用户 #44');expect(apiPost).not.toHaveBeenCalled()
 await button(w,'确认凭据变更').trigger('click');await flushPromises();expect(apiPost).toHaveBeenCalledWith('/api/admin/relay-metering/credentials',expect.objectContaining({kind:'user_generation',user_id:44,action:'retire',confirm:true}))
})

it('missing epoch cannot validate an apparently matching successful revision',async()=>{
 current.sync_epoch='';current.sync={'-1':{state:'ok',revision:13,request_revision:10,started_revision:11},'0':{state:'ok',revision:12}}
 const w=await render();await save(w);expect(w.text()).not.toContain('本轮节点下发已成功');expect(w.text()).toContain('等待本次确认')
})
it('a later failed save clears the prior operation success banner',async()=>{
 current.sync={'-1':{state:'ok',revision:13,request_revision:10,started_revision:11},'0':{state:'ok',revision:12}}
 const w=await render();await save(w);expect(w.text()).toContain('本轮节点下发已成功')
 vi.mocked(apiPut).mockRejectedValueOnce(new Error('新的保存失败'));await save(w)
 expect(w.text()).toContain('新的保存失败');expect(w.text()).not.toContain('本轮节点下发已成功');expect(w.text()).toContain('最近一次返回的下发记录')
})

it('separates an installed Vision marker from missing WebSocket/HTTPUpgrade repair',async()=>{
 preflight={valid:false,errors:['WebSocket/HTTPUpgrade 缓冲修复能力未确认'],scope_note:'只读取已安装内核记录，实际下发另验运行进程',nodes:[{...node(1,'WS landing'),version:'1.14.2+qz-vmess.9b95ab8c9478',has_vision_framing_fix:true,vision_required:false,transport_required:true,has_transport_read_buffer_fix:false,requires_reinstall:true,reasons:['请安装 1.14.2+qz-vmess.9b95ab8c9478-transport.07512b10；旧 Vision 专用修复内核不包含这项修复']}]}
 const w=await render();await inspect(w)
 expect(w.text()).toContain('WebSocket/HTTPUpgrade 修复标记：未检测到支持');expect(w.text()).toContain('需要重装内核');expect(w.text()).not.toContain('Vision 修复标记：')
 expect(w.text()).toContain('运行能力仍以下发时核验为准');expect(button(w,'确认保存并下发').attributes('disabled')).toBeDefined();expect(apiPut).not.toHaveBeenCalled();expect(apiPost).not.toHaveBeenCalled()
})
it('reports transport capability as unconfirmed after failed installed probing even with a saved marker',async()=>{
 preflight={valid:false,errors:['WebSocket/HTTPUpgrade 检测失败'],scope_note:'只读',nodes:[{...node(),transport_required:true,has_transport_read_buffer_fix:true,error:'probe offline',requires_check:true,reasons:['probe offline']}]}
 const w=await render();await inspect(w);expect(w.text()).toContain('WebSocket/HTTPUpgrade 修复标记：待确认');expect(w.text()).toContain('probe offline');expect(w.text()).not.toContain('运行已就绪')
})
