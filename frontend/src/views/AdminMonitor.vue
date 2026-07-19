<template>
  <div>
    <h2 class="page-title">监控管理</h2>
    <p class="page-sub">服务器监控与告警</p>

    <!-- 汇总卡 -->
    <div class="sum-grid">
      <div class="sum-card">
        <div class="sum-ic" style="background:#e9f0eb;color:#5c7c63;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
        </div>
        <div><span class="sum-val">{{ dash.total_servers || 0 }}</span><span class="sum-lab">服务器</span></div>
      </div>
      <div class="sum-card">
        <div class="sum-ic" style="background:#ecfdf5;color:#10b981;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
        </div>
        <div><span class="sum-val" style="color:#059669;">{{ dash.online || 0 }}</span><span class="sum-lab">在线</span></div>
      </div>
      <div class="sum-card">
        <div class="sum-ic" style="background:#fef2f2;color:#ef4444;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
        </div>
        <div><span class="sum-val" style="color:#dc2626;">{{ dash.offline || 0 }}</span><span class="sum-lab">离线</span></div>
      </div>
      <div class="sum-card">
        <div class="sum-ic" style="background:#e8eef5;color:#5e7a99;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/></svg>
        </div>
        <div><span class="sum-val">{{ fmtBytes(dash.summary?.total_mem_used) }}</span><span class="sum-lab">内存已用 / {{ fmtBytes(dash.summary?.total_mem_total) }}</span></div>
      </div>
      <div class="sum-card">
        <div class="sum-ic" style="background:#f7efda;color:#bf9540;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
        </div>
        <div><span class="sum-val">{{ fmtBytes(dash.summary?.total_disk_used) }}</span><span class="sum-lab">磁盘已用 / {{ fmtBytes(dash.summary?.total_disk_total) }}</span></div>
      </div>
      <div class="sum-card">
        <div class="sum-ic" style="background:#fef3c7;color:#d97706;">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
        </div>
        <div><span class="sum-val" style="color:var(--warn);">{{ dash.alerts_unread || 0 }}</span><span class="sum-lab">未读告警</span></div>
      </div>
    </div>

    <!-- 热力图 -->
    <n-card size="small" style="margin-bottom:16px;">
      <template #header><span class="card-h">可用性热力图</span></template>
      <template #header-extra>
        <n-space :size="4" align="center">
          <n-button v-for="r in heatRanges" :key="r.value" size="tiny" :type="heatRange===r.value?'primary':'default'" @click="loadHeatmap(r.value)">{{ r.label }}</n-button>
          <span class="hm-legend"><i class="hm-dot ok" />正常 <i class="hm-dot warn" />高负载 <i class="hm-dot crit" />离线/无数据</span>
        </n-space>
      </template>
      <div ref="heatEl" class="heat-chart" />
      <n-empty v-if="!heatLoading && !heatData?.servers?.length" size="small" description="暂无探针服务器" style="padding:16px 0;" />
    </n-card>

    <!-- 告警条 -->
    <n-card v-if="unreadAlerts.length" size="small" style="margin-bottom:16px;">
      <template #header><span class="card-h">未读告警</span></template>
      <div v-for="a in unreadAlerts" :key="a.id" class="alert-row">
        <div>
          <n-tag type="warning" size="small" bordered style="margin-right:8px;">{{ a.type }}</n-tag>
          <span class="alert-msg">{{ a.message }}</span>
          <span class="alert-time">{{ fmtDateTime(a.ts) }}</span>
        </div>
        <n-button size="tiny" @click="dismissAlert(a.id)">忽略</n-button>
      </div>
    </n-card>

    <!-- 搜索筛选 -->
    <div class="filter-bar">
      <n-input v-model:value="q" placeholder="搜索名称/位置/提供商" size="small" clearable style="max-width:220px;" />
      <n-select v-model:value="fStatus" :options="statusOpts" size="small" placeholder="状态" clearable style="width:120px;" />
      <n-select v-model:value="fLoc" :options="locOpts" size="small" placeholder="位置" clearable style="width:140px;" filterable />
      <n-select v-model:value="fProv" :options="provOpts" size="small" placeholder="提供商" clearable style="width:140px;" filterable />
      <span class="spacer" />
      <span class="cnt">{{ filtered.length }}/{{ servers.length }}</span>
    </div>

    <!-- 服务器卡片 -->
    <div class="card-grid">
      <n-card v-for="s in filtered" :key="s.id" size="small" class="srv-card">
        <template #header>
          <div class="srv-head">
            <span class="dot" :class="s.status" />
            <span class="srv-name" @click="goDetail(s)">{{ s.name }}</span>
            <n-tag v-if="s.location" size="small" :bordered="false">{{ s.location }}</n-tag>
          </div>
        </template>
        <template #header-extra>
          <n-space :size="4">
            <n-button size="tiny" @click="openAsset(s)">编辑</n-button>
            <n-button size="tiny" @click="copyInstall(s)">安装</n-button>
          </n-space>
        </template>

        <div class="asset-line">
          <span v-if="s.provider" class="tag-mini">{{ s.provider }}</span>
          <span v-if="s.spec" class="tag-mini">{{ s.spec }}</span>
          <span v-if="s.price" class="tag-mini">¥{{ s.price }}/月</span>
          <span v-if="s.days_left != null" class="tag-mini" :class="{ danger: s.days_left <= 7 }">剩 {{ s.days_left }} 天</span>
        </div>

        <template v-if="s.metrics">
          <div class="metric">
            <div class="m-row"><span>CPU</span><b>{{ s.metrics.cpu_percent.toFixed(1) }}%</b></div>
            <n-progress type="line" :percentage="s.metrics.cpu_percent" :show-indicator="false" :height="6" :color="pctColor(s.metrics.cpu_percent)" />
          </div>
          <div class="metric">
            <div class="m-row"><span>内存</span><b>{{ fmtBytes(s.metrics.mem_used) }} / {{ fmtBytes(s.metrics.mem_total) }}</b></div>
            <n-progress type="line" :percentage="memPct(s)" :show-indicator="false" :height="6" :color="pctColor(memPct(s))" />
          </div>
          <div class="metric">
            <div class="m-row"><span>磁盘</span><b>{{ fmtBytes(s.metrics.disk_used) }} / {{ fmtBytes(s.metrics.disk_total) }}</b></div>
            <n-progress type="line" :percentage="diskPct(s)" :show-indicator="false" :height="6" :color="pctColor(diskPct(s))" />
          </div>
          <div class="info-strip">
            <span class="tag-mini">↑ {{ fmtBytes(s.metrics.net_tx) }}/s</span>
            <span class="tag-mini">↓ {{ fmtBytes(s.metrics.net_rx) }}/s</span>
            <span class="tag-mini">负载 {{ s.metrics.load1.toFixed(2) }}</span>
            <span class="tag-mini">{{ fmtUptime(s.metrics.uptime) }}</span>
          </div>
        </template>
        <div v-else class="no-data">暂无数据</div>

        <div class="mini-chart-box">
          <n-space :size="4" style="margin-bottom:6px;">
            <n-button v-for="r in ranges" :key="r.value" size="tiny" :type="chartRange[s.id]===r.value?'primary':'default'" @click="loadChart(s.id, r.value)">{{ r.label }}</n-button>
          </n-space>
          <div :ref="(el:any) => setChartRef(s.id, el)" class="mini-chart" />
        </div>
      </n-card>
    </div>
    <n-empty v-if="!loading && !filtered.length" description="无匹配服务器" style="padding:40px 0;" />

    <!-- 资产编辑抽屉 -->
    <n-drawer v-model:show="showAsset" :width="drawerW" placement="right">
      <n-drawer-content title="编辑资产信息" closable>
        <n-form v-if="assetServer" label-placement="left" label-width="80">
          <n-form-item label="提供商"><n-input v-model:value="assetForm.provider" /></n-form-item>
          <n-form-item label="位置"><n-input v-model:value="assetForm.location" /></n-form-item>
          <n-form-item label="规格"><n-input v-model:value="assetForm.spec" /></n-form-item>
          <n-form-item label="月费 (¥)"><n-input-number v-model:value="assetForm.price" :min="0" style="width:100%;" /></n-form-item>
          <n-form-item label="到期时间"><n-input v-model:value="assetExpiry" type="datetime-local" style="width:100%;" /></n-form-item>
          <n-form-item label="备注"><n-input v-model:value="assetForm.notes" type="textarea" :rows="2" /></n-form-item>
          <n-form-item label="启用探针"><n-switch v-model:value="assetForm.probe_enabled" /></n-form-item>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="handleSaveAsset">保存</n-button>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, nextTick, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import {
  NCard, NButton, NSpace, NTag, NProgress, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSelect, NEmpty, useMessage
} from 'naive-ui'
import { apiGet, apiList, apiPost, apiPut } from '@/api'
import { fmtBytes, fmtDateTime, fmtUptime, pct, toLocalDatetimeInput } from '@/utils/format'
import * as echarts from 'echarts'

