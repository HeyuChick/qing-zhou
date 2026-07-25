<template>
  <div>
    <h2 class="page-title">积分商城</h2>
    <p class="page-sub">当前积分：<strong>{{ auth.user?.points || 0 }}</strong> ({{ yuan(auth.user?.points || 0) }})</p>
    <div class="shop-grid">
      <div v-for="pkg in packages" :key="pkg.id" class="shop-card" :class="{ dim: !canAfford(pkg) }">
        <div class="sc-head">
          <div class="sc-name">{{ pkg.name }}</div>
          <span class="sc-badge" :class="typeMeta(pkg.type).cls">{{ typeMeta(pkg.type).label }}</span>
        </div>

        <div class="sc-desc" :class="{ empty: !pkg.description }">
          {{ pkg.description || '暂无套餐说明' }}
        </div>

        <ul v-if="pkg.highlights?.length" class="sc-highlights">
          <li v-for="(h, i) in pkg.highlights" :key="i">{{ h }}</li>
        </ul>

        <div class="sc-specs">
          <div v-for="s in specsOf(pkg)" :key="s.label" class="sc-spec">
            <span class="k">{{ s.label }}</span>
            <span class="v">{{ s.value }}</span>
          </div>
        </div>

        <div class="sc-foot">
          <div v-if="willQueue(pkg)" class="sc-queue-note">✓ 已在使用 · 再买将排队，当前份结束后自动启用</div>
          <div class="sc-price">
            <span class="sc-points">{{ pkg.price_points }}</span>
            <span class="sc-unit">积分</span>
          </div>
          <div class="sc-yuan">{{ yuan(pkg.price_points) }}</div>
          <div v-if="pkg.stock >= 0" class="sc-stock" :class="{ hot: pkg.stock <= 5 }">
            {{ pkg.stock === 0 ? '已售罄' : `仅剩 ${pkg.stock} 件` }}
          </div>
          <n-button type="primary" block class="sc-buy"
            :loading="buying===pkg.id"
            :disabled="!canAfford(pkg) || pkg.stock === 0"
            @click="handleBuy(pkg)">
            {{ pkg.stock === 0 ? '已售罄' : canAfford(pkg) ? '购买' : '积分不足' }}
          </n-button>
        </div>
      </div>
    </div>
    <n-empty v-if="packages.length===0" description="暂无可购买的商品" style="padding:60px 0;" />
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { NButton, NEmpty, useMessage, useDialog } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { apiList, apiPost } from '@/api'
import { fmtTotal, yuan } from '@/utils/format'
const auth = useAuthStore()
const message = useMessage()
const dialog = useDialog()
const packages = ref<any[]>([])
const buying = ref<number|null>(null)
// Package ids the user already has an ACTIVE plan bucket for — buying one of
// these again queues behind the current份 instead of stacking, so we set that
// expectation on the card and in the confirm/success copy.
const heldActive = ref<Set<number>>(new Set())
async function loadHeld() {
  try {
    const plans = await apiList<any>('/api/user/plans')
    heldActive.value = new Set(plans.filter((p: any) => p.kind === 'plan' && p.status === 'active' && p.package_id > 0).map((p: any) => p.package_id))
  } catch {}
}
function willQueue(pkg: any): boolean { return pkg.type === 'plan' && heldActive.value.has(pkg.id) }

function typeMeta(type: string) {
  if (type === 'traffic') return { label: '流量包', cls: 't-traffic' }
  if (type === 'plan') return { label: '订阅计划', cls: 't-plan' }
  return { label: '设备扩展', cls: 't-device' }
}

function canAfford(pkg: any): boolean {
  return (auth.user?.points || 0) >= pkg.price_points
}

// specsOf 为每张卡片生成对齐一致的规格行，方便用户逐项对比不同套餐。
function specsOf(pkg: any) {
  const s: { label: string; value: string }[] = []
  if (pkg.type === 'traffic' || pkg.type === 'plan') {
    s.push({ label: '流量', value: pkg.traffic_bytes ? fmtTotal(pkg.traffic_bytes) : '不限' })
  }
  s.push({ label: '有效期', value: pkg.duration_days ? `${pkg.duration_days} 天` : '永久' })
  if (pkg.device_add) s.push({ label: '设备', value: `+${pkg.device_add} 台` })
  return s
}

