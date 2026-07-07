<template>
  <div>
    <h2 class="page-title">账户设置</h2>
    <p class="page-sub">管理你的账户信息和安全</p>

    <!-- 基本信息 -->
    <n-card title="基本信息" size="small" style="margin-bottom:16px;">
      <n-descriptions :column="2" bordered size="small">
        <n-descriptions-item label="用户名">{{ auth.user?.username }}</n-descriptions-item>
        <n-descriptions-item label="邮箱">
          {{ auth.user?.email || '未绑定' }}
          <n-tag v-if="auth.user?.email && auth.user?.email_verified" type="success" size="tiny" bordered style="margin-left:6px;">已验证</n-tag>
          <n-tag v-else-if="auth.user?.email" type="warning" size="tiny" bordered style="margin-left:6px;">未验证</n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="积分">{{ auth.user?.points || 0 }} ({{ yuan(auth.user?.points || 0) }})</n-descriptions-item>
        <n-descriptions-item label="角色">{{ auth.user?.is_admin ? '管理员' : '用户' }}</n-descriptions-item>
      </n-descriptions>
    </n-card>

    <!-- 邮箱设置 -->
    <n-card title="邮箱设置" size="small" style="margin-bottom:16px;">
      <n-form label-placement="left" label-width="80" style="max-width:400px;">
        <n-form-item label="邮箱">
          <n-input v-model:value="emailForm.email" placeholder="输入邮箱地址" />
        </n-form-item>
        <n-form-item>
          <n-space>
            <n-button type="primary" :loading="bindingEmail" @click="handleBindEmail">绑定邮箱</n-button>
            <n-button v-if="auth.user?.email && !auth.user?.email_verified" :loading="resending" @click="handleResendVerify">发送验证邮件</n-button>
          </n-space>
        </n-form-item>
      </n-form>
      <p v-if="auth.user?.email && !auth.user?.email_verified" style="font-size:12px;color:var(--text-3);margin:0;">
        验证邮件可能需要几分钟，请检查垃圾邮件文件夹
      </p>
    </n-card>

    <!-- 修改密码 -->
    <n-card title="修改密码" size="small" style="margin-bottom:16px;">
      <n-form label-placement="left" label-width="80" style="max-width:400px;">
        <n-form-item label="旧密码"><n-input v-model:value="pwForm.old" type="password" show-password-on="click" /></n-form-item>
        <n-form-item label="新密码"><n-input v-model:value="pwForm.new" type="password" show-password-on="click" /></n-form-item>
        <n-form-item label="确认密码"><n-input v-model:value="pwForm.confirm" type="password" show-password-on="click" /></n-form-item>
        <n-form-item>
          <n-button type="primary" :loading="changingPw" @click="handleChangePw">修改密码</n-button>
        </n-form-item>
      </n-form>
      <p style="font-size:12px;color:var(--text-3);margin:0;">修改密码后，其他设备将被要求重新登录</p>
    </n-card>

    <!-- 登录会话 -->
    <n-card title="登录会话" size="small">
      <n-data-table :columns="sessionCols" :data="sortedSessions" :bordered="false" size="small" :loading="loadingSessions" :pagination="{pageSize:5}" />
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, h } from 'vue'
import {
  NCard, NForm, NFormItem, NInput, NButton, NDescriptions, NDescriptionsItem,
  NDataTable, NTag, NSpace, useMessage
} from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { apiGet, apiList, apiPost } from '@/api'
import { fmtDateTime, yuan } from '@/utils/format'

const auth = useAuthStore()
const message = useMessage()

const pwForm = reactive({ old: '', new: '', confirm: '' })
const emailForm = reactive({ email: '' })
const changingPw = ref(false)
const bindingEmail = ref(false)
const resending = ref(false)
const sessions = ref<any[]>([])
const loadingSessions = ref(false)

/** 解析 UA 为可读描述 */
function parseUA(ua: string): string {
  if (!ua) return '未知设备'
  const os = ua.includes('Windows') ? 'Windows' : ua.includes('Mac') ? 'macOS' : ua.includes('Linux') ? 'Linux' : ua.includes('Android') ? 'Android' : ua.includes('iPhone') || ua.includes('iOS') ? 'iOS' : '未知'
  const br = ua.includes('Edg/') ? 'Edge' : ua.includes('Chrome') ? 'Chrome' : ua.includes('Firefox') ? 'Firefox' : ua.includes('Safari') ? 'Safari' : ua.includes('curl') ? 'curl' : '浏览器'
  return `${os} · ${br}`
}

/** 判断是否当前设备（粗略匹配） */
function isCurrentDevice(s: any): boolean {
  // 当前设备是最新登录的那个
  return sessions.value.length > 0 && s.id === sessions.value[0]?.id
}

const sortedSessions = computed(() => {
  return [...sessions.value].sort((a, b) => {
    if (isCurrentDevice(a)) return -1
    if (isCurrentDevice(b)) return 1
    return (b.created_at || 0) - (a.created_at || 0)
  })
})

const sessionCols = [
  {
    title: '设备', key: 'user_agent',
    render: (r: any) => h('div', [
      h('span', { style: 'font-weight:600;' }, parseUA(r.user_agent)),
      isCurrentDevice(r) ? h(NTag, { type: 'success', size: 'tiny', bordered: false, style: 'margin-left:6px;' }, { default: () => '当前设备' }) : null,
    ]),
  },
  { title: 'IP', key: 'ip', width: 130 },
  { title: '登录时间', key: 'created_at', width: 150, render: (r: any) => fmtDateTime(r.created_at) },
  {
    title: '操作', key: 'act', width: 70,
    render: (r: any) => isCurrentDevice(r) ? null : h(NButton, { size: 'tiny', type: 'error', onClick: () => revokeSession(r.id) }, { default: () => '撤销' }),
  },
]

async function handleChangePw() {
  if (pwForm.new !== pwForm.confirm) { message.error('两次密码不一致'); return }
  if (!pwForm.new || pwForm.new.length < 6) { message.error('密码至少6位'); return }
  changingPw.value = true
  try {
    await apiPost('/api/user/password', { old_password: pwForm.old, new_password: pwForm.new })
    message.success('密码已修改，其他设备需重新登录')
    pwForm.old = ''; pwForm.new = ''; pwForm.confirm = ''
  } catch (e: any) { message.error(e.message) } finally { changingPw.value = false }
}

async function handleBindEmail() {
  if (!emailForm.email) { message.warning('请输入邮箱'); return }
  bindingEmail.value = true
  try { await apiPost('/api/user/email', { email: emailForm.email }); message.success('绑定成功'); await auth.fetchMe() } catch (e: any) { message.error(e.message) } finally { bindingEmail.value = false }
}

async function handleResendVerify() {
  resending.value = true
  try { await apiPost('/api/user/resend-verify'); message.success('验证邮件已发送') } catch (e: any) { message.error(e.message) } finally { resending.value = false }
}

async function revokeSession(id: number) {
  try { await apiPost(`/api/user/sessions/${id}/revoke`); sessions.value = sessions.value.filter(s => s.id !== id); message.success('已撤销') } catch (e: any) { message.error(e.message) }
}

onMounted(async () => {
  emailForm.email = auth.user?.email || ''
  loadingSessions.value = true
  try { sessions.value = await apiList('/api/user/sessions') } catch {} finally { loadingSessions.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
</style>
