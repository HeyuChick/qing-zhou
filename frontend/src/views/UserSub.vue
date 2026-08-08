<template>
  <div>
    <!-- 页面头 -->
    <div class="sub-head">
      <div>
        <h2 class="page-title">订阅管理</h2>
        <p class="page-sub">订阅链接、导入格式与节点开关，都在这里管理</p>
      </div>
      <n-space size="small">
        <n-button size="small" secondary @click="router.push('/dashboard')">
          <template #icon><n-icon><SpeedometerOutline /></n-icon></template>
          控制台
        </n-button>
        <n-button size="small" secondary @click="router.push('/orders')">订单记录</n-button>
        <n-button size="small" type="primary" @click="router.push('/shop')">去商城</n-button>
      </n-space>
    </div>

    <!-- 订阅链接 -->
    <n-card size="small" class="sec">
      <template #header><span class="sec-title">订阅链接</span></template>
      <n-input-group>
        <n-input :value="sub.url" readonly placeholder="暂无订阅" />
        <n-button type="primary" @click="copy(sub.url)">复制</n-button>
      </n-input-group>
      <div style="margin-top:12px;display:flex;gap:8px;flex-wrap:wrap;">
        <n-button size="small" @click="copy(sub.formats?.clash)">Clash</n-button>
        <n-button size="small" @click="copy(sub.formats?.singbox)">sing-box</n-button>
        <n-button size="small" @click="copy(sub.formats?.surge)">Surge</n-button>
        <!-- formats.base64, not formats.default: default has no ?format= and so
             picks its output from the client's User-Agent, which silently hands
             YAML to anything whose UA contains "clash". This button is for
             v2rayN / NekoBox / Shadowrocket, so it must pin the link list. -->
        <n-button size="small" @click="copy(sub.formats?.base64 || sub.formats?.default)">通用 / v2rayN</n-button>
        <n-button size="small" @click="showQr=!showQr">{{ showQr?'隐藏':'显示' }}二维码</n-button>
        <!-- 两个按钮代价完全不同，分开呈现：换地址是纯面板操作、立即生效、不影响
             任何人；换凭据要同步到每个节点才生效，因此默认禁用 + 30 天冷却。 -->
        <n-button size="small" type="warning" @click="handleResetSub">更换订阅地址</n-button>
        <!-- 禁用与否跟随后端开关，不写死：后端本来就要校验 node_creds_reset_enabled，
             按钮读同一个值才不会出现「管理员开了但按钮还是灰的」。 -->
        <n-tooltip trigger="hover" :disabled="credsResetEnabled">
          <template #trigger>
            <n-button size="small" type="error" :disabled="!credsResetEnabled" :loading="resettingCreds"
                      @click="handleResetNodeCreds">重置节点凭据</n-button>
          </template>
          该功能暂时禁用，有需要请联系管理员
        </n-tooltip>
      </div>
      <div style="margin-top:8px;font-size:11px;color:var(--text-3);line-height:1.6;">
        订阅地址泄露时用「更换订阅地址」：旧地址立即失效，无需重启节点。
        注意它不会使已经导出的节点失效——那需要「重置节点凭据」。
      </div>
      <div v-if="showQr && sub.url" style="margin-top:12px;text-align:center;">
        <canvas ref="qrCanvas" />
        <div style="font-size:11px;color:var(--text-3);margin-top:4px;">手机扫描导入订阅</div>
      </div>
    </n-card>

    <!-- 我的套餐：每个套餐独立计量，各自展示剩余流量与到期时间（可能多份并存，不合并） -->
    <n-card v-if="plans.length" size="small" class="sec" title="我的套餐">
      <template #header-extra>
        <span v-if="hasQueued" style="font-size:11.5px;color:var(--text-3);">重复购买自动排队，一次只用一份</span>
      </template>
      <!-- 网格而非纵向平铺：每份套餐的信息量固定（一条进度 + 两行小字），窄卡片
           完全放得下，多份并存时横向排开比一路往下堆省掉大半屏高度。
           auto-fill + minmax 让它在窄屏自动退回单列，不用另写断点。 -->
      <div class="plan-grid">
        <div v-for="p in sortedPlans" :key="p.id" class="plan-row" :class="{ queued: p.status === 'queued' }">
          <div style="display:flex;justify-content:space-between;align-items:center;gap:6px;margin-bottom:6px;">
            <span style="font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{{ p.name || '套餐 #' + p.id }}</span>
            <n-tag :type="planStatus(p).type" size="small" bordered>{{ planStatus(p).label }}</n-tag>
          </div>
          <n-progress v-if="p.status !== 'queued'" type="line" :percentage="planPct(p)" :color="planPct(p)>90?'#c2685c':'#6f8f76'" />
          <div v-else style="height:6px;border-radius:3px;background:repeating-linear-gradient(45deg,var(--border),var(--border) 4px,transparent 4px,transparent 8px);"></div>
          <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--text-3);margin-top:4px;gap:8px;">
            <span>{{ p.status === 'queued' ? '待用流量 ' + (p.traffic_limit>0 ? fmtTotal(p.traffic_limit) : '不限') : '剩余 ' + (p.remaining < 0 ? '不限' : fmtBytes(p.remaining)) + '（' + fmtBytes(p.used) + ' / ' + fmtTotal(p.traffic_limit) + '）' }}</span>
            <span style="white-space:nowrap;">{{ planTime(p) }}</span>
          </div>
        </div>
      </div>
    </n-card>

    <!-- HTTP/SOCKS5 代理（mixed 节点，不在订阅里，单独复制填入 1Panel/Docker 等） -->
    <n-card v-if="proxies.length" size="small" class="sec" title="HTTP / SOCKS5 代理">
      <template #header-extra><span style="font-size:11px;color:var(--text-3);">可填入 1Panel、Docker、git 等只认 HTTP/SOCKS 代理的地方</span></template>
      <!-- 默认只留一行：节点 + 地址:端口 + 复制链接。原先六行「标签 + 只读输入框 +
           复制」每个代理就吃掉大半屏，而有了整串 URL，逐字段复制只在 1Panel 这类
           分字段表单里才需要——那是少数情况，收进「详情」里按需展开。 -->
      <div v-for="p in proxies" :key="p.tag" class="proxy-row">
        <div class="px-head">
          <span class="px-name">{{ p.tag }}</span>
          <n-tag v-if="p.expired" type="error" size="small" bordered>已过期</n-tag>
          <n-tag :type="p.tls?'success':'warning'" size="small" bordered>{{ p.tls ? 'HTTPS' : 'HTTP / SOCKS5' }}</n-tag>
          <code class="px-addr">{{ p.host }}:{{ p.port }}</code>
          <!-- 一键复制成 scheme://user:pass@host:port —— 大多数工具（git、docker、
               curl、各类 SDK 的 HTTPS_PROXY）只认这一种整串形式。
               只给按钮不显示明文：URL 里带着密码，直接铺在页面上就把详情里那个
               掩码输入框的意义抵消了。
               按钮自成一组：直接摊在 px-head 里的话，窄屏换行会把它们拆散到两行的
               两端（第一行末尾一个、第二行开头一个），整组一起换行才读得出是一组。 -->
          <div class="px-actions">
            <!-- 标签不带「链接」二字：窄屏上这一行本来就要换行，少几个字就少一行，
                 而卡片标题 + 下方说明已经交代了复制到手的是整串 URL。 -->
            <template v-if="p.tls">
              <n-button size="tiny" type="primary" secondary @click="copy(proxyUrl(p, 'https'))">复制 HTTPS</n-button>
            </template>
            <template v-else>
              <n-button size="tiny" type="primary" secondary @click="copy(proxyUrl(p, 'http'))">复制 HTTP</n-button>
              <n-button size="tiny" type="primary" secondary @click="copy(proxyUrl(p, 'socks5'))">复制 SOCKS5</n-button>
            </template>
            <n-button size="tiny" quaternary @click="toggleProxyDetail(p.tag)">
              {{ expandedProxies.includes(p.tag) ? '收起' : '详情' }}
            </n-button>
          </div>
        </div>
        <!-- 默认账号是个待办事项，不该只在展开后才看得见 -->
        <div v-if="!p.custom" class="px-hint">系统默认账号，建议点「详情 → 编辑账号」自设</div>
        <div v-if="expandedProxies.includes(p.tag)" class="px-detail">
          <div class="pxrow"><span class="pxk">类型</span><span style="font-size:13px;">{{ p.tls ? 'HTTPS' : 'HTTP / SOCKS5' }}</span></div>
          <div class="pxrow"><span class="pxk">地址</span><div class="pxv"><n-input-group><n-input :value="p.host" readonly size="small" /><n-button size="small" @click="copy(p.host)">复制</n-button></n-input-group></div></div>
          <div class="pxrow"><span class="pxk">端口</span><div class="pxv"><n-input-group><n-input :value="String(p.port)" readonly size="small" /><n-button size="small" @click="copy(String(p.port))">复制</n-button></n-input-group></div></div>
          <div class="pxrow"><span class="pxk">用户名</span><div class="pxv"><n-input-group><n-input :value="p.username" readonly size="small" /><n-button size="small" @click="copy(p.username)">复制</n-button></n-input-group></div></div>
          <div class="pxrow"><span class="pxk">密码</span><div class="pxv"><n-input-group><n-input :value="p.password" type="password" show-password-on="click" readonly size="small" /><n-button size="small" @click="copy(p.password)">复制</n-button></n-input-group></div></div>
          <div class="pxrow">
            <span class="pxk">有效期</span>
            <span style="font-size:12px;color:var(--text-2);flex:1;">{{ p.expires_at ? fmtDate(p.expires_at) : '永久' }}</span>
            <n-button size="tiny" @click="openEditProxy(p)">编辑账号</n-button>
          </div>
        </div>
      </div>
      <div style="font-size:11px;color:var(--text-3);margin-top:10px;">命令行 / Docker / git 等直接点「复制 HTTP」「复制 SOCKS5」，拿到的是 <code>scheme://用户名:密码@地址:端口</code> 整串。1Panel 这类分字段的表单展开「详情」逐项复制：代理类型选 <b>HTTP</b> 或 <b>SOCKS5</b>（标着 <b>HTTPS</b> 的节点则选 HTTPS）。</div>
    </n-card>

    <!-- 编辑代理账号 -->
    <n-modal v-model:show="showEditProxy" preset="card" title="编辑代理账号" style="max-width:440px;">
      <n-form label-placement="left" label-width="72">
        <n-form-item label="节点"><n-input :value="editForm.tag" readonly /></n-form-item>
        <n-form-item label="用户名">
          <n-input v-model:value="editForm.username" placeholder="仅字母/数字/ _.@- ，不能以 qz_ 开头" />
        </n-form-item>
        <n-form-item label="密码">
          <n-input-group>
            <n-input v-model:value="editForm.password" type="password" show-password-on="click" placeholder="6-128 位" />
            <n-button @click="genProxyPassword">生成32位</n-button>
          </n-input-group>
        </n-form-item>
        <n-form-item label="有效期">
          <div style="width:100%;">
            <n-switch v-model:value="editForm.permanent" style="margin-bottom:8px;"><template #checked>永久</template><template #unchecked>指定日期</template></n-switch>
            <n-date-picker v-if="!editForm.permanent" v-model:value="editForm.expireTs" type="datetime" clearable style="width:100%;" />
          </div>
        </n-form-item>
        <div style="font-size:11px;color:var(--text-3);margin-bottom:12px;">这是仅用于该协议的独立账号，与登录账号无关。密码泄露可随时来此更改；到期后该代理自动停用（可续期）。</div>
        <n-button type="primary" block :loading="savingProxy" @click="saveProxy">保存</n-button>
      </n-form>
    </n-modal>

    <!-- 节点列表 -->
    <n-card size="small" class="sec" title="节点列表">
      <template #header-extra>
        <n-space size="small">
          <n-input v-model:value="search" placeholder="搜索节点 / 线路" size="small" style="width:160px;" clearable />
          <n-select v-model:value="protoFilter" :options="protoOptions" placeholder="协议" size="small" style="width:100px;" clearable />
          <n-button size="small" @click="handlePing" :loading="pinging">测速</n-button>
          <n-button size="small" @click="handleToggleAll(true)">全启用</n-button>
          <n-button size="small" @click="handleToggleAll(false)">全禁用</n-button>
        </n-space>
      </template>
      <!-- 按套餐分节：一个节点归哪份套餐，决定的是它走谁的流量和有效期，所以这条
           归属线才是节点列表真正的分组依据（后端 plan_id 就是计费用的那个桶）。 -->
      <div v-for="g in nodeGroups" :key="g.planId" class="ngrp">
        <div class="ngrp-head">
          <span class="ngrp-name">{{ g.planName }}</span>
          <span class="ngrp-meta">{{ g.nodes.length }} 个节点<template v-if="g.offCount"> · {{ g.offCount }} 个禁用</template></span>
          <span style="flex:1;"></span>
          <n-button size="tiny" quaternary @click="handlePlanToggle(g, true)">全启用</n-button>
          <n-button size="tiny" quaternary @click="handlePlanToggle(g, false)">全禁用</n-button>
        </div>
        <n-data-table :columns="nodeCols" :data="g.nodes" :bordered="false" size="small"
                      :pagination="g.nodes.length > 20 ? { pageSize: 20 } : false" :row-key="(r:any)=>r.key"
                      :checked-row-keys="selectedByPlan[g.planId] || []"
                      @update:checked-row-keys="(k:any) => selectedByPlan[g.planId] = k" />
      </div>
      <n-empty v-if="!loadingNodes && !nodeGroups.length" :description="nodes.length ? '没有匹配的节点' : '暂无节点'" style="padding:28px 0;" />
      <div v-if="selectedKeys.length" style="margin-top:10px;display:flex;gap:8px;align-items:center;">
        <span style="font-size:12px;color:var(--text-3);">已选 {{ selectedKeys.length }} 个</span>
        <n-button size="small" type="primary" @click="handleBulk(true)">批量启用</n-button>
        <n-button size="small" type="error" @click="handleBulk(false)">批量禁用</n-button>
      </div>
      <div style="margin-top:8px;font-size:11px;color:var(--text-3);">共 {{ nodes.length }} 个节点，{{ filteredNodes.length }} 个匹配，{{ nodes.filter((n:any)=>n.disabled).length }} 个禁用。线路一栏从左到右是客户端实际走的路径：入口机 → 中转机 → 出网。</div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, h, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NInput, NInputGroup, NButton, NDataTable, NTag, NTooltip, NSelect, NSpace, NModal, NForm, NFormItem, NSwitch, NDatePicker, NProgress, NIcon, useMessage, useDialog } from 'naive-ui'
