<template>
  <div>
    <h2 class="page-title">系统设置</h2>
    <p class="page-sub">配置站点参数</p>

    <n-spin :show="loading">
      <n-card title="基本设置" size="small" style="margin-bottom:16px;">
        <n-form label-placement="left" label-width="120">
          <n-form-item label="站点名称"><n-input v-model:value="form.site_name" /></n-form-item>
          <n-form-item label="站点描述"><n-input v-model:value="form.site_description" /></n-form-item>
          <n-form-item label="注册模式">
            <n-select v-model:value="form.register_mode" :options="[{label:'开放注册',value:'open'},{label:'邀请码注册',value:'code'},{label:'关闭注册',value:'closed'}]" />
          </n-form-item>
          <n-form-item label="邮箱验证"><n-switch v-model:value="emailVerify" /></n-form-item>
          <n-form-item label="积分汇率（积分=1元）"><n-input-number v-model:value="pointsRate" :min="1" style="width:200px;" /></n-form-item>
          <n-form-item label="注册赠送积分"><n-input-number v-model:value="signupBonus" :min="0" style="width:200px;" /></n-form-item>
          <n-form-item label="新用户默认流量 (GB)"><n-input-number v-model:value="defaultTraffic" :min="0" style="width:200px;" /></n-form-item>
          <n-form-item label="新用户默认天数"><n-input-number v-model:value="defaultExpiry" :min="0" style="width:200px;" /></n-form-item>
          <n-form-item label="免费节点分组">
            <n-select v-model:value="freeGroupId" :options="groupOptions" placeholder="无计划用户可用的节点分组" clearable style="width:300px;" />
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="首页设置" size="small" style="margin-bottom:16px;">
        <n-form label-placement="left" label-width="120">
          <n-form-item label="首页模式">
            <n-select v-model:value="form.homepage_mode" :options="[{label:'监控大屏',value:'monitor'},{label:'自定义页面',value:'custom'}]" />
          </n-form-item>
          <n-form-item v-if="form.homepage_mode === 'custom'" label="自定义 URL">
            <n-input v-model:value="form.homepage_url" placeholder="https://example.com" />
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="SMTP 邮件" size="small" style="margin-bottom:16px;">
        <n-form label-placement="left" label-width="120">
          <n-form-item label="SMTP 主机"><n-input v-model:value="form.smtp_host" /></n-form-item>
          <n-form-item label="SMTP 端口"><n-input v-model:value="form.smtp_port" /></n-form-item>
          <n-form-item label="加密方式">
            <n-select v-model:value="form.smtp_security" :options="[{label:'自动',value:'auto'},{label:'SSL/TLS',value:'ssl'},{label:'STARTTLS',value:'starttls'},{label:'无',value:'none'}]" />
          </n-form-item>
          <n-form-item label="SMTP 用户"><n-input v-model:value="form.smtp_user" /></n-form-item>
          <n-form-item label="SMTP 密码"><n-input v-model:value="form.smtp_pass" type="password" show-password-on="click" /></n-form-item>
          <n-form-item label="发件人地址"><n-input v-model:value="form.smtp_from" /></n-form-item>
          <n-form-item label="发件人名称"><n-input v-model:value="form.smtp_from_name" /></n-form-item>
          <n-form-item label="测试收件人">
            <n-input v-model:value="testEmail" placeholder="发送测试邮件的目标地址" style="width:260px;" />
            <n-button style="margin-left:8px;" :loading="testingSmtp" @click="handleTestSMTP">发送测试</n-button>
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="订阅模板" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:12px;">自定义 Clash/sing-box 订阅输出模板。留空使用内置默认模板。</p>
        <n-form label-placement="left" label-width="120">
          <n-form-item label="Clash 模板 (YAML)">
            <n-input v-model:value="form.sub_clash_template" type="textarea" :rows="8" placeholder="留空用内置模板" style="font-family:monospace;font-size:12px;" />
          </n-form-item>
          <n-form-item label="sing-box 模板 (JSON)">
            <n-input v-model:value="form.sub_singbox_template" type="textarea" :rows="8" placeholder="留空用内置模板" style="font-family:monospace;font-size:12px;" />
          </n-form-item>
        </n-form>
      </n-card>

      <n-space>
        <n-button type="primary" :loading="saving" @click="handleSave">保存设置</n-button>
        <n-button @click="handleRebuild" :loading="rebuilding">重建 sing-box 配置</n-button>
      </n-space>
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { NCard, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch, NButton, NSpace, NSpin, useMessage } from 'naive-ui'
import { apiGet, apiPost, apiPut, apiList } from '@/api'

const message = useMessage()
const loading = ref(false)
const saving = ref(false)
const testingSmtp = ref(false)
const rebuilding = ref(false)
const form = reactive<Record<string, any>>({})
const emailVerify = ref(true)
const pointsRate = ref(10)
const signupBonus = ref(0)
const defaultTraffic = ref(0)
const defaultExpiry = ref(0)
const freeGroupId = ref<number | null>(null)
const testEmail = ref('')
const groupOptions = ref<any[]>([])

async function handleSave() {
  saving.value = true
  try {
    const body: Record<string, any> = {
      ...form,
      email_verify_required: emailVerify.value ? 'true' : 'false',
      points_per_cny: String(pointsRate.value),
      signup_bonus_points: String(signupBonus.value),
      default_traffic: String(defaultTraffic.value * 1024 * 1024 * 1024),
      default_expiry_days: String(defaultExpiry.value),
      free_group_id: freeGroupId.value ? String(freeGroupId.value) : '',
    }
    await apiPut('/api/admin/settings', body)
    message.success('保存成功')
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function handleTestSMTP() {
  if (!testEmail.value) { message.warning('请输入测试收件人'); return }
  testingSmtp.value = true
  try { await apiPost('/api/admin/settings/test-smtp', { to: testEmail.value }); message.success('测试邮件已发送') } catch (e: any) { message.error(e.message) } finally { testingSmtp.value = false }
}

async function handleRebuild() {
  rebuilding.value = true
  try { await apiPost('/api/admin/rebuild'); message.success('重建成功') } catch (e: any) { message.error(e.message) } finally { rebuilding.value = false }
}

onMounted(async () => {
  loading.value = true
  try {
    const [data, groups] = await Promise.all([
      apiGet<Record<string, string>>('/api/admin/settings'),
      apiList<any>('/api/admin/node-groups').catch(() => []),
    ])
    if (data) {
      Object.assign(form, data)
      emailVerify.value = data.email_verify_required === 'true'
      pointsRate.value = parseInt(data.points_per_cny) || 10
      signupBonus.value = parseInt(data.signup_bonus_points) || 0
      defaultTraffic.value = (parseInt(data.default_traffic) || 0) / (1024 * 1024 * 1024)
      defaultExpiry.value = parseInt(data.default_expiry_days) || 0
      freeGroupId.value = parseInt(data.free_group_id) || null
    }
    groupOptions.value = (groups || []).map((g: any) => ({ label: g.name, value: g.id }))
  } catch {} finally { loading.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
</style>
