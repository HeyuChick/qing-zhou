<template>
  <div>
    <div class="page-head"><div><h2 class="page-title">订单管理</h2><p class="page-sub">收入、退款、套餐归属与每笔订单的完整状态轨迹</p></div></div>

    <div class="stat-row">
      <div class="stat-card"><div class="s-label">总收入</div><div class="s-value" style="color:var(--success);">{{ stats.revenue }} 积分</div></div>
      <div class="stat-card"><div class="s-label">已退款</div><div class="s-value" style="color:var(--warn);">{{ stats.refunded }} 积分</div></div>
      <div class="stat-card"><div class="s-label">订单数</div><div class="s-value">{{ orders.length }}</div></div>
      <div class="stat-card"><div class="s-label">退款率</div><div class="s-value">{{ stats.refundRate }}%</div></div>
    </div>

    <div class="page-toolbar">
      <div class="seg">
        <button v-for="t in tabs" :key="t.key" class="seg-btn" :class="{ active: statusTab === t.key }" @click="statusTab = t.key">
          {{ t.label }}<span class="seg-count">{{ t.count }}</span>
        </button>
      </div>
      <div class="seg">
        <span class="seg-label">分组</span>
        <button v-for="g in groupOpts" :key="g.key" class="seg-btn" :class="{ active: groupBy === g.key }" @click="groupBy = g.key">
          {{ g.label }}
        </button>
      </div>
      <span class="spacer" />
      <n-input v-model:value="search" placeholder="搜索用户名" size="small" style="width:200px;" clearable />
    </div>

    <n-spin :show="loading">
      <!-- 桌面端：一览表格 -->
      <div v-if="view.length" class="orders-table-wrap">
        <table class="orders-table">
          <thead>
            <tr>
              <th>套餐</th>
              <th>用户</th>
              <th>类型</th>
              <th class="sortable num" @click="toggleSort('price_points')">金额<span class="sort-ind">{{ sortInd('price_points') }}</span></th>
              <th>状态</th>
              <th class="sortable" @click="toggleSort('created_at')">下单时间<span class="sort-ind">{{ sortInd('created_at') }}</span></th>
              <th class="act-col">操作</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="g in groups" :key="g.key">
              <tr v-if="g.label" class="group-row">
                <td :colspan="7">
                  <span class="group-name">{{ g.label }}</span>
                  <span class="group-meta">{{ g.count }} 单 · 收入 {{ g.revenue }} 积分</span>
                </td>
              </tr>
              <tr v-for="o in g.items" :key="o.id" :class="{ refunded: o.status === 'refunded' }">
                <td class="strong">{{ o.name || '—' }}</td>
                <td :class="{ muted: !o.username }">{{ o.username || '已删除' }}</td>
                <td><n-tag :type="o.type === 'plan' ? 'success' : 'info'" size="tiny" :bordered="false">{{ o.type || '—' }}</n-tag></td>
                <td class="num">
                  <span class="amount">{{ o.price_points }}</span>
                  <span v-if="o.status === 'refunded'" class="amount-sub">退 {{ o.refunded_points }}<template v-if="o.refund_ratio > 0 && o.refund_ratio < 1"> ({{ Math.round(o.refund_ratio * 100) }}%)</template></span>
                </td>
                <td>
                  <span class="pill" :class="o.status === 'success' ? 'pill-ok' : 'pill-warn'">{{ o.status === 'success' ? '成功' : '已退款' }}</span>
                </td>
                <td class="time">{{ fmtDateTime(o.created_at) }}</td>
                <td class="act-col">
                  <n-button v-if="o.status === 'success'" size="tiny" type="warning" @click="openRefund(o.id)">退款</n-button>
                  <n-button size="tiny" type="error" @click="handleDelete(o)">删除</n-button>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <!-- 移动端：卡片（同样按分组） -->
      <div v-if="view.length" class="orders-cards">
        <div v-for="g in groups" :key="g.key" class="order-group">
          <div v-if="g.label" class="group-head">
            <span class="group-name">{{ g.label }}</span>
            <span class="group-meta">{{ g.count }} 单 · 收入 {{ g.revenue }} 积分</span>
          </div>
          <div class="card-grid">
            <div v-for="o in g.items" :key="o.id" class="list-card">
              <div class="lc-head">
                <span class="lc-title">{{ o.name || '—' }}</span>
                <span class="pill" :class="o.status === 'success' ? 'pill-ok' : 'pill-warn'">{{ o.status === 'success' ? '成功' : '已退款' }}</span>
              </div>
              <div class="lc-meta">
                <span class="kv">用户 <b>{{ o.username || '已删除' }}</b></span>
                <span class="kv"><n-tag :type="o.type === 'plan' ? 'success' : 'info'" size="tiny" :bordered="false">{{ o.type || '—' }}</n-tag></span>
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
        </div>
      </div>

      <n-empty v-if="!view.length && !loading" :description="orders.length ? '没有符合条件的订单' : '暂无订单'" style="padding:40px 0;">
        <template v-if="orders.length" #extra><n-button size="small" @click="statusTab='all'; groupBy='plan'; search=''">清除筛选</n-button></template>
      </n-empty>
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
const statusTab = ref<'all' | 'success' | 'refunded'>('all')
const groupBy = ref<'plan' | 'status' | 'none'>('plan')
const sortKey = ref<'created_at' | 'price_points'>('created_at')
const sortDir = ref<'asc' | 'desc'>('desc')
const refundShow = ref(false)
const refundId = ref<number | null>(null)