import { SpeedometerOutline } from '@vicons/ionicons5'
import { apiGet, apiList, apiPost, apiPut } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { fmtBytes, fmtTotal, fmtDate, pct } from '@/utils/format'
import { planStatusMeta, planTimeText, planSortKey } from '@/utils/plan'
import { copyText } from '@/utils/clipboard'
import QRCode from 'qrcode'

const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuthStore()
const sub = ref<any>({})
const proxies = ref<any[]>([])
const nodes = ref<any[]>([])
// 我的套餐：后端按套餐独立计量（可能多份并存、含排队份），全部列出，不合并
const plans = ref<any[]>([])
// Read: 使用中 first, then 排队中 (by soonest activation), then finished — so the
// current份 and what's next are always at the top.
const sortedPlans = computed<any[]>(() => {
  const list = [...plans.value]
  list.sort((a, b) => planSortKey(a) - planSortKey(b) || (a.activate_by || a.expiry_at || 0) - (b.activate_by || b.expiry_at || 0))
  return list
})
const hasQueued = computed(() => plans.value.some(p => p.status === 'queued'))
function planPct(p: any) { return p.status === 'queued' ? 0 : pct(p.used, p.traffic_limit) }
const planStatus = planStatusMeta
const planTime = (p: any) => planTimeText(p, fmtDate)
// 由后端的 node_creds_reset_enabled 决定，缺省按关闭处理——拿不到就当没开，
// 不要给用户一个点了必然 403 的按钮。
const credsResetEnabled = computed(() => sub.value?.creds_reset_enabled === true)
const resettingCreds = ref(false)

