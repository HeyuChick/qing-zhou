import { expect, it, vi } from 'vitest'
import { applyInboundToggle } from '@/utils/inboundBatch'
it('reports every failed inbound without changing it or masking partial success',async()=>{
 const targets=[{id:1,tag:'入口 A',enabled:false},{id:2,tag:'落地 B',enabled:false},{id:3,tag:'入口 C',enabled:false},{id:4,tag:'已启用',enabled:true}]
 const save=vi.fn(async n=>{if(n.id===1)throw new Error('会产生同机多跳');if(n.id===3)throw new Error('落地已禁用')})
 const result=await applyInboundToggle(targets,true,save)
 expect(result).toEqual({updated:1,unchanged:1,failures:[{id:1,name:'入口 A',error:'会产生同机多跳'},{id:3,name:'入口 C',error:'落地已禁用'}]})
 expect(targets.map(n=>n.enabled)).toEqual([false,true,false,true]);expect(save).toHaveBeenCalledTimes(3)
})
it('never counts a failed batch as success and can safely retry unchanged targets',async()=>{
 const target={id:1,tag:'entry',enabled:true};const fail=vi.fn().mockRejectedValue(new Error('拓扑包含环路'))
 expect(await applyInboundToggle([target],false,fail)).toEqual({updated:0,unchanged:0,failures:[{id:1,name:'entry',error:'拓扑包含环路'}]});expect(target.enabled).toBe(true)
 expect(await applyInboundToggle([target],false,async()=>{})).toEqual({updated:1,unchanged:0,failures:[]});expect(target.enabled).toBe(false)
})
