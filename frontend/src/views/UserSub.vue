<template>
  <div>
    <!-- 页面头 -->
    <div class="sub-head">
      <div>
        <h2 class="page-title">订阅管理</h2>
        <p class="page-sub">订阅链接、导入格式与节点开关，都在这里管理</p>
      </div>
      <n-space size="small">
        <n-button size="small" secondary @click="router.push('/dashboard')">
          <template #icon><n-icon><SpeedometerOutline /></n-icon></template>
          控制台
        </n-button>
        <n-button size="small" secondary @click="router.push('/orders')">订单记录</n-button>
        <n-button size="small" type="primary" @click="router.push('/shop')">去商城</n-button>
      </n-space>
    </div>

    <!-- 订阅链接 -->
    <n-card size="small" class="sec">
      <template #header><span class="sec-title">订阅链接</span></template>
      <n-input-group>
        <n-input :value="sub.url" readonly placeholder="暂无订阅" />
        <n-button type="primary" @click="copy(sub.url)">复制</n-button>
      </n-input-group>
      <div style="margin-top:12px;display:flex;gap:8px;flex-wrap:wrap;">
        <n-button size="small" @click="copy(sub.formats?.clash)">Clash</n-button>
        <n-button size="small" @click="copy(sub.formats?.singbox)">sing-box</n-button>
        <n-button size="small" @click="copy(sub.formats?.surge)">Surge</n-button>
        <!-- formats.base64, not formats.default: default has no ?format= and so
             picks its output from the client's User-Agent, which silently hands
             YAML to anything whose UA contains "clash". This button is for
             v2rayN / NekoBox / Shadowrocket, so it must pin the link list. -->
        <n-button size="small" @click="copy(sub.formats?.base64 || sub.formats?.default)">通用 / v2rayN</n-button>
        <n-button size="small" @click="showQr=!showQr">{{ showQr?'隐藏':'显示' }}二维码</n-button>
        <!-- 两个按钮代价完全不同，分开呈现：换地址是纯面板操作、立即生效、不影响
             任何人；换凭据要同步到每个节点才生效，因此默认禁用 + 30 天冷却。 -->
        <n-button size="small" type="warning" @click="handleResetSub">更换订阅地址</n-button>
        <!-- 禁用与否跟随后端开关，不写死：后端本来就要校验 node_creds_reset_enabled，
             按钮读同一个值才不会出现「管理员开了但按钮还是灰的」。 -->
        <n-tooltip trigger="hover" :disabled="credsResetEnabled">
          <template #trigger>
            <n-button size="small" type="error" :disabled="!credsResetEnabled" :loading="resettingCreds"
                      @click="handleResetNodeCreds">重置节点凭据</n-button>
          </template>
          该功能暂时禁用，有需要请联系管理员
        </n-tooltip>
      </div>
      <div style="margin-top:8px;font-size:11px;color:var(--text-3);line-height:1.6;">
        订阅地址泄露时用「更换订阅地址」：旧地址立即失效，无需重启节点。
        注意它不会使已经导出的节点失效——那需要「重置节点凭据」。
      </div>
      <div v-if="showQr && sub.url" style="margin-top:12px;text-align:center;">
        <canvas ref="qrCanvas" />
        <div style="font-size:11px;color:var(--text-3);margin-top:4px;">手机扫描导入订阅</div>
      </div>
    </n-card>

    <!-- HTTP/SOCKS5 代理（mixed 节点，不在订阅里，单独复制填入 1Panel/Docker 等） -->
    <n-card v-if="proxies.length" size="small" class="sec" title="HTTP / SOCKS5 代理">
      <template #header-extra><span style="font-size:11px;color:var(--text-3);">可填入 1Panel、Docker、git 等只认 HTTP/SOCKS 代理的地方</span></template>
      <div v-for="p in proxies" :key="p.tag" class="proxy-row">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;gap:8px;">
          <span style="font-weight:600;">{{ p.tag }}</span>
          <div style="display:flex;align-items:center;gap:6px;">
            <n-tag v-if="p.expired" type="error" size="small" bordered>已过期</n-tag>
            <n-tag :type="p.tls?'success':'warning'" size="small" bordered>{{ p.tls ? 'HTTPS 代理' : 'HTTP / SOCKS5' }}</n-tag>
            <n-button size="tiny" @click="openEditProxy(p)">编辑账号</n-button>
          </div>
        </div>
        <div class="pxrow"><span class="pxk">类型</span><span style="font-size:13px;">{{ p.tls ? 'HTTPS' : 'HTTP / SOCKS5' }}</span></div>
        <div class="pxrow"><span class="pxk">地址</span><div class="pxv"><n-input-group><n-input :value="p.host" readonly size="small" /><n-button size="small" @click="copy(p.host)">复制</n-button></n-input-group></div></div>
        <div class="pxrow"><span class="pxk">端口</span><div class="pxv"><n-input-group><n-input :value="String(p.port)" readonly size="small" /><n-button size="small" @click="copy(String(p.port))">复制</n-button></n-input-group></div></div>
        <div class="pxrow"><span class="pxk">用户名</span><div class="pxv"><n-input-group><n-input :value="p.username" readonly size="small" /><n-button size="small" @click="copy(p.username)">复制</n-button></n-input-group></div></div>
        <div class="pxrow"><span class="pxk">密码</span><div class="pxv"><n-input-group><n-input :value="p.password" type="password" show-password-on="click" readonly size="small" /><n-button size="small" @click="copy(p.password)">复制</n-button></n-input-group></div></div>
        <div class="pxrow"><span class="pxk">有效期</span><span style="font-size:12px;color:var(--text-2);">{{ p.expires_at ? fmtDate(p.expires_at) : '永久' }}<span v-if="!p.custom" style="color:var(--text-3);"> · 系统默认账号，建议点「编辑账号」自设</span></span></div>
      </div>
      <div style="font-size:11px;color:var(--text-3);">在 1Panel「代理服务器」里：代理类型选 <b>HTTP</b> 或 <b>SOCKS5</b>（显示「HTTPS 代理」则选 <b>HTTPS</b>），地址 / 端口 / 用户名 / 密码 按上面填。</div>
    </n-card>

    <!-- 编辑代理账号 -->
    <n-modal v-model:show="showEditProxy" preset="card" title="编辑代理账号" style="max-width:440px;">
      <n-form label-placement="left" label-width="72">
        <n-form-item label="节点"><n-input :value="editForm.tag" readonly /></n-form-item>
        <n-form-item label="用户名">
          <n-input v-model:value="editForm.username" placeholder="仅字母/数字/ _.@- ，不能以 qz_ 开头" />
        </n-form-item>
        <n-form-item label="密码">
          <n-input-group>
            <n-input v-model:value="editForm.password" type="password" show-password-on="click" placeholder="6-128 位" />
            <n-button @click="genProxyPassword">生成32位</n-button>
          </n-input-group>
        </n-form-item>
        <n-form-item label="有效期">
          <div style="width:100%;">
            <n-switch v-model:value="editForm.permanent" style="margin-bottom:8px;"><template #checked>永久</template><template #unchecked>指定日期</template></n-switch>
            <n-date-picker v-if="!editForm.permanent" v-model:value="editForm.expireTs" type="datetime" clearable style="width:100%;" />
          </div>
        </n-form-item>
        <div style="font-size:11px;color:var(--text-3);margin-bottom:12px;">这是仅用于该协议的独立账号，与登录账号无关。密码泄露可随时来此更改；到期后该代理自动停用（可续期）。</div>
        <n-button type="primary" block :loading="savingProxy" @click="saveProxy">保存</n-button>
      </n-form>
    </n-modal>

    <!-- 节点列表 -->
    <n-card size="small" class="sec" title="节点列表">
      <template #header-extra>
        <n-space size="small">
          <n-input v-model:value="search" placeholder="搜索节点" size="small" style="width:160px;" clearable />
          <n-select v-model:value="protoFilter" :options="protoOptions" placeholder="协议" size="small" style="width:100px;" clearable />
          <n-button size="small" @click="handlePing" :loading="pinging">测速</n-button>
          <n-button size="small" @click="handleToggleAll(true)">全启用</n-button>
          <n-button size="small" @click="handleToggleAll(false)">全禁用</n-button>
        </n-space>
      </template>
      <n-data-table :columns="nodeCols" :data="filteredNodes" :bordered="false" size="small" :loading="loadingNodes" :pagination="{pageSize:20}" :row-key="(r:any)=>r.key" v-model:checked-row-keys="selectedKeys" />
      <div v-if="selectedKeys.length" style="margin-top:10px;display:flex;gap:8px;align-items:center;">
        <span style="font-size:12px;color:var(--text-3);">已选 {{ selectedKeys.length }} 个</span>
        <n-button size="small" type="primary" @click="handleBulk(true)">批量启用</n-button>
        <n-button size="small" type="error" @click="handleBulk(false)">批量禁用</n-button>
      </div>
      <div style="margin-top:8px;font-size:11px;color:var(--text-3);">共 {{ nodes.length }} 个节点，{{ filteredNodes.length }} 个匹配，{{ nodes.filter((n:any)=>n.disabled).length }} 个禁用</div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, h, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NInput, NInputGroup, NButton, NDataTable, NTag, NTooltip, NSelect, NSpace, NModal, NForm, NFormItem, NSwitch, NDatePicker, NIcon, useMessage, useDialog } from 'naive-ui'
