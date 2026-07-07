<template>
  <div>
    <h2 class="page-title">节点管理</h2>
    <n-tabs v-model:value="tab" animated>
      <!-- 节点 -->
      <n-tab-pane name="nodes" tab="节点">
        <div class="page-toolbar">
          <span class="spacer" />
          <n-button size="small" @click="openNodeImport">批量导入</n-button>
          <n-button size="small" type="primary" @click="openNode()">添加节点</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="nodes.length" class="card-grid">
            <div v-for="r in nodes" :key="r.id" class="list-card">
              <div class="lc-head">
                <span class="lc-title">{{ r.name || '—' }}</span>
                <n-tag :type="r.enabled ? 'success' : 'default'" size="tiny" bordered="false">{{ r.enabled ? '启用' : '禁用' }}</n-tag>
              </div>
              <div class="lc-meta">
                <span class="kv"><n-tag :type="r.type === 'self_built' ? 'info' : 'warning'" size="tiny" bordered="false">{{ r.type === 'self_built' ? '自建' : '外部' }}</n-tag></span>
                <span class="kv">协议 <b>{{ nodeProtocol(r) }}</b></span>
              </div>
              <div class="lc-meta"><span class="kv">分组 <b>{{ groupNames(r.group_ids) }}</b></span></div>
              <div class="lc-foot">
                <n-button size="tiny" @click="openNode(r)">编辑</n-button>
                <n-button size="tiny" type="error" @click="handleDeleteNode(r.id)">删除</n-button>
              </div>
            </div>
          </div>
          <n-empty v-else-if="!loading" description="暂无节点" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>

      <!-- 分组 -->
      <n-tab-pane name="groups" tab="分组">
        <div class="page-toolbar">
          <span class="spacer" />
          <n-button size="small" type="primary" @click="openGroup()">添加分组</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="groups.length" class="card-grid">
            <div v-for="r in groups" :key="r.id" class="list-card">
              <div class="lc-head">
                <span class="lc-title">{{ r.name || '—' }}</span>
                <n-tag size="tiny" bordered="false">{{ r.node_count || 0 }} 节点</n-tag>
              </div>
              <div v-if="r.description" class="lc-meta" style="color:var(--text-2);">{{ r.description }}</div>
              <div class="lc-meta"><span class="kv">排序 <b>{{ r.sort_order ?? 0 }}</b></span></div>
              <div class="lc-foot">
                <n-button size="tiny" @click="openGroup(r)">编辑</n-button>
                <n-button size="tiny" type="error" @click="handleDeleteGroup(r.id)">删除</n-button>
              </div>
            </div>
          </div>
          <n-empty v-else-if="!loading" description="暂无分组" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>

      <!-- 订阅源 -->
      <n-tab-pane name="sources" tab="订阅源">
        <div class="page-toolbar">
          <span class="spacer" />
          <n-button size="small" type="primary" @click="openSource()">添加订阅源</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="sources.length" class="card-grid">
            <div v-for="r in sources" :key="r.id" class="list-card">
              <div class="lc-head">
                <span class="lc-title">{{ r.name || '—' }}</span>
                <n-tag size="tiny" bordered="false">{{ r.last_count || 0 }} 节点</n-tag>
              </div>
              <div class="lc-meta" style="word-break:break-all;"><span class="kv">{{ r.url }}</span></div>
              <div class="lc-foot">
                <n-button size="tiny" type="primary" @click="handleFetchSource(r.id)">拉取</n-button>
                <n-button size="tiny" @click="openSource(r)">编辑</n-button>
                <n-button size="tiny" type="error" @click="handleDeleteSource(r.id)">删除</n-button>
              </div>
            </div>
          </div>
          <n-empty v-else-if="!loading" description="暂无订阅源" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>
    </n-tabs>

    <!-- 节点编辑抽屉 -->
    <n-drawer v-model:show="showNode" :width="drawerW" placement="right">
      <n-drawer-content :title="editingNode ? '编辑节点' : '添加节点'" closable>
        <n-form label-placement="left" label-width="80">
          <n-form-item label="名称"><n-input v-model:value="nodeForm.name" /></n-form-item>
          <n-form-item label="类型">
            <n-radio-group v-model:value="nodeForm.type">
              <n-radio value="self_built">自建</n-radio>
              <n-radio value="external">外部</n-radio>
            </n-radio-group>
          </n-form-item>
          <n-form-item v-if="nodeForm.type === 'self_built'" label="入站 Tag">
            <n-select v-model:value="nodeForm.inbound_tag" :options="inboundOptions" placeholder="选择入站" />
          </n-form-item>
          <n-form-item v-if="nodeForm.type === 'external'" label="分享链接">
            <n-input v-model:value="nodeForm.link" type="textarea" :rows="3" placeholder="vless://... 或订阅链接" />
          </n-form-item>
          <n-form-item label="分组">
            <n-select v-model:value="nodeForm.group_ids" :options="groupOptions" multiple placeholder="选择分组" />
          </n-form-item>
          <n-form-item label="启用"><n-switch v-model:value="nodeForm.enabled" /></n-form-item>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="handleSaveNode">保存</n-button>
      </n-drawer-content>
    </n-drawer>

    <!-- 批量导入抽屉 -->
    <n-drawer v-model:show="showImport" :width="drawerW" placement="right">
      <n-drawer-content title="批量导入节点" closable>
        <n-form label-placement="left" label-width="80">
          <n-form-item label="分享链接">
            <n-input v-model:value="importLinks" type="textarea" :rows="8" placeholder="每行一个分享链接或粘贴订阅内容(base64)" />
          </n-form-item>
          <n-form-item label="分组">
            <n-select v-model:value="importGroupIds" :options="groupOptions" multiple placeholder="选择分组" />
          </n-form-item>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="handleImport">导入</n-button>
      </n-drawer-content>
    </n-drawer>

    <!-- 分组编辑抽屉 -->
    <n-drawer v-model:show="showGroup" :width="drawerW" placement="right">
      <n-drawer-content :title="editingGroup ? '编辑分组' : '添加分组'" closable>
        <n-form label-placement="left" label-width="60">
          <n-form-item label="名称"><n-input v-model:value="groupForm.name" /></n-form-item>
          <n-form-item label="描述"><n-input v-model:value="groupForm.description" /></n-form-item>
          <n-form-item label="排序"><n-input-number v-model:value="groupForm.sort_order" :min="0" style="width:100%;" /></n-form-item>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="handleSaveGroup">保存</n-button>
      </n-drawer-content>
    </n-drawer>

    <!-- 订阅源编辑抽屉 -->
    <n-drawer v-model:show="showSource" :width="drawerW" placement="right">
      <n-drawer-content :title="editingSource ? '编辑订阅源' : '添加订阅源'" closable>
        <n-form label-placement="left" label-width="60">
          <n-form-item label="名称"><n-input v-model:value="sourceForm.name" /></n-form-item>
          <n-form-item label="URL"><n-input v-model:value="sourceForm.url" placeholder="https://..." /></n-form-item>
          <n-form-item label="分组">
            <n-select v-model:value="sourceForm.group_ids" :options="groupOptions" multiple placeholder="选择分组" />
          </n-form-item>
          <n-form-item label="启用"><n-switch v-model:value="sourceForm.enabled" /></n-form-item>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="handleSaveSource">保存</n-button>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import {
  NTabs, NTabPane, NDrawer, NDrawerContent, NButton, NForm, NFormItem, NInput, NInputNumber,
  NSelect, NSwitch, NRadioGroup, NRadio, NTag, NSpin, NEmpty, useMessage
} from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'

