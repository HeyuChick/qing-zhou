<template>
  <div>
    <h2 class="page-title">管理概览</h2>
    <p class="page-sub">系统运营数据一览</p>

    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin-bottom:16px;">
      <n-card size="small"><div class="stat"><span class="stat-l">总用户</span><span class="stat-v">{{ ov.total_users||0 }}</span><span class="stat-s">今日新增 {{ ov.new_today||0 }}</span></div></n-card>
      <n-card size="small" style="cursor:pointer;" @click="showOnline=!showOnline"><div class="stat"><span class="stat-l">当前在线</span><span class="stat-v" style="color:#10b981;">{{ ov.online||0 }}</span><span class="stat-s">已开通 {{ ov.active_users||0 }}</span></div></n-card>
      <n-card size="small"><div class="stat"><span class="stat-l">累计流量</span><span class="stat-v">{{ fmtBytes(ov.total_traffic) }}</span></div></n-card>
      <n-card size="small"><div class="stat"><span class="stat-l">积分发行</span><span class="stat-v">{{ ov.points_issued||0 }}</span><span class="stat-s">在售商品 {{ ov.packages_on||0 }}</span></div></n-card>
    </div>

    <!-- 在线用户 -->
    <n-card v-if="showOnline && onlineUsers.length" title="在线用户" size="small" style="margin-bottom:16px;">
      <div v-for="u in onlineUsers" :key="u.name" style="display:flex;justify-content:space-between;padding:4px 0;font-size:13px;">
        <span><span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:#10b981;margin-right:6px;" />{{ u.name }}</span>
        <span style="font-size:11px;color:var(--text-3);">{{ timeAgo(u.value) }}</span>
      </div>
    </n-card>

    <!-- 流量趋势 -->
    <n-card title="流量趋势 (14天)" size="small" style="margin-bottom:16px;">
      <div style="display:flex;align-items:flex-end;gap:3px;height:120px;">
        <div v-for="(bar,i) in trafficBars" :key="i" style="flex:1;display:flex;flex-direction:column;align-items:center;gap:2px;">
          <div style="flex:1;width:100%;display:flex;flex-direction:column;justify-content:flex-end;gap:1px;">
            <div :style="{height:bar.upH+'px',background:'#6f8f76',borderRadius:'2px 2px 0 0'}" />
            <div :style="{height:bar.downH+'px',background:'#5e7a99',borderRadius:'0 0 2px 2px'}" />
          </div>
          <span style="font-size:9px;color:var(--text-3);white-space:nowrap;">{{ bar.label }}</span>
        </div>
      </div>
    </n-card>

    <!-- 排行 + 分布 -->
    <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:16px;margin-bottom:16px;">
      <n-card title="流量排行" size="small">
        <div v-for="(t,i) in topTraffic" :key="i" style="display:flex;justify-content:space-between;padding:4px 0;font-size:13px;border-bottom:1px solid var(--border-soft,#f1efe8);">
          <span>{{ i+1}}. {{ t.name }}</span><span style="font-weight:600;">{{ fmtBytes(t.value) }}</span>
        </div>
        <div v-if="!topTraffic.length" style="text-align:center;color:var(--text-3);padding:16px;">暂无数据</div>
      </n-card>
      <n-card title="消费排行" size="small">
        <div v-for="(t,i) in topSpend" :key="i" style="display:flex;justify-content:space-between;padding:4px 0;font-size:13px;border-bottom:1px solid var(--border-soft,#f1efe8);">
          <span>{{ i+1}}. {{ t.name }}</span><span style="font-weight:600;">{{ t.value }} 积分</span>
        </div>
        <div v-if="!topSpend.length" style="text-align:center;color:var(--text-3);padding:16px;">暂无数据</div>
      </n-card>
      <n-card title="用户分布" size="small">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px;">
          <div class="dist-item"><span class="dist-label">活跃</span><span class="dist-val" style="color:#10b981;">{{ dist.status_active||0 }}</span></div>
          <div class="dist-item"><span class="dist-label">封禁</span><span class="dist-val" style="color:#ef4444;">{{ dist.status_banned||0 }}</span></div>
          <div class="dist-item"><span class="dist-label">7天到期</span><span class="dist-val" style="color:#f59e0b;">{{ dist.expire_7d||0 }}</span></div>
          <div class="dist-item"><span class="dist-label">已过期</span><span class="dist-val" style="color:var(--text-3);">{{ dist.expired||0 }}</span></div>
        </div>
      </n-card>
    </div>

    <!-- 积分收支 -->
    <n-card title="积分收支 (30天)" size="small">
      <div style="display:flex;align-items:flex-end;gap:3px;height:100px;">
        <div v-for="(bar,i) in revenueBars" :key="i" style="flex:1;display:flex;flex-direction:column;align-items:center;gap:2px;">
          <div style="flex:1;width:100%;display:flex;flex-direction:column;justify-content:flex-end;gap:1px;">
            <div :style="{height:bar.issueH+'px',background:'#6f8f76',borderRadius:'2px 2px 0 0'}" />
            <div :style="{height:bar.consumeH+'px',background:'#bf9540',borderRadius:'0 0 2px 2px'}" />
          </div>
          <span style="font-size:9px;color:var(--text-3);white-space:nowrap;">{{ bar.label }}</span>
        </div>
      </div>
      <div style="display:flex;gap:16px;justify-content:center;margin-top:6px;font-size:11px;color:var(--text-2);">
        <span><span style="display:inline-block;width:10px;height:10px;background:#6f8f76;border-radius:2px;margin-right:4px;vertical-align:middle;" />发行</span>
        <span><span style="display:inline-block;width:10px;height:10px;background:#bf9540;border-radius:2px;margin-right:4px;vertical-align:middle;" />消费</span>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { NCard } from 'naive-ui'
