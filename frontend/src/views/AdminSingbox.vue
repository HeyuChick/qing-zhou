<template>
  <div>
    <h2 class="page-title">sing-box 配置</h2>
    <n-tabs v-model:value="tab" animated>
      <n-tab-pane name="tls" tab="TLS 配置">
        <div class="page-toolbar">
          <n-input v-model:value="tlsSearch" placeholder="搜索名称/SNI" size="small" clearable style="width:200px;max-width:50%;" />
          <span class="spacer" />
          <n-button size="small" type="primary" @click="openTls()">添加 TLS</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="filteredTls.length" class="card-grid">
            <div v-for="r in filteredTls" :key="r.id" class="list-card">
              <div class="lc-head">
                <span class="lc-title">{{ r.name || '—' }}</span>
                <n-tag :type="r.mode === 'reality' ? 'success' : 'info'" size="tiny" bordered="false">{{ r.mode === 'reality' ? 'Reality' : '证书 TLS' }}</n-tag>
                <n-tag v-if="r.cert_info" :type="r.cert_info.expired ? 'error' : r.cert_info.expiring ? 'warning' : 'success'" size="tiny" bordered="false">
                  {{ r.cert_info.expired ? '已过期' : r.cert_info.days_left + '天' }}
                </n-tag>
              </div>
              <div class="lc-meta">
                <span class="kv">SNI <b>{{ jp(r.server_json).server_name || '—' }}</b></span>
                <span class="kv">服务器 <b>{{ serverName(r.server_id) }}</b></span>
              </div>
              <div class="lc-meta"><span class="kv">入站数 <b>{{ tlsUseCount(r.id) }}</b></span></div>
              <div class="lc-foot">
                <n-button size="tiny" @click="openTls(r)">编辑</n-button>
                <n-button size="tiny" @click="cloneTls(r)">克隆</n-button>
                <n-button size="tiny" type="error" @click="deleteTls(r.id)">删除</n-button>
              </div>
            </div>
          </div>
          <n-empty v-else-if="!loading" description="暂无 TLS 配置" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>

      <n-tab-pane name="inbounds" tab="入站">
        <div class="page-toolbar">
          <n-input v-model:value="inbSearch" placeholder="搜索 tag/协议" size="small" clearable style="width:160px;max-width:40%;" />
          <n-select v-model:value="presetType" :options="presetOpts" placeholder="一键模板" size="small" style="width:180px;" @update:value="applyPreset" />
          <span class="spacer" />
          <n-button v-if="checkedIds.size" size="small" @click="batchToggle(true)">批量启用</n-button>
          <n-button v-if="checkedIds.size" size="small" @click="batchToggle(false)">批量停用</n-button>
          <n-button v-if="checkedIds.size" size="small" type="error" @click="batchDelete">批量删除</n-button>
          <n-button size="small" type="primary" @click="openInbound()">添加入站</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="filteredInbounds.length" class="card-grid">
            <div v-for="r in filteredInbounds" :key="r.id" class="list-card">
              <div class="lc-head">
                <n-checkbox :checked="checkedIds.has(r.id)" @update:checked="toggleCheck(r.id)" style="margin-right:6px;" />
                <span class="lc-title">{{ r.tag || '—' }}</span>
                <n-tag :type="r.enabled ? 'success' : 'error'" size="tiny" bordered="false" style="cursor:pointer;" @click="toggleInbound(r)">{{ r.enabled ? '启用' : '停用' }}</n-tag>
              </div>
              <div class="lc-meta">
                <span class="kv"><n-tag size="tiny" bordered="false">{{ (r.type || '').toUpperCase() }}</n-tag></span>
                <span class="kv">端口 <b>{{ r.listen_port }}</b></span>
                <span class="kv">用户 <b>{{ r.user_count ?? 0 }}</b></span>
              </div>
              <div class="lc-meta">
                <span class="kv">服务器 <b>{{ serverName(r.server_id) }}</b></span>
                <span class="kv">TLS <b>{{ tlsName(r.tls_id) }}</b></span>
              </div>
              <div class="lc-foot">
                <n-button size="tiny" @click="openInbound(r)">编辑</n-button>
                <n-button size="tiny" @click="cloneInbound(r)">克隆</n-button>
                <n-button size="tiny" :loading="portChecking === r.id" @click="checkPort(r)">测端口</n-button>
                <n-button size="tiny" type="error" @click="deleteInbound(r.id)">删除</n-button>
              </div>
              <div v-if="portResult[r.id]" class="port-result" :class="{ ok: portResult[r.id].reachable, err: !portResult[r.id].reachable }">
                {{ portResult[r.id].reachable ? '可达 · ' + portResult[r.id].ms.toFixed(0) + 'ms' : '不可达 · ' + (portResult[r.id].error || '失败') }}
              </div>
            </div>
          </div>
          <n-empty v-else-if="!loading" description="暂无入站" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>

      <n-tab-pane name="preview" tab="配置预览">
        <div class="page-toolbar">
          <n-select v-model:value="previewSid" :options="serverOpts" placeholder="本机" clearable style="width:200px;max-width:60%;" size="small" />
          <span class="spacer" />
          <n-button size="small" type="primary" :loading="previewLoading" @click="loadPreview">刷新预览</n-button>
        </div>
        <n-code :code="previewJson" language="json" style="max-height:60vh;overflow:auto;" />
      </n-tab-pane>
    </n-tabs>

    <!-- TLS 编辑抽屉 -->
    <n-drawer v-model:show="showTls" :width="drawerW" placement="right">
      <n-drawer-content :title="te.id ? '编辑 TLS' : '添加 TLS'" closable>
        <n-form label-placement="left" label-width="100">
          <n-form-item label="类型">
            <n-radio-group v-model:value="te.mode" :disabled="!!te.id">
              <n-radio value="reality">Reality</n-radio>
              <n-radio value="tls">证书 TLS</n-radio>
            </n-radio-group>
          </n-form-item>
          <n-form-item label="名称"><n-input v-model:value="te.name" /></n-form-item>
          <n-form-item label="所属服务器"><n-select v-model:value="te.server_id" :options="serverOpts" placeholder="本机" clearable /></n-form-item>
          <n-form-item label="SNI 伪装域名">
            <n-input-group>
              <n-input v-model:value="te.server_name" placeholder="www.microsoft.com" style="flex:1;" />
              <n-button :loading="sniTesting" @click="testSni">测试延迟</n-button>
            </n-input-group>
          </n-form-item>
          <n-form-item label=" " v-if="!te.id">
            <n-select :options="sniPresets" placeholder="选择常用 SNI 预设" size="small" @update:value="v => te.server_name = v" />
          </n-form-item>
          <n-form-item v-if="sniResult" label=" ">
            <div class="sni-result" :class="sniResult.status">
              <span v-if="sniResult.status === 'ok'" class="ok">
                <b>连通</b> · 平均 {{ sniResult.avg_ms.toFixed(0) }}ms · 最小 {{ sniResult.min_ms.toFixed(0) }}ms · 最大 {{ sniResult.max_ms.toFixed(0) }}ms ({{ sniResult.ok }}/{{ sniResult.total }})
              </span>
              <span v-else-if="sniResult.status === 'partial'" class="warn">
                <b>不稳定</b> · 平均 {{ sniResult.avg_ms.toFixed(0) }}ms · {{ sniResult.ok }}/{{ sniResult.total }} 次成功
              </span>
              <span v-else class="err"><b>不可达</b> · {{ sniResult.samples?.[0]?.error || '连接失败' }}</span>
            </div>
          </n-form-item>
          <n-form-item label="uTLS 指纹"><n-select v-model:value="te.fingerprint" :options="fpOpts" /></n-form-item>
          <template v-if="te.mode === 'reality'">
            <n-form-item label="握手目标">
              <n-input-group>
                <n-input v-model:value="te.handshake_server" placeholder="留空=同 SNI" style="flex:1;" />
                <n-button :loading="hsTesting" @click="testHandshake">测握手</n-button>
              </n-input-group>
            </n-form-item>
            <n-form-item label="握手端口"><n-input-number v-model:value="te.handshake_port" :min="1" :max="65535" style="width:100%;" /></n-form-item>
            <n-form-item v-if="hsResult" label=" ">
              <div class="sni-result" :class="hsResult.status">
                <span v-if="hsResult.status === 'ok'" class="ok"><b>连通</b> · 平均 {{ hsResult.avg_ms.toFixed(0) }}ms ({{ hsResult.ok }}/{{ hsResult.total }})</span>
                <span v-else-if="hsResult.status === 'partial'" class="warn"><b>不稳定</b> · {{ hsResult.ok }}/{{ hsResult.total }} 次成功</span>
                <span v-else class="err"><b>不可达</b> · {{ hsResult.samples?.[0]?.error || '连接失败' }}</span>
              </div>
            </n-form-item>
            <n-form-item>
              <n-button type="primary" @click="genKeys" :loading="genLoading">一键生成 Reality 密钥对</n-button>
            </n-form-item>
            <n-form-item label="私钥"><n-input :value="te.private_key" readonly placeholder="点击上方按钮生成" @click="copy(te.private_key)" style="cursor:pointer;" /></n-form-item>
            <n-form-item label="公钥"><n-input :value="te.public_key" readonly placeholder="点击上方按钮生成" @click="copy(te.public_key)" style="cursor:pointer;" /></n-form-item>
            <n-form-item label="Short ID">
              <div style="display:flex;flex-direction:column;gap:6px;width:100%;">
                <div v-for="(_, i) in te.short_ids" :key="i" style="display:flex;gap:6px;">
                  <n-input v-model:value="te.short_ids[i]" placeholder="自动生成" style="flex:1;" />
                  <n-button size="tiny" quaternary @click="te.short_ids.splice(i, 1)">✕</n-button>
                </div>
                <n-button size="tiny" dashed @click="te.short_ids.push('')">+ 添加 Short ID</n-button>
              </div>
            </n-form-item>
          </template>
          <template v-if="te.mode === 'tls'">
            <n-form-item label="证书 PEM"><n-input v-model:value="te.certificate" type="textarea" :rows="3" placeholder="-----BEGIN CERTIFICATE-----" /></n-form-item>
            <n-form-item label="私钥 PEM"><n-input v-model:value="te.key" type="textarea" :rows="3" placeholder="-----BEGIN PRIVATE KEY-----" /></n-form-item>
            <n-form-item label="ALPN">
              <n-select v-model:value="te.alpn" :options="[{label:'h3',value:'h3'},{label:'h2',value:'h2'},{label:'http/1.1',value:'http/1.1'}]" multiple />
            </n-form-item>
            <n-form-item label="最低 TLS"><n-select v-model:value="te.min_version" :options="verOpts" clearable /></n-form-item>
            <n-form-item label="最高 TLS"><n-select v-model:value="te.max_version" :options="verOpts" clearable /></n-form-item>
            <n-form-item label="允许不安全"><n-switch v-model:value="te.insecure" /></n-form-item>
          </template>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="saveTls">保存</n-button>
      </n-drawer-content>
    </n-drawer>

    <!-- 入站编辑抽屉 -->
    <n-drawer v-model:show="showInb" :width="drawerW" placement="right">
      <n-drawer-content :title="ie.id ? '编辑入站' : '添加入站'" closable>
        <n-form label-placement="left" label-width="100">
          <n-form-item label="协议"><n-select v-model:value="ie.type" :options="protoOpts" :disabled="!!ie.id" /></n-form-item>
          <n-form-item label="名称 / Tag"><n-input v-model:value="ie.tag" /></n-form-item>
          <n-form-item label="监听地址"><n-select v-model:value="ie.listen" :options="listenOpts" /></n-form-item>
          <n-form-item label="监听端口"><n-input-number v-model:value="ie.listen_port" :min="1" :max="65535" style="width:100%;" /></n-form-item>
          <n-form-item label="所属服务器"><n-select v-model:value="ie.server_id" :options="serverOpts" placeholder="本机" clearable /></n-form-item>
          <n-form-item v-if="ie.type !== 'shadowsocks'" label="TLS / Reality"><n-select v-model:value="ie.tls_id" :options="tlsOpts" placeholder="无" clearable /></n-form-item>
          <n-form-item v-if="['vless','vmess','trojan'].includes(ie.type) && !ie.tls_id && ie.type !== 'shadowsocks'" label=" ">
            <n-tag type="warning" size="small">未配置 TLS，建议为 VLESS/VMess/Trojan 绑定 TLS 或 Reality</n-tag>
          </n-form-item>
          <n-form-item v-if="ie.type === 'vless' && ie.tls_id" label="Flow">
            <n-select v-model:value="ie.flow" :options="[{label:'xtls-rprx-vision（默认）',value:'vision'},{label:'关闭',value:'none'}]" />
          </n-form-item>
          <n-form-item label="TCP Fast Open"><n-switch v-model:value="ie.tfo" /></n-form-item>
          <n-form-item label="MPTCP"><n-switch v-model:value="ie.mptcp" /></n-form-item>
          <n-form-item label="启用"><n-switch v-model:value="ie.enabled" /></n-form-item>
          <template v-if="ie.type === 'tuic'">
            <n-form-item label="拥塞控制"><n-select v-model:value="ie.cc" :options="[{label:'bbr',value:'bbr'},{label:'cubic',value:'cubic'},{label:'new_reno',value:'new_reno'}]" /></n-form-item>
            <n-form-item label="0-RTT"><n-switch v-model:value="ie.zero_rtt" /></n-form-item>
          </template>
          <template v-if="ie.type === 'hysteria2' || ie.type === 'hysteria'">
            <n-form-item label="上行 Mbps"><n-input-number v-model:value="ie.up_mbps" :min="0" style="width:100%;" /></n-form-item>
            <n-form-item label="下行 Mbps"><n-input-number v-model:value="ie.down_mbps" :min="0" style="width:100%;" /></n-form-item>
          </template>
          <template v-if="ie.type === 'hysteria2'">
            <n-form-item label="混淆密码"><n-input v-model:value="ie.obfs_password" placeholder="留空不混淆" /></n-form-item>
            <n-form-item label="伪装 URL"><n-input v-model:value="ie.masquerade" placeholder="留空不伪装" /></n-form-item>
          </template>
          <template v-if="ie.type === 'shadowsocks'">
            <n-form-item label="加密方式"><n-select v-model:value="ie.ss_method" :options="ssOpts" /></n-form-item>
          </template>
          <template v-if="ie.type === 'anytls'">
            <n-form-item label="空闲检查(秒)"><n-input-number v-model:value="ie.anytls_idle_check" :min="0" :placeholder="30" style="width:100%;" /></n-form-item>
            <n-form-item label="空闲超时(秒)"><n-input-number v-model:value="ie.anytls_idle_timeout" :min="0" :placeholder="30" style="width:100%;" /></n-form-item>
            <n-form-item label="最小空闲会话"><n-input-number v-model:value="ie.anytls_min_idle" :min="0" :placeholder="0" style="width:100%;" /></n-form-item>
          </template>
          <template v-if="['vless','vmess','trojan'].includes(ie.type)">
            <n-form-item label="传输层"><n-select v-model:value="ie.net" :options="[{label:'TCP',value:'tcp'},{label:'WebSocket',value:'ws'},{label:'gRPC',value:'grpc'},{label:'HTTPUpgrade',value:'httpupgrade'}]" /></n-form-item>
            <n-form-item v-if="ie.net === 'ws' || ie.net === 'httpupgrade'" label="路径"><n-input v-model:value="ie.ws_path" placeholder="/" /></n-form-item>
            <n-form-item v-if="ie.net === 'ws' || ie.net === 'httpupgrade'" label="Host 头"><n-input v-model:value="ie.ws_host" placeholder="留空=同 SNI" /></n-form-item>
            <n-form-item v-if="ie.net === 'ws' || ie.net === 'httpupgrade'" label="Early Data"><n-input-number v-model:value="ie.ws_early_data" :min="0" placeholder="0=关闭" style="width:100%;" /></n-form-item>
            <n-form-item v-if="ie.net === 'grpc'" label="服务名"><n-input v-model:value="ie.grpc_service" placeholder="grpc-service" /></n-form-item>
            <n-form-item v-if="ie.net === 'grpc'" label="Multi Mode"><n-switch v-model:value="ie.grpc_multi" /></n-form-item>
            <n-form-item label="多路复用"><n-switch v-model:value="ie.mux" /></n-form-item>
            <template v-if="ie.mux">
              <n-form-item label="Brutal"><n-switch v-model:value="ie.brutal" /></n-form-item>
              <n-form-item v-if="ie.brutal" label="Brutal 上行"><n-input-number v-model:value="ie.brutal_up" :min="0" style="width:100%;" /></n-form-item>
              <n-form-item v-if="ie.brutal" label="Brutal 下行"><n-input-number v-model:value="ie.brutal_down" :min="0" style="width:100%;" /></n-form-item>
            </template>
          </template>
        </n-form>
        <n-button type="primary" block :loading="saving" @click="saveInbound">保存</n-button>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import {
  NTabs, NTabPane, NDrawer, NDrawerContent, NButton, NForm, NFormItem, NInput, NInputNumber, NInputGroup,
  NSelect, NSwitch, NRadioGroup, NRadio, NTag, NSpin, NEmpty, NCode, NCheckbox, useMessage
} from 'naive-ui'
import { apiList, apiGet, apiPost, apiPut, apiDelete } from '@/api'

