<template>
  <div class="monitor-page">
    <AppHeader />

    <!-- SVG 渐变定义（供仪表盘 / 迷你图引用） -->
    <svg width="0" height="0" style="position:absolute" aria-hidden="true">
      <defs>
        <linearGradient id="gg-ok" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stop-color="#7fa588" /><stop offset="1" stop-color="#5c7c63" />
        </linearGradient>
        <linearGradient id="gg-warn" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stop-color="#e8c069" /><stop offset="1" stop-color="#c99728" />
        </linearGradient>
        <linearGradient id="gg-crit" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stop-color="#d98a7f" /><stop offset="1" stop-color="#c2685c" />
        </linearGradient>
        <linearGradient id="spark-grad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stop-color="#6f8f76" stop-opacity="0.32" />
          <stop offset="1" stop-color="#6f8f76" stop-opacity="0" />
        </linearGradient>
      </defs>
    </svg>

    <div class="monitor-content">
      <!-- 自定义首页模式 -->
      <template v-if="config.config.homepage_mode === 'custom' && config.config.homepage_url">
        <iframe
          :src="config.config.homepage_url"
          style="width: 100%; height: calc(100vh - 56px); border: none;"
          sandbox="allow-scripts allow-same-origin allow-popups"
        />
      </template>

      <!-- 监控大屏模式 -->
      <template v-else>
        <!-- 顶部标题栏 -->
        <div class="hero">
          <div class="hero-left">
            <h1 class="hero-title">服务器监控</h1>
            <p class="hero-sub">实时掌握每台节点的运行状态</p>
          </div>
          <div class="hero-right">
            <div class="clock">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>
              {{ clock }}
            </div>
            <div class="auto-badge">
              <span class="auto-dot" :class="{ active: refreshing }"></span>
              自动刷新 · 30s
            </div>
            <button class="refresh-btn" :class="{ spinning: refreshing }" @click="doRefresh" :disabled="refreshing" title="立即刷新">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
            </button>
          </div>
        </div>

        <!-- 汇总卡片 -->
        <div class="summary-grid">
          <div class="summary-card">
            <div class="summary-icon i-server">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/><line x1="6" y1="7" x2="6.01" y2="7"/><line x1="6" y1="17" x2="6.01" y2="17"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ Math.round(dTotal) }}</span>
              <span class="summary-label">服务器 · <b class="ok-text">{{ Math.round(dOnline) }} 在线</b><template v-if="offlineCount"> · <b class="crit-text">{{ offlineCount }} 离线</b></template></span>
            </div>
          </div>

          <div class="summary-card">
            <div class="summary-icon i-cpu">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ hasData ? dCpu.toFixed(1) : '—' }}<i>%</i></span>
              <span class="summary-label">平均 CPU</span>
              <div class="mini-bar"><div class="mini-fill" :class="lvl(avgCpuN)" :style="{ width: Math.min(avgCpuN,100)+'%' }" /></div>
            </div>
          </div>

          <div class="summary-card">
            <div class="summary-icon i-mem">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="7" width="20" height="10" rx="2"/><line x1="6" y1="11" x2="6" y2="13"/><line x1="10" y1="11" x2="10" y2="13"/><line x1="14" y1="11" x2="14" y2="13"/><line x1="18" y1="11" x2="18" y2="13"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ hasData ? dMem.toFixed(1) : '—' }}<i>%</i></span>
              <span class="summary-label">平均内存</span>
              <div class="mini-bar"><div class="mini-fill" :class="lvl(avgMemN)" :style="{ width: Math.min(avgMemN,100)+'%' }" /></div>
            </div>
          </div>

          <div class="summary-card">
            <div class="summary-icon i-disk">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ hasData ? dDisk.toFixed(1) : '—' }}<i>%</i></span>
              <span class="summary-label">平均磁盘</span>
              <div class="mini-bar"><div class="mini-fill" :class="lvl(avgDiskN)" :style="{ width: Math.min(avgDiskN,100)+'%' }" /></div>
            </div>
          </div>

          <div class="summary-card">
            <div class="summary-icon i-up">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val small">{{ fmtBytes(dUp) }}<i>/s</i></span>
              <span class="summary-label">总上行</span>
            </div>
          </div>

          <div class="summary-card">
            <div class="summary-icon i-down">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val small">{{ fmtBytes(dDown) }}<i>/s</i></span>
              <span class="summary-label">总下行</span>
            </div>
          </div>
        </div>

        <!-- 刷新 / 热力图范围 -->
        <div class="section-bar">
          <span class="section-title">可用性热力图</span>
          <div class="heat-range">
            <button v-for="r in heatRanges" :key="r.value" class="heat-range-btn" :class="{ active: heatRange === r.value }" @click="loadHeatmap(r.value)">{{ r.label }}</button>
            <span class="heat-legend"><i class="hm-dot ok"></i>正常 <i class="hm-dot warn"></i>高负载 <i class="hm-dot crit"></i>离线/无数据</span>
          </div>
        </div>

        <!-- 可用性热力图 -->
        <div class="heatmap-card">
          <div ref="heatEl" class="heat-chart"></div>
          <div v-if="!heatData?.servers?.length" class="heat-empty">暂无探针服务器</div>
        </div>

        <!-- 空状态 -->
        <n-empty v-if="!loading && servers.length === 0" description="暂无服务器数据" style="padding: 80px 0;" />

        <!-- 服务器卡片 -->
        <div class="server-grid">
          <div v-for="(s, i) in servers" :key="s.name" class="server-card" :style="{ '--i': i }">
            <div class="card-top-line" :class="s.status" />

            <!-- 头部 -->
            <div class="card-header">
              <div class="card-title">
                <span class="status-beacon" :class="s.status" />
                <span class="server-name" :title="s.name">{{ s.name }}</span>
              </div>
              <span class="status-badge" :class="s.status">
                <i class="badge-dot" /> {{ s.status === 'online' ? '运行中' : '离线' }}
              </span>
            </div>

            <!-- 标签 -->
            <div class="tag-line">
              <span v-if="s.location" class="tag loc">{{ s.location }}</span>
              <span v-if="s.provider" class="tag">{{ s.provider }}</span>
              <span v-if="s.spec" class="tag spec">{{ s.spec }}</span>
            </div>

            <template v-if="s.metrics">
              <!-- 三仪表盘 -->
              <div class="gauges">
                <div class="gauge" v-for="g in gauges(s)" :key="g.key">
                  <svg viewBox="0 0 64 64" class="gauge-svg">
                    <circle class="gauge-bg" cx="32" cy="32" r="26" />
                    <circle class="gauge-fg" cx="32" cy="32" r="26"
                      :stroke="`url(#gg-${g.lvl})`"
                      :stroke-dasharray="GAUGE_C"
                      :stroke-dashoffset="g.off" />
                  </svg>
                  <div class="gauge-center">
                    <span class="gauge-val" :class="g.lvl">{{ g.val.toFixed(0) }}<i>%</i></span>
                  </div>
                  <div class="gauge-meta">
                    <span class="gauge-label">{{ g.key }}</span>
                    <span class="gauge-sub">{{ g.sub }}</span>
                  </div>
                </div>
              </div>

              <!-- CPU 迷你趋势图 -->
              <div v-if="s.spark && s.spark.cpu && s.spark.cpu.length" class="spark-wrap">
                <svg class="spark" :viewBox="`0 0 100 ${SPARK_H}`" preserveAspectRatio="none">
                  <path :d="sparkArea(s.spark.cpu)" class="spark-area" />
                  <path :d="sparkLine(s.spark.cpu)" class="spark-line" />
                </svg>
                <span class="spark-tag">CPU · 近 1 小时</span>
              </div>

              <!-- 交换分区（有才显示） -->
              <div v-if="s.metrics.swap_total > 0" class="swap-row">
                <span class="swap-label">交换</span>
                <div class="swap-track"><div class="swap-fill" :class="lvl(swapPct(s))" :style="{ width: Math.min(swapPct(s),100)+'%' }" /></div>
                <span class="swap-detail">{{ fmtBytes(s.metrics.swap_used) }} / {{ fmtBytes(s.metrics.swap_total) }}</span>
              </div>

              <!-- 详细信息网格 -->
              <div class="info-grid">
                <div class="info-cell" title="实时上行速率">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>
                  <div><span class="ic-label">上行</span><span class="ic-val">{{ fmtBytes(s.metrics.net_up) }}/s</span></div>
                </div>
                <div class="info-cell" title="实时下行速率">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg>
                  <div><span class="ic-label">下行</span><span class="ic-val">{{ fmtBytes(s.metrics.net_down) }}/s</span></div>
                </div>
                <div class="info-cell" :title="`1 / 5 / 15 分钟平均负载`">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20V10"/><path d="M18 20V4"/><path d="M6 20v-4"/></svg>
                  <div><span class="ic-label">负载</span><span class="ic-val">{{ s.metrics.load1.toFixed(2) }} / {{ s.metrics.load5.toFixed(2) }} / {{ s.metrics.load15.toFixed(2) }}</span></div>
                </div>
                <div class="info-cell" title="运行时长">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>
                  <div><span class="ic-label">运行</span><span class="ic-val">{{ fmtUptime(s.metrics.uptime) }}</span></div>
                </div>
                <div class="info-cell" v-if="s.metrics.tcp_connections != null" title="TCP 连接数">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 6l6 0"/><circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M6 8.5v7a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2v-7"/><circle cx="12" cy="19" r="2.5"/></svg>
                  <div><span class="ic-label">连接</span><span class="ic-val">{{ s.metrics.tcp_connections }}</span></div>
                </div>
                <div class="info-cell" v-if="s.metrics.process_count != null" title="进程数">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
                  <div><span class="ic-label">进程</span><span class="ic-val">{{ s.metrics.process_count }}</span></div>
                </div>
                <div class="info-cell wide" v-if="s.metrics.platform" title="操作系统 / 架构">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
                  <div><span class="ic-label">系统</span><span class="ic-val ellip">{{ s.metrics.platform }}<template v-if="s.metrics.arch"> · {{ s.metrics.arch }}</template></span></div>
                </div>
              </div>
            </template>

            <div v-else class="card-no-data">
              <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity=".3"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
              <span>暂无数据</span>
            </div>

            <div class="card-footer">
              <span class="footer-time">
                <span class="dot" :class="s.status" />
                最后更新 {{ timeAgo(s.last_seen) }}
              </span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick, shallowRef } from 'vue'
