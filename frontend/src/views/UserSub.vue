<template>
  <div>
    <h2 class="page-title">订阅管理</h2>
    <p class="page-sub">管理你的订阅链接和节点</p>

    <!-- 资源概览 -->
    <n-card size="small" style="margin-bottom:16px;">
      <div style="display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:12px;">
        <div style="flex:1;min-width:200px;">
          <div style="font-size:12px;color:var(--text-3);margin-bottom:4px;">总流量使用</div>
          <n-progress type="line" :percentage="totalPct" :color="totalPct>90?'#c2685c':'#6f8f76'" />
          <div style="font-size:12px;color:var(--text-2);margin-top:4px;">{{ fmtBytes(totalUsed) }} / {{ fmtTotal(totalCap) }}</div>
        </div>
        <n-space>
          <n-button size="small" @click="router.push('/shop')">去商城</n-button>
          <n-button size="small" @click="router.push('/orders')">订单记录</n-button>
        </n-space>
      </div>
    </n-card>

    <!-- 分计划资源 -->
    <n-card v-if="plans.length" title="我的计划" size="small" style="margin-bottom:16px;">
      <div v-for="p in plans" :key="p.id" style="margin-bottom:12px;padding:10px;background:var(--bg-soft);border-radius:10px;">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;">
          <span style="font-weight:600;">{{ p.name || '计划 #' + p.id }}</span>
          <n-tag :type="planStatus(p).type" size="small" bordered>{{ planStatus(p).label }}</n-tag>
        </div>
        <n-progress type="line" :percentage="planPct(p)" :color="planPct(p)>90?'#c2685c':'#6f8f76'" />
        <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--text-3);margin-top:4px;">
          <span>{{ fmtBytes(p.used) }} / {{ fmtTotal(p.traffic_limit) }}</span>
          <span>{{ fmtDate(p.expiry_at) }}</span>
        </div>
      </div>
    </n-card>

    <!-- 订阅链接 -->
    <n-card title="订阅链接" size="small" style="margin-bottom:16px;">
      <n-input-group>
        <n-input :value="sub.url" readonly placeholder="暂无订阅" />
        <n-button type="primary" @click="copy(sub.url)">复制</n-button>
      </n-input-group>
      <div style="margin-top:10px;display:flex;gap:8px;flex-wrap:wrap;">
        <n-button size="small" @click="copy(sub.formats?.clash)">Clash</n-button>
        <n-button size="small" @click="copy(sub.formats?.singbox)">sing-box</n-button>
        <n-button size="small" @click="copy(sub.formats?.surge)">Surge</n-button>
        <n-button size="small" @click="copy(sub.formats?.default)">通用</n-button>
        <n-button size="small" @click="showQr=!showQr">{{ showQr?'隐藏':'显示' }}二维码</n-button>
        <n-button size="small" type="error" @click="handleResetSub">重置链接</n-button>
      </div>
      <div v-if="showQr && sub.url" style="margin-top:12px;text-align:center;">
        <canvas ref="qrCanvas" />
        <div style="font-size:11px;color:var(--text-3);margin-top:4px;">手机扫描导入订阅</div>
      </div>
    </n-card>

    <!-- 节点列表 -->
    <n-card title="节点列表" size="small">
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
import { NCard, NInput, NInputGroup, NButton, NDataTable, NProgress, NTag, NSelect, NSpace, useMessage } from 'naive-ui'
import { apiGet, apiList, apiPost } from '@/api'
import { fmtBytes, fmtTotal, fmtDate, pct } from '@/utils/format'
import QRCode from 'qrcode'

const router = useRouter()
const message = useMessage()
const sub = ref<any>({})
const plans = ref<any[]>([])
const nodes = ref<any[]>([])
const showQr = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const search = ref('')
const protoFilter = ref<string | null>(null)
const selectedKeys = ref<string[]>([])
const loadingNodes = ref(false)
const pinging = ref(false)

const totalUsed = computed(() => plans.value.reduce((s, p) => s + (p.used || 0), 0))
const totalCap = computed(() => plans.value.reduce((s, p) => s + (p.traffic_limit || 0), 0))
const totalPct = computed(() => pct(totalUsed.value, totalCap.value))

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

function planPct(p: any) { return pct(p.used, p.traffic_limit) }
function planStatus(p: any) {
  if (p.expiry_at && p.expiry_at * 1000 < Date.now()) return { type: 'error' as const, label: '已过期' }
  if (p.traffic_limit > 0 && p.used >= p.traffic_limit) return { type: 'warning' as const, label: '已用尽' }
  return { type: 'success' as const, label: '正常' }
}
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
async function handleResetSub() {
  try { await apiPost('/api/user/reset-sub'); sub.value = await apiGet('/api/user/subscription') || {}; message.success('订阅链接已重置') } catch (e: any) { message.error(e.message) }
}
function copy(text: string) {
  if (!text) { message.warning('暂无链接'); return }
  navigator.clipboard.writeText(text); message.success('已复制')
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
  try { sub.value = await apiGet('/api/user/subscription') || {} } catch {}
  try { plans.value = await apiList('/api/user/plans') } catch {}
  loadingNodes.value = true
  try { nodes.value = await apiList('/api/user/nodes') } catch {} finally { loadingNodes.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
</style>