// 代理账号编辑
const showEditProxy = ref(false)
const savingProxy = ref(false)
const editForm = ref<any>({ bucket_id: 0, tag: '', username: '', password: '', permanent: true, expireTs: null as number | null })

// 展开的代理（按 tag 记）。默认全收起——常用路径是复制整串 URL，逐字段只是备用。
const expandedProxies = ref<string[]>([])
function toggleProxyDetail(tag: string) {
  const i = expandedProxies.value.indexOf(tag)
  if (i >= 0) expandedProxies.value.splice(i, 1)
  else expandedProxies.value.push(tag)
}

// proxyUrl 拼出 scheme://user:pass@host:port。
// - 用户名/密码走 encodeURIComponent：密码是用户自设的任意 6-128 位，里面出现
//   @ : / # ? 都会把 URL 解析歪，必须转义。
// - host 若是裸 IPv6（含 :）要加方括号，否则冒号会被当成端口分隔符。
function proxyUrl(p: any, scheme: string) {
  const host = p.host?.includes(':') && !p.host.startsWith('[') ? `[${p.host}]` : p.host
  const cred = p.username ? `${encodeURIComponent(p.username)}:${encodeURIComponent(p.password || '')}@` : ''
  return `${scheme}://${cred}${host}:${p.port}`
}

