<template>
  <div>
    <h2 class="page-title">监控管理</h2>
    <p class="page-sub">服务器监控与告警</p>

    <!-- 汇总 -->
    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin-bottom:16px;">
      <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">总服务器</span><span style="font-size:22px;font-weight:720;">{{ dash.total_servers || 0 }}</span></div></n-card>
      <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">在线</span><span style="font-size:22px;font-weight:720;color:#10b981;">{{ dash.online || 0 }}</span></div></n-card>
      <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">离线</span><span style="font-size:22px;font-weight:720;color:var(--danger);">{{ dash.offline || 0 }}</span></div></n-card>
      <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">未读告警</span><span style="font-size:22px;font-weight:720;color:var(--warn);">{{ dash.alerts_unread || 0 }}</span></div></n-card>
    </div>

    <!-- 告警条 -->
    <n-card v-if="unreadAlerts.length" title="未读告警" size="small" style="margin-bottom:16px;">
      <div v-for="a in unreadAlerts" :key="a.id" style="display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid var(--border-soft,#f1efe8);">
        <div>
          <n-tag type="warning" size="small" bordered style="margin-right:8px;">{{ a.type }}</n-tag>
          <span style="font-size:13px;">{{ a.message }}</span>
          <span style="font-size:11px;color:var(--text-3);margin-left:8px;">{{ fmtDateTime(a.ts) }}</span>
        </div>
        <n-button size="tiny" @click="dismissAlert(a.id)">忽略</n-button>
      </div>
    </n-card>

    <!-- 服务器卡片 -->
    <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(380px,1fr));gap:14px;">
      <n-card v-for="s in servers" :key="s.id" size="small">
        <template #header>
          <div style="display:flex;align-items:center;gap:8px;">
            <span style="width:9px;height:9px;border-radius:50;flex-shrink:0;" :style="{background:s.status==='online'?'#10b981':'#ef4444',boxShadow:s.status==='online'?'0 0 8px rgba(16,185,129,.5)':'0 0 6px rgba(239,68,68,.3)'}" />
            <span style="font-weight:650;">{{ s.name }}</span>
            <n-tag v-if="s.location" size="small" :bordered="false">{{ s.location }}</n-tag>
          </div>
        </template>
        <template #header-extra>
          <n-space size="small">
            <n-button size="tiny" @click="openAsset(s)">编辑</n-button>
            <n-button size="tiny" @click="copyInstall(s)">安装命令</n-button>
          </n-space>
        </template>

        <!-- 基础信息 -->
        <div style="font-size:11px;color:var(--text-3);margin-bottom:10px;display:flex;flex-wrap:wrap;gap:4px;">
          <span v-if="s.provider" style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">{{ s.provider }}</span>
          <span v-if="s.spec" style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">{{ s.spec }}</span>
          <span v-if="s.price" style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">¥{{ s.price }}/月</span>
          <span v-if="s.days_left != null" style="padding:2px 6px;border-radius:4px;" :style="{background:s.days_left<=7?'var(--danger-soft)':'var(--bg-soft)',color:s.days_left<=7?'var(--danger)':undefined}">剩余 {{ s.days_left }} 天</span>
        </div>

        <!-- 指标 -->
        <template v-if="s.metrics">
          <div style="margin-bottom:8px;">
            <div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:3px;"><span>CPU</span><span style="font-weight:600;">{{ s.metrics.cpu_percent.toFixed(1) }}%</span></div>
            <n-progress type="line" :percentage="s.metrics.cpu_percent" :show-indicator="false" :height="6" :color="pctColor(s.metrics.cpu_percent)" />
          </div>
          <div style="margin-bottom:8px;">
            <div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:3px;"><span>内存</span><span style="font-weight:600;">{{ fmtBytes(s.metrics.mem_used) }} / {{ fmtBytes(s.metrics.mem_total) }}</span></div>
            <n-progress type="line" :percentage="memPct(s)" :show-indicator="false" :height="6" :color="pctColor(memPct(s))" />
          </div>
          <div style="margin-bottom:8px;">
            <div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:3px;"><span>磁盘</span><span style="font-weight:600;">{{ fmtBytes(s.metrics.disk_used) }} / {{ fmtBytes(s.metrics.disk_total) }}</span></div>
            <n-progress type="line" :percentage="diskPct(s)" :show-indicator="false" :height="6" :color="pctColor(diskPct(s))" />
          </div>
          <div style="display:flex;flex-wrap:wrap;gap:4px;font-size:11px;color:var(--text-2);margin-top:6px;">
            <span style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">↑ {{ fmtBytes(s.metrics.net_tx) }}/s</span>
            <span style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">↓ {{ fmtBytes(s.metrics.net_rx) }}/s</span>
            <span style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">负载 {{ s.metrics.load1.toFixed(2) }}</span>
            <span style="padding:2px 6px;background:var(--bg-soft);border-radius:4px;">{{ fmtUptime(s.metrics.uptime) }}</span>
          </div>
        </template>
        <div v-else style="text-align:center;color:var(--text-3);padding:16px;font-size:13px;">暂无数据</div>

        <!-- ECharts 图表 -->
        <div style="margin-top:12px;">
          <n-space size="small" style="margin-bottom:8px;">
            <n-button v-for="r in ranges" :key="r.value" size="tiny" :type="chartRange[s.id]===r.value?'primary':'default'" @click="loadChart(s.id,r.value)">{{ r.label }}</n-button>
          </n-space>
          <div :ref="(el:any)=>{if(el) chartRefs[s.id]=el}" style="height:200px;" />
        </div>
      </n-card>
    </div>

    <!-- 资产编辑 -->
    <n-modal v-model:show="showAsset" preset="card" title="编辑资产信息" style="max-width:450px;">
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
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import { NCard, NButton, NSpace, NTag, NProgress, NModal, NForm, NFormItem, NInput, NInputNumber, NSwitch, useMessage } from 'naive-ui'
import { apiGet, apiList, apiPost, apiPut } from '@/api'
import { fmtBytes, fmtDateTime, fmtUptime, pct } from '@/utils/format'
import * as echarts from 'echarts'

