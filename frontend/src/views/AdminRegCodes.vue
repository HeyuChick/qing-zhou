<template>
  <div>
    <h2 class="page-title">注册码管理</h2>

    <!-- 生成区 -->
    <n-card size="small" style="margin-bottom:16px;">
      <div style="display:flex;gap:10px;align-items:flex-end;flex-wrap:wrap;">
        <div><div style="font-size:12px;color:var(--text-3);margin-bottom:4px;">数量</div><n-input-number v-model:value="genCount" :min="1" :max="100" style="width:100px;" /></div>
        <div><div style="font-size:12px;color:var(--text-3);margin-bottom:4px;">最大使用次数（0=不限）</div><n-input-number v-model:value="genMaxUses" :min="0" style="width:140px;" /></div>
        <div style="flex:1;min-width:160px;"><div style="font-size:12px;color:var(--text-3);margin-bottom:4px;">备注</div><n-input v-model:value="genNote" placeholder="可选" /></div>
        <div style="flex:1;min-width:200px;">
          <div style="font-size:12px;color:var(--text-3);margin-bottom:4px;">加入用户组（可选）</div>
          <n-select v-model:value="genGroupIDs" :options="userGroupOptions" multiple clearable placeholder="用此码注册即加入" />
        </div>
        <n-button type="primary" :loading="generating" @click="handleGenerate">生成</n-button>
      </div>
      <div v-if="genGroupIDs.length" style="margin-top:8px;font-size:12px;color:var(--text-3);">
        用这批注册码注册的用户将自动加入「{{ groupNames(genGroupIDs) }}」，从而可以购买这些用户组的专属套餐。
      </div>
      <div v-if="generatedCodes.length" style="margin-top:12px;padding:10px;background:var(--bg-soft);border-radius:8px;">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;">
          <span style="font-size:12px;font-weight:600;">已生成 {{ generatedCodes.length }} 个</span>
          <n-button size="tiny" @click="copyAll">复制全部</n-button>
        </div>
        <div style="font-family:monospace;font-size:12px;word-break:break-all;color:var(--text-2);">{{ generatedCodes.join('\n') }}</div>
      </div>
    </n-card>

    <!-- 卡片列表 -->
    <n-spin :show="loading">
      <div v-if="codes.length" class="card-grid">
        <div v-for="r in codes" :key="r.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title" style="font-family:monospace;font-size:12.5px;">{{ r.code }}</span>
            <n-tag :type="regStatusType(r)" size="tiny" :bordered="false">{{ regStatusLabel(r) }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">已用/上限 <b>{{ r.used || 0 }} / {{ r.max_uses || '∞' }}</b></span>
            <span class="kv">{{ fmtDateTime(r.created_at) }}</span>
          </div>
          <div v-if="r.note" class="lc-meta" style="color:var(--text-3);">{{ r.note }}</div>
          <div v-if="r.group_ids?.length" class="lc-meta">
            <span class="kv">加入用户组 <b>{{ groupNames(r.group_ids) }}</b></span>
          </div>
          <div v-if="r.uses && r.uses.length" class="lc-meta" style="flex-direction:column;align-items:flex-start;gap:2px;font-size:11px;">
            <span v-for="(u, i) in r.uses" :key="i">{{ u.username || u.email || '#' + u.user_id }} · {{ fmtDateTime(u.used_at) }}</span>
          </div>
          <div class="lc-foot">
            <n-button size="tiny" @click="copyCode(r.code)">复制</n-button>
            <n-button size="tiny" @click="toggleEnabled(r)">{{ r.enabled ? '禁用' : '启用' }}</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(r.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无注册码" style="padding:40px 0;" />
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { NCard, NInputNumber, NInput, NButton, NTag, NSpin, NSelect, NEmpty, useMessage } from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtDateTime } from '@/utils/format'
import { copyText } from '@/utils/clipboard'

const message = useMessage()
const codes = ref<any[]>([])
const loading = ref(false)
const generating = ref(false)
const genCount = ref(10)
const genMaxUses = ref(0)
const genNote = ref('')
const genGroupIDs = ref<number[]>([])
const generatedCodes = ref<string[]>([])

const userGroups = ref<any[]>([])
const userGroupOptions = computed(() => userGroups.value.map(g => ({ label: g.name, value: g.id })))
function groupNames(ids?: number[]) {
  return (ids || []).map(id => userGroups.value.find(g => g.id === id)?.name).filter(Boolean).join('、')
}

function regStatusType(r: any): any {
  if (!r.enabled) return 'default'
  if (r.max_uses > 0 && (r.used || 0) >= r.max_uses) return 'warning'
  return 'success'
}
function regStatusLabel(r: any): string {
  if (!r.enabled) return '禁用'
  if (r.max_uses > 0 && (r.used || 0) >= r.max_uses) return '用尽'
  return '可用'
}

async function handleGenerate() {
  generating.value = true
  try {
    const data = await apiPost<any>('/api/admin/reg-codes/generate', {
      count: genCount.value, max_uses: genMaxUses.value, note: genNote.value, group_ids: genGroupIDs.value,
    })
    const newCodes = data?.codes || []
    generatedCodes.value = newCodes
    message.success(`已生成 ${newCodes.length} 个注册码`)
    await load()
  } catch (e: any) { message.error(e.message) } finally { generating.value = false }
}

async function copyCode(code: string) {
  if (await copyText(code)) message.success('已复制'); else message.error('复制失败，请手动复制')
}
async function copyAll() {
  if (await copyText(generatedCodes.value.join('\n'))) message.success('已复制全部'); else message.error('复制失败，请手动复制')
}

async function toggleEnabled(r: any) {
  try { await apiPut(`/api/admin/reg-codes/${r.id}`, { enabled: !r.enabled }); r.enabled = !r.enabled; message.success(r.enabled ? '已启用' : '已禁用') } catch (e: any) { message.error(e.message) }
}
async function handleDelete(id: number) {
  try { await apiDelete(`/api/admin/reg-codes/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

async function load() {
  loading.value = true
  try {
    const [cs, gs] = await Promise.all([
      apiList('/api/admin/reg-codes'),
      apiList('/api/admin/user-groups').catch(() => []),
    ])
    codes.value = cs; userGroups.value = gs
  } catch {} finally { loading.value = false }
}
onMounted(load)
</script>