function openEditProxy(p: any) {
  editForm.value = {
    bucket_id: p.bucket_id,
    tag: p.tag,
    // 默认用登录账号名（仅作默认，是独立的代理账号）；已自设过则回填现有用户名。
    username: p.custom ? p.username : (auth.user?.username || ''),
    password: '',
    permanent: !p.expires_at,
    expireTs: p.expires_at ? p.expires_at * 1000 : null,
  }
  showEditProxy.value = true
}

function genProxyPassword() {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const arr = new Uint32Array(32)
  crypto.getRandomValues(arr)
  editForm.value.password = Array.from(arr, (n) => chars[n % chars.length]).join('')
}

async function saveProxy() {
  const f = editForm.value
  if (!f.username?.trim()) { message.warning('请填写用户名'); return }
  if (!f.password || f.password.length < 6) { message.warning('密码至少 6 位'); return }
  if (!f.permanent && !f.expireTs) { message.warning('请选择有效期，或切换为永久'); return }
  savingProxy.value = true
  try {
    await apiPut('/api/user/proxies/' + f.bucket_id, {
      username: f.username.trim(),
      password: f.password,
      expires_at: f.permanent ? 0 : Math.floor(f.expireTs / 1000),
    })
    message.success('已保存，代理账号已更新')
    showEditProxy.value = false
    proxies.value = await apiList('/api/user/proxies')
  } catch (e: any) { message.error(e.message) } finally { savingProxy.value = false }
}
const showQr = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const search = ref('')
const protoFilter = ref<string | null>(null)
// 每个套餐一张表，勾选状态也得一张表一份：几张表共用一个 ref 时，任何一张表的
// update 事件都会把自己那份完整勾选列表写回去，等于清空其他表的选择。
const selectedByPlan = ref<Record<string, string[]>>({})
// 去重：一条线路可以同时属于两份套餐，两个分组里都勾上就会出现两次同一个 key。
const selectedKeys = computed<string[]>(() => [...new Set(Object.values(selectedByPlan.value).flat())])
const loadingNodes = ref(false)
const pinging = ref(false)