const message = useMessage()
const router = useRouter()
const dash = ref<any>({})
const servers = ref<any[]>([])
const saving = ref(false)
const loading = ref(false)
const chartRefs = ref<Record<number, HTMLElement>>({})
const chartInstances = ref<Record<number, echarts.ECharts>>({})
const chartRange = ref<Record<number, string>>({})
const chartLoaded = ref<Record<number, boolean>>({}) // 懒加载标记

const ranges = [
  { label: '1h', value: '1h' }, { label: '6h', value: '6h' },
  { label: '24h', value: '24h' }, { label: '7d', value: '7d' }, { label: '30d', value: '30d' },
]

const unreadAlerts = ref<any[]>([])

// 移动端抽屉宽度
const isMobile = ref(false)
const drawerW = computed(() => isMobile.value ? '100%' : 420)
function checkMobile() { isMobile.value = window.matchMedia('(max-width: 768px)').matches }

// 搜索筛选
const q = ref('')
const fStatus = ref<string | null>(null)
const fLoc = ref<string | null>(null)
const fProv = ref<string | null>(null)
const statusOpts = [{ label: '在线', value: 'online' }, { label: '离线', value: 'offline' }]
const locOpts = computed(() => uniqueOpts(servers.value.map(s => s.location)))
const provOpts = computed(() => uniqueOpts(servers.value.map(s => s.provider)))
function uniqueOpts(arr: string[]) {
  return [...new Set(arr.filter(Boolean))].map(v => ({ label: v, value: v }))
}
const filtered = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return servers.value.filter(s => {
    if (kw && ![s.name, s.location, s.provider].some(v => (v || '').toLowerCase().includes(kw))) return false
    if (fStatus.value && s.status !== fStatus.value) return false
    if (fLoc.value && s.location !== fLoc.value) return false
    if (fProv.value && s.provider !== fProv.value) return false
    return true
  })
})

