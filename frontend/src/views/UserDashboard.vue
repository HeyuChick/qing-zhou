<template>
  <div>
    <h2 class="page-title">控制台</h2>
    <p class="page-sub">欢迎回来，{{ auth.user?.username }}</p>

    <n-alert v-if="auth.user?.status==='banned'" type="error" style="margin-bottom:14px;" closable>账号已被封禁，请联系管理员</n-alert>
    <n-alert v-else-if="dash.expiry_at && dash.expiry_at*1000 < Date.now()" type="warning" style="margin-bottom:14px;">账号已过期，请<router-link to="/shop">续费</router-link></n-alert>
    <n-alert v-else-if="dash.traffic?.total > 0 && dash.traffic?.used >= dash.traffic?.total" type="warning" style="margin-bottom:14px;">流量已用尽，请<router-link to="/shop">购买流量包</router-link></n-alert>

    <n-card v-if="notices.length" title="最新公告" size="small" style="margin-bottom:20px;">
      <template #header-extra>
        <router-link to="/notices" style="font-size:12px;">查看全部</router-link>
      </template>
      <n-list bordered>
        <n-list-item v-for="n in notices.slice(0,3)" :key="n.id" style="cursor:pointer;" @click="openNotice(n)">
          <n-thing>
            <template #header><span style="font-weight:600;">{{ n.title }}</span><n-tag v-if="n.pinned" size="tiny" type="warning" style="margin-left:6px;">置顶</n-tag></template>
            <template #header-extra><span style="font-size:11px;color:var(--text-3);">{{ fmtDate(n.created_at) }}</span></template>
          </n-thing>
        </n-list-item>
      </n-list>
    </n-card>

    <div class="dash-top">
      <n-card size="small" style="display:flex;align-items:center;justify-content:center;">
        <div style="position:relative;width:140px;height:140px;margin:0 auto;">
          <svg viewBox="0 0 140 140" style="transform:rotate(-90deg);">
            <circle cx="70" cy="70" r="58" fill="none" stroke="#eee" stroke-width="12" />
            <circle cx="70" cy="70" r="58" fill="none" :stroke="ringColor" stroke-width="12" stroke-linecap="round"
              :stroke-dasharray="ringDash" :stroke-dashoffset="0" style="transition:stroke-dasharray .6s ease;" />
          </svg>
          <div style="position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;">
            <span style="font-size:22px;font-weight:750;">{{ usedPct }}%</span>
            <span style="font-size:11px;color:var(--text-3);">已使用</span>
          </div>
        </div>
        <div style="text-align:center;margin-top:8px;font-size:12px;color:var(--text-2);">
          {{ fmtBytes(dash.traffic?.used) }} / {{ fmtTotal(dash.traffic?.total) }}
        </div>
      </n-card>
      <div class="dash-stats">
        <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">剩余流量</span><span style="font-size:20px;font-weight:720;">{{ dash.traffic?.total>0 ? fmtBytes(dash.traffic.remaining) : '不限' }}</span></div></n-card>
        <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">到期时间</span><span style="font-size:20px;font-weight:720;">{{ fmtDate(dash.expiry_at) }}</span><span v-if="dl!==null" style="font-size:11px;" :style="{color:dl<=7?'var(--danger)':'var(--text-3)'}">剩余 {{ dl }} 天</span></div></n-card>
        <n-card size="small"><div style="display:flex;flex-direction:column;gap:4px;"><span style="font-size:12px;color:var(--text-3);font-weight:550;">积分</span><span style="font-size:20px;font-weight:720;">{{ dash.points||0 }}</span><span style="font-size:11px;color:var(--text-3);">{{ yuan(dash.points||0) }}</span></div></n-card>
      </div>
    </div>

    <!-- 分套餐资源：每个套餐独立计量，分别显示剩余流量与到期 -->
    <n-card v-if="plans.length" title="我的套餐" size="small" style="margin-bottom:20px;">
      <div v-for="p in plans" :key="p.id" style="margin-bottom:12px;padding:10px;background:var(--bg-soft);border-radius:10px;">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;">
          <span style="font-weight:600;">{{ p.name || '套餐 #' + p.id }}</span>
          <n-tag :type="planStatus(p).type" size="small" bordered>{{ planStatus(p).label }}</n-tag>
        </div>
        <n-progress type="line" :percentage="planPct(p)" :color="planPct(p)>90?'#c2685c':'#6f8f76'" />
        <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--text-3);margin-top:4px;">
          <span>剩余 {{ p.remaining < 0 ? '不限' : fmtBytes(p.remaining) }}（{{ fmtBytes(p.used) }} / {{ fmtTotal(p.traffic_limit) }}）</span>
          <span>{{ p.expiry_at ? fmtDate(p.expiry_at) : '不过期' }}</span>
        </div>
      </div>
    </n-card>

    <n-card title="流量趋势" size="small" style="margin-bottom:20px;">
      <template #header-extra>
        <n-radio-group v-model:value="trendRange" size="small">
          <n-radio-button value="7d">7天</n-radio-button>
          <n-radio-button value="30d">30天</n-radio-button>
        </n-radio-group>
      </template>
      <div style="display:flex;align-items:flex-end;gap:3px;height:120px;padding:0 4px;">
        <div v-for="(bar,i) in trendBars" :key="i" style="flex:1;display:flex;flex-direction:column;align-items:center;gap:2px;">
          <div style="flex:1;width:100%;display:flex;flex-direction:column;justify-content:flex-end;gap:1px;">
            <div :style="{height:bar.upH+'px',background:'#6f8f76',borderRadius:'2px 2px 0 0'}" />
            <div :style="{height:bar.downH+'px',background:'#5e7a99',borderRadius:'0 0 2px 2px'}" />
          </div>
          <span style="font-size:9px;color:var(--text-3);white-space:nowrap;">{{ bar.label }}</span>
        </div>
      </div>
    </n-card>

    <n-card title="订阅链接" size="small" style="margin-bottom:20px;">
      <n-input-group>
        <n-input :value="dash.subscription_url" readonly placeholder="暂无订阅" />
        <n-button type="primary" @click="copySub">复制</n-button>
      </n-input-group>
    </n-card>

    <n-modal v-model:show="showNotice" preset="card" style="max-width:640px;" :title="activeNotice?.title">
      <template #header-extra v-if="activeNotice">
        <span style="font-size:12px;color:var(--text-3);">{{ fmtDate(activeNotice.created_at) }}</span>
      </template>
      <div class="md" v-html="mdToHtml(activeNotice?.content || '')" />
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NAlert, NInputGroup, NInput, NButton, NList, NListItem, NThing, NTag, NRadioGroup, NRadioButton, NProgress, NModal, useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { apiGet, apiList } from '@/api'
import { fmtBytes, fmtTotal, fmtDate, daysLeft, yuan, pct } from '@/utils/format'
import { mdToHtml } from '@/utils/markdown'