const tabs = computed(() => [
  { key: 'all' as const, label: '全部', count: orders.value.length },
  { key: 'success' as const, label: '成功', count: orders.value.filter((o: any) => o.status === 'success').length },
  { key: 'refunded' as const, label: '已退款', count: orders.value.filter((o: any) => o.status === 'refunded').length },
])

const groupOpts = [
  { key: 'plan' as const, label: '按套餐' },
  { key: 'status' as const, label: '按状态' },
  { key: 'none' as const, label: '不分组' },
]

// 保留收入口径：成功计全额，部分退款计留存部分
function retained(o: any): number {
  if (o.status === 'success') return o.price_points || 0
  if (o.status === 'refunded') return (o.price_points || 0) - (o.refunded_points || 0)
  return 0
}

// groups = 在 view（已过滤+排序）之上按所选维度分组；不分组时归为单一无标题组
const groups = computed(() => {
  const rows = view.value
  if (groupBy.value === 'none') {
    return [{ key: '__all', label: '', count: rows.length, revenue: 0, items: rows }]
  }
  const map = new Map<string, any[]>()
  for (const o of rows) {
    const key = groupBy.value === 'plan' ? (o.name || '—') : o.status
    if (!map.has(key)) map.set(key, [])
    map.get(key)!.push(o)
  }
  const arr = [...map.entries()].map(([key, items]) => ({
    key,
    label: groupBy.value === 'status' ? (key === 'success' ? '成功' : '已退款') : key,
    count: items.length,
    revenue: items.reduce((s, o) => s + retained(o), 0),
    items,
  }))
  // 按状态：成功在前；按套餐：单量多的在前
  if (groupBy.value === 'status') arr.sort((a, b) => (a.key === 'success' ? 0 : 1) - (b.key === 'success' ? 0 : 1))
  else arr.sort((a, b) => b.count - a.count || b.revenue - a.revenue)
  return arr
})

// view = 按标签 + 搜索过滤后再排序
const view = computed(() => {
  let list = orders.value
  if (statusTab.value !== 'all') list = list.filter((o: any) => o.status === statusTab.value)
  if (search.value) {
    const q = search.value.toLowerCase()
    list = list.filter((o: any) => o.username?.toLowerCase().includes(q))
  }
  const k = sortKey.value, dir = sortDir.value === 'asc' ? 1 : -1
  return [...list].sort((a: any, b: any) => ((a[k] || 0) - (b[k] || 0)) * dir)
})

function toggleSort(k: 'created_at' | 'price_points') {
  if (sortKey.value === k) sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = k; sortDir.value = 'desc' }
}
function sortInd(k: 'created_at' | 'price_points') {
  if (sortKey.value !== k) return ''
  return sortDir.value === 'asc' ? ' ↑' : ' ↓'
}