function memPct(s: any) { return s.metrics ? pct(s.metrics.mem_used, s.metrics.mem_total) : 0 }
function diskPct(s: any) { return s.metrics ? pct(s.metrics.disk_used, s.metrics.disk_total) : 0 }
function pctColor(v: number) { return v >= 90 ? '#c2685c' : v >= 70 ? '#bf9540' : '#6f8f76' }

// 热力图分类：绿/黄/红
// 旧版 cell 热力图已替换为 ECharts 时间热力图（Y=机器, X=时间桶），见 loadHeatmap。
function goDetail(s: any) { router.push({ name: 'admin-monitor-detail', params: { id: s.id } }) }

// --- 可用性热力图（ECharts heatmap：Y=服务器, X=时间）---
const heatEl = ref<HTMLElement | null>(null)
const heatChart = shallowRef<echarts.ECharts | null>(null)
const heatData = ref<any>(null)
const heatRange = ref('24h')
const heatLoading = ref(false)
const heatRanges = [
  { label: '1h', value: '1h' }, { label: '6h', value: '6h' },
  { label: '24h', value: '24h' }, { label: '7d', value: '7d' },
]
function fmtHeatTime(ts: number, range: string): string {
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  if (range === '7d' || range === '24h') return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}
async function loadHeatmap(range: string) {
  heatRange.value = range
  heatLoading.value = true
  try {
    const data = await apiGet<any>(`/api/admin/monitor/heatmap?range=${range}`)
    heatData.value = data
    await nextTick()
    renderHeatmap()
  } catch {} finally { heatLoading.value = false }
}
function renderHeatmap() {
  const data = heatData.value
  if (!heatEl.value || !data) return
  const servers: any[] = data.servers || []
  if (!servers.length) {
    // 无探针服务器：清空已有图表
    heatChart.value?.clear()
    return
  }
  if (!heatChart.value) heatChart.value = echarts.init(heatEl.value)
  const chart = heatChart.value
  const buckets: number[] = data.buckets || []
  const matrix: number[][] = data.matrix || []
  const range = data.range || '24h'
  // 组装 ECharts heatmap data: [xIndex, yIndex, value]
  const pts: [number, number, number][] = []
  for (let y = 0; y < servers.length; y++) {
    const row = matrix[y] || []
    for (let x = 0; x < buckets.length; x++) {
      pts.push([x, y, row[x] ?? 2])
    }
  }
  const xLabels = buckets.map((t: number) => fmtHeatTime(t, range))
  const yLabels = servers.map((s: any) => s.name)
  // 计算正方形单元格：根据容器宽度和列数推算 cellSize，再设置容器高度
  const cw = heatEl.value.clientWidth || 600
  const maxNameLen = Math.max(...yLabels.map((n: string) => String(n).length), 4)
  const yLabelW = Math.min(maxNameLen * 7 + 16, 150)
  const padL = 8, padR = 8, padT = 12, padB = 4, xLabelH = 22
  const gridW = Math.max(cw - padL - padR - yLabelW, 100)
  const cellSize = Math.max(6, Math.floor(gridW / buckets.length))
  const chartH = servers.length * cellSize + padT + padB + xLabelH
  heatEl.value.style.height = chartH + 'px'
  chart.setOption({
    tooltip: {
      formatter: (p: any) => {
        const [x, y, v] = p.value
        const sname = yLabels[y] || ''
        const t = xLabels[x] || ''
        const tag = v === 0 ? '<span style="color:#10b981">正常</span>' : v === 1 ? '<span style="color:#fbbf24">高负载</span>' : '<span style="color:#ef4444">离线/无数据</span>'
        return `${sname}<br/>${t}<br/>${tag}`
      },
    },
    grid: { left: padL, right: padR, top: padT, bottom: padB, containLabel: true },
    xAxis: {
      type: 'category', data: xLabels, splitArea: { show: true },
      axisLabel: { fontSize: 10, hideOverlap: true },
      axisTick: { show: false }, axisLine: { show: false },
    },
    yAxis: {
      type: 'category', data: yLabels, splitArea: { show: true },
      axisLabel: { fontSize: 11 },
      axisTick: { show: false }, axisLine: { show: false },
    },
    visualMap: {
      min: 0, max: 2, calculable: false, show: false,
      inRange: { color: ['#10b981', '#fbbf24', '#ef4444'] },
    },
    series: [{
      type: 'heatmap', data: pts, progressive: 0,
      itemStyle: { borderColor: '#fff', borderWidth: 1, borderRadius: 2 },
      emphasis: { itemStyle: { borderColor: '#333', borderWidth: 1 } },
    }],
  })
  chart.resize()
}

