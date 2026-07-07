<template>
  <div>
    <h2 class="page-title">订单管理</h2>

    <div class="stat-row">
      <div class="stat-card"><div class="s-label">总收入</div><div class="s-value" style="color:#10b981;">{{ stats.revenue }} 积分</div></div>
      <div class="stat-card"><div class="s-label">已退款</div><div class="s-value" style="color:var(--warn);">{{ stats.refunded }} 积分</div></div>
      <div class="stat-card"><div class="s-label">订单数</div><div class="s-value">{{ orders.length }}</div></div>
    </div>

    <div class="page-toolbar">
      <n-input v-model:value="search" placeholder="搜索用户名" size="small" style="width:220px;" clearable />
    </div>

    <n-spin :show="loading">
      <div v-if="filtered.length" class="card-grid">
        <div v-for="o in filtered" :key="o.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ o.name || '—' }}</span>
            <n-tag :type="o.status === 'success' ? 'success' : 'warning'" size="small" bordered="false">{{ o.status === 'success' ? '成功' : '已退款' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">用户 <b>{{ o.username || '已删除' }}</b></span>
            <span class="kv"><n-tag :type="o.type === 'plan' ? 'success' : 'info'" size="tiny" bordered="false">{{ o.type || '—' }}</n-tag></span>
            <span class="kv">积分 <b>{{ o.price_points }}</b></span>
            <span class="kv">{{ fmtDateTime(o.created_at) }}</span>
          </div>
          <div class="lc-foot">
            <n-button v-if="o.status === 'success'" size="tiny" type="warning" @click="handleRefund(o.id)">退款</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(o.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无订单" style="padding:40px 0;" />
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { NSpin, NInput, NButton, NTag, NEmpty, useMessage } from 'naive-ui'
import { apiList, apiPost, apiDelete } from '@/api'
import { fmtDateTime } from '@/utils/format'

const message = useMessage()
const orders = ref<any[]>([])
const loading = ref(false)
const search = ref('')

const filtered = computed(() => {
  if (!search.value) return orders.value
  const q = search.value.toLowerCase()
  return orders.value.filter((o: any) => o.username?.toLowerCase().includes(q))
})

const stats = computed(() => {
  let revenue = 0, refunded = 0
  for (const o of orders.value) {
    if (o.status === 'success') revenue += o.price_points || 0
    if (o.status === 'refunded') refunded += o.price_points || 0
  }
  return { revenue, refunded }
})

async function handleRefund(id: number) {
  try { await apiPost(`/api/admin/orders/${id}/refund`); message.success('退款成功'); await load() } catch (e: any) { message.error(e.message) }
}
async function handleDelete(id: number) {
  try { await apiDelete(`/api/admin/orders/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

async function load() {
  loading.value = true
  try { orders.value = await apiList('/api/admin/orders') } catch {} finally { loading.value = false }
}
onMounted(load)
</script>