const message = useMessage()
const tab = ref('nodes')
const loading = ref(false)
const saving = ref(false)

// 抽屉宽度：移动端全屏，桌面 460px
const isMobile = ref(false)
const drawerW = computed(() => isMobile.value ? '100%' : 460)
function checkMobile() { isMobile.value = window.matchMedia('(max-width: 768px)').matches }
onMounted(() => { checkMobile(); window.addEventListener('resize', checkMobile); load() })
onUnmounted(() => window.removeEventListener('resize', checkMobile))

const nodes = ref<any[]>([])
const groups = ref<any[]>([])
const sources = ref<any[]>([])
const inbounds = ref<any[]>([])

const groupMap = computed(() => new Map(groups.value.map(g => [g.id, g.name])))
const inboundMap = computed(() => new Map(inbounds.value.map(i => [i.tag, i])))
const groupOptions = computed(() => groups.value.map(g => ({ label: g.name, value: g.id })))
const inboundOptions = computed(() => inbounds.value.map(i => ({ label: `${i.tag} (${i.type}:${i.listen_port})`, value: i.tag })))

function nodeProtocol(r: any): string {
  if (r.protocol) return r.protocol.toUpperCase()
  if (r.type === 'self_built' && r.inbound_tag) {
    const ib = inboundMap.value.get(r.inbound_tag)
    if (ib) return ib.type.toUpperCase()
  }
  return '—'
}
function groupNames(ids: number[] | undefined): string {
  if (!ids || !ids.length) return '—'
  return ids.map(id => groupMap.value.get(id) || '#' + id).join(', ')
}

