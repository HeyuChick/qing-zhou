<template>
  <div>
    <div class="page-head"><div><h2 class="page-title">手动通知</h2><p class="page-sub">通过 Telegram 或邮件，向全部正常用户或指定用户发送，并保留逐用户结果。</p></div></div>
    <div class="resource-overview">
      <div class="resource-metric"><b>{{ users.length }}</b><span>可选接收用户</span></div>
      <div class="resource-metric"><b>{{ history.length }}</b><span>历史发送任务</span></div>
      <div class="resource-metric success"><b>{{ history.reduce((sum, item) => sum + (item.sent || 0), 0) }}</b><span>累计发送成功</span></div>
      <div class="resource-metric" :class="{ danger: history.reduce((sum, item) => sum + (item.failed || 0), 0) }"><b>{{ history.reduce((sum, item) => sum + (item.failed || 0), 0) }}</b><span>累计发送失败</span></div>
    </div>

    <n-card title="发送通知" size="small" style="margin-bottom:16px;">
      <n-form label-placement="left" label-width="90">
        <n-form-item label="标题">
          <n-input ref="titleInput" v-model:value="form.title" maxlength="100" show-count
                   :placeholder="'例如：' + token('username') + '，套餐即将到期'" @focus="lastFocused = 'title'" />
        </n-form-item>
        <n-form-item label="内容">
          <n-input ref="contentInput" v-model:value="form.content" type="textarea" :rows="6" maxlength="3000" show-count
                   :placeholder="'支持 ' + token('username') + ' 等变量，发送时按每位用户替换'" @focus="lastFocused = 'content'" />
        </n-form-item>
        <n-form-item label="变量">
          <div class="mn-vars">
            <div class="mn-vars-h">发送时按收件人替换，点击插入到标题或正文</div>
            <div class="mn-vars-list">
              <button v-for="v in notifyVars" :key="v.key" type="button" class="mn-var" @click="insertVar(v.key)">
                <code>{{ token(v.key) }}</code>
                <span>{{ v.desc }}</span>
              </button>
            </div>
            <p class="mn-vars-note">Telegram 和邮件都会替换。未知变量会原样留下。订阅地址不会作为变量发出。</p>
          </div>
        </n-form-item>
        <n-form-item label="发送渠道">
          <n-radio-group v-model:value="form.channel">
            <n-space>
              <n-radio value="telegram" :disabled="!config.telegram_enabled">Telegram</n-radio>
              <n-radio value="email" :disabled="!config.email_enabled">邮件</n-radio>
              <n-radio value="both" :disabled="!config.telegram_enabled || !config.email_enabled">Telegram + 邮件</n-radio>
            </n-space>
          </n-radio-group>
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
        “全部”包含所有正常的非管理员用户。缺少对应渠道的用户也会记入历史，并标记为“未发送”。Telegram 看绑定，邮件看账号邮箱。
      </n-alert>
      <n-alert v-if="channelHint" type="warning" :bordered="false" style="margin-bottom:12px;">{{ channelHint }}</n-alert>
      <n-button type="primary" :loading="sending" :disabled="!canSend" @click="send">确认发送</n-button>
    </n-card>

    <n-card title="发送历史" size="small">
      <n-spin :show="loadingHistory">
        <n-data-table :columns="columns" :data="history" :row-key="(row:any) => row.id" />
        <n-empty v-if="!loadingHistory && history.length === 0" description="暂无发送记录" style="padding:30px;" />
      </n-spin>
    </n-card>

    <n-modal v-model:show="showDetail" preset="card" title="发送详情" style="max-width:980px;">
      <template v-if="detail">
        <n-descriptions bordered :column="2" size="small" style="margin-bottom:16px;">
          <n-descriptions-item label="标题">{{ detail.notification.title }}</n-descriptions-item>
          <n-descriptions-item label="时间">{{ fmtDate(detail.notification.created_at) }}</n-descriptions-item>
          <n-descriptions-item label="范围">{{ detail.notification.target_type === 'all' ? '全部正常用户' : '指定用户' }}</n-descriptions-item>
          <n-descriptions-item label="渠道">{{ channelLabel(detail.notification.channel) }}</n-descriptions-item>
          <n-descriptions-item label="结果" :span="2">
            成功 {{ detail.notification.sent }} / 未发送 {{ detail.notification.skipped }} / 失败 {{ detail.notification.failed }} / 发送中 {{ detail.notification.pending }}
          </n-descriptions-item>
          <n-descriptions-item label="内容" :span="2"><div style="white-space:pre-wrap;">{{ detail.notification.content || '—' }}</div></n-descriptions-item>
        </n-descriptions>
        <n-data-table :columns="recipientColumns" :data="detail.recipients" :row-key="(row:any) => `${row.user_id}-${row.channel}`" :max-height="480" />
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NEmpty, NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup, NSelect, NSpace, NSpin, NTag, useDialog, useMessage } from 'naive-ui'
import { apiGet, apiList, apiPost } from '@/api'
import { useConfigStore } from '@/stores/config'
import { fmtDate } from '@/utils/format'