function genKey(): string {
  try { if (crypto?.randomUUID) return crypto.randomUUID() } catch {}
  return 'k-' + Date.now() + '-' + Math.random().toString(36).slice(2)
}
async function purchaseWithRetry(packageId: number, key: string) {
  try {
    return await apiPost('/api/user/purchase', { package_id: packageId, idempotency_key: key })
  } catch (e: any) {
    // Retry ONCE on a network-level failure (an error with no HTTP status): the first
    // request may have committed server-side before its response was lost. Reusing the
    // same key makes the backend return the existing order instead of charging twice.
    if (e && e.status === undefined) {
      return await apiPost('/api/user/purchase', { package_id: packageId, idempotency_key: key })
    }
    throw e
  }
}
function handleBuy(pkg: any) {
  const queue = willQueue(pkg)
  const content = queue
    ? `确定花费 ${pkg.price_points} 积分购买「${pkg.name}」？\n你已在使用该套餐，本次购买将排队，在当前份用完或到期后自动启用（有效期届时才开始计算）。`
    : `确定花费 ${pkg.price_points} 积分购买「${pkg.name}」？`
  dialog.warning({ title: '确认购买', content, positiveText: '确定', negativeText: '取消',
    onPositiveClick: async () => {
      buying.value = pkg.id
      const key = genKey() // one key per confirmed purchase intent; stable across the retry
      try {
        await purchaseWithRetry(pkg.id, key)
        message.success(queue ? '已购买并加入队列，将在当前套餐结束后自动启用' : '购买成功，已生效！')
        await auth.fetchMe(); await loadHeld()
      }
      catch (e: any) { message.error(e.message) } finally { buying.value = null }
    } })
}
onMounted(async () => { try { packages.value = await apiList('/api/user/packages') } catch {}; loadHeld() })
</script>
<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }

.shop-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 16px; }

.shop-card {
  display: flex;
  flex-direction: column;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--r);
  padding: 18px 18px 16px;
  transition: box-shadow .18s ease, transform .18s ease, border-color .18s ease;
}
.shop-card:hover { box-shadow: var(--shadow); border-color: #d8d8d8; transform: translateY(-2px); }
.shop-card.dim { opacity: .78; }

.sc-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; }
.sc-name { font-size: 16px; font-weight: 680; color: var(--text); line-height: 1.35; }

.sc-badge {
  flex: none;
  font-size: 11px;
  font-weight: 600;
  padding: 2px 9px;
  border-radius: 999px;
  white-space: nowrap;
  border: 1px solid transparent;
}
.t-traffic { color: var(--info); background: #eef2f6; border-color: #dde6ef; }
.t-plan { color: #4b7a5c; background: #edf4ef; border-color: #d9e8df; }
.t-device { color: var(--warn); background: #f7f1e2; border-color: #ece0c6; }

.sc-desc {
  margin-top: 10px;
  font-size: 13px;
  color: var(--text-2);
  line-height: 1.6;
  min-height: 41px; /* ~2 lines: keeps spec/price rows aligned across cards */
}
.sc-desc.empty { color: var(--text-3); }

.sc-highlights { list-style: none; margin: 12px 0 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.sc-highlights li {
  position: relative;
  padding-left: 22px;
  font-size: 13px;
  color: var(--text);
  line-height: 1.5;
}
.sc-highlights li::before {
  content: "✓";
  position: absolute;
  left: 0;
  top: -1px;
  font-size: 12px;
  font-weight: 700;
  color: #4b7a5c;
}

.sc-specs {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px dashed var(--border);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.sc-spec { display: flex; justify-content: space-between; align-items: baseline; font-size: 13px; }
.sc-spec .k { color: var(--text-3); }
.sc-spec .v { color: var(--text); font-weight: 600; }

.sc-foot { margin-top: 16px; }
.sc-price { display: flex; align-items: baseline; gap: 6px; }
.sc-points { font-size: 24px; font-weight: 740; color: var(--accent-strong); letter-spacing: -.01em; }
.sc-unit { font-size: 13px; color: var(--text-2); }
.sc-yuan { font-size: 12px; color: var(--text-3); margin-top: 2px; }
.sc-stock { font-size: 11px; color: var(--text-3); margin-top: 6px; }
.sc-stock.hot { color: var(--warn); }
.sc-queue-note { font-size: 11px; color: #4b7a5c; background: #edf4ef; border: 1px solid #d9e8df; border-radius: 8px; padding: 5px 8px; margin-bottom: 10px; line-height: 1.4; }
.sc-buy { margin-top: 12px; }
</style>