const protoOptions = computed(() => {
  const set = new Set(nodes.value.map((n: any) => n.protocol).filter(Boolean))
  return Array.from(set).map(p => ({ label: p.toUpperCase(), value: p }))
})

const filteredNodes = computed(() => {
  let list: any[] = nodes.value
  if (search.value) {
    const q = search.value.toLowerCase()
    // 节点名和链路上的机器名/地区都匹配：两列都在眼前，搜哪个都该命中。
    list = list.filter((n: any) => [
      n.name, n.server,
      ...(n.topo?.hops || []).flatMap((h: any) => [h.name, h.location]),
    ].some((s: any) => s?.toLowerCase().includes(q)))
  }
  if (protoFilter.value) list = list.filter((n: any) => n.protocol === protoFilter.value)
  return list
})

// 分组顺序跟着「我的套餐」卡片走（使用中在前、排队在后），两处对同一份套餐的排序
// 一致，滚上去核对时不用重新找。套餐列表里没有的归属（免费线路）排在最后。
//
// 后端给的是「哪几份套餐能用这条线路」，一条线路可能被两份套餐同时覆盖，那它就在
// 两个分组里各出现一次——这是事实，不是重复。节点对象是同一个引用，所以在任一处
// 开关，另一处的状态跟着变。
const nodeGroups = computed(() => {
  const order = new Map<string, number>()
  sortedPlans.value.forEach((p, i) => order.set(String(p.id), i))
  const byPlan = new Map<string, any>()
  for (const n of filteredNodes.value) {
    const refs = n.plans?.length ? n.plans : [{ id: 0, name: '未归属套餐' }]
    for (const ref of refs) {
      const id = String(ref.id)
      let g = byPlan.get(id)
      if (!g) {
        g = { planId: id, planName: ref.name, nodes: [] as any[], offCount: 0 }
        byPlan.set(id, g)
      }
      g.nodes.push(n)
      if (n.disabled) g.offCount++
    }
  }
  return [...byPlan.values()].sort((a, b) =>
    (order.get(a.planId) ?? Number.MAX_SAFE_INTEGER) - (order.get(b.planId) ?? Number.MAX_SAFE_INTEGER))
})