import { SpeedometerOutline } from '@vicons/ionicons5'
import { apiGet, apiList, apiPost, apiPut } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { fmtBytes, fmtDate } from '@/utils/format'
import { copyText } from '@/utils/clipboard'
import QRCode from 'qrcode'

const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuthStore()
const sub = ref<any>({})
const proxies = ref<any[]>([])
const nodes = ref<any[]>([])
// 由后端的 node_creds_reset_enabled 决定，缺省按关闭处理——拿不到就当没开，
// 不要给用户一个点了必然 403 的按钮。
const credsResetEnabled = computed(() => sub.value?.creds_reset_enabled === true)
const resettingCreds = ref(false)

// 代理账号编辑
const showEditProxy = ref(false)
const savingProxy = ref(false)
const editForm = ref<any>({ bucket_id: 0, tag: '', username: '', password: '', permanent: true, expireTs: null as number | null })

function openEditProxy(p: any) {
  editForm.value = {
    bucket_id: p.bucket_id,
    tag: p.tag,
    // 默认用登录账号名（仅作默认，是独立的代理账号）；已自设过则回填现有用户名。
    username: p.custom ? p.username : (auth.user?.username || ''),
    password: '',
    permanent: !p.expires_at,
    expireTs: p.expires_at ? p.expires_at * 1000 : null,
  }
  showEditProxy.value = true
}

