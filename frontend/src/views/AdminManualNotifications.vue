<template>
  <div>
    <div class="page-head"><div><h2 class="page-title">手动通知</h2><p class="page-sub">通过 Telegram 向全部正常用户或指定多个用户发送，并保留逐用户结果。</p></div></div>
    <div class="resource-overview">
      <div class="resource-metric"><b>{{ users.length }}</b><span>可选接收用户</span></div>
      <div class="resource-metric"><b>{{ history.length }}</b><span>历史发送任务</span></div>
      <div class="resource-metric success"><b>{{ history.reduce((sum, item) => sum + (item.sent || 0), 0) }}</b><span>累计发送成功</span></div>
      <div class="resource-metric" :class="{ danger: history.reduce((sum, item) => sum + (item.failed || 0), 0) }"><b>{{ history.reduce((sum, item) => sum + (item.failed || 0), 0) }}</b><span>累计发送失败</span></div>
    </div>

    <n-card title="发送通知" size="small" style="margin-bottom:16px;">
      <n-form label-placement="left" label-width="90">
        <n-form-item label="标题">
          <n-input v-model:value="form.title" maxlength="100" show-count placeholder="通知标题" />
        </n-form-item>
        <n-form-item label="内容">
          <n-input v-model:value="form.content" type="textarea" :rows="6" maxlength="3000" show-count placeholder="通知内容" />
        </n-form-item>
        <n-form-item label="接收用户">
          <n-radio-group v-model:value="form.target_type">
            <n-space>
              <n-radio value="all">全部正常用户</n-radio>
              <n-radio value="selected">指定用户</n-radio>
            </n-space>
          </n-radio-group>
        </n-form-item>
        <n-form-item v-if="form.target_type === 'selected'" label="选择用户">
          <n-select v-model:value="form.user_ids" multiple filterable clearable :options="userOptions"
                    :loading="loadingUsers" placeholder="可选择多个用户" />
        </n-form-item>
      </n-form>
      <n-alert type="info" :bordered="false" style="margin-bottom:12px;">
        “全部”包含所有正常的非管理员用户；未绑定 Telegram 的用户也会记录，并标记为“未发送”。
      </n-alert>
      <n-button type="primary" :loading="sending" @click="send">确认发送</n-button>
    </n-card>

    <n-card title="发送历史" size="small">
      <n-spin :show="loadingHistory">
        <n-data-table :columns="columns" :data="history" :row-key="(row:any) => row.id" />
        <n-empty v-if="!loadingHistory && history.length === 0" description="暂无发送记录" style="padding:30px;" />
      </n-spin>
    </n-card>

    <n-modal v-model:show="showDetail" preset="card" title="发送详情" style="max-width:900px;">
      <template v-if="detail">
        <n-descriptions bordered :column="2" size="small" style="margin-bottom:16px;">
          <n-descriptions-item label="标题">{{ detail.notification.title }}</n-descriptions-item>
          <n-descriptions-item label="时间">{{ fmtDate(detail.notification.created_at) }}</n-descriptions-item>
          <n-descriptions-item label="范围">{{ detail.notification.target_type === 'all' ? '全部正常用户' : '指定用户' }}</n-descriptions-item>
          <n-descriptions-item label="结果">
            成功 {{ detail.notification.sent }} / 未发送 {{ detail.notification.skipped }} / 失败 {{ detail.notification.failed }} / 发送中 {{ detail.notification.pending }}
          </n-descriptions-item>
          <n-descriptions-item label="内容" :span="2"><div style="white-space:pre-wrap;">{{ detail.notification.content || '—' }}</div></n-descriptions-item>
        </n-descriptions>
        <n-data-table :columns="recipientColumns" :data="detail.recipients" :row-key="(row:any) => row.user_id" :max-height="480" />
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NEmpty, NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup, NSelect, NSpace, NSpin, NTag, useDialog, useMessage } from 'naive-ui'
import { apiGet, apiList, apiPost } from '@/api'
import { fmtDate } from '@/utils/format'