function latencyColor(ms: number) {
  if (ms < 0) return 'var(--text-3)'
  if (ms < 150) return '#10b981'
  if (ms < 400) return '#bf9540'
  return '#ef4444'
}

// 一段链路的胶囊。这些 vnode 由 n-data-table 渲染，拿不到本组件 scoped 样式的
// 属性标记，所以类名走文件末尾那个非 scoped 的 style 块。
function hopChip(kind: string, name: string, proto?: string, loc?: string) {
  return h('span', { class: 'qz-hop qz-hop-' + kind, title: loc || undefined }, [
    h('b', null, name),
    proto ? h('span', { class: 'qz-hop-proto' }, proto.toUpperCase()) : null,
  ])
}

// 「哪台机器 → 哪台机器」。这一栏只画路径，不重复节点名——名字在左边独立一列，
// 因为它是唯一能和客户端里那条订阅对上号的标识，路径本身回答的是另一个问题
// （流量实际怎么走），两件事各占一列比挤在一起清楚。
function renderTopo(r: any) {
  const kids: any[] = []
  const hops = r.topo?.hops || []
  if (!hops.length) {
    // 外部导入的分享链接：这条链路不在我们手里，除了它自己什么都不知道。
    kids.push(hopChip('ext', '外部节点', r.protocol))
  } else {
    hops.forEach((hp: any, i: number) => {
      if (i) kids.push(h('span', { class: 'qz-arrow' }, hp.kind === 'egress' ? '⇢ 出口 ⇢' : '⇢ 中转 ⇢'))
      kids.push(hopChip(hp.kind, hp.name, hp.protocol, hp.location))
    })
  }
  // 降级不是细节：落地/出口断了，流量改从上一跳的 IP 出网，出口地址静默变了。
  // 这段本身就带箭头，末尾不再补一个，免得出现「⇢ … ⇢ →」。
  if (r.topo?.warn) {
    kids.push(h('span', { class: 'qz-arrow qz-arrow-warn' },
      r.topo.warn === 'egress' ? '⇢ 出口已失效 ⇢' : '⇢ 落地已失效 ⇢'))
  } else {
    kids.push(h('span', { class: 'qz-arrow' }, '→'))
  }
  kids.push(h('span', { class: 'qz-hop qz-hop-inet' }, '🌐 互联网'))
  return h('div', { class: 'qz-topo', title: r.name || '' }, kids)
}

const nodeCols = [
  { type: 'selection' as const },
  // 节点名单独一列而不是塞进线路里：客户端（Clash / v2rayN）节点选择器上显示的就是
  // 这个名字，没有它就对不上「我现在连的是哪条」。宽度写死 + 省略号，免得长名字
  // 把右边的线路挤没。
  { title: '节点', key: 'name', width: 132, ellipsis: { tooltip: true } },
  { title: '线路', key: 'topo', render: renderTopo },
  {
    title: '延迟', key: 'latency', width: 70,
    render: (r: any) => {
      if (r.latency == null) return h('span', { style: 'color:var(--text-3)' }, '—')
      if (r.latency < 0) return h(NTag, { size: 'tiny', type: 'error', bordered: false }, { default: () => '超时' })
      return h('span', { style: `color:${latencyColor(r.latency)};font-weight:600;` }, r.latency + 'ms')
    },
  },
  {
    title: '状态', key: 'disabled', width: 70,
    render: (r: any) => h(NTag, { type: r.disabled ? 'default' : 'success', size: 'small', bordered: false }, { default: () => r.disabled ? '禁用' : '启用' }),
  },
  {
    title: '操作', key: 'act', width: 70,
    render: (r: any) => h(NButton, { size: 'tiny', onClick: () => toggleNode(r) }, { default: () => r.disabled ? '启用' : '禁用' }),
  },
]