function genProxyPassword() {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const arr = new Uint32Array(32)
  crypto.getRandomValues(arr)
  editForm.value.password = Array.from(arr, (n) => chars[n % chars.length]).join('')
}

async function saveProxy() {
  const f = editForm.value
  if (!f.username?.trim()) { message.warning('请填写用户名'); return }
  if (!f.password || f.password.length < 6) { message.warning('密码至少 6 位'); return }
  if (!f.permanent && !f.expireTs) { message.warning('请选择有效期，或切换为永久'); return }
  savingProxy.value = true
  try {
    await apiPut('/api/user/proxies/' + f.bucket_id, {
      username: f.username.trim(),
      password: f.password,
      expires_at: f.permanent ? 0 : Math.floor(f.expireTs / 1000),
    })
    message.success('已保存，代理账号已更新')
    showEditProxy.value = false
    proxies.value = await apiList('/api/user/proxies')
  } catch (e: any) { message.error(e.message) } finally { savingProxy.value = false }
}
const showQr = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const search = ref('')
const protoFilter = ref<string | null>(null)
const selectedKeys = ref<string[]>([])
const loadingNodes = ref(false)
const pinging = ref(false)

const protoOptions = computed(() => {
  const set = new Set(nodes.value.map((n: any) => n.protocol).filter(Boolean))
  return Array.from(set).map(p => ({ label: p.toUpperCase(), value: p }))
})