const message = useMessage()
const tab = ref('tls')
const loading = ref(false)
const saving = ref(false)
const genLoading = ref(false)

// 抽屉宽度：移动端全屏，桌面 500px
const isMobile = ref(false)
const drawerW = computed(() => isMobile.value ? '100%' : 500)
function checkMobile() { isMobile.value = window.matchMedia('(max-width: 768px)').matches }
onMounted(() => { checkMobile(); window.addEventListener('resize', checkMobile); load() })
onUnmounted(() => window.removeEventListener('resize', checkMobile))

const tlsList = ref<any[]>([])
const inbounds = ref<any[]>([])
const servers = ref<any[]>([])
const previewJson = ref('')
const previewLoading = ref(false)
const previewSid = ref<number | null>(null)

// 搜索/筛选
const tlsSearch = ref('')
const inbSearch = ref('')
const filteredTls = computed(() => {
  const q = tlsSearch.value.trim().toLowerCase()
  if (!q) return tlsList.value
  return tlsList.value.filter(t => (t.name || '').toLowerCase().includes(q) || (jp(t.server_json).server_name || '').toLowerCase().includes(q))
})
const filteredInbounds = computed(() => {
  const q = inbSearch.value.trim().toLowerCase()
  if (!q) return inbounds.value
  return inbounds.value.filter(n => (n.tag || '').toLowerCase().includes(q) || (n.type || '').toLowerCase().includes(q))
})

