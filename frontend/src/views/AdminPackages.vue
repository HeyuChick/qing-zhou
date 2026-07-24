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
            <n-tag v-if="p.user_group_ids?.length" type="warning" size="tiny" bordered="false" :title="userGroupNames(p.user_group_ids)">
              专属
            </n-tag>
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
          <div v-if="p.user_group_ids?.length" class="lc-meta" style="color:var(--text-3);">
            <span class="kv">仅限 <b>{{ userGroupNames(p.user_group_ids) }}</b> 购买</span>
          </div>
          <div v-if="p.description" class="lc-meta" style="color:var(--text-3);">{{ p.description }}</div>
          <div v-if="p.highlights?.length" class="lc-meta" style="color:var(--text-3);gap:4px 10px;">
            <span v-for="(h, i) in p.highlights" :key="i" class="kv">✓ {{ h }}</span>
          </div>
          <div class="lc-foot" style="flex-wrap:wrap;">
            <n-button size="tiny" @click="openForm(p)">编辑</n-button>
            <n-button v-if="p.enabled !== false" size="tiny" type="warning" @click="handleRetire(p)">下架</n-button>
            <n-button v-else size="tiny" type="success" @click="handleEnable(p.id)">上架</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(p)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无套餐" style="padding:40px 0;" />
    </n-spin>

    <n-modal v-model:show="showForm" preset="card" :title="editing ? '编辑套餐' : '创建套餐'" style="max-width:520px;">
      <n-form label-placement="left" label-width="100">
        <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
        <n-form-item label="类型">
          <n-select v-model:value="form.type" :options="[{label:'流量包',value:'traffic'},{label:'订阅计划',value:'plan'}]" />
        </n-form-item>
        <n-form-item label="描述"><n-input v-model:value="form.description" type="textarea" :autosize="{ minRows: 1, maxRows: 3 }" placeholder="套餐一句话说明" /></n-form-item>
        <n-form-item label="亮点">
          <div style="width:100%;">
            <n-dynamic-input v-model:value="form.highlights" :max="8" placeholder="一条卖点，如：全球 50+ 节点 / 不限速 / 7×24 客服" />
            <div style="margin-top:4px;font-size:12px;color:var(--text-3);line-height:1.5;">
              一行一个卖点，商城里会以清单形式展示，最多 8 条。留空则不显示。
            </div>
          </div>
        </n-form-item>
        <n-form-item label="流量 (GB)"><n-input-number v-model:value="form.traffic_gb" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item label="天数"><n-input-number v-model:value="form.days" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item v-if="form.type==='device'" label="设备数"><n-input-number v-model:value="form.device_add" :min="1" style="width:100%;" /></n-form-item>
        <n-form-item label="积分"><n-input-number v-model:value="form.price" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item label="库存（-1不限）"><n-input-number v-model:value="form.stock" :min="-1" style="width:100%;" /></n-form-item>
        <n-form-item v-if="form.type==='plan'" label="节点分组">
          <n-select v-model:value="form.group_ids" :options="groupOptions" multiple placeholder="买了这个套餐，能用哪些节点" />
        </n-form-item>
        <n-form-item label="可购买用户组">
          <div style="width:100%;">
            <n-select
              v-model:value="form.user_group_ids"
              :options="userGroupOptions"
              multiple
              clearable
              placeholder="留空 = 所有人都能买"
            />
            <div style="margin-top:4px;font-size:12px;color:var(--text-3);line-height:1.5;">
              {{ form.user_group_ids.length
                ? '专属套餐：只有所选用户组的成员能看到并购买。'
                : '公开套餐：所有用户都能购买。选择用户组后即变为专属。' }}
            </div>
          </div>
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
  NSelect, NTag, NEmpty, NDynamicInput, useMessage, useDialog
} from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtTotal, yuan } from '@/utils/format'