const filteredNodes = computed(() => {
  let list: any[] = nodes.value
  if (search.value) {
    const q = search.value.toLowerCase()
    list = list.filter((n: any) => n.name?.toLowerCase().includes(q) || n.server?.toLowerCase().includes(q))
  }
  if (protoFilter.value) list = list.filter((n: any) => n.protocol === protoFilter.value)
  return list
})

function latencyColor(ms: number) {
  if (ms < 0) return 'var(--text-3)'
  if (ms < 150) return '#10b981'
  if (ms < 400) return '#bf9540'
  return '#ef4444'
}

const nodeCols = [
  { type: 'selection' as const },
  { title: '节点', key: 'name', ellipsis: { tooltip: true } },
  { title: '协议', key: 'protocol', width: 70, render: (r: any) => r.protocol?.toUpperCase() || '—' },
  {
    title: '延迟', key: 'latency', width: 70,
    render: (r: any) => {
      if (r.latency == null) return h('span', { style: 'color:var(--text-3)' }, '—')
      if (r.latency < 0) return h(NTag, { size: 'tiny', type: 'error', bordered: false }, { default: () => '超时' })
      return h('span', { style: `color:${latencyColor(r.latency)};font-weight:600;` }, r.latency + 'ms')
    },
  },
  {
    title: '状态', key: 'disabled', width: 70,
    render: (r: any) => h(NTag, { type: r.disabled ? 'default' : 'success', size: 'small', bordered: false }, { default: () => r.disabled ? '禁用' : '启用' }),
  },
  {
    title: '操作', key: 'act', width: 70,
    render: (r: any) => h(NButton, { size: 'tiny', onClick: () => toggleNode(r) }, { default: () => r.disabled ? '启用' : '禁用' }),
  },
]