import { apiGet, apiList } from '@/api'
import { fmtBytes, timeAgo } from '@/utils/format'

const ov = ref<any>({})
const onlineUsers = ref<any[]>([])
const trafficData = ref<any[]>([])
const topTraffic = ref<any[]>([])
const topSpend = ref<any[]>([])
const dist = ref<any>({})
const revenueData = ref<any[]>([])
const showOnline = ref(false)

const trafficBars = computed(() => {
  if (!trafficData.value.length) return []
  const max = Math.max(...trafficData.value.map((d: any) => Math.max(d.up || 0, d.down || 0)), 1)
  return trafficData.value.map((d: any) => ({
    label: d.date?.slice(5) || '',
    upH: Math.max(1, Math.round(((d.up || 0) / max) * 90)),
    downH: Math.max(1, Math.round(((d.down || 0) / max) * 90)),
  }))
})

const revenueBars = computed(() => {
  if (!revenueData.value.length) return []
  const max = Math.max(...revenueData.value.map((d: any) => Math.max(d.a || 0, d.b || 0)), 1)
  return revenueData.value.map((d: any) => ({
    label: d.date?.slice(5) || '',
    issueH: Math.max(1, Math.round(((d.a || 0) / max) * 80)),
    consumeH: Math.max(1, Math.round(((d.b || 0) / max) * 80)),
  }))
})

onMounted(async () => {
  try {
    const data = await apiGet<any>('/api/admin/stats/overview')
    ov.value = data || {}
    onlineUsers.value = data?.online_users || []
  } catch {}
  try { trafficData.value = await apiList('/api/admin/stats/traffic?range=14d') } catch {}
  try {
    const top = await apiGet<any>('/api/admin/stats/top')
    topTraffic.value = top?.traffic || []
    topSpend.value = top?.spend || []
  } catch {}
  try {
    const d = await apiGet<any>('/api/admin/stats/distribution')
    dist.value = d?.distribution || {}
    revenueData.value = d?.revenue || []
  } catch {}
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
.stat { display: flex; flex-direction: column; gap: 4px; }
.stat-l { font-size: 12.5px; color: var(--text-2); font-weight: 550; }
.stat-v { font-size: 24px; font-weight: 720; }
.stat-s { font-size: 11px; color: var(--text-3); }
.dist-item { padding: 10px; background: var(--bg-soft); border-radius: 10px; text-align: center; }
.dist-label { display: block; font-size: 12px; color: var(--text-3); margin-bottom: 4px; }
.dist-val { font-size: 22px; font-weight: 720; }
</style>