const message = useMessage()
const dialog = useDialog()
const form = reactive<{title:string;content:string;target_type:'all'|'selected';user_ids:number[]}>({ title:'', content:'', target_type:'all', user_ids:[] })
const users = ref<any[]>([])
const history = ref<any[]>([])
const detail = ref<any>(null)
const loadingUsers = ref(false)
const loadingHistory = ref(false)
const sending = ref(false)
const showDetail = ref(false)
let refreshTimer: ReturnType<typeof setTimeout> | null = null
const userOptions = ref<any[]>([])

function statusTag(status:string) {
  const map:any = { sent:['success','已发送'], failed:['error','失败'], skipped:['warning','未发送'], pending:['info','发送中'] }
  const item = map[status] || ['default', status]
  return h(NTag, { type:item[0], size:'small', bordered:false }, { default:() => item[1] })
}
const columns:any[] = [
  { title:'时间', key:'created_at', width:170, render:(r:any) => fmtDate(r.created_at) },
  { title:'标题', key:'title', ellipsis:{tooltip:true} },
  { title:'范围', key:'target_type', width:110, render:(r:any) => r.target_type === 'all' ? '全部用户' : '指定用户' },
  { title:'总数', key:'total', width:70 },
  { title:'已发送', key:'sent', width:80 },
  { title:'未发送', key:'skipped', width:80 },
  { title:'失败', key:'failed', width:70 },
  { title:'操作', key:'actions', width:80, render:(r:any) => h(NButton,{size:'tiny',onClick:() => openDetail(r.id)},{default:()=>'详情'}) },
]
const recipientColumns:any[] = [
  { title:'用户', key:'username', width:180 },
  { title:'用户 ID', key:'user_id', width:90 },
  { title:'状态', key:'status', width:100, render:(r:any) => statusTag(r.status) },
  { title:'发送时间', key:'sent_at', width:170, render:(r:any) => r.sent_at ? fmtDate(r.sent_at) : '—' },
  { title:'原因', key:'error', ellipsis:{tooltip:true}, render:(r:any) => r.error || '—' },
]

async function loadUsers() {
  loadingUsers.value = true
  try {
    users.value = await apiList('/api/admin/manual-notifications/users')
    userOptions.value = users.value.map((u:any) => ({
      label: `${u.username}${u.email ? ` · ${u.email}` : ''}${u.telegram_bound ? '' : ' · 未绑定 Telegram'}`,
      value: u.id,
    }))
  } catch (e:any) { message.error(e.message) } finally { loadingUsers.value = false }
}
async function loadHistory() {
  loadingHistory.value = true
  try { history.value = await apiList('/api/admin/manual-notifications') }
  catch (e:any) { message.error(e.message) }
  finally { loadingHistory.value = false }
}
async function openDetail(id:number) {
  try { detail.value = await apiGet(`/api/admin/manual-notifications/${id}`); showDetail.value = true }
  catch (e:any) { message.error(e.message) }
}
function send() {
  if (!form.title.trim()) return message.warning('请填写标题')
  if (form.target_type === 'selected' && form.user_ids.length === 0) return message.warning('请选择至少一个用户')
  const target = form.target_type === 'all' ? '全部正常用户' : `选中的 ${form.user_ids.length} 个用户`
  dialog.warning({ title:'确认发送', content:`确定通过 Telegram 向${target}发送此通知吗？`, positiveText:'确认发送', negativeText:'取消', onPositiveClick:submit })
}
async function submit() {
  sending.value = true
  try {
    const created = await apiPost<any>('/api/admin/manual-notifications', { ...form })
    message.success(`通知已创建，共 ${created.total} 位接收用户`)
    form.title = ''; form.content = ''; form.user_ids = []
    await loadHistory()
    if (refreshTimer) clearTimeout(refreshTimer)
    refreshTimer = setTimeout(loadHistory, 1200)
  } catch (e:any) { message.error(e.message) } finally { sending.value = false }
}

onMounted(() => { loadUsers(); loadHistory() })
onBeforeUnmount(() => { if (refreshTimer) clearTimeout(refreshTimer) })
</script>