async function toggleNode(node: any) {
  try {
    const newDisabled = !node.disabled
    await apiPost('/api/user/nodes/toggle', { key: node.key, disabled: newDisabled })
    node.disabled = newDisabled
  } catch (e: any) { message.error(e.message) }
}
async function handlePing() {
  pinging.value = true
  try {
    const data = await apiList<any>('/api/user/nodes/ping')
    const m = new Map(data.map((d: any) => [d.key, d.latency]))
    nodes.value = nodes.value.map((n: any) => ({ ...n, latency: m.get(n.key) ?? null }))
    message.success('测速完成')
  } catch (e: any) { message.error(e.message) } finally { pinging.value = false }
}
// applyBulk 是「一批节点一起开/关」的唯一路径：批量按钮和每个套餐的全启用/全禁用
// 都走它，避免两处各写一遍本地状态回写。
async function applyBulk(keys: string[], enable: boolean) {
  if (!keys.length) return
  const body = enable ? { enable: keys, disable: [] } : { enable: [], disable: keys }
  await apiPost('/api/user/nodes/bulk', body)
  const set = new Set(keys)
  nodes.value = nodes.value.map((n: any) => set.has(n.key) ? { ...n, disabled: !enable } : n)
}
async function handleBulk(enable: boolean) {
  try {
    await applyBulk(selectedKeys.value, enable)
    selectedByPlan.value = {}
    message.success(enable ? '已批量启用' : '已批量禁用')
  } catch (e: any) { message.error(e.message) }
}
// 只动这份套餐下的节点——分组之后「全启用」按钮就在分组标题里，它要是仍然扫全部
// 节点，点下去会连别的套餐一起改，和它所在的位置说的不是一回事。
async function handlePlanToggle(g: any, enable: boolean) {
  try {
    await applyBulk(g.nodes.map((n: any) => n.key), enable)
    message.success(`「${g.planName}」已全部${enable ? '启用' : '禁用'}`)
  } catch (e: any) { message.error(e.message) }
}
async function handleToggleAll(enable: boolean) {
  try {
    await apiPost(enable ? '/api/user/nodes/enable-all' : '/api/user/nodes/disable-all')
    nodes.value = nodes.value.map((n: any) => ({ ...n, disabled: !enable }))
    message.success(enable ? '已全部启用' : '已全部禁用')
  } catch (e: any) { message.error(e.message) }
}
function handleResetSub() {
  // Swapping the address invalidates every client configured with the old one,
  // so it still needs a confirm — but it does NOT revoke the nodes those clients
  // already hold, and the copy must not claim otherwise.
  dialog.warning({
    title: '确认更换订阅地址',
    content: '更换后旧地址立即失效，所有已导入的客户端（Clash / sing-box 等）都需要用新地址重新导入。注意：已经从旧地址导出的节点仍然可用，如需彻底吊销请联系管理员重置节点凭据。确定更换？',
    positiveText: '更换', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiPost('/api/user/reset-sub'); sub.value = await apiGet('/api/user/subscription') || {}; message.success('订阅地址已更换') }
      catch (e: any) { message.error(e.message) }
    },
  })
}
function handleResetNodeCreds() {
  // The expensive half. Unlike the address swap this one has a real cost to
  // spell out: it needs a node-side push before it takes effect, it breaks every
  // client until they re-import, and it can only be done once a month.
  dialog.error({
    title: '确认重置节点凭据',
    content: '这会为你的所有节点重新生成凭据，从旧订阅导出的节点将彻底失效。'
      + '新凭据需要同步到各节点后才生效（通常 1 分钟内），期间你自己的连接也会中断，需要重新导入订阅。'
      + '每 30 天只能重置一次。确定重置？',
    positiveText: '重置', negativeText: '取消',
    onPositiveClick: async () => {
      resettingCreds.value = true
      try {
        const r: any = await apiPost('/api/user/reset-node-creds')
        sub.value = await apiGet('/api/user/subscription') || {}
        // 节点链接与代理账号都嵌了刚刚轮换掉的凭据，必须重新拉一次，
        // 否则页面上还挂着一份已经失效的旧链接。
        try { nodes.value = await apiList('/api/user/nodes') } catch {}
        try { proxies.value = await apiList('/api/user/proxies') } catch {}
        const secs = Number(r?.applies_in_seconds) || 60
        message.success(`节点凭据已重置，约 ${secs} 秒后在各节点生效，请重新导入订阅`)
      } catch (e: any) { message.error(e.message) }
      finally { resettingCreds.value = false }
    },
  })
}
async function copy(text: string) {
  if (!text) { message.warning('暂无链接'); return }
  // Honest feedback: navigator.clipboard is unavailable on plain-HTTP origins
  // (common for self-hosted panels), so copyText falls back and reports failure
  // rather than us claiming success on a silent no-op.
  if (await copyText(text)) message.success('已复制'); else message.error('复制失败，请手动选择并复制')
}

