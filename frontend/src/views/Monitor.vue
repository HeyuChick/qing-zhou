<template>
  <div class="monitor-page">
    <AppHeader />

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
        <!-- 汇总卡片 -->
        <div class="summary-grid">
          <div class="summary-card">
            <div class="summary-icon" style="background:#e9f0eb;color:#5c7c63;">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ servers.length }}</span>
              <span class="summary-label">服务器</span>
            </div>
          </div>
          <div class="summary-card">
            <div class="summary-icon" style="background:#ecfdf5;color:#10b981;">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val" style="color:#059669;">{{ onlineCount }}</span>
              <span class="summary-label">在线</span>
            </div>
          </div>
          <div class="summary-card">
            <div class="summary-icon" style="background:#fef2f2;color:#ef4444;">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val" style="color:#dc2626;">{{ servers.length - onlineCount }}</span>
              <span class="summary-label">离线</span>
            </div>
          </div>
          <div class="summary-card">
            <div class="summary-icon" style="background:#e8eef5;color:#5e7a99;">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ avgCpu }}%</span>
              <span class="summary-label">平均 CPU</span>
            </div>
          </div>
          <div class="summary-card">
            <div class="summary-icon" style="background:#f7efda;color:#bf9540;">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 19v-9a6 6 0 0 1 12 0v9"/><rect x="2" y="19" width="20" height="2" rx="1"/></svg>
            </div>
            <div class="summary-body">
              <span class="summary-val">{{ avgMem }}%</span>
              <span class="summary-label">平均内存</span>
            </div>
          </div>
        </div>

        <!-- 刷新栏 -->
        <div class="refresh-bar">
          <div class="auto-badge">
            <span class="auto-dot" :class="{ active: refreshing }"></span>
            自动刷新 · 每 30 秒
          </div>
          <div class="heat-range">
            <button v-for="r in heatRanges" :key="r.value" class="heat-range-btn" :class="{ active: heatRange === r.value }" @click="loadHeatmap(r.value)">{{ r.label }}</button>
            <span class="heat-legend"><i class="hm-dot ok"></i>正常 <i class="hm-dot warn"></i>高负载 <i class="hm-dot crit"></i>离线/无数据</span>
          </div>
          <button class="refresh-btn" :class="{ spinning: refreshing }" @click="doRefresh" :disabled="refreshing">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
            {{ refreshing ? '刷新中...' : '刷新' }}
          </button>
        </div>

        <!-- 可用性热力图 -->
        <div class="heatmap-card">
          <div class="heatmap-title">可用性热力图</div>
          <div ref="heatEl" class="heat-chart"></div>
          <div v-if="!heatData?.servers?.length" class="heat-empty">暂无探针服务器</div>
        </div>

        <!-- 空状态 -->
        <n-empty v-if="!loading && servers.length === 0" description="暂无服务器数据" style="padding: 80px 0;" />

        <!-- 服务器卡片 -->
        <div class="server-grid">
          <div v-for="s in servers" :key="s.name" class="server-card">
            <!-- 顶部装饰线 -->
            <div class="card-top-line" :class="s.status" />

            <!-- 头部 -->
            <div class="card-header">
              <div class="card-title">
                <span class="status-beacon" :class="s.status" />
                <span class="server-name">{{ s.name }}</span>
              </div>
              <div class="header-right">
                <span v-if="s.location" class="location-tag">{{ s.location }}</span>
                <span class="status-badge" :class="s.status">{{ s.status === 'online' ? '运行中' : '离线' }}</span>
              </div>
            </div>

            <!-- 指标 -->
            <div v-if="s.metrics" class="card-metrics">
              <!-- CPU -->
              <div class="metric-block">
                <div class="metric-head">
                  <span class="metric-name">CPU</span>
                  <span class="metric-pct" :class="pctClass(s.metrics.cpu_percent)">{{ s.metrics.cpu_percent.toFixed(1) }}%</span>
                </div>
                <div class="bar-track">
                  <div class="bar-fill" :class="pctClass(s.metrics.cpu_percent)" :style="{ width: Math.min(s.metrics.cpu_percent, 100) + '%' }" />
                </div>
              </div>

              <!-- 内存 -->
              <div class="metric-block">
                <div class="metric-head">
                  <span class="metric-name">内存</span>
                  <span class="metric-pct" :class="pctClass(memPct(s))">{{ memPct(s).toFixed(1) }}%</span>
                </div>
                <div class="bar-track">
                  <div class="bar-fill" :class="pctClass(memPct(s))" :style="{ width: Math.min(memPct(s), 100) + '%' }" />
                </div>
                <div class="metric-detail">{{ fmtBytes(s.metrics.mem_used) }} / {{ fmtBytes(s.metrics.mem_total) }}</div>
              </div>

              <!-- 磁盘 -->
              <div class="metric-block">
                <div class="metric-head">
                  <span class="metric-name">磁盘</span>
                  <span class="metric-pct" :class="pctClass(diskPct(s))">{{ diskPct(s).toFixed(1) }}%</span>
                </div>
                <div class="bar-track">
                  <div class="bar-fill" :class="pctClass(diskPct(s))" :style="{ width: Math.min(diskPct(s), 100) + '%' }" />
                </div>
                <div class="metric-detail">{{ fmtBytes(s.metrics.disk_used) }} / {{ fmtBytes(s.metrics.disk_total) }}</div>
              </div>

              <!-- 底部信息条 -->
              <div class="info-strip">
                <div class="info-cell">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="17 1 21 5 17 9"/><path d="M3 11V9a4 4 0 0 1 4-4h14"/></svg>
                  <span>↑ {{ fmtBytes(s.metrics.net_up) }}/s</span>
                </div>
                <div class="info-cell">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="7 23 3 19 7 15"/><path d="M21 13v2a4 4 0 0 1-4 4H3"/></svg>
                  <span>↓ {{ fmtBytes(s.metrics.net_down) }}/s</span>
                </div>
                <div class="info-cell">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20V10"/><path d="M18 20V4"/><path d="M6 20v-4"/></svg>
                  <span>负载 {{ s.metrics.load1.toFixed(2) }}</span>
                </div>
                <div class="info-cell">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
                  <span>{{ fmtUptime(s.metrics.uptime) }}</span>
                </div>
              </div>
            </div>

            <div v-else class="card-no-data">
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity=".3"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
              <span>暂无数据</span>
            </div>

            <!-- 底部 -->
            <div class="card-footer">
              <span class="footer-time">{{ timeAgo(s.last_seen) }}</span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, shallowRef } from 'vue'