const stats = computed(() => {
  let revenue = 0, refunded = 0, refundedCount = 0
  for (const o of orders.value) {
    if (o.status === 'success') revenue += o.price_points || 0
    // Refunded tile reflects the actual returned points (prorated), not the
    // original price; and the retained portion of a partial refund still counts
    // as revenue.
    if (o.status === 'refunded') { refunded += o.refunded_points || 0; revenue += (o.price_points || 0) - (o.refunded_points || 0); refundedCount++ }
  }
  const refundRate = orders.value.length ? Math.round((refundedCount / orders.value.length) * 100) : 0
  return { revenue, refunded, refundRate }
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

<style scoped>
/* 分段筛选 */
.seg { display: inline-flex; background: #eef1f4; border: 1px solid rgba(28,48,70,.09); border-radius: 8px; padding: 3px; }
.seg-btn {
  display: inline-flex; align-items: center; gap: 5px;
  border: none; background: none; cursor: pointer;
  font: inherit; font-size: 12.5px; color: var(--text-2);
  padding: 4px 12px; border-radius: 5px; transition: background .15s, color .15s, box-shadow .15s;
}
.seg-btn:hover { color: var(--text); }
.seg-btn.active { background: #fff; color: var(--text); font-weight: 600; box-shadow: 0 1px 2px rgba(30,45,60,.1); }
.seg-count { font-size: 11px; color: var(--text-3); }
.seg-btn.active .seg-count { color: var(--text-2); }
.seg-label { font-size: 11.5px; color: var(--text-3); padding: 0 8px 0 6px; align-self: center; }

/* 状态胶囊 */
.pill { display: inline-flex; align-items: center; padding: 1px 9px; border-radius: 999px; font-size: 12px; font-weight: 600; line-height: 1.6; }
.pill-ok { background: rgba(16,185,129,.12); color: #0f9d6f; }
.pill-warn { background: rgba(191,149,64,.15); color: var(--warn); }

/* 桌面表格 */
.orders-table-wrap { background: var(--card); border: 1px solid var(--border); border-radius: var(--r-sm); overflow-x: auto; }
.orders-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.orders-table thead th {
  text-align: left; font-weight: 600; color: var(--text-3); font-size: 12px;
  padding: 10px 14px; border-bottom: 1px solid var(--border); white-space: nowrap;
  background: var(--bg-soft);
}
.orders-table th.num, .orders-table td.num { text-align: right; }
.orders-table th.act-col, .orders-table td.act-col { text-align: right; white-space: nowrap; }
.orders-table th.sortable { cursor: pointer; user-select: none; }
.orders-table th.sortable:hover { color: var(--text); }
.sort-ind { color: var(--text); font-weight: 700; }
.orders-table tbody td { padding: 11px 14px; border-bottom: 1px solid var(--border); color: var(--text-2); vertical-align: middle; }
.orders-table tbody tr:last-child td { border-bottom: none; }
.orders-table tbody tr:hover td { background: var(--bg-soft); }
.orders-table tbody tr.refunded td { color: var(--text-3); }
.orders-table td.strong { color: var(--text); font-weight: 600; }
.orders-table td.muted { color: var(--text-3); font-style: italic; }
.orders-table td.time { color: var(--text-3); white-space: nowrap; }
.orders-table .amount { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
.orders-table .amount-sub { display: block; margin-top: 2px; font-size: 11.5px; color: var(--warn); font-variant-numeric: tabular-nums; }
.orders-table .act-col :deep(.n-button) { margin-left: 6px; }

/* 分组表头行 */
.orders-table tbody tr.group-row td {
  background: var(--bg); padding: 7px 14px; border-bottom: 1px solid var(--border);
}
.orders-table tbody tr.group-row:hover td { background: var(--bg); }
.group-name { font-weight: 650; font-size: 12.5px; color: var(--text); }
.group-meta { margin-left: 10px; font-size: 11.5px; color: var(--text-3); font-variant-numeric: tabular-nums; }

/* 默认隐藏卡片，窄屏才显示 */
.orders-cards { display: none; }
.order-group + .order-group { margin-top: 14px; }
.order-group .group-head {
  display: flex; align-items: baseline; gap: 10px;
  padding: 4px 2px 8px; border-bottom: 1px solid var(--border); margin-bottom: 10px;
}
@media (max-width: 720px) {
  .orders-table-wrap { display: none; }
  .orders-cards { display: block; }
  .page-toolbar .seg { flex-wrap: wrap; }
}
</style>