watch(showQr, async (v) => {
  if (v && sub.value.url) {
    await nextTick()
    if (qrCanvas.value) {
      QRCode.toCanvas(qrCanvas.value, sub.value.url, { width: 180, margin: 2 }, (err: any) => {
        if (err) console.error('QR error:', err)
      })
    }
  }
})

onMounted(async () => {
  try { sub.value = await apiGet('/api/user/subscription') || {} } catch (e: any) { message.error('订阅信息加载失败：' + (e?.message || '请稍后重试')) }
  try { plans.value = await apiList('/api/user/plans') } catch {}
  try { proxies.value = await apiList('/api/user/proxies') } catch {}
  loadingNodes.value = true
  try { nodes.value = await apiList('/api/user/nodes') } catch {} finally { loadingNodes.value = false }
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 0; }
.sub-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 20px; }
.sec { margin-bottom: 16px; border-radius: var(--r-sm); }
.sec-title { font-weight: 650; font-size: 14px; }
.plan-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 10px; }
.plan-row { padding: 12px; background: var(--bg-soft); border-radius: 10px; min-width: 0; }
.plan-row.queued { opacity: .72; border: 1px dashed var(--border); background: transparent; }
.proxy-row { margin-bottom: 8px; padding: 10px 12px; background: var(--bg-soft); border-radius: 10px; }
.proxy-row:last-child { margin-bottom: 0; }
/* 一行放不下时按钮整体换行，地址不被挤成省略号 */
.px-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.px-name { font-weight: 600; }
.px-addr { font-size: 12px; color: var(--text-2); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
/* margin-left:auto 把整组按钮推到右端；宽度不够时它整块换到下一行 */
.px-actions { display: flex; align-items: center; gap: 6px; margin-left: auto; flex-wrap: wrap; }
.px-hint { font-size: 11px; color: var(--text-3); margin-top: 6px; }
.px-detail { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--border); }
.pxrow { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.pxrow:last-child { margin-bottom: 0; }
.pxk { width: 52px; flex-shrink: 0; font-size: 12px; color: var(--text-3); }
.pxv { flex: 1; min-width: 0; }

.ngrp { margin-bottom: 14px; }
.ngrp:last-of-type { margin-bottom: 0; }
.ngrp-head { display: flex; align-items: center; gap: 8px; padding: 0 2px 6px; }
.ngrp-name { font-weight: 650; font-size: 13px; }
.ngrp-meta { font-size: 11px; color: var(--text-3); }
</style>

<!--
  非 scoped：链路胶囊是 nodeCols 的 render 用 h() 造的 vnode，由 n-data-table
  渲染，拿不到本组件 scoped 样式的属性标记。类名统一加 qz- 前缀圈住作用域。
-->
<style>
.qz-topo { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; line-height: 1.9; }
.qz-hop { display: inline-flex; align-items: center; gap: 5px; padding: 1px 7px; border-radius: 6px; font-size: 12px; white-space: nowrap; }
.qz-hop b { font-weight: 600; }
.qz-hop-proto { font-size: 10px; opacity: .75; letter-spacing: .3px; }
/* 入口=蓝、中转=紫、出口=橙、互联网=灰，与管理端链路拓扑的配色一致 */
.qz-hop-entry { background: rgba(32, 128, 240, .12); color: #2080f0; }
.qz-hop-relay { background: rgba(139, 92, 246, .14); color: #7c53d8; }
.qz-hop-egress { background: rgba(217, 119, 6, .14); color: #c2751a; }
.qz-hop-ext { background: rgba(120, 120, 120, .14); color: var(--text-2, #666); }
.qz-hop-inet { background: transparent; color: var(--text-3, #999); padding-left: 0; }
.qz-arrow { font-size: 11px; color: var(--text-3, #999); white-space: nowrap; }
.qz-arrow-warn { color: #d03050; font-weight: 600; }
</style>
