<template>
  <div>
    <!-- 页面头 -->
    <div class="dash-head">
      <div>
        <h2 class="page-title">控制台</h2>
        <p class="page-sub">欢迎回来，{{ auth.user?.username }}，这里是你的服务概览</p>
      </div>
      <div class="dash-actions">
        <n-button size="small" secondary @click="router.push('/sub')">
          <template #icon><n-icon><LinkOutline /></n-icon></template>
          订阅管理
        </n-button>
        <n-button size="small" type="primary" @click="router.push('/shop')">
          <template #icon><n-icon><CartOutline /></n-icon></template>
          去商城
        </n-button>
      </div>
    </div>

    <n-alert v-if="auth.user?.status==='banned'" type="error" style="margin-bottom:14px;" closable>账号已被封禁，请联系管理员</n-alert>
    <n-alert v-else-if="dash.expiry_at && dash.expiry_at*1000 < Date.now()" type="warning" style="margin-bottom:14px;">账号已过期，请<router-link to="/shop">续费</router-link></n-alert>
    <n-alert v-else-if="dash.traffic?.total > 0 && dash.traffic?.used >= dash.traffic?.total" type="warning" style="margin-bottom:14px;">流量已用尽，请<router-link to="/shop">购买流量包</router-link></n-alert>

    <!-- 核心指标 -->
    <div class="kpi-row">
      <StatCard label="剩余流量" :value="remainingText" :badge="usedPctLabel" :badge-color="ringColor">
        <div class="mini-progress"><div class="mini-fill" :style="{ width: usedPct + '%', background: ringColor }" /></div>
      </StatCard>
      <StatCard label="到期时间" :value="expiryText" :badge="dlLabel" :badge-color="dlBadgeColor" :value-color="dlColor" />
      <StatCard label="积分" :value="String(dash.points || 0)" :sub="yuan(dash.points || 0)" />
      <StatCard v-if="dash.current_plan" label="当前套餐" :value="dash.current_plan" sub="当前正在使用的套餐" />
    </div>

    <n-card v-if="notices.length" size="small" class="sec" style="margin-bottom:16px;">
      <template #header>
        <span class="sec-title">最新公告</span>
        <router-link class="sec-link" to="/notices">查看全部</router-link>
      </template>
      <n-list bordered size="small">
        <n-list-item v-for="n in notices.slice(0,3)" :key="n.id" style="cursor:pointer;" @click="openNotice(n)">
          <n-thing>
            <template #header><span style="font-weight:600;">{{ n.title }}</span><n-tag v-if="n.pinned" size="tiny" type="warning" style="margin-left:6px;">置顶</n-tag></template>
            <template #header-extra><span style="font-size:11px;color:var(--text-3);">{{ fmtDate(n.created_at) }}</span></template>
          </n-thing>
        </n-list-item>
      </n-list>
    </n-card>

    <!-- 主区域：左侧用量环，右侧套餐 -->
    <div class="dash-grid">
      <n-card size="small" class="sec" style="margin-bottom:0;">
        <template #header><span class="sec-title">流量用量</span></template>
        <div class="ring-wrap">
          <div class="ring-box">
            <svg viewBox="0 0 140 140" class="ring-svg">
              <circle cx="70" cy="70" r="58" fill="none" stroke="#eee" stroke-width="12" />
              <circle cx="70" cy="70" r="58" fill="none" :stroke="ringColor" stroke-width="12" stroke-linecap="round"
                :stroke-dasharray="ringDash" style="transition:stroke-dasharray .6s ease, stroke .6s ease;" />
            </svg>
            <div class="ring-center">
              <span class="ring-pct">{{ usedPct }}%</span>
              <span class="ring-label">已使用</span>
            </div>
          </div>
          <div class="ring-foot">{{ fmtBytes(dash.traffic?.used) }} / {{ fmtTotal(dash.traffic?.total) }}</div>
        </div>
        <n-space vertical size="small" style="margin-top:14px;">
          <n-button block secondary @click="router.push('/sub')">
            <template #icon><n-icon><LinkOutline /></n-icon></template>
            管理订阅
          </n-button>
          <n-button block secondary @click="router.push('/orders')">
            <template #icon><n-icon><ReceiptOutline /></n-icon></template>
            订单记录
          </n-button>
        </n-space>
      </n-card>

      <div class="dash-main">
        <n-card v-if="plans.length" size="small" class="sec">
          <template #header>
            <span class="sec-title">我的套餐</span>
            <span v-if="hasQueued" class="sec-hint">重复购买自动排队，一次只用一份</span>
          </template>
          <div v-for="p in plans" :key="p.id" class="plan-row" :class="{ queued: p.status === 'queued' }">
            <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;">
              <span style="font-weight:600;">{{ p.name || '套餐 #' + p.id }}</span>
              <n-tag :type="planStatus(p).type" size="small" bordered>{{ planStatus(p).label }}</n-tag>
            </div>
            <n-progress v-if="p.status !== 'queued'" type="line" :percentage="planPct(p)" :color="planPct(p)>90?'#c2685c':'#6f8f76'" />
            <div v-else style="height:6px;border-radius:3px;background:repeating-linear-gradient(45deg,var(--border),var(--border) 4px,transparent 4px,transparent 8px);"></div>
            <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--text-3);margin-top:4px;">
              <span>{{ p.status === 'queued' ? '待用流量 ' + (p.traffic_limit>0 ? fmtTotal(p.traffic_limit) : '不限') : '剩余 ' + (p.remaining < 0 ? '不限' : fmtBytes(p.remaining)) + '（' + fmtBytes(p.used) + ' / ' + fmtTotal(p.traffic_limit) + '）' }}</span>
              <span>{{ planTime(p) }}</span>
            </div>
          </div>
        </n-card>

        <n-card size="small" class="sec">
          <template #header>
            <span class="sec-title">流量趋势</span>
            <n-radio-group v-model:value="trendRange" size="small">
              <n-radio-button value="7d">7天</n-radio-button>
              <n-radio-button value="30d">30天</n-radio-button>
            </n-radio-group>
          </template>
          <TrafficTrendChart v-if="trendData.length" :data="trendData" />
          <div v-else class="empty">暂无流量数据</div>
        </n-card>
      </div>
    </div>

    <n-card title="订阅链接" size="small" class="sec" style="margin-top:16px;">
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
import { NCard, NAlert, NInputGroup, NInput, NButton, NList, NListItem, NThing, NTag, NRadioGroup, NRadioButton, NProgress, NModal, NSpace, NIcon, useMessage } from 'naive-ui'
import { LinkOutline, CartOutline, ReceiptOutline } from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { apiGet, apiList } from '@/api'
import { fmtBytes, fmtTotal, fmtDate, daysLeft, yuan, pct } from '@/utils/format'
import { mdToHtml } from '@/utils/markdown'
import { planStatusMeta, planTimeText, planSortKey } from '@/utils/plan'
import StatCard from '@/components/StatCard.vue'
import TrafficTrendChart from '@/components/TrafficTrendChart.vue'

