<template>
  <section class="relay-metering" aria-label="中转链路计量">
    <details>
      <summary>中转链路计量 <span>{{ state.enabled ? '已启用' : '未启用' }}</span></summary>
      <p>先让落地接受新旧身份，再切换入口。机器业务量与用户套餐分别记录。启用逐用户计量后，每台机器独立记录同一用户的实测流量，套餐仍只在入口扣一次</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <form @submit.prevent="requestConfirmation">
        <label><input v-model="enabled" type="checkbox" :disabled="busy || loading || !loaded">启用每条入口到落地的独立中转身份</label>
        <label><input v-model="cumulative" type="checkbox" :disabled="busy || loading || !loaded || state.cumulative_started">启用累计快照采集，避免每次读取清零</label>
        <small>累计模式要求能够验证sing-box进程代次；不支持的节点保留旧式采集。已经切换的节点不能直接退回清零模式，避免重复扣费</small>
        <label><input v-model="perUser" type="checkbox" :disabled="busy || loading || !loaded || !enabled">启用逐用户中转实测（需先启用独立中转身份）</label>
        <small>用户现有账号、密码和订阅不变。支持 VLESS、VMess、Trojan、TUIC、Hysteria/Hysteria2、AnyTLS 和 SS2022 AES-128/256；mixed 仅作入口。各协议和传输的实测范围以该版本验收记录为准。内部逐用户身份会增加配置规模；共享兼容与历史记录仍单列，未归属流量不会均摊给用户</small>
        <div class="actions"><button type="submit" :disabled="busy || loading || !loaded">保存并重新下发</button><button type="button" :disabled="busy || loading" @click="load">刷新状态</button></div>
      </form>
      <div v-if="confirming" class="confirmation" role="alert">
        <p>启用链路计量会生成并加密保存内部中转凭据，仅下发到相关节点。配置变化会重启sing-box，可能中断现有连接。旧共享凭据继续兼容，不会立即撤销</p>
        <p v-if="perUser">逐用户中转将按用户和线路生成内部身份，并先准备落地再切换入口。所有机器分别实测，不复制入口用量；套餐不在落地重复扣除</p>
        <p v-else-if="state.per_user_enabled">关闭逐用户中转后，新下发路径将退回共享链路计量；已有逐用户历史记录保留</p>
        <p>累计采集会执行一次旧计数清零交接，随后持续读取累计值；进程崩溃前未采到的尾部仍可能缺失</p>
        <div class="actions"><button :disabled="busy" @click="save">确认变更</button><button :disabled="busy" @click="confirming=false">取消</button></div>
      </div>
      <ul v-if="state.links.length" class="links">
        <li v-for="link in state.links" :key="link.id"><span>{{ link.source_name }} → {{ link.target_name }}</span><b>{{ phaseLabel(link.state) }}</b></li>
      </ul>
      <p v-else-if="!loading && !error">尚无独立链路身份；已采集的旧共享中转仍会在机器来源中单独显示</p>
      <div v-if="state.credentials?.length" class="credentials">
        <h4>兼容凭据生命周期</h4><p>系统只核验受管入口与两次成功采集；手工配置的中转也必须由管理员确认完成迁移。停用后需先恢复并确认旧共享凭据，才能关闭链路计量</p>
        <div v-for="item in state.credentials" :key="`${item.kind}:${item.link_id}:${item.inbound_id}:${item.generation}`" class="credential-row">
          <div><b>{{ item.name }}</b> · {{ credentialPhase(item.state) }}<small v-if="item.reason">{{ item.reason }}</small></div>
          <button v-if="item.state==='active'" :disabled="busy || !item.can_retire" @click="credentialAction={credential:item,action:'retire'}">停用旧凭据</button>
          <button v-if="item.state==='retired'||item.state==='retiring'" :disabled="busy" @click="credentialAction={credential:item,action:'restore'}">恢复兼容</button>
        </div>
      </div>
      <div v-if="credentialAction" class="confirmation" role="alert">
        <p>目标：{{ credentialAction.credential.name }}（服务器 #{{ credentialAction.credential.server_id }}，入站 #{{ credentialAction.credential.inbound_id }}）</p>
        <p v-if="credentialAction.action==='retire'">确认所有手工中转也已迁移？停用将使仍使用该旧凭据的连接失效；配置变更可能中断当前连接</p>
        <p v-else>恢复会重新允许该旧凭据在目标入站认证；配置变更可能中断当前连接</p>
        <div class="actions"><button :disabled="busy" @click="changeCredential">确认凭据变更</button><button :disabled="busy" @click="credentialAction=null">取消</button></div>
      </div>
      <p v-if="syncFailure" class="error" role="status">{{ syncFailure }}</p>
      <p v-if="saved" role="status">设置已保存，请刷新确认各链路进入“入口已切换”。未满足下游准备条件的入口不会切换；其他下发失败请按节点错误恢复</p>
    </details>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { apiGet, apiPut, apiPost } from '@/api'