import { NEmpty } from 'naive-ui'
import { apiGet } from '@/api'
import { useConfigStore } from '@/stores/config'
import { fmtBytes, fmtUptime, timeAgo, pct } from '@/utils/format'
import AppHeader from '@/components/AppHeader.vue'
import * as echarts from 'echarts'

interface ServerMetrics {
  cpu_percent: number; mem_used: number; mem_total: number
  disk_used: number; disk_total: number
  net_up: number; net_down: number
  load1: number; load5: number; load15: number; uptime: number
}
interface Server {
  name: string; status: 'online' | 'offline'; location: string
  metrics: ServerMetrics | null; last_seen: number
}

const config = useConfigStore()
const servers = ref<Server[]>([])
const loading = ref(false)
const refreshing = ref(false)
let timer: ReturnType<typeof setInterval> | null = null

const onlineCount = computed(() => servers.value.filter(s => s.status === 'online').length)

const avgCpu = computed(() => {
  const arr = servers.value.filter(s => s.metrics)
  if (!arr.length) return '—'
  return (arr.reduce((s, x) => s + (x.metrics?.cpu_percent || 0), 0) / arr.length).toFixed(1)
})
const avgMem = computed(() => {
  const arr = servers.value.filter(s => s.metrics)
  if (!arr.length) return '—'
  const u = arr.reduce((s, x) => s + (x.metrics?.mem_used || 0), 0)
  const t = arr.reduce((s, x) => s + (x.metrics?.mem_total || 0), 0)
  return t ? ((u / t) * 100).toFixed(1) : '—'
})

function memPct(s: Server) { return s.metrics ? pct(s.metrics.mem_used, s.metrics.mem_total) : 0 }
function diskPct(s: Server) { return s.metrics ? pct(s.metrics.disk_used, s.metrics.disk_total) : 0 }

function pctClass(v: number) {
  if (v >= 90) return 'crit'
  if (v >= 70) return 'warn'
  return 'ok'
}