// 批量操作
const checkedIds = ref(new Set<number>())
function toggleCheck(id: number) {
  const s = new Set(checkedIds.value)
  if (s.has(id)) s.delete(id); else s.add(id)
  checkedIds.value = s
}

// 端口测试
const portChecking = ref<number | null>(null)
const portResult = ref<Record<number, any>>({})

function jp(s: string) { try { return JSON.parse(s || '{}') } catch { return {} } }

const serverOpts = computed(() => [{ label: '本机', value: 0 }, ...servers.value.map(s => ({ label: s.name, value: s.id }))])
const tlsOpts = computed(() => tlsList.value.map(t => ({ label: t.name + ' (' + t.mode + ')', value: t.id })))
const protoOpts = [
  { label: 'VLESS', value: 'vless' }, { label: 'VMess', value: 'vmess' }, { label: 'Trojan', value: 'trojan' },
  { label: 'TUIC', value: 'tuic' }, { label: 'Hysteria2', value: 'hysteria2' }, { label: 'Shadowsocks', value: 'shadowsocks' },
  { label: 'AnyTLS', value: 'anytls' }, { label: 'Hysteria v1', value: 'hysteria' },
]
const fpOpts = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized'].map(v => ({ label: v, value: v }))
const verOpts = [{ label: '1.2', value: '1.2' }, { label: '1.3', value: '1.3' }]
const ssOpts = ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'].map(v => ({ label: v, value: v }))
const listenOpts = [
  { label: '::（IPv6+IPv4，默认）', value: '::' },
  { label: '0.0.0.0（仅 IPv4）', value: '0.0.0.0' },
  { label: '127.0.0.1（仅本机）', value: '127.0.0.1' },
]
const sniPresets = [
  'www.microsoft.com', 'www.tesla.com', 'www.apple.com', 'www.icloud.com',
  'www.akamai.com', 'www.cloudflare.com', 'www.amazon.com', 'gateway.icloud.com',
].map(v => ({ label: v, value: v }))
const presetOpts = [
  { label: 'VLESS + Reality + Vision', value: 'vless-reality' },
  { label: 'VLESS + WS + TLS', value: 'vless-ws-tls' },
  { label: 'Hysteria2', value: 'hysteria2' },
  { label: 'TUIC', value: 'tuic' },
  { label: 'Trojan + TLS', value: 'trojan-tls' },
  { label: 'Shadowsocks 2022', value: 'shadowsocks' },
]

