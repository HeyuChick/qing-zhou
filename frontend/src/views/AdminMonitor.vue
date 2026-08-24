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
          <span class="range-switch">
            <button v-for="r in heatRanges" :key="r.value" type="button" :class="{ active: heatRange===r.value }" @click="loadHeatmap(r.value)">{{ r.label }}</button>
          </span>
          <span class="hm-legend"><i class="hm-dot ok" />正常 <i class="hm-dot warn" />高负载 <i class="hm-dot crit" />严重 <i class="hm-dot none" />无数据</span>
        </n-space>
      </template>
      <div ref="heatEl" class="heat-chart" />
      <n-empty v-if="!heatLoading && !heatData?.servers?.length" size="small" description="暂无探针服务器" style="padding:16px 0;" />
    </n-card>

    <!-- 告警条 -->
    <n-card v-if="unreadAlerts.length" size="small" style="margin-bottom:16px;">
      <template #header>
        <span class="card-h">未读告警</span>
        <span class="alert-cnt">{{ unreadAlerts.length }}</span>
      </template>
      <template #header-extra>
        <n-button size="tiny" @click="dismissAll">全部忽略</n-button>
      </template>
      <div v-for="a in shownAlerts" :key="a.id" class="alert-row">
        <div class="alert-main">
          <div class="alert-line">
            <n-tag :type="alertKind(a.type)" size="small" :bordered="false">{{ alertLabel(a.type) }}</n-tag>
            <span class="alert-msg">{{ a.message }}</span>
          </div>
          <div class="alert-meta">
            首次 {{ fmtDateTime(a.first_ts || a.ts) }}
            <template v-if="alertDur(a) >= 60"> · 持续 {{ fmtUptime(alertDur(a)) }}</template>
            <template v-if="a.hits > 1"> · 触发 {{ a.hits }} 次</template>
          </div>
        </div>
        <n-button size="tiny" @click="dismissAlert(a.id)">忽略</n-button>
      </div>
      <div v-if="unreadAlerts.length > ALERT_PREVIEW" class="alert-more">
        <n-button text size="tiny" @click="alertsExpanded = !alertsExpanded">
          {{ alertsExpanded ? '收起' : `还有 ${unreadAlerts.length - ALERT_PREVIEW} 条，展开全部` }}
        </n-button>
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
            <!-- 本机没有资产信息可填、也不需要装探针，标一下省得找那两个按钮 -->
            <n-tag v-if="s.local" size="small" type="info" :bordered="false">面板自采集</n-tag>
            <n-tag v-if="s.location" size="small" :bordered="false">{{ s.location }}</n-tag>
          </div>
        </template>
        <template #header-extra>
          <n-space :size="4" align="center">
            <n-tooltip trigger="hover">
              <template #trigger>
                <n-switch :value="s.public_visible" size="small" :loading="visSaving === s.id" @update:value="(v:boolean) => setPublicVisible(s, v)" />
              </template>
              {{ s.public_visible ? '显示在公开状态页' : '不显示在公开状态页' }}
            </n-tooltip>
            <n-button size="tiny" @click="openAsset(s)">编辑</n-button>
            <!-- 本机是面板自采集，没有探针可装 -->
            <n-button v-if="!s.local" size="tiny" @click="copyInstall(s)">安装</n-button>
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
          <div class="range-switch mini-range">
            <button v-for="r in ranges" :key="r.value" type="button" :class="{ active: chartRange[s.id]===r.value }" @click="loadChart(s.id, r.value)">{{ r.label }}</button>
          </div>
          <div :ref="(el:any) => setChartRef(s.id, el)" class="mini-chart" />
        </div>
      </n-card>
    </div>
    <n-empty v-if="!loading && !filtered.length" description="无匹配服务器" style="padding:40px 0;" />

    <!-- 资产编辑抽屉 -->
    <n-drawer v-model:show="showAsset" :width="drawerW" placement="right">
      <n-drawer-content :title="assetServer?.local ? '编辑资产信息 · 面板本机' : '编辑资产信息'" closable>
        <n-alert v-if="assetServer?.local" type="info" :bordered="false" style="margin-bottom:12px;">
          面板本机由面板自采集，无需探针。这里记的是这台机器本身 —— 到期提醒尤其值得填：落地机到期只掉一个节点，面板机到期是整站。
        </n-alert>
        <n-form v-if="assetServer" label-placement="left" label-width="80">
          <n-form-item label="提供商"><n-input v-model:value="assetForm.provider" /></n-form-item>
          <n-form-item label="位置"><n-input v-model:value="assetForm.location" /></n-form-item>
          <n-form-item label="规格"><n-input v-model:value="assetForm.spec" /></n-form-item>
          <n-form-item label="月费 (¥)"><n-input-number v-model:value="assetForm.price" :min="0" style="width:100%;" /></n-form-item>
          <n-form-item label="到期时间"><n-input v-model:value="assetExpiry" :input-props="{ type: 'datetime-local' }" style="width:100%;" /></n-form-item>
          <n-form-item label="备注"><n-input v-model:value="assetForm.notes" type="textarea" :rows="2" /></n-form-item>
          <n-form-item v-if="!assetServer.local" label="启用探针"><n-switch v-model:value="assetForm.probe_enabled" /></n-form-item>
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
  NCard, NButton, NSpace, NTag, NProgress, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSelect, NEmpty, NTooltip, NAlert, useMessage
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
const chartInstances = ref<Partial<Record<number, echarts.ECharts>>>({})
const chartRange = ref<Record<number, string>>({})
const chartLoaded = ref<Record<number, boolean>>({}) // 懒加载标记

