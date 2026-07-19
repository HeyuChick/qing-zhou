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
            <span class="kv" v-if="o.status === 'refunded'">已退 <b style="color:var(--warn);">{{ o.refunded_points }}</b>
              <template v-if="o.refund_ratio > 0 && o.refund_ratio < 1">（{{ Math.round(o.refund_ratio * 100) }}%）</template>
            </span>
            <span class="kv">{{ fmtDateTime(o.created_at) }}</span>
          </div>
          <div class="lc-foot">
            <n-button v-if="o.status === 'success'" size="tiny" type="warning" @click="openRefund(o.id)">退款</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(o)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无订单" style="padding:40px 0;" />
    </n-spin>

    <refund-dialog v-model:show="refundShow" :order-id="refundId" @done="load" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { NSpin, NInput, NButton, NTag, NEmpty, useMessage, useDialog } from 'naive-ui'
import { apiList, apiDelete } from '@/api'
import { fmtDateTime } from '@/utils/format'
import RefundDialog from '@/components/RefundDialog.vue'

const message = useMessage()
const dialog = useDialog()
const orders = ref<any[]>([])
const loading = ref(false)
const search = ref('')
const refundShow = ref(false)
const refundId = ref<number | null>(null)

const filtered = computed(() => {
  if (!search.value) return orders.value
  const q = search.value.toLowerCase()
  return orders.value.filter((o: any) => o.username?.toLowerCase().includes(q))
})

const stats = computed(() => {
  let revenue = 0, refunded = 0
  for (const o of orders.value) {
    if (o.status === 'success') revenue += o.price_points || 0
    // Refunded tile reflects the actual returned points (prorated), not the
    // original price; and the retained portion of a partial refund still counts
    // as revenue.
    if (o.status === 'refunded') { refunded += o.refunded_points || 0; revenue += (o.price_points || 0) - (o.refunded_points || 0) }
  }
  return { revenue, refunded }
})

function openRefund(id: number) {
  refundId.value = id
  refundShow.value = true
}
function handleDelete(o: any) {
  dialog.warning({
    title: '确认删除订单',
    content: '删除后该订单记录将永久消失（仅用于清理已删除用户的孤儿订单）。确定删除？',
    positiveText: '删除', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiDelete(`/api/admin/orders/${o.id}`); message.success('已删除'); await load() }
      catch (e: any) { message.error(e.message) }
    },
  })
}

async function load() {
  loading.value = true
  try { orders.value = await apiList('/api/admin/orders') }
  catch (e: any) { message.error('加载失败：' + (e?.message || '请稍后重试')) }
  finally { loading.value = false }
}
onMounted(load)
</script>