function serverName(id: number) { if (!id) return '本机'; const s = servers.value.find(s => s.id === id); return s ? s.name : '#' + id }
function tlsName(id: number) { if (!id) return '无'; const t = tlsList.value.find(t => t.id === id); return t ? t.name : '#' + id }
function tlsUseCount(id: number) { return inbounds.value.filter(n => n.tls_id === id).length }

// ========== TLS ==========
const showTls = ref(false)
const te = reactive({
  id: 0, mode: 'reality', name: '', server_id: 0, server_name: 'www.microsoft.com',
  handshake_server: '', handshake_port: 443, fingerprint: 'chrome',
  private_key: '', public_key: '', short_ids: [] as string[],
  certificate: '', key: '', alpn: [] as string[], min_version: '', max_version: '', insecure: false,
})

function openTls(t?: any, clone = false) {
  sniResult.value = null
  hsResult.value = null
  if (t) {
    const s = jp(t.server_json), c = jp(t.client_json), r = s.reality || {}, hs = r.handshake || {}
    // short_id 可能是数组或单值，统一为数组
    let sids: string[] = []
    if (Array.isArray(r.short_id)) sids = r.short_id.filter((x: string) => x)
    else if (r.short_id) sids = [r.short_id]
    if (c.short_id && !sids.includes(c.short_id)) sids.unshift(c.short_id)
    Object.assign(te, {
      id: clone ? 0 : t.id, mode: t.mode, name: clone ? t.name + ' (副本)' : t.name, server_id: t.server_id || 0,
      server_name: s.server_name || '', handshake_server: hs.server || '', handshake_port: hs.server_port || 443,
      fingerprint: (c.utls && c.utls.fingerprint) || 'chrome',
      private_key: r.private_key || '', public_key: (c.reality && c.reality.public_key) || '',
      short_ids: sids.length ? sids : [''],
      certificate: Array.isArray(s.certificate) ? s.certificate.join('\n') : (s.certificate || ''), key: Array.isArray(s.key) ? s.key.join('\n') : (s.key || ''),
      alpn: s.alpn || [], min_version: s.min_version || '', max_version: s.max_version || '',
      insecure: !!c.insecure,
    })
  } else {
    Object.assign(te, {
      id: 0, mode: 'reality', name: '', server_id: 0, server_name: 'www.microsoft.com',
      handshake_server: '', handshake_port: 443, fingerprint: 'chrome',
      private_key: '', public_key: '', short_ids: [''],
      certificate: '', key: '', alpn: ['h3', 'h2', 'http/1.1'], min_version: '', max_version: '', insecure: false,
    })
  }
  showTls.value = true
}

