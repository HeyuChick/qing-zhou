<template>
  <div>
    <div class="detail-head">
      <n-button size="small" @click="back">返回</n-button>
      <h2 class="page-title">
        <span class="status-beacon" :class="server?.status || ''" />
        {{ server?.name || '加载中…' }}
      </h2>
      <n-tag v-if="server?.location" size="small" :bordered="false">{{ server.location }}</n-tag>
    </div>

    <n-spin :show="loading">
      <template v-if="server">
        <!-- 资产信息条 -->
        <div class="asset-strip">
          <span v-if="server.provider" class="chip">{{ server.provider }}</span>
          <span v-if="server.spec" class="chip">{{ server.spec }}</span>
          <span v-if="server.price" class="chip">¥{{ server.price }}/月</span>
          <span v-if="server.days_left != null" class="chip" :class="{ danger: server.days_left <= 7 }">剩余 {{ server.days_left }} 天</span>
          <span v-if="server.metrics" class="chip">运行 {{ fmtUptime(server.metrics.uptime) }}</span>
        </div>

        <!-- 实时指标卡 -->
        <div v-if="server.metrics" class="metric-grid">
          <div class="metric-card">
            <span class="m-label">CPU</span>
            <span class="m-val" :class="pctClass(server.metrics.cpu_percent)">{{ server.metrics.cpu_percent.toFixed(1) }}%</span>
            <n-progress type="line" :percentage="server.metrics.cpu_percent" :show-indicator="false" :height="6" :color="pctColor(server.metrics.cpu_percent)" />
          </div>
          <div class="metric-card">
            <span class="m-label">内存</span>
            <span class="m-val" :class="pctClass(memPct)">{{ memPct.toFixed(1) }}%</span>
            <span class="m-sub">{{ fmtBytes(server.metrics.mem_used) }} / {{ fmtBytes(server.metrics.mem_total) }}</span>
            <n-progress type="line" :percentage="memPct" :show-indicator="false" :height="6" :color="pctColor(memPct)" />
          </div>
          <div class="metric-card">
            <span class="m-label">磁盘</span>
            <span class="m-val" :class="pctClass(diskPct)">{{ diskPct.toFixed(1) }}%</span>
            <span class="m-sub">{{ fmtBytes(server.metrics.disk_used) }} / {{ fmtBytes(server.metrics.disk_total) }}</span>
            <n-progress type="line" :percentage="diskPct" :show-indicator="false" :height="6" :color="pctColor(diskPct)" />
          </div>
          <div class="metric-card">
            <span class="m-label">网络上行</span>
            <span class="m-val">{{ fmtBytes(server.metrics.net_tx) }}/s</span>
          </div>
          <div class="metric-card">
            <span class="m-label">网络下行</span>
            <span class="m-val">{{ fmtBytes(server.metrics.net_rx) }}/s</span>
          </div>
          <div class="metric-card">
            <span class="m-label">系统负载</span>
            <span class="m-val">{{ server.metrics.load1.toFixed(2) }}</span>
            <span class="m-sub">{{ server.metrics.load5.toFixed(2) }} / {{ server.metrics.load15.toFixed(2) }}</span>
          </div>
        </div>

        <!-- 大图趋势 -->
        <n-card size="small" style="margin-top:16px;">
          <div class="chart-toolbar">
            <n-radio-group v-model:value="range" size="small" @update:value="loadChart">
              <n-radio-button v-for="r in ranges" :key="r.value" :value="r.value">{{ r.label }}</n-radio-button>
            </n-radio-group>
            <n-checkbox-group v-model:value="series" size="small" @update:value="drawChart">
              <n-space :size="4">
                <n-checkbox value="cpu">CPU</n-checkbox>
                <n-checkbox value="mem">内存</n-checkbox>
                <n-checkbox value="net">网络</n-checkbox>
                <n-checkbox value="disk">磁盘</n-checkbox>
                <n-checkbox value="load">负载</n-checkbox>
              </n-space>
            </n-checkbox-group>
          </div>
          <div ref="chartEl" class="big-chart" />
        </n-card>
      </template>
      <n-empty v-else-if="!loading" description="服务器不存在" style="padding:60px 0;" />
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NSpin, NCard, NButton, NTag, NProgress, NRadioGroup, NRadioButton, NCheckboxGroup, NCheckbox, NSpace, NEmpty
} from 'naive-ui'
import { apiGet } from '@/api'
import { fmtBytes, fmtUptime, pct } from '@/utils/format'
import * as echarts from 'echarts'

const route = useRoute()
const router = useRouter()
const sid = Number(route.params.id)

const loading = ref(true)
const server = ref<any>(null)
const range = ref('24h')
const series = ref(['cpu', 'mem', 'net'])
const chartEl = ref<HTMLElement | null>(null)
let chart: echarts.ECharts | null = null
let metrics: any[] = []
let resizeObs: ResizeObserver | null = null

const ranges = [
  { label: '1h', value: '1h' }, { label: '6h', value: '6h' },
  { label: '24h', value: '24h' }, { label: '7d', value: '7d' }, { label: '30d', value: '30d' },
]