const ranges = [
  { label: '1h', value: '1h' }, { label: '6h', value: '6h' },
  { label: '24h', value: '24h' }, { label: '7d', value: '7d' }, { label: '30d', value: '30d' },
]

// --- 告警 ---
// 后端已把同一台机器的同一类问题合并成一条「事件」（持续时间 + 触发次数），
// 这里只负责按严重度排序、折叠长列表，避免一屏刷满重复条目。
const unreadAlerts = ref<any[]>([])
const alertsExpanded = ref(false)
const ALERT_PREVIEW = 5
const ALERT_META: Record<string, { label: string; kind: 'error' | 'warning'; rank: number }> = {
  offline: { label: '离线', kind: 'error', rank: 0 },
  expired: { label: '已过期', kind: 'error', rank: 1 },
  disk_full: { label: '磁盘将满', kind: 'warning', rank: 2 },
  high_mem: { label: '内存偏高', kind: 'warning', rank: 3 },
  high_cpu: { label: 'CPU 偏高', kind: 'warning', rank: 4 },
  expiring: { label: '即将到期', kind: 'warning', rank: 5 },
}
function alertLabel(t: string) { return ALERT_META[t]?.label || t }
function alertKind(t: string) { return ALERT_META[t]?.kind || 'warning' }
function alertDur(a: any) { return Math.max(0, (a.ts || 0) - (a.first_ts || a.ts || 0)) }
const sortedAlerts = computed(() => [...unreadAlerts.value].sort(
  (a, b) => (ALERT_META[a.type]?.rank ?? 9) - (ALERT_META[b.type]?.rank ?? 9) || b.ts - a.ts
))
const shownAlerts = computed(() => alertsExpanded.value ? sortedAlerts.value : sortedAlerts.value.slice(0, ALERT_PREVIEW))

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
function fmtHeatAxisTime(ts: number, range: string): string {
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  if (range === '7d') return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}
function escapeHeatHtml(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] || c))
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
  const hasDistinctNoData = Number(data.state_count || 0) >= 4
  for (let y = 0; y < servers.length; y++) {
    const row = matrix[y] || []
    for (let x = 0; x < buckets.length; x++) {
      const raw = row[x] ?? 3
      pts.push([x, y, !hasDistinctNoData && raw === 2 ? 3 : raw])
    }
  }
  const xLabels = buckets.map((t: number) => fmtHeatAxisTime(t, range))
  const yLabels = servers.map((s: any) => s.name)
  const cw = heatEl.value.clientWidth || 600
  const maxNameLen = Math.max(...yLabels.map((n: string) => String(n).length), 4)
  const yLabelW = Math.min(maxNameLen * 7 + 18, cw < 640 ? 92 : 150)
  const padR = 4, padT = 5, padB = 30
  const rowHeight = cw < 640 ? 18 : 22
  const gridHeight = Math.max(1, servers.length) * rowHeight
  const chartH = gridHeight + padT + padB
  const labelCount = cw < 520 ? 4 : cw < 900 ? 6 : 8
  const labelInterval = Math.max(0, Math.ceil(buckets.length / labelCount) - 1)
  const states = [
    { label: '运行正常', color: '#63a887' },
    { label: '高负载', color: '#d2a34c' },
    { label: '严重负载', color: '#c96d67' },
    { label: '离线 / 无数据', color: '#b9c2cc' },
  ]
  heatEl.value.style.height = chartH + 'px'
  chart.setOption({
    animationDuration: 560,
    animationDurationUpdate: 380,
    animationEasing: 'cubicOut',
    animationEasingUpdate: 'cubicOut',
    tooltip: {
      backgroundColor: 'rgba(255,255,255,.98)', borderColor: '#dfe4ea', borderWidth: 1,
      padding: [10, 12], textStyle: { color: '#26323f', fontSize: 12 },
      extraCssText: 'border-radius:10px;box-shadow:0 10px 30px rgba(42,55,70,.14);',
      formatter: (p: any) => {
        const [x, y, v] = p.value
        const state = states[v] || states[3]
        const time = buckets[x] ? fmtHeatTime(buckets[x], range) : ''
        return `<div style="font-weight:650;margin-bottom:4px">${escapeHeatHtml(yLabels[y])}</div><div style="color:#7b8794;margin-bottom:7px">${escapeHeatHtml(time)}</div><div style="display:flex;align-items:center;gap:7px"><i style="width:8px;height:8px;border-radius:3px;background:${state.color};display:inline-block"></i>${state.label}</div>`
      },
    },
    grid: { left: yLabelW, right: padR, top: padT, height: gridHeight },
    xAxis: {
      type: 'category', data: xLabels, splitArea: { show: true },
      axisLabel: { interval: labelInterval, color: '#7b8794', fontSize: 10, margin: 10, hideOverlap: true },
      axisTick: { show: false }, axisLine: { show: false },
    },
    yAxis: {
      type: 'category', data: yLabels, splitArea: { show: true },
      axisLabel: { color: '#606d7b', fontSize: 11, width: yLabelW - 12, overflow: 'truncate', margin: 10 },
      axisTick: { show: false }, axisLine: { show: false },
    },
    visualMap: { type: 'piecewise', show: false, pieces: states.map((state, value) => ({ value, color: state.color })) },
    series: [{
      type: 'heatmap', data: pts, progressive: 0,
      itemStyle: { borderColor: 'rgba(255,255,255,.96)', borderWidth: 3, borderRadius: 5 },
      emphasis: { itemStyle: { borderColor: '#fff', borderWidth: 2, shadowBlur: 10, shadowColor: 'rgba(42,55,70,.18)' } },
    }],
  }, true)
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
    // 本机没有探针可启用，别把这个字段发过去（后端也会忽略）
    if (assetServer.value.local) delete body.probe_enabled
    // 清空到期时间要能存回去，否则填错了就再也去不掉
    body.expiry_date = assetExpiry.value ? Math.floor(new Date(assetExpiry.value).getTime() / 1000) : 0
    await apiPut(`/api/admin/servers/${assetServer.value.id}/monitor`, body)
    message.success('保存成功'); showAsset.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function dismissAlert(id: number) {
  try {
    await apiPost(`/api/admin/monitor/alerts/${id}/read`)
    unreadAlerts.value = unreadAlerts.value.filter(a => a.id !== id)
    dash.value.alerts_unread = unreadAlerts.value.length
  } catch {}
}