// --- Nodes ---
const showNode = ref(false)
const editingNode = ref<any>(null)
const nodeForm = reactive({ name: '', type: 'self_built', inbound_tag: '', link: '', group_ids: [] as number[], enabled: true })
function openNode(n?: any) {
  editingNode.value = n || null
  if (n) {
    Object.assign(nodeForm, { name: n.name, type: n.type || 'self_built', inbound_tag: n.inbound_tag || '', link: '', group_ids: n.group_ids || [], enabled: n.enabled })
  } else {
    Object.assign(nodeForm, { name: '', type: 'self_built', inbound_tag: '', link: '', group_ids: [], enabled: true })
  }
  showNode.value = true
}
async function handleSaveNode() {
  saving.value = true
  try {
    if (editingNode.value) await apiPut(`/api/admin/nodes/${editingNode.value.id}`, nodeForm)
    else await apiPost('/api/admin/nodes', nodeForm)
    message.success('保存成功'); showNode.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}
async function handleDeleteNode(id: number) {
  try { await apiDelete(`/api/admin/nodes/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

// --- Bulk import ---
const showImport = ref(false)
const importLinks = ref('')
const importGroupIds = ref<number[]>([])
function openNodeImport() { importLinks.value = ''; importGroupIds.value = []; showImport.value = true }
async function handleImport() {
  saving.value = true
  try {
    await apiPost('/api/admin/nodes/import', { links: importLinks.value, group_ids: importGroupIds.value })
    message.success('导入成功'); showImport.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// --- Groups ---
const showGroup = ref(false)
const editingGroup = ref<any>(null)
const groupForm = reactive({ name: '', description: '', sort_order: 0 })
function openGroup(g?: any) {
  editingGroup.value = g || null
  if (g) Object.assign(groupForm, { name: g.name, description: g.description || '', sort_order: g.sort_order || 0 })
  else Object.assign(groupForm, { name: '', description: '', sort_order: 0 })
  showGroup.value = true
}
async function handleSaveGroup() {
  saving.value = true
  try {
    if (editingGroup.value) await apiPut(`/api/admin/node-groups/${editingGroup.value.id}`, groupForm)
    else await apiPost('/api/admin/node-groups', groupForm)
    message.success('保存成功'); showGroup.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}
async function handleDeleteGroup(id: number) {
  try { await apiDelete(`/api/admin/node-groups/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

// --- Sources ---
const showSource = ref(false)
const editingSource = ref<any>(null)
const sourceForm = reactive({ name: '', url: '', group_ids: [] as number[], enabled: true })
function openSource(s?: any) {
  editingSource.value = s || null
  if (s) Object.assign(sourceForm, { name: s.name, url: s.url, group_ids: s.group_ids || [], enabled: s.enabled })
  else Object.assign(sourceForm, { name: '', url: '', group_ids: [], enabled: true })
  showSource.value = true
}
async function handleSaveSource() {
  saving.value = true
  try {
    if (editingSource.value) await apiPut(`/api/admin/node-sources/${editingSource.value.id}`, sourceForm)
    else await apiPost('/api/admin/node-sources', sourceForm)
    message.success('保存成功'); showSource.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}
async function handleFetchSource(id: number) {
  try { await apiPost(`/api/admin/node-sources/${id}/fetch`); message.success('拉取成功'); await load() } catch (e: any) { message.error(e.message) }
}
async function handleDeleteSource(id: number) {
  try { await apiDelete(`/api/admin/node-sources/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

async function load() {
  loading.value = true
  try {
    const [n, g, s, i] = await Promise.all([
      apiList('/api/admin/nodes'), apiList('/api/admin/node-groups'),
      apiList('/api/admin/node-sources'), apiList('/api/admin/inbounds'),
    ])
    nodes.value = n; groups.value = g; sources.value = s; inbounds.value = i
  } catch {} finally { loading.value = false }
}
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 16px; }
:deep(.n-drawer-content-body) { display: flex; flex-direction: column; }
</style>