interface Credential {kind:string;link_id:number;server_id:number;inbound_id:number;generation:number;state:string;name:string;can_retire:boolean;reason:string}
interface Link {id:number;source_name:string;target_name:string;state:string}
interface State {enabled:boolean;per_user_enabled:boolean;cumulative_enabled:boolean;cumulative_started:boolean;links:Link[];credentials?:Credential[];sync?:Record<string,{state:string;error:string}>}
const state=ref<State>({enabled:false,per_user_enabled:false,cumulative_enabled:false,cumulative_started:false,links:[]})
const enabled=ref(false),cumulative=ref(false),perUser=ref(false),loaded=ref(false),busy=ref(false),loading=ref(false),confirming=ref(false),saved=ref(false),error=ref('')
const credentialAction=ref<{credential:Credential;action:'retire'|'restore'}|null>(null)
const syncFailure=computed(()=>Object.values(state.value.sync||{}).find(s=>s.state==='failed')?.error||'')
function phaseLabel(phase:string){return ({prepared:'等待落地接受',accepted:'落地已接受，等待入口切换',active:'入口已切换'} as Record<string,string>)[phase]||phase}
async function load(){if(loading.value)return;loading.value=true;loaded.value=false;confirming.value=false;error.value='';try{const r:any=await apiGet('/api/admin/relay-metering');state.value={enabled:!!r.enabled,per_user_enabled:!!r.per_user_enabled,cumulative_enabled:!!r.cumulative_enabled,cumulative_started:!!r.cumulative_started,links:r.links||[],credentials:r.credentials||[],sync:r.sync};enabled.value=state.value.enabled;cumulative.value=state.value.cumulative_enabled||state.value.cumulative_started;perUser.value=state.value.per_user_enabled;loaded.value=true}catch(e:any){error.value=e.message||'读取链路计量失败'}finally{loading.value=false}}
function requestConfirmation(){if(!busy.value && !loading.value && loaded.value)confirming.value=true}
watch(enabled,value=>{if(!value)perUser.value=false})
async function save(){if(busy.value || !loaded.value)return;busy.value=true;error.value='';try{await apiPut('/api/admin/relay-metering',{enabled:enabled.value,cumulative_enabled:cumulative.value,per_user_enabled:perUser.value,confirm:true});confirming.value=false;saved.value=true;await load()}catch(e:any){error.value=e.message||'保存失败'}finally{busy.value=false}}
function credentialPhase(phase:string){return ({current:'当前代',active:'旧凭据兼容中',retiring:'等待停用下发',retired:'已确认停用',restoring:'等待恢复确认'} as Record<string,string>)[phase]||phase}
async function changeCredential(){if(!credentialAction.value || busy.value)return;busy.value=true;error.value='';try{const action=credentialAction.value;await apiPost('/api/admin/relay-metering/credentials',{...action.credential,action:action.action,confirm:true});credentialAction.value=null;saved.value=true;await load()}catch(e:any){error.value=e.message||'凭据变更失败'}finally{busy.value=false}}
onMounted(load)
</script>
<style scoped>
.relay-metering{border:1px solid var(--border);border-radius:12px;padding:14px 16px;margin-bottom:16px;background:var(--card);font-size:12px}.relay-metering summary{cursor:pointer;font-size:14px;font-weight:600}.relay-metering summary span{margin-left:12px;color:var(--text-3);font-size:12px}.relay-metering p,.relay-metering small{line-height:1.8;color:var(--text-3)}.relay-metering label{display:flex;gap:8px;align-items:center;margin:12px 0;color:var(--text)}.actions{display:flex;gap:10px;margin-top:12px}.actions button{border:1px solid var(--border);background:var(--bg-soft);color:var(--text);border-radius:6px;padding:6px 12px;cursor:pointer}.actions button:disabled{opacity:.5;cursor:default}.confirmation{padding:10px 14px;margin-top:12px;border:1px solid var(--warning,#b6813e);border-radius:8px}.links{padding:0;list-style:none}.links li{display:flex;justify-content:space-between;gap:12px;padding:9px 0;border-bottom:1px solid var(--border)}.links b{font-size:11px;white-space:nowrap}.relay-metering .error{color:var(--danger,#b45151)}
.credential-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 0;border-bottom:1px solid var(--border)}.credential-row small{display:block}.credential-row button{white-space:nowrap;border:1px solid var(--border);background:var(--bg-soft);color:var(--text);border-radius:6px;padding:5px 9px}.credential-row button:disabled{opacity:.5}
</style>