const message = useMessage()
const dash = ref<any>({})
const servers = ref<any[]>([])
const alerts = ref<any[]>([])
const saving = ref(false)
const chartRefs = ref<Record<number, HTMLElement>>({})
const chartInstances = ref<Record<number, echarts.ECharts>>({})
const chartRange = ref<Record<number, string>>({})

const ranges = [
  { label: '1h', value: '1h' }, { label: '6h', value: '6h' },
  { label: '24h', value: '24h' }, { label: '7d', value: '7d' }, { label: '30d', value: '30d' },
]

const unreadAlerts = ref<any[]>([])

function memPct(s: any) { return s.metrics ? pct(s.metrics.mem_used, s.metrics.mem_total) : 0 }
function diskPct(s: any) { return s.metrics ? pct(s.metrics.disk_used, s.metrics.disk_total) : 0 }
function pctColor(v: number) { return v >= 90 ? '#c2685c' : v >= 70 ? '#bf9540' : '#6f8f76' }

// --- Asset editing ---
const showAsset = ref(false)
const assetServer = ref<any>(null)
const assetForm = reactive({ provider: '', location: '', spec: '', price: 0, notes: '', probe_enabled: false })
const assetExpiry = ref('')
function openAsset(s: any) {
  assetServer.value = s
  Object.assign(assetForm, { provider: s.provider || '', location: s.location || '', spec: s.spec || '', price: s.price || 0, notes: s.notes || '', probe_enabled: s.probe_enabled })
  assetExpiry.value = s.expiry_date ? new Date(s.expiry_date * 1000).toISOString().slice(0, 16) : ''
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

// --- ECharts ---
async function loadChart(serverId: number, range: string) {
  chartRange.value[serverId] = range
  try {
    const data = await apiGet<any>(`/api/admin/monitor/servers/${serverId}/metrics?range=${range}`)
    const metrics = data?.data || []
    await nextTick()
    const el = chartRefs.value[serverId]
    if (!el) return
    if (!chartInstances.value[serverId]) {
      chartInstances.value[serverId] = echarts.init(el)
    }
    const chart = chartInstances.value[serverId]
    const times = metrics.map((m: any) => new Date(m.ts * 1000).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }))
    chart.setOption({
      tooltip: { trigger: 'axis' },
      grid: { left: 40, right: 10, top: 10, bottom: 24 },
      xAxis: { type: 'category', data: times, axisLabel: { fontSize: 10 } },
      yAxis: [
        { type: 'value', name: '%', max: 100, axisLabel: { fontSize: 10 } },
        { type: 'value', name: 'MB/s', axisLabel: { fontSize: 10 } },
      ],
      series: [
        { name: 'CPU', type: 'line', data: metrics.map((m: any) => m.cpu_percent?.toFixed(1)), smooth: true, lineStyle: { width: 1.5 }, showSymbol: false },
        { name: '内存', type: 'line', data: metrics.map((m: any) => pct(m.mem_used, m.mem_total).toFixed(1)), smooth: true, lineStyle: { width: 1.5 }, showSymbol: false },
        { name: '网络↑', type: 'line', yAxisIndex: 1, data: metrics.map((m: any) => ((m.net_tx || 0) / 1024 / 1024).toFixed(2)), smooth: true, lineStyle: { width: 1 }, showSymbol: false },
        { name: '网络↓', type: 'line', yAxisIndex: 1, data: metrics.map((m: any) => ((m.net_rx || 0) / 1024 / 1024).toFixed(2)), smooth: true, lineStyle: { width: 1 }, showSymbol: false },
      ],
    })
  } catch {}
}

let refreshTimer: ReturnType<typeof setInterval> | null = null

async function load() {
  try {
    const [d, s, a] = await Promise.all([
      apiGet('/api/admin/monitor/dashboard'),
      apiList('/api/admin/monitor/servers'),
      apiList('/api/admin/monitor/alerts'),
    ])
    dash.value = d || {}
    servers.value = s || []
    alerts.value = a || []
    unreadAlerts.value = (a || []).filter((x: any) => !x.read)
    // Load default chart for each server
    for (const sv of servers.value) {
      if (!chartRange.value[sv.id]) chartRange.value[sv.id] = '24h'
    }
  } catch {}
}

onMounted(async () => {
  await load()
  // Init charts after DOM renders
  await nextTick()
  for (const sv of servers.value) {
    loadChart(sv.id, chartRange.value[sv.id] || '24h')
  }
  refreshTimer = setInterval(load, 30000)
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  Object.values(chartInstances.value).forEach(c => c.dispose())
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
</style>