import { NEmpty } from 'naive-ui'
import { apiGet } from '@/api'
import { useConfigStore } from '@/stores/config'
import { fmtBytes, fmtUptime, timeAgo, pct } from '@/utils/format'
import AppHeader from '@/components/AppHeader.vue'
import * as echarts from 'echarts'

interface ServerMetrics {
  cpu_percent: number; mem_used: number; mem_total: number
  swap_used: number; swap_total: number
  disk_used: number; disk_total: number
  net_up: number; net_down: number
  load1: number; load5: number; load15: number
  tcp_connections: number; process_count: number
  uptime: number; platform: string; arch: string
}
interface Spark { name: string; cpu: number[]; net_up: number[]; net_down: number[] }
interface Server {
  name: string; status: 'online' | 'offline'; location: string
  provider: string; spec: string
  metrics: ServerMetrics | null; last_seen: number; spark?: Spark | null
}

const config = useConfigStore()
const servers = ref<Server[]>([])
const loading = ref(false)
const refreshing = ref(false)
let timer: ReturnType<typeof setInterval> | null = null

// 实时时钟
const clock = ref('')
let clockTimer: ReturnType<typeof setInterval> | null = null
function tickClock() {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  clock.value = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

const onlineCount = computed(() => servers.value.filter(s => s.status === 'online').length)
const offlineCount = computed(() => servers.value.length - onlineCount.value)
const hasData = computed(() => servers.value.some(s => s.metrics))

const avgCpuN = computed(() => {
  const arr = servers.value.filter(s => s.metrics)
  if (!arr.length) return 0
  return arr.reduce((s, x) => s + (x.metrics?.cpu_percent || 0), 0) / arr.length
})
const avgMemN = computed(() => {
  const arr = servers.value.filter(s => s.metrics)
  const u = arr.reduce((s, x) => s + (x.metrics?.mem_used || 0), 0)
  const t = arr.reduce((s, x) => s + (x.metrics?.mem_total || 0), 0)
  return t ? (u / t) * 100 : 0
})
const avgDiskN = computed(() => {
  const arr = servers.value.filter(s => s.metrics)
  const u = arr.reduce((s, x) => s + (x.metrics?.disk_used || 0), 0)
  const t = arr.reduce((s, x) => s + (x.metrics?.disk_total || 0), 0)
  return t ? (u / t) * 100 : 0
})
const totalUp = computed(() => servers.value.reduce((s, x) => s + (x.metrics?.net_up || 0), 0))
const totalDown = computed(() => servers.value.reduce((s, x) => s + (x.metrics?.net_down || 0), 0))

// 数字滚动动画
function useCountUp(src: () => number) {
  const disp = ref(0)
  let raf = 0
  const DUR = 650
  watch(src, (to) => {
    cancelAnimationFrame(raf)
    const from = disp.value
    const start = performance.now()
    const tick = (now: number) => {
      const p = Math.min((now - start) / DUR, 1)
      const e = 1 - Math.pow(1 - p, 3)
      disp.value = from + (to - from) * e
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
  }, { immediate: true })
  return disp
}
const dTotal = useCountUp(() => servers.value.length)
const dOnline = useCountUp(() => onlineCount.value)
const dCpu = useCountUp(() => avgCpuN.value)
const dMem = useCountUp(() => avgMemN.value)
const dDisk = useCountUp(() => avgDiskN.value)
const dUp = useCountUp(() => totalUp.value)
const dDown = useCountUp(() => totalDown.value)

function memPct(s: Server) { return s.metrics ? pct(s.metrics.mem_used, s.metrics.mem_total) : 0 }
function diskPct(s: Server) { return s.metrics ? pct(s.metrics.disk_used, s.metrics.disk_total) : 0 }
function swapPct(s: Server) { return s.metrics ? pct(s.metrics.swap_used, s.metrics.swap_total) : 0 }
function lvl(v: number) { return v >= 90 ? 'crit' : v >= 70 ? 'warn' : 'ok' }

// 仪表盘几何
const GAUGE_R = 26
const GAUGE_C = 2 * Math.PI * GAUGE_R
function dashOff(v: number) { return GAUGE_C * (1 - Math.min(Math.max(v, 0), 100) / 100) }
function gauges(s: Server) {
  const cpu = s.metrics!.cpu_percent
  const mp = memPct(s), dp = diskPct(s)
  return [
    { key: 'CPU', val: cpu, lvl: lvl(cpu), off: dashOff(cpu), sub: '' },
    { key: '内存', val: mp, lvl: lvl(mp), off: dashOff(mp), sub: `${fmtBytes(s.metrics!.mem_used)} / ${fmtBytes(s.metrics!.mem_total)}` },
    { key: '磁盘', val: dp, lvl: lvl(dp), off: dashOff(dp), sub: `${fmtBytes(s.metrics!.disk_used)} / ${fmtBytes(s.metrics!.disk_total)}` },
  ]
}

// 迷你趋势图路径
const SPARK_H = 34
function sparkPts(arr: number[]) {
  const n = arr.length
  if (n < 2) return [] as [number, number][]
  return arr.map((v, i): [number, number] => {
    const x = (i / (n - 1)) * 100
    const y = SPARK_H - (Math.min(Math.max(v, 0), 100) / 100) * (SPARK_H - 3) - 1.5
    return [x, y]
  })
}
function sparkLine(arr: number[]) {
  const pts = sparkPts(arr)
  if (!pts.length) return ''
  return pts.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(2)},${p[1].toFixed(2)}`).join(' ')
}
function sparkArea(arr: number[]) {
  const line = sparkLine(arr)
  if (!line) return ''
  return `${line} L100,${SPARK_H} L0,${SPARK_H} Z`
}

async function fetchData() {
  try {
    const [pub, spk] = await Promise.all([
      apiGet<{ servers: Server[] }>('/api/monitor/public'),
      apiGet<{ servers: Spark[] }>('/api/monitor/public/sparklines?range=1h').catch(() => null),
    ])
    const sparks: Record<string, Spark> = {}
    if (spk?.servers) for (const s of spk.servers) sparks[s.name] = s
    const list = Array.isArray(pub?.servers) ? pub.servers : []
    for (const s of list) s.spark = sparks[s.name] || null
    servers.value = list
  } catch {}
}

async function doRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  await fetchData()
  setTimeout(() => { refreshing.value = false }, 600)
}

// --- 可用性热力图（Y=机器, X=时间桶）---
const heatEl = ref<HTMLElement | null>(null)
const heatChart = shallowRef<echarts.ECharts | null>(null)
const heatData = ref<any>(null)
const heatRange = ref('24h')
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
  try {
    const data = await apiGet<any>(`/api/monitor/heatmap?range=${range}`)
    heatData.value = data
    await nextTick()
    renderHeatmap()
  } catch {}
}
function renderHeatmap() {
  const data = heatData.value
  if (!heatEl.value || !data) return
  const servers: any[] = data.servers || []
  if (!servers.length) { heatChart.value?.clear(); return }
  if (!heatChart.value) heatChart.value = echarts.init(heatEl.value)
  const chart = heatChart.value
  const buckets: number[] = data.buckets || []
  const matrix: number[][] = data.matrix || []
  const range = data.range || '24h'
  const pts: [number, number, number][] = []
  for (let y = 0; y < servers.length; y++) {
    const row = matrix[y] || []
    for (let x = 0; x < buckets.length; x++) pts.push([x, y, row[x] ?? 2])
  }
  const xLabels = buckets.map((t: number) => fmtHeatTime(t, range))
  const yLabels = servers.map((s: any) => s.name)
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
    xAxis: { type: 'category', data: xLabels, splitArea: { show: true }, axisLabel: { fontSize: 10, hideOverlap: true }, axisTick: { show: false }, axisLine: { show: false } },
    yAxis: { type: 'category', data: yLabels, splitArea: { show: true }, axisLabel: { fontSize: 11 }, axisTick: { show: false }, axisLine: { show: false } },
    visualMap: { min: 0, max: 2, calculable: false, show: false, inRange: { color: ['#6f8f76', '#d9a441', '#c2685c'] } },
    series: [{ type: 'heatmap', data: pts, progressive: 0, itemStyle: { borderColor: '#fff', borderWidth: 1, borderRadius: 2 }, emphasis: { itemStyle: { borderColor: '#333', borderWidth: 1 } } }],
  })
  chart.resize()
}
function onWinResize() { if (heatData.value) renderHeatmap() }

onMounted(async () => {
  loading.value = true
  tickClock()
  clockTimer = setInterval(tickClock, 1000)
  await fetchData()
  loading.value = false
  timer = setInterval(fetchData, 30000)
  loadHeatmap('24h')
  window.addEventListener('resize', onWinResize)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (clockTimer) clearInterval(clockTimer)
  window.removeEventListener('resize', onWinResize)
  heatChart.value?.dispose()
})
</script>

<style scoped>
.monitor-page { min-height: 100vh; background: var(--bg); }
.monitor-content { padding: 22px 24px 56px; max-width: 1320px; margin: 0 auto; }

/* ===== 顶部标题 ===== */
.hero { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; margin-bottom: 20px; flex-wrap: wrap; }
.hero-title {
  font-size: 24px; font-weight: 760; letter-spacing: -.02em; margin: 0; line-height: 1.1;
  background: linear-gradient(120deg, var(--text) 30%, var(--accent-strong)); -webkit-background-clip: text; background-clip: text; color: transparent;
}
.hero-sub { margin: 4px 0 0; font-size: 13px; color: var(--text-3); }
.hero-right { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.clock {
  display: inline-flex; align-items: center; gap: 6px; font-variant-numeric: tabular-nums;
  font-size: 13px; font-weight: 650; color: var(--text-2); letter-spacing: .02em;
  padding: 6px 12px; background: var(--card); border: 1px solid var(--border); border-radius: 9px;
}
.clock svg { opacity: .55; }
.auto-badge { display: inline-flex; align-items: center; gap: 7px; font-size: 12px; color: var(--text-3); font-weight: 500; }
.auto-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--accent); box-shadow: 0 0 0 0 rgba(111,143,118,.5); }
.auto-dot.active { animation: ping 1s ease-out; }
@keyframes ping { 0% { box-shadow: 0 0 0 0 rgba(111,143,118,.5); } 100% { box-shadow: 0 0 0 8px rgba(111,143,118,0); } }
.refresh-btn {
  display: inline-grid; place-items: center; width: 32px; height: 32px; border-radius: 9px;
  border: 1px solid var(--border); background: var(--card); color: var(--text-2); cursor: pointer; transition: all .15s;
}
.refresh-btn:hover:not(:disabled) { color: var(--accent-strong); border-color: var(--accent); background: var(--accent-soft); }
.refresh-btn:disabled { opacity: .5; cursor: not-allowed; }
.refresh-btn.spinning svg { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* ===== 汇总卡片 ===== */
.summary-grid { display: grid; grid-template-columns: repeat(6, 1fr); gap: 12px; margin-bottom: 22px; }
.summary-card {
  display: flex; align-items: center; gap: 12px; padding: 14px 16px;
  background: var(--card); border: 1px solid var(--border); border-radius: 14px;
  box-shadow: var(--shadow-sm); transition: box-shadow .2s, transform .2s; min-width: 0;
}
.summary-card:hover { box-shadow: var(--shadow); transform: translateY(-2px); }
.summary-icon { width: 42px; height: 42px; border-radius: 12px; display: grid; place-items: center; flex-shrink: 0; color: #fff; }
.summary-icon.i-server { background: linear-gradient(135deg, #7fa588, #5c7c63); }
.summary-icon.i-cpu { background: linear-gradient(135deg, #6f96b8, #4f7799); }
.summary-icon.i-mem { background: linear-gradient(135deg, #b892c9, #9166a8); }
.summary-icon.i-disk { background: linear-gradient(135deg, #d3a95a, #bf9540); }
.summary-icon.i-up { background: linear-gradient(135deg, #7fa588, #5c7c63); }
.summary-icon.i-down { background: linear-gradient(135deg, #6f96b8, #4f7799); }
.summary-body { display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1; }
.summary-val { font-size: 23px; font-weight: 770; letter-spacing: -.03em; color: var(--text); font-variant-numeric: tabular-nums; line-height: 1.05; }
.summary-val.small { font-size: 18px; }
.summary-val i { font-size: 13px; font-weight: 600; color: var(--text-3); font-style: normal; margin-left: 1px; }
.summary-label { font-size: 11.5px; color: var(--text-3); font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.summary-label b { font-weight: 650; }
.ok-text { color: var(--accent-strong); } .crit-text { color: var(--danger); }
.mini-bar { height: 4px; border-radius: 3px; background: var(--bg); overflow: hidden; margin-top: 5px; }
.mini-fill { height: 100%; border-radius: 3px; transition: width .8s cubic-bezier(.4,0,.2,1); }
.mini-fill.ok { background: var(--accent); } .mini-fill.warn { background: var(--warn); } .mini-fill.crit { background: var(--danger); }

/* ===== 分节栏 ===== */
.section-bar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; gap: 12px; flex-wrap: wrap; }
.section-title { font-size: 14px; font-weight: 680; color: var(--text); }
.heat-range { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; }
.heat-range-btn {
  padding: 4px 11px; border-radius: 8px; border: 1px solid var(--border);
  background: var(--card); color: var(--text-3); font-size: 12px; font-weight: 600; cursor: pointer; transition: all .15s; font-family: inherit;
}
.heat-range-btn:hover { color: var(--text); }
.heat-range-btn.active { background: var(--accent-soft); color: var(--accent-strong); border-color: transparent; }
.heat-legend { display: inline-flex; align-items: center; gap: 4px; font-size: 11px; color: var(--text-3); margin-left: 10px; }
.hm-dot { width: 9px; height: 9px; border-radius: 50%; display: inline-block; margin-left: 6px; }
.hm-dot.ok { background: #6f8f76; } .hm-dot.warn { background: #d9a441; } .hm-dot.crit { background: #c2685c; }

.heatmap-card { background: var(--card); border: 1px solid var(--border); border-radius: 14px; box-shadow: var(--shadow-sm); padding: 16px 18px; margin-bottom: 26px; }
.heat-chart { width: 100%; height: 340px; min-height: 120px; }
.heat-chart:empty { display: none; }
.heat-empty { text-align: center; color: var(--text-3); padding: 24px; font-size: 13px; }

/* ===== 服务器卡片 ===== */
.server-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 16px; }

.server-card {
  position: relative; overflow: hidden;
  background: var(--card); border: 1px solid var(--border); border-radius: 16px;
  box-shadow: var(--shadow-sm); transition: box-shadow .25s ease, transform .25s ease, border-color .25s;
  animation: cardIn .5s cubic-bezier(.22,1,.36,1) backwards; animation-delay: calc(var(--i) * 45ms);
}
@keyframes cardIn { from { opacity: 0; transform: translateY(14px); } to { opacity: 1; transform: none; } }
.server-card:hover { box-shadow: var(--shadow); transform: translateY(-3px); border-color: #ddd9cf; }

.card-top-line { height: 3px; }
.card-top-line.online { background: linear-gradient(90deg, #5c7c63, #8fb097); }
.card-top-line.offline { background: linear-gradient(90deg, #c2685c, #d98a7f); }

.card-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; padding: 14px 16px 8px; }
.card-title { display: flex; align-items: flex-start; gap: 8px; min-width: 0; flex: 1; }
.status-beacon { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; margin-top: 5px; }
.status-beacon.online { background: #5c9c6e; box-shadow: 0 0 8px rgba(92,156,110,.55); animation: beacon 2.4s ease-in-out infinite; }
.status-beacon.offline { background: #c2685c; box-shadow: 0 0 6px rgba(194,104,92,.35); }
@keyframes beacon { 0%,100% { opacity: 1; } 50% { opacity: .35; } }
.server-name {
  font-weight: 680; font-size: 14.5px; line-height: 1.35; min-width: 0; color: var(--text);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; word-break: break-word;
}
.status-badge {
  display: inline-flex; align-items: center; gap: 5px; flex-shrink: 0; white-space: nowrap;
  padding: 3px 9px; border-radius: 20px; font-size: 11px; font-weight: 650;
}
.status-badge .badge-dot { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.status-badge.online { background: var(--accent-soft); color: var(--accent-strong); }
.status-badge.offline { background: var(--danger-soft); color: var(--danger); }

.tag-line { display: flex; flex-wrap: wrap; gap: 6px; padding: 0 16px 12px; }
.tag { padding: 2px 8px; border-radius: 6px; font-size: 11px; font-weight: 500; background: var(--bg-soft); color: var(--text-2); white-space: nowrap; }
.tag.loc { background: var(--accent-soft); color: var(--accent-strong); }
.tag.spec { font-variant-numeric: tabular-nums; }

/* 仪表盘 */
.gauges { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6px; padding: 2px 12px 12px; }
.gauge { position: relative; display: flex; flex-direction: column; align-items: center; text-align: center; }
.gauge-svg { width: 100%; max-width: 76px; aspect-ratio: 1; transform: rotate(-90deg); }
.gauge-bg { fill: none; stroke: var(--bg); stroke-width: 5; }
.gauge-fg { fill: none; stroke-width: 5; stroke-linecap: round; transition: stroke-dashoffset 1s cubic-bezier(.4,0,.2,1); }
.gauge-center { position: absolute; top: 0; left: 0; right: 0; display: grid; place-items: center; pointer-events: none; }
.gauge-svg + .gauge-center { height: 100%; max-height: 76px; }
.gauge-center { height: min(76px, 100%); }
.gauge-val { font-size: 15px; font-weight: 750; color: var(--text); font-variant-numeric: tabular-nums; line-height: 1; }
.gauge-val i { font-size: 9px; font-weight: 600; font-style: normal; color: var(--text-3); margin-left: 1px; }
.gauge-val.warn { color: var(--warn); } .gauge-val.crit { color: var(--danger); }
.gauge-meta { display: flex; flex-direction: column; gap: 1px; margin-top: 3px; width: 100%; }
.gauge-label { font-size: 11px; font-weight: 650; color: var(--text-2); }
.gauge-sub { font-size: 9.5px; color: var(--text-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }

/* 迷你趋势图 */
.spark-wrap { position: relative; margin: 0 16px 12px; height: 40px; border-radius: 9px; background: var(--bg-soft); overflow: hidden; }
.spark { width: 100%; height: 100%; display: block; }
.spark-area { fill: url(#spark-grad); }
.spark-line { fill: none; stroke: var(--accent); stroke-width: 1.6; stroke-linejoin: round; stroke-linecap: round; vector-effect: non-scaling-stroke; }
.spark-tag { position: absolute; top: 5px; left: 8px; font-size: 9.5px; color: var(--text-3); font-weight: 600; letter-spacing: .02em; }

/* 交换分区 */
.swap-row { display: flex; align-items: center; gap: 8px; padding: 0 16px 12px; font-size: 11px; }
.swap-label { color: var(--text-3); font-weight: 600; flex-shrink: 0; }
.swap-track { flex: 1; height: 5px; border-radius: 3px; background: var(--bg); overflow: hidden; }
.swap-fill { height: 100%; border-radius: 3px; transition: width .8s cubic-bezier(.4,0,.2,1); }
.swap-fill.ok { background: var(--accent); } .swap-fill.warn { background: var(--warn); } .swap-fill.crit { background: var(--danger); }
.swap-detail { color: var(--text-3); flex-shrink: 0; font-variant-numeric: tabular-nums; }

/* 详细信息网格 */
.info-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 7px; padding: 4px 16px 14px; border-top: 1px solid var(--border); margin: 0 0 0; }
.info-cell { display: flex; align-items: center; gap: 8px; min-width: 0; }
.info-cell svg { width: 15px; height: 15px; flex-shrink: 0; color: var(--text-3); opacity: .85; }
.info-cell > div { display: flex; flex-direction: column; min-width: 0; line-height: 1.25; }
.info-cell.wide { grid-column: span 2; }
.ic-label { font-size: 10px; color: var(--text-3); font-weight: 500; }
.ic-val { font-size: 12px; color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
.ic-val.ellip { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

.card-no-data { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 30px 16px; color: var(--text-3); font-size: 13px; }

.card-footer { padding: 9px 16px; border-top: 1px solid var(--border); }
.footer-time { display: inline-flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-3); }
.footer-time .dot { width: 6px; height: 6px; border-radius: 50%; }
.footer-time .dot.online { background: #5c9c6e; } .footer-time .dot.offline { background: #c2685c; }

/* ===== 响应式 ===== */
@media (max-width: 1080px) { .summary-grid { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 760px) {
  .monitor-content { padding: 16px 13px 40px; }
  .hero-title { font-size: 20px; }
  .summary-grid { grid-template-columns: repeat(2, 1fr); gap: 10px; }
  .server-grid { grid-template-columns: 1fr; }
  .heat-legend { display: none; }
}
@media (max-width: 380px) { .summary-grid { grid-template-columns: 1fr 1fr; } .summary-icon { width: 38px; height: 38px; } .summary-val { font-size: 20px; } }
</style>