async function toggleNode(node: any) {
  try {
    const newDisabled = !node.disabled
    await apiPost('/api/user/nodes/toggle', { key: node.key, disabled: newDisabled })
    node.disabled = newDisabled
  } catch (e: any) { message.error(e.message) }
}
async function handlePing() {
  pinging.value = true
  try {
    const data = await apiList<any>('/api/user/nodes/ping')
    const m = new Map(data.map((d: any) => [d.key, d.latency]))
    nodes.value = nodes.value.map((n: any) => ({ ...n, latency: m.get(n.key) ?? null }))
    message.success('测速完成')
  } catch (e: any) { message.error(e.message) } finally { pinging.value = false }
}
async function handleBulk(enable: boolean) {
  try {
    const keys = selectedKeys.value
    const body = enable ? { enable: keys, disable: [] } : { enable: [], disable: keys }
    await apiPost('/api/user/nodes/bulk', body)
    nodes.value = nodes.value.map((n: any) => keys.includes(n.key) ? { ...n, disabled: !enable } : n)
    selectedKeys.value = []
    message.success(enable ? '已批量启用' : '已批量禁用')
  } catch (e: any) { message.error(e.message) }
}
async function handleToggleAll(enable: boolean) {
  try {
    await apiPost(enable ? '/api/user/nodes/enable-all' : '/api/user/nodes/disable-all')
    nodes.value = nodes.value.map((n: any) => ({ ...n, disabled: !enable }))
    message.success(enable ? '已全部启用' : '已全部禁用')
  } catch (e: any) { message.error(e.message) }
}
function handleResetSub() {
  // Swapping the address invalidates every client configured with the old one,
  // so it still needs a confirm — but it does NOT revoke the nodes those clients
  // already hold, and the copy must not claim otherwise.
  dialog.warning({
    title: '确认更换订阅地址',
    content: '更换后旧地址立即失效，所有已导入的客户端（Clash / sing-box 等）都需要用新地址重新导入。注意：已经从旧地址导出的节点仍然可用，如需彻底吊销请联系管理员重置节点凭据。确定更换？',
    positiveText: '更换', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiPost('/api/user/reset-sub'); sub.value = await apiGet('/api/user/subscription') || {}; message.success('订阅地址已更换') }
      catch (e: any) { message.error(e.message) }
    },
  })
}
function handleResetNodeCreds() {
  // The expensive half. Unlike the address swap this one has a real cost to
  // spell out: it needs a node-side push before it takes effect, it breaks every
  // client until they re-import, and it can only be done once a month.
  dialog.error({
    title: '确认重置节点凭据',
    content: '这会为你的所有节点重新生成凭据，从旧订阅导出的节点将彻底失效。'
      + '新凭据需要同步到各节点后才生效（通常 1 分钟内），期间你自己的连接也会中断，需要重新导入订阅。'
      + '每 30 天只能重置一次。确定重置？',
    positiveText: '重置', negativeText: '取消',
    onPositiveClick: async () => {
      resettingCreds.value = true
      try {
        const r: any = await apiPost('/api/user/reset-node-creds')
        sub.value = await apiGet('/api/user/subscription') || {}
        // 节点链接与代理账号都嵌了刚刚轮换掉的凭据，必须重新拉一次，
        // 否则页面上还挂着一份已经失效的旧链接。
        try { nodes.value = await apiList('/api/user/nodes') } catch {}
        try { proxies.value = await apiList('/api/user/proxies') } catch {}
        const secs = Number(r?.applies_in_seconds) || 60
        message.success(`节点凭据已重置，约 ${secs} 秒后在各节点生效，请重新导入订阅`)
      } catch (e: any) { message.error(e.message) }
      finally { resettingCreds.value = false }
    },
  })
}
async function copy(text: string) {
  if (!text) { message.warning('暂无链接'); return }
  // Honest feedback: navigator.clipboard is unavailable on plain-HTTP origins
  // (common for self-hosted panels), so copyText falls back and reports failure
  // rather than us claiming success on a silent no-op.
  if (await copyText(text)) message.success('已复制'); else message.error('复制失败，请手动选择并复制')
}

watch(showQr, async (v) => {
  if (v && sub.value.url) {
    await nextTick()
    if (qrCanvas.value) {
      QRCode.toCanvas(qrCanvas.value, sub.value.url, { width: 180, margin: 2 }, (err: any) => {
        if (err) console.error('QR error:', err)
      })
    }
  }
})

onMounted(async () => {
  try { sub.value = await apiGet('/api/user/subscription') || {} } catch (e: any) { message.error('订阅信息加载失败：' + (e?.message || '请稍后重试')) }
  try { proxies.value = await apiList('/api/user/proxies') } catch {}
  loadingNodes.value = true
  try { nodes.value = await apiList('/api/user/nodes') } catch {} finally { loadingNodes.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 0; }
.sub-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 20px; }
.sec { margin-bottom: 16px; border-radius: var(--r-sm); }
.sec-title { font-weight: 650; font-size: 14px; }
.proxy-row { margin-bottom: 12px; padding: 12px; background: var(--bg-soft); border-radius: 10px; }
.proxy-row:last-child { margin-bottom: 0; }
.pxrow { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.pxk { width: 52px; flex-shrink: 0; font-size: 12px; color: var(--text-3); }
.pxv { flex: 1; min-width: 0; }
</style>
