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
          <n-form-item label="用户自助重置凭据">
            <div>
              <n-switch v-model:value="credsResetEnabled" />
              <div style="font-size:12px;color:var(--text-3);line-height:1.7;margin-top:4px;max-width:520px;">
                允许用户在「我的订阅」页自行重置节点凭据，彻底吊销已泄露订阅导出的节点（每人 30 天一次）。
                每次重置都需要把新凭据推送到相关服务器并重启 sing-box，<b>期间同机器上其他用户的连接也会中断</b>，
                因此默认关闭。关闭时管理员仍可在「用户管理」里逐个重置。
              </div>
            </div>
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="面板访问地址" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:12px;line-height:1.7;">
          面板对外访问地址，用于订阅链接、探针安装、邮件验证/找回链接，以及下方的 sing-box 一键安装命令。
          填写完整地址，例如 <code>https://node.example.com</code> 或 <code>http://1.2.3.4:8081</code>；
          不带 <code>http(s)://</code> 前缀时默认按 <code>https</code> 处理。留空则自动依据反向代理头 / 请求 Host 推断。
        </p>
        <n-form label-placement="left" label-width="120">
          <n-form-item label="访问地址">
            <n-input v-model:value="form.public_base" :disabled="envLocked('public_base')"
              placeholder="https://node.example.com 或 http://1.2.3.4:8081" style="max-width:420px;" />
          </n-form-item>
          <n-form-item v-if="envLocked('public_base')" label=" ">
            <span style="font-size:12px;color:var(--warning,#d97706);">
              已由环境变量 QZ_PUBLIC_BASE 固定；如需在面板内修改，请移除该环境变量后重启。
            </span>
          </n-form-item>
          <n-form-item label="sing-box 安装命令">
            <div style="width:100%;max-width:560px;">
              <n-input-group>
                <n-input :value="installCmd" readonly style="font-family:monospace;font-size:12px;" />
                <n-button type="primary" ghost @click="copyInstall">复制</n-button>
              </n-input-group>
              <p style="font-size:12px;color:var(--text-3);margin-top:6px;line-height:1.7;">
                在落地服务器上以 root 运行此命令：已安装 sing-box 会自动检测并打印信息；未安装则拉取官方最新版（含
                v2ray_api，面板统计依赖）并完成内核调优，最后输出可填入「服务器」的接管信息。
              </p>
            </div>
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="退款策略" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:12px;line-height:1.7;">
          管理员对订单退款时的默认规则。<b>按剩余比例</b>只退还未使用的部分（如 100G 用了 50G 退 50%）；
          套餐同时含流量与有效期，<b>计算基准</b>决定按哪个维度算比例，推荐 <b>min(流量,时间)</b> 取更小值以防滥用。
          流量包无有效期，恒按流量。下架商品时也按此策略退款。
        </p>
        <n-form label-placement="left" label-width="120">
          <n-form-item label="退款方式">
            <n-select v-model:value="refundMode" style="width:240px;" :options="[
              {label:'按剩余比例退款',value:'prorated'},
              {label:'全额退款',value:'full'},
            ]" />
          </n-form-item>
          <n-form-item label="套餐计算基准">
            <n-select v-model:value="refundBasis" :disabled="refundMode==='full'" style="width:240px;" :options="[
              {label:'min(流量, 时间) 取更小（推荐）',value:'min'},
              {label:'只按剩余流量',value:'traffic'},
              {label:'只按剩余时间',value:'time'},
            ]" />
          </n-form-item>
          <n-form-item label="手续费 (%)">
            <n-input-number v-model:value="refundFee" :disabled="refundMode==='full'" :min="0" :max="100" style="width:200px;" />
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

      <n-card title="证书 / ACME（Cloudflare 自动证书）" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:10px;">
          填写 Cloudflare API Token 后，「证书管理」页即可用 Cloudflare DNS 方式在面板本机一键申请 / 自动续期真实证书（DNS 验证无需节点参与，远程节点也能用）。
        </p>
        <div class="cf-guide">
          <div class="cf-guide-t">如何获取 Cloudflare API Token（约 1 分钟）</div>
          <ol>
            <li>打开 <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener">Cloudflare → 我的个人资料 → API 令牌</a>，点<b>「创建令牌」</b>。</li>
            <li>选用模板 <b>「编辑区域 DNS（Edit zone DNS）」</b> —— 它已自带所需权限。<br>（若手动创建，需添加两条权限：<b>区域 · DNS · 编辑</b> 和 <b>区域 · 区域 · 读取</b>。）</li>
            <li>「区域资源」选 <b>包含 → 特定区域 → 你的域名</b>（或「所有区域」）。</li>
            <li>「继续以显示摘要」→ <b>创建令牌</b> → 复制生成的令牌，粘贴到下方。</li>
          </ol>
          <div class="cf-guide-n">✓ 只需 DNS 编辑权限，<b>不要用 Global API Key</b>（那是全账户权限，不安全）。令牌加密存储，保存后显示为 <code>***</code>。</div>
        </div>
        <n-form label-placement="left" label-width="160">
          <n-form-item label="Cloudflare API Token">
            <n-input v-model:value="form.cf_api_token" type="password" show-password-on="click" placeholder="留空表示未配置；显示 *** 表示已设置" />
          </n-form-item>
          <n-form-item label="ACME 账户邮箱">
            <n-input v-model:value="form.acme_email" placeholder="可选，建议填写（Let's Encrypt 到期提醒）" />
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="节点出口安全" size="small" style="margin-bottom:16px;">
        <n-form label-placement="left" label-width="160">
          <n-form-item label="阻断内网 / 元数据">
            <div style="width:100%;">
              <n-switch v-model:value="blockPrivate" />
              <p style="font-size:12px;color:var(--text-3);margin:6px 0 0;">
                开启后，用户经节点访问 <code>127.0.0.1</code>、内网段（10./172.16./192.168.）、链路本地段（含云厂商元数据地址 <code>169.254.169.254</code>）会被拒绝，只放行公网目标。
                <b>建议保持开启</b>：关闭意味着任何订阅用户都能借落地机的身份访问它所在的内网，并可能读到该机的云凭据。
                仅当你确实要让用户经节点访问自己的内网时才关闭。保存后会自动下发到所有节点。
              </p>
            </div>
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="订阅模板" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:12px;">自定义 Clash/sing-box 订阅输出模板。留空使用内置默认模板；改过之后会一直沿用你的版本（升级带来的新版内置模板不会自动生效），点「恢复内置默认」即可清空覆盖、跟随内置。</p>
        <n-form label-placement="left" label-width="120">
          <n-form-item label="Clash 模板 (YAML)">
            <div style="width:100%;">
              <n-input v-model:value="form.sub_clash_template" type="textarea" :rows="8" placeholder="留空用内置模板" style="font-family:monospace;font-size:12px;" />
              <n-space size="small" style="margin-top:6px;">
                <n-button size="tiny" @click="loadDefaultTemplate('clash')">载入内置默认（可编辑）</n-button>
                <n-button size="tiny" @click="form.sub_clash_template = ''">恢复内置默认（清空）</n-button>
              </n-space>
            </div>
          </n-form-item>
          <n-form-item label="sing-box 模板 (JSON)">
            <div style="width:100%;">
              <n-input v-model:value="form.sub_singbox_template" type="textarea" :rows="8" placeholder="留空用内置模板" style="font-family:monospace;font-size:12px;" />
              <n-space size="small" style="margin-top:6px;">
                <n-button size="tiny" @click="loadDefaultTemplate('singbox')">载入内置默认（可编辑）</n-button>
                <n-button size="tiny" @click="form.sub_singbox_template = ''">恢复内置默认（清空）</n-button>
              </n-space>
            </div>
          </n-form-item>
        </n-form>
        <p style="font-size:12px;color:var(--text-3);margin-top:4px;">改动需点下方「保存设置」后生效。</p>
      </n-card>

      <n-card title="监控告警阈值" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:12px;">超过以下百分比时触发告警（0-100）。修改后下次检查生效。</p>
        <n-form label-placement="left" label-width="120">
          <n-form-item label="CPU 告警 (%)"><n-input-number v-model:value="alertCpu" :min="1" :max="100" style="width:200px;" /></n-form-item>
          <n-form-item label="内存告警 (%)"><n-input-number v-model:value="alertMem" :min="1" :max="100" style="width:200px;" /></n-form-item>
          <n-form-item label="磁盘告警 (%)"><n-input-number v-model:value="alertDisk" :min="1" :max="100" style="width:200px;" /></n-form-item>
          <n-form-item label="连续命中次数">
            <div style="width:100%;">
              <n-input-number v-model:value="alertStreak" :min="1" :max="10" style="width:200px;" />
              <p style="font-size:12px;color:var(--text-3);margin:6px 0 0;">
                CPU / 内存 / 磁盘 / 离线这四项，需要<b>连续命中这么多次检查</b>才真正告警。
                一次编译、一次备份就能让 CPU 瞬间冲顶，只看单次采样会把告警刷成噪音。
                填 <code>1</code> 恢复「采到一次就报」。到期类告警不受影响（日期不会抖动）。
              </p>
            </div>
          </n-form-item>
        </n-form>
      </n-card>

      <n-card title="数据备份" size="small" style="margin-bottom:16px;">
        <p style="font-size:12px;color:var(--text-3);margin-bottom:10px;">
          在线导出整库快照（单个 <code>.db</code> 文件，含用户 / 订单 / 节点 / 证书）。数据库跑在 WAL 模式下，
          <b>直接 <code>scp</code> 拷贝 <code>qingzhou.db</code> 拿到的是残缺副本</b>——已提交的数据可能还在 <code>-wal</code> 里。
          此处导出由 SQLite 自己在一致性快照上生成，导出期间面板照常读写。
          文件里的敏感字段仍是加密的，恢复到别处需要同一个 <code>QZ_SECRET_KEY</code>。
        </p>
        <n-button :loading="backingUp" @click="handleBackup">下载数据库备份</n-button>
      </n-card>

      <n-space>
        <n-button type="primary" :loading="saving" @click="handleSave">保存设置</n-button>
        <n-button @click="handleRebuild" :loading="rebuilding">重建 sing-box 配置</n-button>
      </n-space>
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { NCard, NForm, NFormItem, NInput, NInputGroup, NInputNumber, NSelect, NSwitch, NButton, NSpace, NSpin, useMessage } from 'naive-ui'
import { apiGet, apiPost, apiPut, apiList, apiDownload } from '@/api'

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
// 默认 false，与后端 credsResetEnabled() 的「只有显式 true 才算开」保持一致。
const credsResetEnabled = ref(false)
// 默认 true，与后端「缺省即开启，只有显式 '0' 才关」保持一致。
const blockPrivate = ref(true)
const alertCpu = ref(90)
const alertMem = ref(90)
const alertDisk = ref(85)
const alertStreak = ref(2)
const refundMode = ref('prorated')
const refundBasis = ref('min')
const refundFee = ref(0)
const testEmail = ref('')
const groupOptions = ref<any[]>([])

