import { expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AdminServiceTraffic from '@/components/AdminServiceTraffic.vue'
const service = {total:1500,billable_total:500,new_coverage_start:1,user_coverage_complete:false,sources:[{kind:'relay_link',link_id:1,user_id:0,name:'DMIT → Verizon',up:400,down:600,total:1000},{kind:'unknown',link_id:0,user_id:0,name:'未知代理身份',up:200,down:300,total:500}],quality:{mode:'reset',status:'ok',gaps:0,pending_polls:0}}
it('separates machine service, billing and relay observations without claiming exact NIC reconciliation',()=>{
 const w=mount(AdminServiceTraffic,{props:{service}})
 expect(w.text()).toContain('本机代理业务来源');expect(w.text()).toContain('DMIT → Verizon');expect(w.text()).toContain('仅观测，不重复扣费');expect(w.text()).toContain('暂停按人数估算容量');expect(w.text()).toContain('入口扣一次');w.unmount()
})
it('shows pending and missing reads instead of presenting them as zero',()=>{
 const w=mount(AdminServiceTraffic,{props:{service:{...service,sources:[],quality:{mode:'reset',status:'unavailable',gaps:2,pending_polls:3}}}})
 expect(w.text()).toContain('采集失败');expect(w.text()).toContain('3 个采集批次');expect(w.text()).toContain('2 个统计边界');expect(w.text()).toContain('不能据此判断没有使用');w.unmount()
})
it('paginates complete source data without changing the overall total',async()=>{
 const sources=Array.from({length:12},(_,i)=>({...service.sources[0]!,link_id:i+1,name:`link ${i+1}`}))
 const w=mount(AdminServiceTraffic,{props:{service:{...service,sources}}});expect(w.findAll('li')).toHaveLength(10)
 await w.findAll('button').find(b=>b.text()==='下一页')!.trigger('click');expect(w.findAll('li')).toHaveLength(2);expect(w.text()).toContain('link 12');expect(w.text()).toContain('1.46 KB')
 await w.setProps({service});expect(w.findAll('li')).toHaveLength(2);w.unmount()
})
it('shows one measured total per user while retaining unallocated shared observations separately',()=>{
 const sources=[
  {kind:'direct_user',link_id:0,user_id:7,name:'Alice',up:10,down:20,total:30,billable_total:30},
  {kind:'relay_user',link_id:11,user_id:7,name:'Alice',up:20,down:30,total:50,billable_total:0},
  {kind:'relay_user',link_id:12,user_id:7,name:'Alice',up:30,down:40,total:70,billable_total:0},
  {kind:'relay_link',link_id:13,user_id:0,name:'shared route',up:10,down:40,total:50,billable_total:0},
 ]
 const users=[{user_id:7,name:'Alice',up:60,down:90,total:150,direct_total:30,relay_total:120,billable_total:30}]
 const w=mount(AdminServiceTraffic,{props:{service:{...service,total:200,billable_total:30,sources,users,unallocated_total:50,outbound_links:[{...sources[3]!,kind:'diagnostic_outbound',total:900}]}}})
 expect(w.findAll('.user-sources li')).toHaveLength(1)
 expect(w.find('.user-sources').text()).toContain('Alice用户 #7150 B')
 expect(w.find('.user-sources').text()).toContain('直连 30 B · 中转 120 B · 本机入口套餐扣量 30 B')
 expect(w.findAll('.unallocated-sources li')).toHaveLength(1)
 expect(w.find('.unallocated-sources').text()).toContain('shared route')
 expect(w.find('.service-totals').text()).toContain('代理业务 200 B')
 expect(w.find('details').text()).toContain('不重复计入上方合计')
 expect(w.find('.service-totals').text()).not.toContain('900 B');w.unmount()
})
it('can aggregate older source-only API responses without inferring missing quota debits',()=>{
 const sources=[{kind:'direct_user',link_id:0,user_id:7,name:'Alice',up:1,down:2,total:3},{kind:'relay_user',link_id:11,user_id:7,name:'Alice',up:4,down:5,total:9}]
 const w=mount(AdminServiceTraffic,{props:{service:{...service,total:12,sources}}})
 expect(w.findAll('.user-sources li')).toHaveLength(1)
 expect(w.find('.user-sources').text()).toContain('12 B')
 expect(w.find('.user-sources').text()).toContain('本机入口套餐扣量 —')
 expect(w.find('.unallocated-sources').exists()).toBe(false);w.unmount()
})
it('does not call potential shared compatibility observed loss',()=>{
 const w=mount(AdminServiceTraffic,{props:{service:{...service,sources:[],users:[],observed_user_coverage_complete:true,attribution_ready:false,coverage_reasons:['shared_compatibility_active'],unallocated_total:0}}})
 expect(w.text()).toContain('本观察区间的已记录流量均已归属用户')
 expect(w.text()).toContain('不代表已丢失流量')
 expect(w.text()).toContain('共享兼容身份尚未确认撤除')
 expect(w.find('.unallocated-sources').exists()).toBe(false);w.unmount()
})
it('paginates users independently and resets pages on a newer machine response',async()=>{
 const users=Array.from({length:12},(_,i)=>({user_id:i+1,name:`user ${i+1}`,up:10,down:20,total:30,direct_total:0,relay_total:30,billable_total:0}))
 const w=mount(AdminServiceTraffic,{props:{service:{...service,users,sources:[]}}})
 expect(w.findAll('.user-sources li')).toHaveLength(10)
 await w.find('[aria-label="用户分页"]').findAll('button')[1]!.trigger('click')
 expect(w.findAll('.user-sources li')).toHaveLength(2)
 expect(w.text()).toContain('user 12')
 await w.setProps({service:{...service,users:users.slice(0,1),sources:[]}})
 expect(w.findAll('.user-sources li')).toHaveLength(1)
 expect(w.text()).toContain('user 1');expect(w.text()).not.toContain('user 12');w.unmount()
})