function cloneTls(t: any) { openTls(t, true) }

// --- SNI 延迟测试 ---
const sniTesting = ref(false)
const sniResult = ref<any>(null)
async function testSni() {
  const host = (te.server_name || '').trim()
  if (!host) { message.warning('请输入 SNI 域名'); return }
  sniTesting.value = true
  sniResult.value = null
  try {
    sniResult.value = await apiGet<any>(`/api/admin/sb/sni-test?host=${encodeURIComponent(host)}`)
  } catch (e: any) {
    message.error(e.message || '测试失败')
  } finally {
    sniTesting.value = false
  }
}

// --- Reality 握手目标延迟测试 ---
const hsTesting = ref(false)
const hsResult = ref<any>(null)
async function testHandshake() {
  const host = (te.handshake_server || te.server_name || '').trim()
  if (!host) { message.warning('请输入握手目标'); return }
  const port = te.handshake_port || 443
  hsTesting.value = true
  hsResult.value = null
  try {
    hsResult.value = await apiGet<any>(`/api/admin/sb/sni-test?host=${encodeURIComponent(host)}&port=${port}`)
  } catch (e: any) {
    message.error(e.message || '测试失败')
  } finally {
    hsTesting.value = false
  }
}

async function genKeys() {
  genLoading.value = true
  try {
    const r = await apiPost<any>('/api/admin/sb/reality-keypair')
    te.private_key = r?.private_key || ''
    te.public_key = r?.public_key || ''
    // 生成时填入第一个 short_id 槽位
    if (te.short_ids.length === 0) te.short_ids.push('')
    te.short_ids[0] = r?.short_id || ''
    message.success('密钥对已生成')
  } catch (e: any) { message.error(e.message) } finally { genLoading.value = false }
}