const router = useRouter(); const auth = useAuthStore(); const message = useMessage()
const dash = ref<any>({}); const notices = ref<any[]>([]); const trendRange = ref('7d'); const trendData = ref<any[]>([])
const showNotice = ref(false); const activeNotice = ref<any>(null)
function openNotice(n: any) { activeNotice.value = n; showNotice.value = true }

const dl = computed(() => daysLeft(dash.value.expiry_at))
// Read: 使用中 first, then 排队中 (by soonest activation), then finished — so the
// current份 and what's next are always at the top.
const plans = computed<any[]>(() => {
  const list = [...(dash.value.plans || [])]
  list.sort((a, b) => planSortKey(a) - planSortKey(b) || (a.activate_by || a.expiry_at || 0) - (b.activate_by || b.expiry_at || 0))
  return list
})
const hasQueued = computed(() => plans.value.some(p => p.status === 'queued'))
function planPct(p: any) { return p.status === 'queued' ? 0 : pct(p.used, p.traffic_limit) }
const planStatus = planStatusMeta
const planTime = (p: any) => planTimeText(p, fmtDate)

const usedPct = computed(() => pct(dash.value.traffic?.used, dash.value.traffic?.total))
const usedPctLabel = computed(() => usedPct.value > 0 ? usedPct.value + '%' : '')
const ringColor = computed(() => usedPct.value > 90 ? '#c2685c' : usedPct.value > 70 ? '#bf9540' : '#6f8f76')
const circumference = 2 * Math.PI * 58
const ringDash = computed(() => `${(usedPct.value / 100) * circumference} ${circumference}`)

const remainingText = computed(() => {
  const t = dash.value.traffic
  if (!t) return '—'
  return t.total > 0 ? fmtBytes(t.remaining) : '不限'
})
const expiryText = computed(() => {
  if (!dash.value.expiry_at) return '永久'
  return fmtDate(dash.value.expiry_at) + (dl.value !== null ? `（剩 ${dl.value} 天）` : '')
})
const dlLabel = computed(() => dl.value === null ? '' : `剩 ${dl.value} 天`)
const dlColor = computed(() => (dl.value !== null && dl.value <= 7) ? 'var(--danger)' : '')
const dlBadgeColor = computed(() => (dl.value !== null && dl.value <= 7) ? '#c2685c' : '#6f8f76')

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
.page-sub{color:var(--text-2);margin-bottom:0}
a{color:var(--accent-strong)}
.dash-head{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:20px}
.dash-actions{display:flex;gap:8px;flex-shrink:0}
.sec-title{font-weight:650;font-size:14px}
.sec-hint{font-size:11.5px;color:var(--text-3);margin-left:10px;font-weight:400}
.sec-link{font-size:12px;font-weight:400;margin-left:10px}

/* KPI */
.kpi-row{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:12px;margin-bottom:16px}
.mini-progress{height:4px;border-radius:2px;background:var(--bg-soft);overflow:hidden}
.mini-fill{height:100%;border-radius:2px;transition:width .5s ease}

/* 主区域两栏 */
.dash-grid{display:grid;grid-template-columns:minmax(220px,260px) 1fr;gap:16px;align-items:start}
@media (max-width:840px){.dash-grid{grid-template-columns:1fr}}
.dash-main{display:flex;flex-direction:column;gap:16px;min-width:0}

/* 环形 */
.ring-wrap{display:flex;flex-direction:column;align-items:center;padding:4px 0 2px}
.ring-box{position:relative;width:150px;height:150px}
.ring-svg{width:100%;height:100%;transform:rotate(-90deg)}
.ring-center{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center}
.ring-pct{font-size:26px;font-weight:750;letter-spacing:-0.02em;line-height:1}
.ring-label{font-size:11px;color:var(--text-3);margin-top:4px}
.ring-foot{font-size:12px;color:var(--text-2);margin-top:10px}

/* 套餐 */
.plan-row{margin-bottom:12px;padding:10px;background:var(--bg-soft);border-radius:10px}
.plan-row.queued{opacity:.72;border:1px dashed var(--border);background:transparent}
.plan-row:last-child{margin-bottom:0}
.empty{text-align:center;color:var(--text-3);padding:40px 0;font-size:13px}
</style>