// Mirror the backend's normalizeBase: trim, drop trailing slashes, default to
// https:// when no scheme is given.
function normalizeBase(v: string): string {
  v = (v || '').trim().replace(/\/+$/, '')
  if (!v) return ''
  if (!v.includes('://')) v = 'https://' + v
  return v
}
// Base used to build the copyable command: the configured address, else the
// address the admin is currently browsing (so the command is never blank).
const effectiveBase = computed(() => normalizeBase(form.public_base) || window.location.origin)
const installCmd = computed(() => `curl -fsSL ${effectiveBase.value}/install-singbox.sh | bash`)

function envLocked(key: string): boolean {
  return (form._env_keys || '').split(',').includes(key)
}

async function copyInstall() {
  try {
    await navigator.clipboard.writeText(installCmd.value)
    message.success('已复制安装命令')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

async function handleSave() {
  saving.value = true
  try {
    const body: Record<string, any> = {
      ...form,
      email_verify_required: emailVerify.value ? 'true' : 'false',
      points_per_cny: String(pointsRate.value),
      signup_bonus_points: String(signupBonus.value),
      default_traffic: String(Math.round(defaultTraffic.value * 1024 * 1024 * 1024)),
      default_expiry_days: String(defaultExpiry.value),
      free_group_id: freeGroupId.value ? String(freeGroupId.value) : '',
      node_creds_reset_enabled: credsResetEnabled.value ? 'true' : 'false',
      sb_block_private: blockPrivate.value ? '1' : '0',
      alert_cpu_threshold: String(alertCpu.value),
      alert_mem_threshold: String(alertMem.value),
      alert_disk_threshold: String(alertDisk.value),
      alert_consecutive: String(alertStreak.value),
      refund_mode: refundMode.value,
      refund_basis: refundBasis.value,
      refund_fee_percent: String(refundFee.value),
    }
    await apiPut('/api/admin/settings', body)
    message.success('保存成功')
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// 载入内置默认模板到输入框，方便对照/微调。保存时若与内置完全一致，后端会存为
// 空（保留「留空用内置」语义），因此载入后原样保存也不会锁死在旧版本。
let defaultTemplates: { clash?: string; singbox?: string } | null = null
async function loadDefaultTemplate(which: 'clash' | 'singbox') {
  try {
    if (!defaultTemplates) defaultTemplates = await apiGet('/api/admin/settings/default-templates')
    if (which === 'clash') form.sub_clash_template = defaultTemplates?.clash || ''
    else form.sub_singbox_template = defaultTemplates?.singbox || ''
    message.success('已载入内置默认，可编辑后保存')
  } catch (e: any) { message.error(e.message || '载入失败') }
}

async function handleTestSMTP() {
  if (!testEmail.value) { message.warning('请输入测试收件人'); return }
  testingSmtp.value = true
  try { await apiPost('/api/admin/settings/test-smtp', { to: testEmail.value }); message.success('测试邮件已发送') } catch (e: any) { message.error(e.message) } finally { testingSmtp.value = false }
}

const backingUp = ref(false)
async function handleBackup() {
  backingUp.value = true
  try {
    await apiDownload('/api/admin/backup', 'qingzhou-backup.db')
    message.success('备份已开始下载')
  } catch (e: any) { message.error(e.message || '备份失败') } finally { backingUp.value = false }
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
      credsResetEnabled.value = data.node_creds_reset_enabled === 'true'
      blockPrivate.value = data.sb_block_private !== '0'
      alertCpu.value = parseInt(data.alert_cpu_threshold) || 90
      alertMem.value = parseInt(data.alert_mem_threshold) || 90
      alertDisk.value = parseInt(data.alert_disk_threshold) || 85
      alertStreak.value = parseInt(data.alert_consecutive) || 2
      refundMode.value = data.refund_mode === 'full' ? 'full' : 'prorated'
      refundBasis.value = ['traffic', 'time', 'min'].includes(data.refund_basis) ? data.refund_basis : 'min'
      refundFee.value = parseFloat(data.refund_fee_percent) || 0
    }
    groupOptions.value = (groups || []).map((g: any) => ({ label: g.name, value: g.id }))
  } catch {} finally { loading.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
.cf-guide { background: var(--bg-soft); border: 1px solid var(--border); border-radius: 10px; padding: 12px 14px; margin-bottom: 14px; }
.cf-guide-t { font-size: 12.5px; font-weight: 650; color: var(--text); margin-bottom: 8px; }
.cf-guide ol { margin: 0; padding-left: 20px; display: flex; flex-direction: column; gap: 6px; }
.cf-guide li { font-size: 12.5px; color: var(--text-2); line-height: 1.6; }
.cf-guide a { color: var(--accent-strong); }
.cf-guide-n { margin-top: 10px; font-size: 12px; color: var(--text-3); line-height: 1.55; }
.cf-guide code { background: var(--border); padding: 0 4px; border-radius: 4px; }
</style>