async function saveTls() {
  if (!te.name || !te.server_name) { message.error('名称和 SNI 必填'); return }
  saving.value = true
  try {
    const ep = te.mode === 'reality' ? '/api/admin/sb/tls/reality' : '/api/admin/sb/tls/cert'
    const url = te.id ? ep + '/' + te.id : ep
    const fn = te.id ? apiPut : apiPost
    const body = te.mode === 'reality'
      ? { name: te.name, server_id: te.server_id, server_name: te.server_name, handshake_server: te.handshake_server, handshake_port: te.handshake_port, fingerprint: te.fingerprint, private_key: te.private_key, public_key: te.public_key, short_ids: te.short_ids.filter(s => s.trim()) }
      : { name: te.name, server_id: te.server_id, server_name: te.server_name, certificate: te.certificate, key: te.key, insecure: te.insecure, alpn: te.alpn, fingerprint: te.fingerprint, min_version: te.min_version, max_version: te.max_version }
    await fn(url, body)
    message.success('保存成功'); showTls.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function deleteTls(id: number) { try { await apiDelete('/api/admin/sb/tls/' + id); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) } }

// ========== Inbound ==========
const showInb = ref(false)
const presetType = ref<string | null>(null)
const ie = reactive({
  id: 0, type: 'vless', tag: '', listen: '::', listen_port: 443, tls_id: 0, server_id: 0, enabled: true,
  tfo: false, mptcp: false, cc: 'bbr', zero_rtt: false,
  up_mbps: 0, down_mbps: 0, obfs_password: '', masquerade: '',
  net: 'tcp', ws_path: '/', ws_host: '', ws_early_data: 0, grpc_service: '', grpc_multi: false,
  ss_method: '2022-blake3-aes-128-gcm', flow: 'vision',
  anytls_idle_check: 0, anytls_idle_timeout: 0, anytls_min_idle: 0,
  mux: false, brutal: false, brutal_up: 0, brutal_down: 0,
})

function resetIe() {
  Object.assign(ie, {
    id: 0, type: 'vless', tag: '', listen: '::', listen_port: 443, tls_id: 0, server_id: 0, enabled: true,
    tfo: false, mptcp: false, cc: 'bbr', zero_rtt: false,
    up_mbps: 0, down_mbps: 0, obfs_password: '', masquerade: '',
    net: 'tcp', ws_path: '/', ws_host: '', ws_early_data: 0, grpc_service: '', grpc_multi: false,
    ss_method: '2022-blake3-aes-128-gcm', flow: 'vision',
    anytls_idle_check: 0, anytls_idle_timeout: 0, anytls_min_idle: 0,
    mux: false, brutal: false, brutal_up: 0, brutal_down: 0,
  })
}

function openInbound(n?: any, clone = false) {
  if (n) {
    const o = jp(n.options), tr = o.transport || {}, mx = o.multiplex || {}, br = mx.brutal || {}, obfs = o.obfs || {}
    Object.assign(ie, {
      id: clone ? 0 : n.id, type: n.type, tag: clone ? n.tag + '-copy' : n.tag, listen: n.listen || '::', listen_port: n.listen_port,
      tls_id: n.tls_id || 0, server_id: n.server_id || 0, enabled: clone ? false : n.enabled,
      tfo: !!o.tcp_fast_open, mptcp: !!o.tcp_multi_path, cc: o.congestion_control || 'bbr', zero_rtt: !!o.zero_rtt_handshake,
      up_mbps: o.up_mbps || 0, down_mbps: o.down_mbps || 0,
      obfs_password: obfs.password || '', masquerade: typeof o.masquerade === 'string' ? o.masquerade : (o.masquerade?.url || ''),
      net: tr.type || 'tcp', ws_path: tr.path || '/', ws_host: tr.host || '', ws_early_data: tr.max_early_data || 0,
      grpc_service: tr.service_name || '', grpc_multi: !!tr.multi_mode,
      ss_method: o.method || '2022-blake3-aes-128-gcm', flow: o.flow || 'vision',
      anytls_idle_check: o.idle_session_check_interval || 0, anytls_idle_timeout: o.idle_session_timeout || 0, anytls_min_idle: o.min_idle_session || 0,
      mux: !!mx.enabled, brutal: !!br.enabled, brutal_up: br.up_mbps || 0, brutal_down: br.down_mbps || 0,
    })
  } else {
    resetIe()
  }
  showInb.value = true
}

function cloneInbound(n: any) { openInbound(n, true) }

// 一键模板
function applyPreset(v: string | null) {
  if (!v) return
  resetIe()
  const presets: Record<string, any> = {
    'vless-reality': { type: 'vless', tag: 'vless-reality', listen_port: 443, flow: 'vision' },
    'vless-ws-tls': { type: 'vless', tag: 'vless-ws', listen_port: 443, net: 'ws', ws_path: '/ws', flow: 'none' },
    'hysteria2': { type: 'hysteria2', tag: 'hy2', listen_port: 8443, up_mbps: 100, down_mbps: 100 },
    'tuic': { type: 'tuic', tag: 'tuic', listen_port: 8443, cc: 'bbr' },
    'trojan-tls': { type: 'trojan', tag: 'trojan', listen_port: 443 },
    'shadowsocks': { type: 'shadowsocks', tag: 'ss', listen_port: 8388 },
  }
  Object.assign(ie, presets[v])
  presetType.value = null
  showInb.value = true
}

async function saveInbound() {
  saving.value = true
  try {
    const o: any = { tcp_fast_open: ie.tfo, tcp_multi_path: ie.mptcp }
    if (ie.type === 'vless') o.flow = ie.flow
    if (ie.type === 'tuic') { o.congestion_control = ie.cc; o.zero_rtt_handshake = ie.zero_rtt }
    if (ie.type === 'hysteria2' || ie.type === 'hysteria') { if (ie.up_mbps) o.up_mbps = ie.up_mbps; if (ie.down_mbps) o.down_mbps = ie.down_mbps }
    if (ie.type === 'hysteria2') { if (ie.obfs_password) o.obfs = { type: 'salamander', password: ie.obfs_password }; if (ie.masquerade) o.masquerade = ie.masquerade }
    if (ie.type === 'shadowsocks') o.method = ie.ss_method
    if (ie.type === 'anytls') {
      if (ie.anytls_idle_check) o.idle_session_check_interval = ie.anytls_idle_check
      if (ie.anytls_idle_timeout) o.idle_session_timeout = ie.anytls_idle_timeout
      if (ie.anytls_min_idle) o.min_idle_session = ie.anytls_min_idle
    }
    if (['vless', 'vmess', 'trojan'].includes(ie.type)) {
      if (ie.net !== 'tcp') {
        o.transport = { type: ie.net }
        if (ie.net === 'ws' || ie.net === 'httpupgrade') {
          o.transport.path = ie.ws_path || '/'
          if (ie.ws_host) o.transport.host = ie.ws_host
          if (ie.ws_early_data > 0) { o.transport.max_early_data = ie.ws_early_data; o.transport.early_data_header_name = 'Sec-WebSocket-Protocol' }
        }
        if (ie.net === 'grpc') { o.transport.service_name = ie.grpc_service || ''; o.transport.multi_mode = ie.grpc_multi }
      }
      if (ie.mux) { o.multiplex = { enabled: true }; if (ie.brutal) o.multiplex.brutal = { enabled: true, up_mbps: ie.brutal_up, down_mbps: ie.brutal_down } }
    }
    const body = { type: ie.type, tag: ie.tag, listen: ie.listen || '::', listen_port: ie.listen_port, tls_id: ie.type === 'shadowsocks' ? 0 : (ie.tls_id || 0), server_id: ie.server_id || 0, enabled: ie.enabled, options: JSON.stringify(o) }
    const fn = ie.id ? apiPut : apiPost
    const url = ie.id ? '/api/admin/sb/inbounds/' + ie.id : '/api/admin/sb/inbounds'
    await fn(url, body)
    message.success('保存成功'); showInb.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function deleteInbound(id: number) { try { await apiDelete('/api/admin/sb/inbounds/' + id); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) } }

async function toggleInbound(n: any) {
  try {
    const o = jp(n.options)
    await apiPut('/api/admin/sb/inbounds/' + n.id, { type: n.type, tag: n.tag, listen: n.listen || '::', listen_port: n.listen_port, tls_id: n.tls_id, server_id: n.server_id, enabled: !n.enabled, options: JSON.stringify(o) })
    n.enabled = !n.enabled
  } catch (e: any) { message.error(e.message) }
}

// 端口连通性测试
async function checkPort(n: any) {
  portChecking.value = n.id
  try {
    const r = await apiGet<any>(`/api/admin/sb/port-check?server_id=${n.server_id || 0}&port=${n.listen_port}`)
    portResult.value[n.id] = r
  } catch (e: any) {
    portResult.value[n.id] = { reachable: false, error: e.message || '测试失败' }
  } finally {
    portChecking.value = null
  }
}

// 批量启停
async function batchToggle(enable: boolean) {
  const targets = inbounds.value.filter(n => checkedIds.value.has(n.id))
  let ok = 0
  for (const n of targets) {
    if (n.enabled === enable) { ok++; continue }
    try {
      const o = jp(n.options)
      await apiPut('/api/admin/sb/inbounds/' + n.id, { type: n.type, tag: n.tag, listen: n.listen || '::', listen_port: n.listen_port, tls_id: n.tls_id, server_id: n.server_id, enabled: enable, options: JSON.stringify(o) })
      n.enabled = enable
      ok++
    } catch {}
  }
  checkedIds.value = new Set()
  message.success(`${ok} 个入站已${enable ? '启用' : '停用'}`)
}

async function batchDelete() {
  const ids = [...checkedIds.value]
  let ok = 0
  for (const id of ids) {
    try { await apiDelete('/api/admin/sb/inbounds/' + id); ok++ } catch {}
  }
  checkedIds.value = new Set()
  message.success(`${ok} 个入站已删除`)
  await load()
}

// ========== Preview ==========
async function loadPreview() {
  previewLoading.value = true
  try {
    const url = previewSid.value ? '/api/admin/sb/preview?server_id=' + previewSid.value : '/api/admin/sb/preview'
    previewJson.value = JSON.stringify(await apiGet(url), null, 2)
  } catch {} finally { previewLoading.value = false }
}

function copy(text: string) { if (text) { navigator.clipboard.writeText(text); message.success('已复制') } }

// ========== Load ==========
async function load() {
  loading.value = true
  try {
    const [t, i, s] = await Promise.all([apiList('/api/admin/sb/tls'), apiList('/api/admin/sb/inbounds'), apiList('/api/admin/servers')])
    tlsList.value = t; inbounds.value = i; servers.value = s
  } catch {} finally { loading.value = false }
}
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 16px; }
:deep(.n-drawer-content-body) { display: flex; flex-direction: column; }

.sni-result {
  width: 100%;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.5;
  border: 1px solid var(--n-border-color, #e0e0e6);
  background: var(--n-color, #f7f7fa);
}
.sni-result.ok {
  border-color: #18a058;
  background: rgba(24, 160, 88, 0.08);
  color: #18a058;
}
.sni-result.partial {
  border-color: #f0a020;
  background: rgba(240, 160, 32, 0.08);
  color: #b88200;
}
.sni-result.unreachable {
  border-color: #d03050;
  background: rgba(208, 48, 80, 0.08);
  color: #d03050;
}
.sni-result b { margin-right: 4px; }

.port-result {
  margin-top: 6px;
  padding: 4px 8px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.4;
}
.port-result.ok { background: rgba(24, 160, 88, 0.08); color: #18a058; }
.port-result.err { background: rgba(208, 48, 80, 0.08); color: #d03050; }
</style>