const router = useRouter(); const auth = useAuthStore(); const message = useMessage()
const dash = ref<any>({}); const notices = ref<any[]>([]); const trendRange = ref('7d'); const trendData = ref<any[]>([])
const showNotice = ref(false); const activeNotice = ref<any>(null)
function openNotice(n: any) { activeNotice.value = n; showNotice.value = true }

const dl = computed(() => daysLeft(dash.value.expiry_at))
const plans = computed<any[]>(() => dash.value.plans || [])
function planPct(p: any) { return pct(p.used, p.traffic_limit) }
function planStatus(p: any) {
  if (p.status === 'expired' || (p.expiry_at && p.expiry_at * 1000 < Date.now())) return { type: 'error' as const, label: '已过期' }
  if (p.status === 'exhausted' || (p.traffic_limit > 0 && p.used >= p.traffic_limit)) return { type: 'warning' as const, label: '已用尽' }
  return { type: 'success' as const, label: '正常' }
}
const usedPct = computed(() => pct(dash.value.traffic?.used, dash.value.traffic?.total))
const ringColor = computed(() => usedPct.value > 90 ? '#c2685c' : usedPct.value > 70 ? '#bf9540' : '#6f8f76')
const circumference = 2 * Math.PI * 58
const ringDash = computed(() => `${(usedPct.value / 100) * circumference} ${circumference}`)

const trendBars = computed(() => {
  if (!trendData.value.length) return []
  const maxVal = Math.max(...trendData.value.map((d: any) => Math.max(d.up || 0, d.down || 0)), 1)
  return trendData.value.map((d: any) => ({
    label: d.date?.slice(5) || '', upH: Math.max(1, Math.round(((d.up || 0) / maxVal) * 90)),
    downH: Math.max(1, Math.round(((d.down || 0) / maxVal) * 90)),
  }))
})

function copySub() { if (!dash.value.subscription_url) { message.warning('暂无订阅'); return }; navigator.clipboard.writeText(dash.value.subscription_url); message.success('已复制') }

async function loadTrend() { try { trendData.value = await apiList(`/api/user/stats/traffic?range=${trendRange.value}`) } catch {} }
watch(trendRange, loadTrend)

onMounted(async () => {
  try { dash.value = await apiGet('/api/user/dashboard') || {} } catch {}
  try { notices.value = await apiList('/api/user/announcements') } catch {}
  await loadTrend()
})
</script>

<style scoped>
.page-title{font-size:21px;margin-bottom:4px}
.page-sub{color:var(--text-2);margin-bottom:22px}
a{color:var(--accent-strong)}
.dash-top{display:grid;grid-template-columns:200px 1fr;gap:16px;margin-bottom:20px}
.dash-stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:12px}
@media (max-width:640px){.dash-top{grid-template-columns:1fr}}
</style>