const message = useMessage()
const dialog = useDialog()
const { config, fetchConfig } = useConfigStore()
const form = reactive<{title:string;content:string;target_type:'all'|'selected';channel:'telegram'|'email'|'both';user_ids:number[]}>({
  title:'', content:'', target_type:'all', channel:'telegram', user_ids:[]
})
const titleInput = ref<any>(null)
const contentInput = ref<any>(null)
const lastFocused = ref<'title'|'content'>('content')
const notifyVars = ref<{key:string;desc:string}[]>([])
const users = ref<any[]>([])
const history = ref<any[]>([])
const detail = ref<any>(null)
const loadingUsers = ref(false)
const loadingHistory = ref(false)
const sending = ref(false)
const showDetail = ref(false)
let refreshTimer: ReturnType<typeof setTimeout> | null = null

const channelReady = computed(() => {
  if (form.channel === 'telegram') return !!config.telegram_enabled
  if (form.channel === 'email') return !!config.email_enabled
  return !!config.telegram_enabled && !!config.email_enabled
})
const canSend = computed(() => channelReady.value)
const channelHint = computed(() => {
  if (form.channel === 'telegram' && !config.telegram_enabled) return '尚未配置 Telegram Bot，无法发送 Telegram 通知。'
  if (form.channel === 'email' && !config.email_enabled) return '尚未配置邮件服务，无法发送邮件通知。'
  if (form.channel === 'both' && (!config.telegram_enabled || !config.email_enabled)) return '同时发送需要 Telegram Bot 和邮件服务都已配置。'
  return ''
})
const userOptions = computed(() => users.value.map((u:any) => {
  const marks:string[] = []
  if (u.email) marks.push(u.email)
  if (!u.telegram_bound) marks.push('未绑定 Telegram')
  if (!u.email_bound) marks.push('未绑定邮箱')
  return { label: marks.length ? `${u.username} · ${marks.join(' · ')}` : u.username, value: u.id }
}))

