<template>
  <n-modal :show="show" @update:show="v => emit('update:show', v)">
    <n-card style="width:440px;max-width:92vw;" title="退款确认" size="small" role="dialog" :bordered="false">
      <n-spin :show="loading">
        <div v-if="q" class="refund-body">
          <div class="rr"><span>商品</span><b>{{ q.name || '—' }}</b></div>
          <div class="rr"><span>类型</span><b>{{ q.type === 'plan' ? '套餐' : '流量包' }}</b></div>
          <div class="rr"><span>原价</span><b>{{ q.price_points }} 积分</b></div>

          <div class="rr"><span>套餐额度</span><b>{{ fmtTotal(q.total_traffic) }}</b></div>
          <div class="rr"><span>已用流量</span><b>{{ fmtBytes(q.used_traffic) }}</b></div>
          <div class="rr"><span>可退流量</span><b>{{ fmtBytes(q.refund_traffic) }}</b></div>

          <div class="rr" v-if="q.traffic_ratio >= 0">
            <span>流量剩余比例</span><b>{{ (q.traffic_ratio * 100).toFixed(1) }}%</b>
          </div>
          <div class="rr" v-if="q.time_ratio >= 0">
            <span>时间剩余比例</span><b>{{ (q.time_ratio * 100).toFixed(1) }}%</b>
          </div>
          <div class="rr" v-if="q.fee_percent > 0">
            <span>手续费</span><b>{{ q.fee_percent }}%</b>
          </div>

          <div class="rr mode-row">
            <span>退款方式</span>
            <n-radio-group :value="mode" size="small" @update:value="onModeChange">
              <n-radio-button value="prorated">按剩余比例</n-radio-button>
              <n-radio-button value="full">全额退</n-radio-button>
            </n-radio-group>
          </div>

          <div class="rr total">
            <span>应退积分</span>
            <b>{{ q.refund_points }} 积分<span class="ratio">（{{ Math.round(q.ratio * 100) }}%）</span></b>
          </div>

          <p v-if="q.already_refunded" class="warn">该订单已退款。</p>
          <p v-else-if="q.refund_points === 0" class="warn">额度已用尽，本次退款为 0 积分（权益仍会撤销）。</p>
        </div>
      </n-spin>
      <template #footer>
        <div style="display:flex;justify-content:flex-end;gap:8px;">
          <n-button size="small" @click="emit('update:show', false)">取消</n-button>
          <n-button size="small" type="warning" :loading="submitting" :disabled="!q || q.already_refunded" @click="confirm">
            确认退款
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal, NCard, NSpin, NButton, NRadioGroup, NRadioButton, useMessage } from 'naive-ui'
import { apiGet, apiPost } from '@/api'
import { fmtBytes, fmtTotal } from '@/utils/format'

const props = defineProps<{ show: boolean; orderId: number | null }>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void; (e: 'done'): void }>()

const message = useMessage()
const q = ref<any>(null)
const loading = ref(false)
const submitting = ref(false)
const mode = ref<'prorated' | 'full'>('prorated')

async function fetchPreview() {
  if (!props.orderId) return
  loading.value = true
  try {
    q.value = await apiGet(`/api/admin/orders/${props.orderId}/refund-preview?mode=${mode.value}`)
  } catch (e: any) {
    message.error(e.message)
    q.value = null
  } finally {
    loading.value = false
  }
}

function onModeChange(v: 'prorated' | 'full') {
  mode.value = v
  fetchPreview()
}

watch(() => props.show, (v) => {
  if (v) { mode.value = 'prorated'; q.value = null; fetchPreview() }
})

async function confirm() {
  if (!props.orderId) return
  submitting.value = true
  try {
    const r = await apiPost(`/api/admin/orders/${props.orderId}/refund`, { mode: mode.value })
    message.success(`已退款 ${r?.refund_points ?? ''} 积分`)
    emit('update:show', false)
    emit('done')
  } catch (e: any) {
    message.error(e.message)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.refund-body { display: flex; flex-direction: column; gap: 8px; }
.rr { display: flex; justify-content: space-between; align-items: center; font-size: 13px; color: var(--text-2); }
.rr b { color: var(--text-1); font-weight: 600; }
.mode-row { margin-top: 4px; }
.total { margin-top: 6px; padding-top: 10px; border-top: 1px solid var(--border, #eee); font-size: 14px; }
.total b { color: #10b981; font-size: 16px; }
.ratio { color: var(--text-3); font-size: 12px; font-weight: 400; }
.warn { margin: 8px 0 0; font-size: 12px; color: var(--warning, #d97706); }
</style>