const message = useMessage()
const dialog = useDialog()
const packages = ref<any[]>([])
const groups = ref<any[]>([])
const userGroups = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const editing = ref<any>(null)
const form = reactive({ name: '', type: 'traffic', description: '', highlights: [] as string[], traffic_gb: 0, days: 30, device_add: 1, price: 100, stock: -1, group_ids: [] as number[], user_group_ids: [] as number[] })

// groups = node groups (which nodes a plan grants); userGroups = who may buy it.
const groupOptions = computed(() => groups.value.map(g => ({ label: g.name, value: g.id })))
const userGroupOptions = computed(() => userGroups.value.map(g => ({ label: g.name, value: g.id })))

function userGroupNames(ids: number[]) {
  return ids
    .map(id => userGroups.value.find(g => g.id === id)?.name)
    .filter(Boolean)
    .join('、')
}

function openForm(pkg?: any) {
  editing.value = pkg || null
  if (pkg) {
    Object.assign(form, {
      name: pkg.name, type: pkg.type, description: pkg.description || '',
      highlights: Array.isArray(pkg.highlights) ? [...pkg.highlights] : [],
      traffic_gb: (pkg.traffic_bytes || 0) / (1024 * 1024 * 1024), days: pkg.duration_days || 0,
      device_add: pkg.device_add || 1, price: pkg.price_points || 0, stock: pkg.stock ?? -1,
      group_ids: pkg.group_ids || [], user_group_ids: pkg.user_group_ids || [],
    })
  } else {
    Object.assign(form, { name: '', type: 'traffic', description: '', highlights: [], traffic_gb: 0, days: 30, device_add: 1, price: 100, stock: -1, group_ids: [], user_group_ids: [] })
  }
  showForm.value = true
}

async function handleSave() {
  saving.value = true
  try {
    const { traffic_gb, days, price, ...rest } = form
    const body = { ...rest, traffic_bytes: Math.round(traffic_gb * 1024 * 1024 * 1024), duration_days: days, price_points: price }
    if (editing.value) await apiPut(`/api/admin/packages/${editing.value.id}`, body)
    else await apiPost('/api/admin/packages', body)
    message.success('保存成功'); showForm.value = false; editing.value = null; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// 下架 refunds every holder and clears their plan — irreversible and moves points,
// so it must be confirmed with the impact spelled out.
function handleRetire(p: any) {
  const cnt = p.subscribers || 0
  dialog.warning({
    title: '确认下架套餐',
    content: cnt > 0
      ? `「${p.name}」当前有 ${cnt} 位用户持有。下架会按剩余流量/时间比例给他们退款并清空该套餐，操作不可撤销。确定继续？`
      : `确定下架「${p.name}」？下架后不可购买（如仍有持有者会被退款并清空）。`,
    positiveText: '下架', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiPost(`/api/admin/packages/${p.id}/retire`); message.success('已下架'); await load() }
      catch (e: any) { message.error(e.message) }
    },
  })
}
async function handleEnable(id: number) {
  try { await apiPost(`/api/admin/packages/${id}/enable`); message.success('已上架'); await load() } catch (e: any) { message.error(e.message) }
}
function handleDelete(p: any) {
  dialog.warning({
    title: '确认删除套餐',
    content: `确定永久删除「${p.name}」？该操作不可撤销。若仍有用户持有，请先下架。`,
    positiveText: '删除', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiDelete(`/api/admin/packages/${p.id}`); message.success('已删除'); await load() }
      catch (e: any) { message.error(e.message) }
    },
  })
}

async function load() {
  loading.value = true
  try {
    const [pkgs, g, ug] = await Promise.all([
      apiList('/api/admin/packages'),
      apiList('/api/admin/node-groups').catch(() => []),
      apiList('/api/admin/user-groups').catch(() => []),
    ])
    packages.value = pkgs; groups.value = g; userGroups.value = ug
  } catch (e: any) { message.error('加载失败：' + (e?.message || '请稍后重试')) } finally { loading.value = false }
}
onMounted(load)
</script>
