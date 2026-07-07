<template>
  <div>
    <h2 class="page-title">积分商城</h2>
    <p class="page-sub">当前积分：<strong>{{ auth.user?.points || 0 }}</strong> ({{ yuan(auth.user?.points || 0) }})</p>
    <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:16px;">
      <n-card v-for="pkg in packages" :key="pkg.id" size="small" hoverable>
        <template #header><span>{{ pkg.name }}</span></template>
        <div style="margin-bottom:10px;">
          <n-tag :type="pkg.type==='traffic'?'info':pkg.type==='plan'?'success':'warning'" size="small" bordered>{{ pkg.type==='traffic'?'流量包':pkg.type==='plan'?'订阅计划':'设备扩展' }}</n-tag>
        </div>
        <div style="font-size:13px;color:var(--text-2);margin-bottom:8px;">
          <template v-if="pkg.traffic_bytes">{{ fmtTotal(pkg.traffic_bytes) }}</template>
          <template v-if="pkg.duration_days"> · {{ pkg.duration_days }}天</template>
          <template v-if="pkg.device_add"> · +{{ pkg.device_add }}设备</template>
        </div>
        <div style="font-size:22px;font-weight:720;color:var(--accent-strong);">{{ pkg.price_points }} 积分</div>
        <div style="font-size:11px;color:var(--text-3);">{{ yuan(pkg.price_points) }}</div>
        <n-button type="primary" block style="margin-top:12px;" :loading="buying===pkg.id" @click="handleBuy(pkg)">购买</n-button>
      </n-card>
    </div>
    <n-empty v-if="packages.length===0" description="暂无可购买的商品" style="padding:60px 0;" />
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { NCard, NButton, NTag, NEmpty, useMessage, useDialog } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { apiList, apiPost } from '@/api'
import { fmtTotal, yuan } from '@/utils/format'
const auth = useAuthStore()
const message = useMessage()
const dialog = useDialog()
const packages = ref<any[]>([])
const buying = ref<number|null>(null)
function handleBuy(pkg: any) {
  dialog.warning({ title: '确认购买', content: `确定花费 ${pkg.price_points} 积分购买「${pkg.name}」？`, positiveText: '确定', negativeText: '取消',
    onPositiveClick: async () => { buying.value = pkg.id; try { await apiPost('/api/user/purchase', { package_id: pkg.id }); message.success('购买成功！'); await auth.fetchMe() } catch (e: any) { message.error(e.message) } finally { buying.value = null } } })
}
onMounted(async () => { try { packages.value = await apiList('/api/user/packages') } catch {} })
</script>
<style scoped>.page-title { font-size: 21px; margin-bottom: 4px; }.page-sub { color: var(--text-2); margin-bottom: 22px; }</style>