async function dismissAll() {
  try {
    await apiPost('/api/admin/monitor/alerts/read-all')
    unreadAlerts.value = []
    dash.value.alerts_unread = 0
    alertsExpanded.value = false
  } catch (e: any) { message.error(e.message) }
}

// 公开状态页是不鉴权的，所以「监控它」和「对外公布它」分成两件事。其他机器默认
// 公开（和加这个开关之前的行为一致），面板本机默认不公开。
const visSaving = ref<number | null>(null)
async function setPublicVisible(s: any, v: boolean) {
  visSaving.value = s.id
  try {
    await apiPut(`/api/admin/servers/${s.id}/monitor`, { public_visible: v })
    s.public_visible = v
    message.success(v ? `「${s.name}」已显示在公开状态页` : `「${s.name}」已从公开状态页隐藏`)
  } catch (e: any) { message.error(e.message) } finally { visSaving.value = null }
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
      // unread=1：只取未读，否则 200 条上限可能被历史已读告警占满。
      apiList('/api/admin/monitor/alerts?unread=1'),
    ])
    dash.value = d || {}
    servers.value = s || []
    unreadAlerts.value = a || []
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
  Object.values(chartInstances.value).forEach(c => c?.dispose())
  heatChart.value?.dispose()
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 18px; }
.card-h { font-weight: 650; font-size: 14px; }

