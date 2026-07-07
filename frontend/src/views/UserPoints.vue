<template>
  <div>
    <h2 class="page-title">积分明细</h2>
    <div class="stat-row">
      <div class="stat-card">
        <div class="s-label">当前积分</div>
        <div class="s-value">{{ auth.user?.points || 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="s-label">约合</div>
        <div class="s-value" style="font-size:16px;font-weight:650;">{{ yuan(auth.user?.points || 0) }}</div>
      </div>
    </div>
    <n-spin :show="loading">
      <div v-if="txs.length" class="card-grid">
        <div v-for="t in txs" :key="t.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ typeLabel[t.type] || t.type || '—' }}</span>
            <span :style="`color:${t.amount > 0 ? '#10b981' : '#ef4444'};font-weight:700;`">{{ (t.amount > 0 ? '+' : '') + t.amount }}</span>
          </div>
          <div class="lc-meta">
            <span class="kv">余额 <b>{{ t.balance_after }}</b></span>
            <span class="kv">{{ fmtDateTime(t.created_at) }}</span>
          </div>
          <div v-if="t.note" class="lc-meta" style="color:var(--text-3);">{{ t.note }}</div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无明细" style="padding:40px 0;" />
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { NSpin, NEmpty } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { apiGet } from '@/api'
import { fmtDateTime, yuan } from '@/utils/format'

const auth = useAuthStore()
const txs = ref<any[]>([])
const loading = ref(false)

const typeLabel: Record<string, string> = {
  admin_recharge: '管理员充值', purchase: '购买消费', signup_bonus: '注册赠送',
  refund: '退款', adjust: '调整', admin_grant: '管理员赠送',
}

onMounted(async () => {
  loading.value = true
  try {
    const data = await apiGet<any>('/api/user/points')
    txs.value = data?.transactions || []
  } catch {} finally { loading.value = false }
})
</script>
