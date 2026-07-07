<template>
  <div>
    <h2 class="page-title">套餐管理</h2>
    <div class="page-toolbar">
      <span class="spacer" />
      <n-button type="primary" @click="openForm()">创建套餐</n-button>
    </div>
    <n-spin :show="loading">
      <div v-if="packages.length" class="card-grid">
        <div v-for="p in packages" :key="p.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ p.name || '—' }}</span>
            <n-tag :type="p.enabled !== false ? 'success' : 'default'" size="tiny" bordered="false">{{ p.enabled !== false ? '上架' : '下架' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv"><n-tag :type="p.type === 'traffic' ? 'info' : p.type === 'plan' ? 'success' : 'warning'" size="tiny" bordered="false">{{ p.type === 'traffic' ? '流量' : p.type === 'plan' ? '计划' : '设备' }}</n-tag></span>
            <span class="kv">积分 <b>{{ p.price_points }}</b></span>
            <span class="kv">库存 <b>{{ p.stock < 0 ? '不限' : p.stock }}</b></span>
            <span class="kv">订阅 <b>{{ p.subscribers || 0 }}</b></span>
          </div>
          <div class="lc-meta" style="color:var(--text-2);">
            <span v-if="p.traffic_bytes" class="kv">{{ fmtTotal(p.traffic_bytes) }}</span>
            <span v-if="p.duration_days" class="kv">{{ p.duration_days }}天</span>
            <span v-if="p.device_add" class="kv">+{{ p.device_add }}设备</span>
          </div>
          <div v-if="p.description" class="lc-meta" style="color:var(--text-3);">{{ p.description }}</div>
          <div class="lc-foot" style="flex-wrap:wrap;">
            <n-button size="tiny" @click="openForm(p)">编辑</n-button>
            <n-button v-if="p.enabled !== false" size="tiny" type="warning" @click="handleRetire(p.id)">下架</n-button>
            <n-button v-else size="tiny" type="success" @click="handleEnable(p.id)">上架</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(p.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无套餐" style="padding:40px 0;" />
    </n-spin>

    <n-modal v-model:show="showForm" preset="card" :title="editing ? '编辑套餐' : '创建套餐'" style="max-width:520px;">
      <n-form label-placement="left" label-width="100">
        <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
        <n-form-item label="类型">
          <n-select v-model:value="form.type" :options="[{label:'流量包',value:'traffic'},{label:'订阅计划',value:'plan'},{label:'设备扩展',value:'device'}]" />
        </n-form-item>
        <n-form-item label="描述"><n-input v-model:value="form.description" placeholder="套餐说明" /></n-form-item>
        <n-form-item label="流量 (GB)"><n-input-number v-model:value="form.traffic_gb" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item label="天数"><n-input-number v-model:value="form.days" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item v-if="form.type==='device'" label="设备数"><n-input-number v-model:value="form.device_add" :min="1" style="width:100%;" /></n-form-item>
        <n-form-item label="积分"><n-input-number v-model:value="form.price" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item label="库存（-1不限）"><n-input-number v-model:value="form.stock" :min="-1" style="width:100%;" /></n-form-item>
        <n-form-item v-if="form.type==='plan'" label="节点分组">
          <n-select v-model:value="form.group_ids" :options="groupOptions" multiple placeholder="该套餐可使用的节点分组" />
        </n-form-item>
      </n-form>
      <n-button type="primary" block :loading="saving" @click="handleSave">保存</n-button>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import {
  NSpin, NButton, NModal, NForm, NFormItem, NInput, NInputNumber,
  NSelect, NTag, NEmpty, useMessage
} from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtTotal, yuan } from '@/utils/format'

const message = useMessage()
const packages = ref<any[]>([])
const groups = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const editing = ref<any>(null)
const form = reactive({ name: '', type: 'traffic', description: '', traffic_gb: 0, days: 30, device_add: 1, price: 100, stock: -1, group_ids: [] as number[] })

const groupOptions = computed(() => groups.value.map(g => ({ label: g.name, value: g.id })))

function openForm(pkg?: any) {
  editing.value = pkg || null
  if (pkg) {
    Object.assign(form, {
      name: pkg.name, type: pkg.type, description: pkg.description || '',
      traffic_gb: (pkg.traffic_bytes || 0) / (1024 * 1024 * 1024), days: pkg.duration_days || 0,
      device_add: pkg.device_add || 1, price: pkg.price_points || 0, stock: pkg.stock ?? -1,
      group_ids: pkg.group_ids || [],
    })
  } else {
    Object.assign(form, { name: '', type: 'traffic', description: '', traffic_gb: 0, days: 30, device_add: 1, price: 100, stock: -1, group_ids: [] })
  }
  showForm.value = true
}

async function handleSave() {
  saving.value = true
  try {
    const { traffic_gb, days, price, ...rest } = form
    const body = { ...rest, traffic_bytes: traffic_gb * 1024 * 1024 * 1024, duration_days: days, price_points: price }
    if (editing.value) await apiPut(`/api/admin/packages/${editing.value.id}`, body)
    else await apiPost('/api/admin/packages', body)
    message.success('保存成功'); showForm.value = false; editing.value = null; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function handleRetire(id: number) {
  try { await apiPost(`/api/admin/packages/${id}/retire`); message.success('已下架'); await load() } catch (e: any) { message.error(e.message) }
}
async function handleEnable(id: number) {
  try { await apiPost(`/api/admin/packages/${id}/enable`); message.success('已上架'); await load() } catch (e: any) { message.error(e.message) }
}
async function handleDelete(id: number) {
  try { await apiDelete(`/api/admin/packages/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

async function load() {
  loading.value = true
  try {
    const [pkgs, g] = await Promise.all([apiList('/api/admin/packages'), apiList('/api/admin/node-groups').catch(() => [])])
    packages.value = pkgs; groups.value = g
  } catch {} finally { loading.value = false }
}
onMounted(load)
</script>