const memPct = computed(() => server.value?.metrics ? pct(server.value.metrics.mem_used, server.value.metrics.mem_total) : 0)
const diskPct = computed(() => server.value?.metrics ? pct(server.value.metrics.disk_used, server.value.metrics.disk_total) : 0)

function pctClass(v: number) { return v >= 90 ? 'crit' : v >= 70 ? 'warn' : 'ok' }
function pctColor(v: number) { return v >= 90 ? '#c2685c' : v >= 70 ? '#bf9540' : '#6f8f76' }
function back() { router.push({ name: 'admin-monitor' }) }

async function loadServer() {
  try {
    const list = await apiGet<any[]>('/api/admin/monitor/servers')
    server.value = (list || []).find((s: any) => s.id === sid)
  } catch {}
}

async function loadChart() {
  try {
    const data = await apiGet<any>(`/api/admin/monitor/servers/${sid}/metrics?range=${range.value}`)
    metrics = data?.data || []
    await nextTick()
    drawChart()
  } catch {}
}

function drawChart() {
  if (!chartEl.value) return
  if (!chart) chart = echarts.init(chartEl.value)
  const times = metrics.map((m: any) => {
    const d = new Date(m.ts * 1000)
    return range.value === '1h' || range.value === '6h'
      ? `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`
      : `${d.getMonth() + 1}/${d.getDate()} ${d.getHours()}:00`
  })
  const s: any[] = []
  if (series.value.includes('cpu')) s.push({ name: 'CPU %', type: 'line', smooth: true, showSymbol: false, lineStyle: { width: 1.5 }, data: metrics.map(m => m.cpu_percent?.toFixed(1)) })
  if (series.value.includes('mem')) s.push({ name: '内存 %', type: 'line', smooth: true, showSymbol: false, lineStyle: { width: 1.5 }, data: metrics.map(m => pct(m.mem_used, m.mem_total).toFixed(1)) })
  if (series.value.includes('disk')) s.push({ name: '磁盘 %', type: 'line', smooth: true, showSymbol: false, lineStyle: { width: 1.5 }, data: metrics.map(m => pct(m.disk_used, m.disk_total).toFixed(1)) })
  if (series.value.includes('net')) {
    s.push({ name: '上行 MB/s', type: 'line', yAxisIndex: 1, smooth: true, showSymbol: false, lineStyle: { width: 1 }, data: metrics.map(m => ((m.net_tx || 0) / 1048576).toFixed(2)) })
    s.push({ name: '下行 MB/s', type: 'line', yAxisIndex: 1, smooth: true, showSymbol: false, lineStyle: { width: 1 }, data: metrics.map(m => ((m.net_rx || 0) / 1048576).toFixed(2)) })
  }
  if (series.value.includes('load')) s.push({ name: '负载', type: 'line', yAxisIndex: 1, smooth: true, showSymbol: false, lineStyle: { width: 1 }, data: metrics.map(m => m.load1?.toFixed(2)) })

  chart.setOption({
    tooltip: { trigger: 'axis' },
    legend: { top: 0, textStyle: { fontSize: 11 } },
    grid: { left: 44, right: 48, top: 32, bottom: 28 },
    xAxis: { type: 'category', data: times, axisLabel: { fontSize: 10 } },
    yAxis: [
      { type: 'value', name: '%', max: 100, axisLabel: { fontSize: 10 } },
      { type: 'value', name: 'MB/s', axisLabel: { fontSize: 10 } },
    ],
    series: s,
  }, true)
}

onMounted(async () => {
  loading.value = true
  await loadServer()
  loading.value = false
  await nextTick()
  await loadChart()
  // 监听容器尺寸变化，自动 resize 图表
  if (chartEl.value && typeof ResizeObserver !== 'undefined') {
    resizeObs = new ResizeObserver(() => chart?.resize())
    resizeObs.observe(chartEl.value)
  }
})
onUnmounted(() => {
  resizeObs?.disconnect()
  chart?.dispose()
})
</script>

<style scoped>
.detail-head { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; }
.page-title { font-size: 20px; margin: 0; display: flex; align-items: center; gap: 8px; }
.status-beacon { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; }
.status-beacon.online { background: #10b981; box-shadow: 0 0 8px rgba(16,185,129,.5); }
.status-beacon.offline { background: #ef4444; }

.asset-strip { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 16px; }
.chip { padding: 3px 10px; border-radius: 7px; background: var(--bg-soft); font-size: 12px; color: var(--text-2); }
.chip.danger { background: var(--danger-soft); color: var(--danger); }

.metric-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 12px; }
.metric-card { display: flex; flex-direction: column; gap: 4px; padding: 14px; background: var(--card); border: 1px solid var(--border); border-radius: 12px; }
.m-label { font-size: 11px; color: var(--text-3); font-weight: 600; text-transform: uppercase; letter-spacing: .05em; }
.m-val { font-size: 22px; font-weight: 720; }
.m-val.ok { color: var(--accent-strong); }
.m-val.warn { color: var(--warn); }
.m-val.crit { color: var(--danger); }
.m-sub { font-size: 11px; color: var(--text-3); }

.chart-toolbar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.big-chart { height: 380px; }
@media (max-width: 768px) { .big-chart { height: 280px; } }
</style>