/* 汇总卡 */
.sum-grid { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 12px; margin-bottom: 16px; }
.sum-card { min-height: 76px; box-sizing: border-box; display: flex; align-items: center; gap: 11px; padding: 13px 14px; background: var(--card); border: 1px solid var(--border); border-radius: 12px; box-shadow: var(--shadow-sm); }
.sum-ic { width: 36px; height: 36px; border-radius: 10px; display: grid; place-items: center; flex: 0 0 36px; }
.sum-card > div:not(.sum-ic) { display: flex; flex-direction: column; justify-content: center; gap: 2px; min-width: 0; }
.sum-val { font-size: 19px; font-weight: 720; line-height: 1.12; white-space: nowrap; font-variant-numeric: tabular-nums; }
.sum-lab { font-size: 11px; line-height: 1.25; color: var(--text-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* 热力图 */
.hm-legend { display: inline-flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-3); margin-left: 8px; }
.hm-dot { width: 8px; height: 8px; border-radius: 3px; display: inline-block; margin-left: 6px; box-shadow: inset 0 0 0 1px rgba(31,43,55,.05); }
.hm-dot.ok { background: #63a887; } .hm-dot.warn { background: #d2a34c; } .hm-dot.crit { background: #c96d67; } .hm-dot.none { background: #b9c2cc; }
.heat-chart { width: 100%; height: 58px; min-height: 0; }
.heat-chart:empty { display: none; }
.range-switch { display:inline-flex; align-items:center; gap:0; padding:3px; border:1px solid rgba(28,48,70,.09); border-radius:8px; background:#eef1f4; }
.range-switch button { min-width:30px; padding:3px 8px; border:0; border-radius:5px; background:transparent; color:var(--text-3); font:600 11px/1.5 var(--ff); cursor:pointer; transition:background .18s var(--ease-standard), color .18s var(--ease-standard), box-shadow .18s var(--ease-standard); }
.range-switch button:hover { color:var(--text); }
.range-switch button.active { background:#fff; color:var(--text); box-shadow:0 1px 2px rgba(30,45,60,.1); }
.range-switch button:focus-visible { outline:0; box-shadow:inset 0 0 0 2px rgba(29,39,51,.16); }
.mini-range { width:max-content; margin-bottom:6px; }

/* 告警 */
.alert-cnt { display: inline-block; margin-left: 6px; padding: 0 6px; border-radius: 9px; background: var(--danger-soft, #fef2f2); color: var(--danger, #dc2626); font-size: 11px; font-weight: 650; }
.alert-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 9px 0; border-bottom: 1px solid var(--border-soft, #f1efe8); }
.alert-row:last-child { border-bottom: none; }
.alert-main { min-width: 0; }
.alert-line { display: flex; align-items: center; gap: 8px; }
.alert-msg { font-size: 13px; overflow-wrap: anywhere; }
.alert-meta { font-size: 11px; color: var(--text-3); margin-top: 3px; }
.alert-more { padding-top: 8px; text-align: center; }

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
@media (min-width: 769px) and (max-width: 1180px) { .sum-grid { grid-template-columns: repeat(3, 1fr); } }
</style>