async function fetchData() {
  try {
    const data = await apiGet<{ servers: Server[] }>('/api/monitor/public')
    servers.value = Array.isArray(data?.servers) ? data.servers : []
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
  if (!servers.length) {
    heatChart.value?.clear()
    return
  }
  if (!heatChart.value) heatChart.value = echarts.init(heatEl.value)
  const chart = heatChart.value
  const buckets: number[] = data.buckets || []
  const matrix: number[][] = data.matrix || []
  const range = data.range || '24h'
  const pts: [number, number, number][] = []
  for (let y = 0; y < servers.length; y++) {
    const row = matrix[y] || []
    for (let x = 0; x < buckets.length; x++) {
      pts.push([x, y, row[x] ?? 2])
    }
  }
  const xLabels = buckets.map((t: number) => fmtHeatTime(t, range))
  const yLabels = servers.map((s: any) => s.name)
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
    grid: { left: 8, right: 8, top: 12, bottom: 4, containLabel: true },
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
function onWinResize() { heatChart.value?.resize() }

onMounted(async () => {
  loading.value = true
  await fetchData()
  loading.value = false
  timer = setInterval(fetchData, 30000)
  loadHeatmap('24h')
  window.addEventListener('resize', onWinResize)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  window.removeEventListener('resize', onWinResize)
  heatChart.value?.dispose()
})
</script>

<style scoped>
.monitor-page { min-height: 100vh; background: var(--bg); }
.monitor-content { padding: 20px 24px 48px; max-width: 1280px; margin: 0 auto; }

/* ===== 汇总卡片 ===== */
.summary-grid { display: grid; grid-template-columns: repeat(5, 1fr); gap: 12px; margin-bottom: 18px; }
@media (max-width: 900px) { .summary-grid { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 540px) { .summary-grid { grid-template-columns: repeat(2, 1fr); } }

.summary-card {
  display: flex; align-items: center; gap: 12px; padding: 16px;
  background: var(--card); border: 1px solid var(--border); border-radius: 14px;
  box-shadow: var(--shadow-sm); transition: box-shadow .2s;
}
.summary-card:hover { box-shadow: var(--shadow); }
.summary-icon {
  width: 40px; height: 40px; border-radius: 12px; display: grid; place-items: center; flex-shrink: 0;
}
.summary-body { display: flex; flex-direction: column; gap: 2px; }
.summary-val { font-size: 22px; font-weight: 750; letter-spacing: -.03em; }
.summary-label { font-size: 12px; color: var(--text-3); font-weight: 500; }

/* ===== 刷新栏 ===== */
.refresh-bar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }
.auto-badge { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-3); font-weight: 500; }
.auto-dot { width: 6px; height: 6px; border-radius: 50%; background: #10b981; box-shadow: 0 0 6px rgba(16,185,129,.5); }
.auto-dot.active { animation: blink .6s ease-in-out; }
@keyframes blink { 0%,100%{ opacity:1 } 50%{ opacity:.2 } }

.refresh-btn {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 7px 16px; border-radius: 9px; border: 1px solid var(--border);
  background: #fff; color: var(--text-2); font-size: 13px; font-weight: 600;
  cursor: pointer; transition: all .15s; font-family: inherit;
}
.refresh-btn:hover:not(:disabled) { background: var(--bg-soft); color: var(--text); border-color: #ddd9cf; }
.refresh-btn:disabled { opacity: .5; cursor: not-allowed; }
.refresh-btn.spinning svg { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg) } }

/* ===== 服务器卡片网格 ===== */
.server-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px; }
@media (max-width: 1100px) { .server-grid { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 640px) { .server-grid { grid-template-columns: 1fr; } .monitor-content { padding: 16px 12px; } }

.server-card {
  position: relative; overflow: hidden;
  background: var(--card); border: 1px solid var(--border); border-radius: 16px;
  box-shadow: var(--shadow-sm); transition: all .25s ease;
}
.server-card:hover { box-shadow: var(--shadow); transform: translateY(-2px); }

.card-top-line { height: 3px; border-radius: 3px 3px 0 0; }
.card-top-line.online { background: linear-gradient(90deg, #10b981, #34d399); }
.card-top-line.offline { background: linear-gradient(90deg, #ef4444, #f87171); }

.card-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px 10px; }
.card-title { display: flex; align-items: center; gap: 10px; }
.status-beacon { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; }
.status-beacon.online { background: #10b981; box-shadow: 0 0 8px rgba(16,185,129,.5); animation: beacon 2.5s ease-in-out infinite; }
.status-beacon.offline { background: #ef4444; box-shadow: 0 0 6px rgba(239,68,68,.3); }
@keyframes beacon { 0%,100%{ opacity:1 } 50%{ opacity:.35 } }

.server-name { font-weight: 680; font-size: 14.5px; }
.header-right { display: flex; align-items: center; gap: 8px; }
.location-tag {
  padding: 2px 8px; border-radius: 6px; font-size: 11px; font-weight: 500;
  background: var(--bg); color: var(--text-3);
}
.status-badge { padding: 2px 9px; border-radius: 20px; font-size: 11px; font-weight: 600; }
.status-badge.online { background: var(--accent-soft); color: var(--accent-strong); }
.status-badge.offline { background: var(--danger-soft); color: var(--danger); }

/* 指标区域 */
.card-metrics { padding: 0 18px 14px; display: flex; flex-direction: column; gap: 12px; }
.metric-block { }
.metric-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 5px; }
.metric-name { font-size: 11.5px; font-weight: 600; color: var(--text-3); text-transform: uppercase; letter-spacing: .05em; }
.metric-pct { font-size: 13px; font-weight: 700; font-variant-numeric: tabular-nums; }
.metric-pct.ok { color: var(--accent-strong); }
.metric-pct.warn { color: var(--warn); }
.metric-pct.crit { color: var(--danger); }

.bar-track { height: 6px; border-radius: 4px; background: var(--bg); overflow: hidden; }
.bar-fill { height: 100%; border-radius: 4px; transition: width .8s cubic-bezier(.4,0,.2,1); }
.bar-fill.ok { background: linear-gradient(90deg, #5c7c63, #6f8f76); }
.bar-fill.warn { background: linear-gradient(90deg, #d97706, #fbbf24); }
.bar-fill.crit { background: linear-gradient(90deg, #dc2626, #f87171); }

.metric-detail { font-size: 11px; color: var(--text-3); margin-top: 3px; text-align: right; }

.info-strip {
  display: flex; flex-wrap: wrap; gap: 4px; margin-top: 4px;
  padding-top: 10px; border-top: 1px solid var(--border-soft, #f1efe8);
}
.info-cell {
  display: inline-flex; align-items: center; gap: 5px;
  padding: 4px 9px; border-radius: 7px;
  background: var(--bg-soft); font-size: 11px; color: var(--text-2);
}
.info-cell svg { opacity: .6; }

.card-no-data {
  display: flex; flex-direction: column; align-items: center; gap: 8px;
  padding: 28px 16px; color: var(--text-3); font-size: 13px;
}

.card-footer { padding: 8px 18px; border-top: 1px solid var(--border-soft, #f1efe8); }
.footer-time { font-size: 11px; color: var(--text-3); }

/* ===== 热力图 ===== */
.heat-range { display: flex; align-items: center; gap: 4px; }
.heat-range-btn {
  padding: 4px 10px; border-radius: 7px; border: 1px solid var(--border);
  background: var(--card); color: var(--text-3); font-size: 12px; font-weight: 600;
  cursor: pointer; transition: all .15s; font-family: inherit;
}
.heat-range-btn:hover { color: var(--text); }
.heat-range-btn.active { background: var(--accent-soft); color: var(--accent-strong); border-color: transparent; }
.heat-legend { display: inline-flex; align-items: center; gap: 4px; font-size: 11px; color: var(--text-3); margin-left: 10px; }
.hm-dot { width: 9px; height: 9px; border-radius: 50%; display: inline-block; margin-left: 6px; }
.hm-dot.ok { background: #10b981; } .hm-dot.warn { background: #fbbf24; } .hm-dot.crit { background: #ef4444; }

.heatmap-card {
  background: var(--card); border: 1px solid var(--border); border-radius: 14px;
  box-shadow: var(--shadow-sm); padding: 16px 18px; margin-bottom: 18px;
}
.heatmap-title { font-weight: 680; font-size: 14px; margin-bottom: 10px; }
.heat-chart { width: 100%; height: 340px; min-height: 180px; }
.heat-chart:empty { display: none; }
.heat-empty { text-align: center; color: var(--text-3); padding: 24px; font-size: 13px; }
@media (max-width: 768px) {
  .heat-chart { height: 260px; }
  .refresh-bar { flex-wrap: wrap; gap: 10px; }
  .heat-legend { display: none; }
}
</style>
