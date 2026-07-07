<template>
  <div>
    <h2 class="page-title">订单记录</h2>
    <p class="page-sub">查看你的购买历史</p>
    <n-spin :show="loading">
      <div v-if="orders.length" class="card-grid">
        <div v-for="o in orders" :key="o.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ o.name || '—' }}</span>
            <n-tag :type="o.status === 'success' ? 'success' : 'warning'" size="small" bordered="false">{{ o.status === 'success' ? '成功' : '已退款' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv"><n-tag :type="o.type === 'plan' ? 'success' : o.type === 'traffic' ? 'info' : 'warning'" size="tiny" bordered="false">{{ o.type === 'plan' ? '计划' : o.type === 'traffic' ? '流量' : o.type || '—' }}</n-tag></span>
            <span class="kv">积分 <b>{{ o.price_points }}</b></span>
            <span class="kv">{{ fmtDateTime(o.created_at) }}</span>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无订单" style="padding:40px 0;">
        <template #extra>
          <n-button size="small" @click="router.push('/shop')">去商城看看</n-button>
        </template>
      </n-empty>
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NSpin, NTag, NEmpty, NButton } from 'naive-ui'
import { apiList } from '@/api'
import { fmtDateTime } from '@/utils/format'

const router = useRouter()
const orders = ref<any[]>([])
const loading = ref(false)

onMounted(async () => { loading.value = true; try { orders.value = await apiList('/api/user/orders') } catch {} finally { loading.value = false } })
</script>

<style scoped>.page-sub { color: var(--text-2); margin-bottom: 16px; }</style>
