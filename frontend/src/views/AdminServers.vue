<template>
  <div>
    <h2 class="page-title">服务器管理</h2>
    <div class="page-toolbar">
      <span class="spacer" />
      <n-button type="primary" @click="openForm()">添加服务器</n-button>
    </div>
    <!-- 各节点实际在跑的 sing-box。数据来自面板本来就在做的能力探测，
         以前只取了「有没有 v2ray_api」，版本号被丢掉了。 -->
    <n-card title="节点 sing-box 版本" size="small" style="margin-bottom:16px;">
      <template #header-extra>
        <n-space size="small">
          <n-button size="tiny" :loading="verLoading" @click="refreshVersions">重新检测</n-button>
        </n-space>
      </template>
      <p class="nv-note">
        一键脚本装完之后，版本号只印在当时的终端里。这里长期显示每台机器实际在跑的版本 ——
        数据来自面板每轮下发配置时本来就会做的探测，不额外连一次机器。
        面板生成的配置要求 <b>sing-box ≥ {{ minSupported }}</b>；低于这个版本，节点的
        <code>sing-box check</code> 会失败，面板会<b>停止向它下发任何配置</b>（旧配置继续跑，所以表面看不出来）。
        「重装」装的是<b>面板自己发布的构建</b>（随面板版本走，含流量统计所需的 <code>v2ray_api</code>），
        不是 sing-box 官方发布版 —— 官方版不带这个插件，装上去流量就统计不到了。
      </p>
      <div v-if="versions.length" class="nv-list">
        <div v-for="n in versions" :key="n.server_id" class="nv-row">
          <div class="nv-main">
            <span class="nv-name">{{ n.name }}<span v-if="n.host" class="nv-host">{{ n.host }}</span></span>
            <span class="nv-ver">{{ n.version || '—' }}</span>
            <n-tag v-if="n.too_old" type="error" size="tiny" bordered="false">版本过低</n-tag>
            <n-tag v-else-if="!n.version" type="default" size="tiny" bordered="false">未知</n-tag>
            <n-tag v-if="n.version && !n.has_v2ray_api" type="warning" size="tiny" bordered="false">无流量统计</n-tag>
          </div>
          <div class="nv-side">
            <span v-if="n.checked_at" class="nv-time">{{ fmtDateTime(n.checked_at) }}</span>
            <n-button size="tiny" :disabled="!n.upgradable || n.upgrading" :loading="n.upgrading"
                      @click="confirmUpgrade(n)">
              {{ n.upgrading ? '安装中' : (n.version ? '重装' : '安装') }}
            </n-button>
          </div>
          <!-- 安装是后台跑的（要往节点上拉 ~60MB 的二进制），这里显示脚本的输出。 -->
          <div v-if="n.upgrading" class="nv-err" style="color:var(--text-3);">
            正在安装…（往节点下载 sing-box 约 60MB，通常 1–3 分钟；离开本页不影响安装）
          </div>
          <div v-else-if="n.upgrade_error" class="nv-err">
            安装失败：<pre class="nv-out">{{ n.upgrade_error }}</pre>
          </div>
          <div v-if="n.error" class="nv-err">探测失败：{{ n.error }}</div>
          <div v-else-if="n.version && !n.has_v2ray_api" class="nv-err">
            该版本不含 <code>v2ray_api</code> 插件 —— 这台机器的流量<b>统计不到</b>，配额也不会生效（界面上一直显示 0）。
            点「重装」换成面板发布的版本即可；官方发布版不带这个插件。
          </div>
        </div>
      </div>
      <n-empty v-else-if="!verLoading" description="暂无数据，点「重新检测」" style="padding:20px 0;" />
    </n-card>

    <n-spin :show="loading">
      <div v-if="servers.length" class="card-grid">
        <div v-for="s in servers" :key="s.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ s.name || '—' }}</span>
            <n-tag :type="s.enabled ? 'success' : 'default'" size="small" bordered="false">{{ s.enabled ? '启用' : '禁用' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">主机 <b>{{ s.host }}</b></span>
            <span class="kv">端口 <b>{{ s.port }}</b></span>
            <span class="kv">探针 <n-tag :type="s.probe_enabled ? 'info' : 'default'" size="tiny" bordered="false">{{ s.probe_enabled ? '开' : '关' }}</n-tag></span>
          </div>
          <div class="lc-foot">
            <n-button size="tiny" @click="handleRebuild(s.id)">重建</n-button>
            <n-button size="tiny" @click="openForm(s)">编辑</n-button>
            <n-button size="tiny" :loading="clearing === s.id" @click="handleClearHostKey(s)">清除密钥指纹</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(s.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无服务器" style="padding:40px 0;" />
    </n-spin>

    <n-modal v-model:show="showForm" preset="card" :title="editing?'编辑服务器':'添加服务器'" style="max-width:560px;">
      <n-form label-placement="left" label-width="100">
        <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
        <n-form-item label="主机"><n-input v-model:value="form.host" placeholder="IP 或域名" /></n-form-item>
        <n-form-item label="SSH 端口"><n-input-number v-model:value="form.port" :min="1" :max="65535" style="width:100%;" /></n-form-item>
        <n-form-item label="SSH 用户"><n-input v-model:value="form.ssh_user" placeholder="root" /></n-form-item>
        <n-form-item label="SSH 密码"><n-input v-model:value="form.ssh_password" type="password" show-password-on="click" placeholder="与密钥二选一" /></n-form-item>
        <n-form-item label="SSH 私钥"><n-input v-model:value="form.ssh_key" type="textarea" :rows="3" placeholder="-----BEGIN ... PRIVATE KEY-----" /></n-form-item>
        <n-form-item label="密钥密码"><n-input v-model:value="form.ssh_key_pass" type="password" show-password-on="click" /></n-form-item>
        <n-form-item label="配置路径"><n-input v-model:value="form.config_path" placeholder="/etc/sing-box/config.json" /></n-form-item>
        <n-form-item label="systemd 单元"><n-input v-model:value="form.systemd_unit" placeholder="sing-box" /></n-form-item>
        <n-form-item label="sing-box 路径"><n-input v-model:value="form.sing_box_bin" placeholder="/usr/local/bin/sing-box" /></n-form-item>
        <n-form-item label="v2ray 监听"><n-input v-model:value="form.v2ray_listen" placeholder="127.0.0.1:18080" /></n-form-item>
        <n-form-item label="启用"><n-switch v-model:value="form.enabled" /></n-form-item>
      </n-form>
      <n-space>
        <n-button type="primary" :loading="saving" @click="handleSave">保存</n-button>
        <n-button @click="handleTest" :loading="testing">测试连接</n-button>
      </n-space>
    </n-modal>
  </div>
</template>
<script setup lang="ts">
import { ref, reactive, onMounted, h } from 'vue'
import { NSpin, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSpace, NTag, NEmpty, NCard, useMessage, useDialog } from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete, apiGet } from '@/api'
import { fmtDateTime } from '@/utils/format'
const message = useMessage()
const dialog = useDialog()
const servers = ref<any[]>([])
const loading = ref(false); const saving = ref(false); const testing = ref(false)
const showForm = ref(false); const editing = ref<any>(null)
const mk = () => ({ name:'', host:'', port:22, ssh_user:'root', ssh_password:'', ssh_key:'', ssh_key_pass:'', config_path:'/etc/sing-box/config.json', systemd_unit:'sing-box', sing_box_bin:'/usr/local/bin/sing-box', v2ray_listen:'127.0.0.1:18080', enabled:true })
const form = reactive(mk())
function openForm(s?:any){
  editing.value = s||null
  if(s){ Object.assign(form,{ name:s.name, host:s.host, port:s.port||22, ssh_user:s.ssh_user||'root', ssh_password:'', ssh_key:'', ssh_key_pass:'', config_path:s.config_path||'/etc/sing-box/config.json', systemd_unit:s.systemd_unit||'sing-box', sing_box_bin:s.sing_box_bin||'/usr/local/bin/sing-box', v2ray_listen:s.v2ray_listen||'127.0.0.1:18080', enabled:s.enabled??true }) }
  else { Object.assign(form, mk()) }
  showForm.value = true
}
async function handleSave(){
  saving.value = true
  try{
    const b:any = {...form}
    if(!b.ssh_password) delete b.ssh_password
    if(!b.ssh_key) delete b.ssh_key
    if(!b.ssh_key_pass) delete b.ssh_key_pass
    if(editing.value) await apiPut(`/api/admin/servers/${editing.value.id}`,b)
    else await apiPost('/api/admin/servers',b)
    message.success('保存成功'); showForm.value=false; editing.value=null; await load()
  }catch(e:any){message.error(e.message)}finally{saving.value=false}
}
async function handleTest(){ testing.value=true; try{ const id=editing.value?.id; if(!id){message.warning('请先保存');return} await apiPost(`/api/admin/servers/${id}/test`); message.success('连接成功') }catch(e:any){message.error(e.message)}finally{testing.value=false} }
async function handleRebuild(id:number){ try{await apiPost(`/api/admin/servers/${id}/rebuild`);message.success('重建成功')}catch(e:any){message.error(e.message)} }

// 清除固定的 SSH 主机密钥并重新信任。
// 面板首次连上一台机器时会记住它的主机密钥，此后每次连接都要求对得上——这是拦住
// 「有人冒充这个 IP 骗走 root 凭据」的那道门。但机器重装系统会重新生成主机密钥，
// 换机复用同一条记录也一样，那时面板就会一直拒连（表现为配置下发失败），而这道门
// 只能从这里打开：host_key 不下发给前端，编辑服务器也不会动它。
// 所以按钮必须带确认：清除等于放弃对这台机器身份的既有判断。
const clearing = ref<number|null>(null)
// 弹窗正文用 pre-line 渲染：naive 的 dialog 把 content 当普通文本塞进一个 div，
// 字符串里的换行会被折掉——这段正好是要分行看的安全提示，挤成一坨就没人读了。
const preLine = (text:string) => () => h('div', { style: 'white-space:pre-line;line-height:1.75;' }, text)
function handleClearHostKey(s:any){
  dialog.warning({
    title: '清除已固定的主机密钥？',
    content: preLine(`面板将忘记「${s.name || s.host}」当前记住的 SSH 主机密钥，并按下一次连接看到的密钥重新记住。\n\n`
      + '✅ 机器重装过系统 / 换了机器 / 供应商做了迁移 → 这是正常操作。\n\n'
      + '⛔ 你想不出它为什么会变 → 先别清。那可能是中间人在冒充这台机器，清掉就等于把 root 凭据交给它。'),
    positiveText: '确认清除并重新连接',
    negativeText: '取消',
    onPositiveClick: async () => {
      clearing.value = s.id
      try{
        const r:any = await apiPost(`/api/admin/servers/${s.id}/clear-host-key`)
        message.success(r?.message || '已重新信任')
        if(r?.fingerprint){
          dialog.success({
            title: '已重新记住这台机器',
            content: preLine(`新指纹：${r.fingerprint}\n\n`
              + '请在这台机器上执行 ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub 核对，对得上说明连的确实是你的机器。\n\n'
              + '接着到「sing-box → 链路拓扑」点「重新下发」，把这段时间没同步上的配置和用户变更推过去。'),
            positiveText: '知道了',
          })
        }
        await load()
      }catch(e:any){ message.error(e.message) }finally{ clearing.value = null }
    },
  })
}
async function handleDelete(id:number){ try{await apiDelete(`/api/admin/servers/${id}`);message.success('已删除');await load()}catch(e:any){message.error(e.message)} }
async function load(){loading.value=true;try{servers.value=await apiList('/api/admin/servers')}catch{}finally{loading.value=false}}

// ---- 节点 sing-box 版本 ----
const versions = ref<any[]>([])
const minSupported = ref('1.12.0')
const verLoading = ref(false)

async function loadVersions(){
  try{
    const d = await apiGet<any>('/api/admin/nodes/singbox')
    versions.value = d?.nodes || []
    if (d?.min_supported) minSupported.value = d.min_supported
  }catch(e:any){ message.error(e?.message || '读取节点版本失败') }
}

async function refreshVersions(){
  verLoading.value = true
  try{
    await apiPost('/api/admin/nodes/singbox/refresh')
    // 探测是后台跑的（不可达的机器要等 SSH 超时），稍等再读一次结果。
    await new Promise(r => setTimeout(r, 2500))
    await loadVersions()
  }catch(e:any){ message.error(e?.message || '检测失败') }
  finally{ verLoading.value = false }
}

function confirmUpgrade(n:any){
  dialog.warning({
    title: '确认重装 sing-box',
    content: `将在「${n.name}」上安装面板发布的 sing-box（随面板版本走）并重启服务。`
      + '安装期间这台机器上的用户会断线，客户端会自动重连。继续？',
    positiveText: '开始安装',
    negativeText: '取消',
    onPositiveClick: () => { doUpgrade(n) },
  })
}

// 安装在后端后台跑：往节点拉 ~60MB 的二进制，同步等会被服务端 WriteTimeout 掐断，
// 于是装成功了前端却报错。这里只负责发起 + 轮询节点列表里的任务状态。
async function doUpgrade(n:any){
  try{
    await apiPost(`/api/admin/nodes/${n.server_id}/singbox/upgrade`)
  }catch(e:any){ message.error(e?.message || '安装启动失败', { duration: 10000 }); return }
  message.info('已开始安装，完成后本页会自动刷新')
  await loadVersions()
  pollUpgrade(n.server_id)
}

// 轮询到该节点的任务结束为止。设上限，免得后端异常时无限轮询。
async function pollUpgrade(id:number){
  for (let i = 0; i < 120; i++){
    await new Promise(r => setTimeout(r, 5000))
    await loadVersions()
    const row = versions.value.find(v => v.server_id === id)
    if (!row || row.upgrading) continue
    if (row.upgrade_error) message.error('安装失败，详情见下方输出', { duration: 10000 })
    else message.success(`安装完成${row.version ? '：' + row.version : ''}`)
    return
  }
}

onMounted(() => { load(); loadVersions() })
</script>

<style scoped>
.nv-note { font-size:12px; color:var(--text-3); line-height:1.75; margin:0 0 12px; }
.nv-list { display:flex; flex-direction:column; gap:2px; }
.nv-row {
  display:flex; flex-wrap:wrap; align-items:center; gap:8px;
  padding:8px 0; border-bottom:1px solid var(--border);
}
.nv-row:last-child { border-bottom:0; }
.nv-main { display:flex; align-items:center; gap:8px; flex:1; min-width:0; flex-wrap:wrap; }
.nv-name { font-weight:600; font-size:13px; }
.nv-host { color:var(--text-3); font-weight:400; font-size:12px; margin-left:6px; }
.nv-ver { font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:13px; }
.nv-side { display:flex; align-items:center; gap:10px; }
.nv-time { font-size:11px; color:var(--text-3); }
.nv-err { flex-basis:100%; font-size:11px; line-height:1.7; color:var(--warning,#d97706); }
/* 脚本输出可能很长，限高 + 可滚动，免得一次失败把整页撑开。 */
.nv-out {
  margin:4px 0 0; padding:8px 10px; max-height:220px; overflow:auto;
  font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:11px; line-height:1.6;
  white-space:pre-wrap; word-break:break-all;
  background:var(--code-bg,rgba(128,128,128,.1)); border-radius:6px; color:var(--text-2);
}
</style>