// --- Asset editing ---
const showAsset = ref(false)
const assetServer = ref<any>(null)
const assetForm = reactive({ provider: '', location: '', spec: '', price: 0, notes: '', probe_enabled: false })
const assetExpiry = ref('')
function openAsset(s: any) {
  assetServer.value = s
  Object.assign(assetForm, { provider: s.provider || '', location: s.location || '', spec: s.spec || '', price: s.price || 0, notes: s.notes || '', probe_enabled: s.probe_enabled })
  assetExpiry.value = toLocalDatetimeInput(s.expiry_date)
  showAsset.value = true
}
async function handleSaveAsset() {
  if (!assetServer.value) return
  saving.value = true
  try {
    const body: any = { ...assetForm }
    if (assetExpiry.value) body.expiry_date = Math.floor(new Date(assetExpiry.value).getTime() / 1000)
    await apiPut(`/api/admin/servers/${assetServer.value.id}/monitor`, body)
    message.success('保存成功'); showAsset.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function dismissAlert(id: number) {
  try { await apiPost(`/api/admin/monitor/alerts/${id}/read`); unreadAlerts.value = unreadAlerts.value.filter(a => a.id !== id) } catch {}
}

function copyInstall(s: any) {
  if (!s.probe_token) { message.warning('请先启用探针'); return }
  const cmd = `bash <(curl -sL ${location.origin}/api/monitor/install.sh) ${s.probe_token}`
  navigator.clipboard.writeText(cmd); message.success('安装命令已复制')
}

// --- ECharts（懒加载 + resize）---
// chartRefs 在卡片卸载时清空引用，便于检测失效实例
function setChartRef(id: number, el: any) {
  if (el) chartRefs.value[id] = el
  else delete chartRefs.value[id]
}

// 安全 resize：跳过/清理已脱离 DOM 的孤儿实例，避免 ECharts 内部 model 损坏报错
function safeResizeAll() {
  for (const id of Object.keys(chartInstances.value)) {
    const sid = Number(id)
    const chart = chartInstances.value[sid]
    if (!chart) continue
    const el = chartRefs.value[sid]
    // 容器已被移除（筛选/卸载）：dispose 掉孤儿实例
    if (!el || !(el as HTMLElement).isConnected) {
      try { chart.dispose() } catch {}
      delete chartInstances.value[sid]
      continue
    }
    try { chart.resize() } catch {}
  }
}

async function loadChart(serverId: number, range: string) {
  chartRange.value[serverId] = range
  chartLoaded.value[serverId] = true
  try {
    const data = await apiGet<any>(`/api/admin/monitor/servers/${serverId}/metrics?range=${range}`)
    const metrics = data?.data || []
    await nextTick()
    const el = chartRefs.value[serverId]
    if (!el) return
    // 若旧实例绑定到已脱离 DOM 的容器，先 dispose 重建
    let chart = chartInstances.value[serverId]
    if (chart && !(el as HTMLElement).isConnected) {
      try { chart.dispose() } catch {}
      chart = undefined
      delete chartInstances.value[serverId]
    }
    if (!chart) {
      chart = echarts.init(el)
      chartInstances.value[serverId] = chart
    }
    const times = metrics.map((m: any) => {
      const d = new Date(m.ts * 1000)
      return `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`
    })
    chart.setOption({
      tooltip: { trigger: 'axis' },
      grid: { left: 36, right: 10, top: 10, bottom: 22 },
      xAxis: { type: 'category', data: times, axisLabel: { fontSize: 10 } },
      yAxis: [
        { type: 'value', name: '%', max: 100, axisLabel: { fontSize: 10 } },
        { type: 'value', name: 'MB/s', axisLabel: { fontSize: 10 } },
      ],
      series: [
        { name: 'CPU', type: 'line', data: metrics.map((m: any) => m.cpu_percent?.toFixed(1)), smooth: true, lineStyle: { width: 1.5 }, showSymbol: false },
        { name: '内存', type: 'line', data: metrics.map((m: any) => pct(m.mem_used, m.mem_total).toFixed(1)), smooth: true, lineStyle: { width: 1.5 }, showSymbol: false },
        { name: '网络↑', type: 'line', yAxisIndex: 1, data: metrics.map((m: any) => ((m.net_tx || 0) / 1048576).toFixed(2)), smooth: true, lineStyle: { width: 1 }, showSymbol: false },
        { name: '网络↓', type: 'line', yAxisIndex: 1, data: metrics.map((m: any) => ((m.net_rx || 0) / 1048576).toFixed(2)), smooth: true, lineStyle: { width: 1 }, showSymbol: false },
      ],
    })
  } catch {}
}

let refreshTimer: ReturnType<typeof setInterval> | null = null
let resizeTimer: ReturnType<typeof setInterval> | null = null

async function load() {
  loading.value = true
  try {
    const [d, s, a] = await Promise.all([
      apiGet('/api/admin/monitor/dashboard'),
      apiList('/api/admin/monitor/servers'),
      apiList('/api/admin/monitor/alerts'),
    ])
    dash.value = d || {}
    servers.value = s || []
    unreadAlerts.value = (a || []).filter((x: any) => !x.read)
    for (const sv of servers.value) {
      if (!chartRange.value[sv.id]) chartRange.value[sv.id] = '24h'
    }
  } catch {} finally { loading.value = false }
}

onMounted(async () => {
  checkMobile()
  window.addEventListener('resize', onWinResize)
  await load()
  await nextTick()
  // 懒加载：只画前 6 台可见服务器，其余按需点击 range 按钮时画
  const initial = servers.value.slice(0, 6)
  for (const sv of initial) {
    loadChart(sv.id, chartRange.value[sv.id] || '24h')
  }
  // 热力图
  loadHeatmap('24h')
  refreshTimer = setInterval(load, 30000)
  // 定期 resize 图表以适配抽屉开合
  resizeTimer = setInterval(() => { safeResizeAll(); heatChart.value?.resize() }, 5000)
})

function onWinResize() {
  checkMobile()
  safeResizeAll()
  // 窗口尺寸变化时重算正方形格子并重渲染
  if (heatData.value) renderHeatmap()
}

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (resizeTimer) clearInterval(resizeTimer)
  window.removeEventListener('resize', onWinResize)
  Object.values(chartInstances.value).forEach(c => c.dispose())
  heatChart.value?.dispose()
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 18px; }
.card-h { font-weight: 650; font-size: 14px; }

/* 汇总卡 */
.sum-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; margin-bottom: 16px; }
.sum-card { display: flex; align-items: center; gap: 10px; padding: 14px; background: var(--card); border: 1px solid var(--border); border-radius: 12px; }
.sum-ic { width: 36px; height: 36px; border-radius: 10px; display: grid; place-items: center; flex-shrink: 0; }
.sum-card div { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
.sum-val { font-size: 19px; font-weight: 720; }
.sum-lab { font-size: 11px; color: var(--text-3); }

/* 热力图 */
.hm-legend { display: inline-flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-3); margin-left: 8px; }
.hm-dot { width: 9px; height: 9px; border-radius: 50%; display: inline-block; margin-left: 6px; }
.hm-dot.ok { background: #10b981; } .hm-dot.warn { background: #fbbf24; } .hm-dot.crit { background: #ef4444; }
.heat-chart { width: 100%; height: 360px; min-height: 120px; }
.heat-chart:empty { display: none; }

/* 告警 */
.alert-row { display: flex; justify-content: space-between; align-items: center; padding: 8px 0; border-bottom: 1px solid var(--border-soft, #f1efe8); }
.alert-row:last-child { border-bottom: none; }
.alert-msg { font-size: 13px; }
.alert-time { font-size: 11px; color: var(--text-3); margin-left: 8px; }

/* 筛选栏 */
.filter-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 14px; }
.spacer { flex: 1; }
.cnt { font-size: 12px; color: var(--text-3); }

/* 服务器卡 */
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); gap: 14px; }
.srv-head { display: flex; align-items: center; gap: 8px; }
.dot { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; }
.dot.online { background: #10b981; box-shadow: 0 0 8px rgba(16,185,129,.5); }
.dot.offline { background: #ef4444; }
.srv-name { font-weight: 650; cursor: pointer; }
.srv-name:hover { text-decoration: underline; }

.asset-line { display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 10px; }
.tag-mini { padding: 2px 6px; background: var(--bg-soft); border-radius: 4px; font-size: 11px; color: var(--text-2); }
.tag-mini.danger { background: var(--danger-soft); color: var(--danger); }

.metric { margin-bottom: 8px; }
.m-row { display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 3px; }
.info-strip { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.no-data { text-align: center; color: var(--text-3); padding: 16px; font-size: 13px; }
.mini-chart-box { margin-top: 12px; }
.mini-chart { height: 160px; }

@media (max-width: 768px) {
  .card-grid { grid-template-columns: 1fr; }
  .sum-grid { grid-template-columns: repeat(2, 1fr); }
}
</style>