function statusTag(status:string) {
  const map:any = { sent:['success','已发送'], failed:['error','失败'], skipped:['warning','未发送'], pending:['info','发送中'] }
  const item = map[status] || ['default', status]
  return h(NTag, { type:item[0], size:'small', bordered:false }, { default:() => item[1] })
}
function channelLabel(channel:string) {
  if (channel === 'email') return '邮件'
  if (channel === 'both') return 'Telegram + 邮件'
  return 'Telegram'
}
function recipientChannelLabel(channel:string) {
  return channel === 'email' ? '邮件' : 'Telegram'
}
function token(key:string) { return '{{' + key + '}}' }
function insertVar(key:string) {
  const field = lastFocused.value
  const tokenText = token(key)
  const inputRef = field === 'title' ? titleInput.value : contentInput.value
  const el: HTMLTextAreaElement | HTMLInputElement | undefined = inputRef?.textareaEl
    || inputRef?.inputEl
    || inputRef?.$el?.querySelector?.('textarea,input')
  const cur = field === 'title' ? form.title : form.content
  if (!el) {
    if (field === 'title') form.title = cur + tokenText
    else form.content = cur + tokenText
    return
  }
  const start = el.selectionStart ?? cur.length
  const end = el.selectionEnd ?? cur.length
  const next = cur.slice(0, start) + tokenText + cur.slice(end)
  if (field === 'title') form.title = next
  else form.content = next
  requestAnimationFrame(() => {
    el.focus()
    const pos = start + tokenText.length
    el.setSelectionRange(pos, pos)
  })
}
const columns:any[] = [
  { title:'时间', key:'created_at', width:170, render:(r:any) => fmtDate(r.created_at) },
  { title:'标题', key:'title', ellipsis:{tooltip:true} },
  { title:'范围', key:'target_type', width:110, render:(r:any) => r.target_type === 'all' ? '全部用户' : '指定用户' },
  { title:'渠道', key:'channel', width:130, render:(r:any) => channelLabel(r.channel) },
  { title:'总数', key:'total', width:70 },
  { title:'已发送', key:'sent', width:80 },
  { title:'未发送', key:'skipped', width:80 },
  { title:'失败', key:'failed', width:70 },
  { title:'操作', key:'actions', width:80, render:(r:any) => h(NButton,{size:'tiny',onClick:() => openDetail(r.id)},{default:()=>'详情'}) },
]
const recipientColumns:any[] = [
  { title:'用户', key:'username', width:160 },
  { title:'用户 ID', key:'user_id', width:90 },
  { title:'渠道', key:'channel', width:90, render:(r:any) => recipientChannelLabel(r.channel) },
  { title:'邮箱', key:'email', width:180, ellipsis:{tooltip:true}, render:(r:any) => r.email || '—' },
  { title:'状态', key:'status', width:100, render:(r:any) => statusTag(r.status) },
  { title:'发送时间', key:'sent_at', width:170, render:(r:any) => r.sent_at ? fmtDate(r.sent_at) : '—' },
  { title:'原因', key:'error', ellipsis:{tooltip:true}, render:(r:any) => r.error || '—' },
]

watch(() => [config.telegram_enabled, config.email_enabled], () => {
  if (form.channel === 'telegram' && !config.telegram_enabled && config.email_enabled) form.channel = 'email'
  else if (form.channel === 'email' && !config.email_enabled && config.telegram_enabled) form.channel = 'telegram'
}, { immediate: true })

async function loadUsers() {
  loadingUsers.value = true
  try {
    users.value = await apiList('/api/admin/manual-notifications/users')
    notifyVars.value = await apiList('/api/admin/manual-notifications/vars')
  }
  catch (e:any) { message.error(e.message) } finally { loadingUsers.value = false }
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
  if (!channelReady.value) return message.warning(channelHint.value || '当前渠道不可用')
  if (form.target_type === 'selected' && form.user_ids.length === 0) return message.warning('请选择至少一个用户')
  const target = form.target_type === 'all' ? '全部正常用户' : `选中的 ${form.user_ids.length} 个用户`
  dialog.warning({ title:'确认发送', content:`确定通过 ${channelLabel(form.channel)} 向${target}发送此通知吗？`, positiveText:'确认发送', negativeText:'取消', onPositiveClick:submit })
}
async function submit() {
  sending.value = true
  try {
    const created = await apiPost<any>('/api/admin/manual-notifications', { ...form })
    message.success(`通知已创建，共 ${created.total} 条投递`)
    form.title = ''; form.content = ''; form.user_ids = []
    await loadHistory()
    if (refreshTimer) clearTimeout(refreshTimer)
    refreshTimer = setTimeout(loadHistory, 1200)
  } catch (e:any) { message.error(e.message) } finally { sending.value = false }
}

onMounted(async () => {
  await fetchConfig()
  loadUsers()
  loadHistory()
})
onBeforeUnmount(() => { if (refreshTimer) clearTimeout(refreshTimer) })
</script>

<style scoped>
.mn-vars { width: 100%; }
.mn-vars-h { font-size: 12px; color: var(--text-3); margin-bottom: 8px; }
.mn-vars-list { display: flex; flex-wrap: wrap; gap: 6px; }
.mn-var {
  display: inline-flex; align-items: center; gap: 6px;
  border: 1px solid var(--border); background: var(--bg-2);
  border-radius: 6px; padding: 4px 8px; cursor: pointer;
}
.mn-var:hover { border-color: var(--accent-strong); }
.mn-var code { font-size: 12px; }
.mn-var span { font-size: 12px; color: var(--text-2); }
.mn-vars-note { margin: 8px 0 0; font-size: 12px; color: var(--text-3); }
</style>
