<template>
  <div>
    <h2 class="page-title">节点管理</h2>
    <n-tabs v-model:value="tab" animated>
      <!-- 节点（按分组卡片展示，同组节点聚在一张卡片里） -->
      <n-tab-pane name="nodes" tab="节点">
        <div class="page-toolbar">
          <span class="spacer" />
          <n-button size="small" @click="openGroup()">添加分组</n-button>
          <n-button size="small" @click="openNodeImport">批量导入</n-button>
          <n-button size="small" type="primary" @click="openNode()">添加节点</n-button>
        </div>
        <n-spin :show="loading">
          <div v-if="groupedView.length">
            <n-card v-for="gv in groupedView" :key="gv.key" size="small" :title="gv.name" class="group-card">
              <template #header-extra>
                <div class="group-actions">
                  <n-tag size="tiny" :type="gv.isUngrouped ? 'default' : 'info'" bordered="false">{{ gv.nodes.length }} 节点</n-tag>
                  <n-button size="tiny" type="primary" @click="openNodeInGroup(gv.group)">＋ 节点</n-button>
                  <n-button v-if="gv.group" size="tiny" @click="openGroup(gv.group)">编辑</n-button>
                  <n-button v-if="gv.group" size="tiny" type="error" @click="handleDeleteGroup(gv.group.id)">删除</n-button>
                </div>
              </template>
              <div v-if="gv.description" class="group-desc">{{ gv.description }}</div>
              <div v-if="gv.nodes.length" class="card-grid">
                <div v-for="(r, idx) in gv.nodes" :key="r.id" class="list-card">
                  <div class="lc-head">
                    <span class="lc-title">{{ r.name || '—' }}</span>
                    <n-tag :type="r.enabled ? 'success' : 'default'" size="tiny" bordered="false">{{ r.enabled ? '启用' : '禁用' }}</n-tag>
                  </div>
                  <div class="lc-meta">
                    <span class="kv"><n-tag :type="r.type === 'self_built' ? 'info' : 'warning'" size="tiny" bordered="false">{{ r.type === 'self_built' ? '自建' : '外部' }}</n-tag></span>
                    <span class="kv">协议 <b>{{ nodeProtocol(r) }}</b></span>
                    <!-- 节点跑在哪台机器上。外部节点不在我们的机器上，自建但入站失踪时
                         也没有答案——那两种情况下面的链路行会说明，这里就不占位了。 -->
                    <span v-if="nodeServer(r)" class="kv">机器 <b>{{ nodeServer(r) }}</b></span>
                  </div>
                  <div v-if="(r.group_ids || []).length > 1" class="lc-meta"><span class="kv">分组 <b>{{ groupNames(r.group_ids) }}</b></span></div>
                  <div v-if="chainSummary(r)" class="lc-chain" :class="{ warn: chainSummary(r)!.broken }">
                    链路 {{ chainSummary(r)!.text }}
                  </div>
                  <div class="lc-foot">
                    <n-button size="tiny" :disabled="idx === 0 || reordering" title="前移（订阅/列表更靠前）" @click="moveNodeInGroup(gv, idx, -1)">←</n-button>
                    <n-button size="tiny" :disabled="idx === gv.nodes.length - 1 || reordering" title="后移" @click="moveNodeInGroup(gv, idx, 1)">→</n-button>
                    <n-button size="tiny" @click="openNode(r)">编辑</n-button>
                    <n-button size="tiny" type="error" @click="handleDeleteNode(r.id)">删除</n-button>
                  </div>
                </div>
              </div>
              <n-empty v-else description="该分组暂无节点" size="small" style="padding:12px 0;">
                <template #extra><n-button size="tiny" @click="openNodeInGroup(gv.group)">添加节点</n-button></template>
              </n-empty>
            </n-card>
          </div>
          <n-empty v-else-if="!loading" description="暂无分组或节点" style="padding:40px 0;" />
        </n-spin>
      </n-tab-pane>

      <!-- 链路拓扑：按节点分组展示每个节点的完整出网路径 -->
      <n-tab-pane name="topology" tab="链路拓扑">
        <n-spin :show="loading">
          <div v-if="nodes.length" class="topo">
            <div class="topo-legend">
              <span><i class="dot client"></i>客户端</span>
              <span><i class="dot entry"></i>节点入口</span>
              <span><i class="dot landing"></i>落地入站</span>
              <span><i class="dot egress"></i>代理出口</span>
              <span><i class="dot inet"></i>互联网</span>
              <span style="flex:1;"></span>
              <n-button size="tiny" quaternary @click="showTopoIp = !showTopoIp">{{ showTopoIp ? '🙈 隐藏 IP' : '👁 显示 IP' }}</n-button>
            </div>
            <div v-for="gv in topoGroups" :key="gv.key" class="topo-machine">
              <div class="topo-mhead">
                <span class="machine-name">{{ gv.name }}</span>
                <n-tag size="tiny" :type="gv.isUngrouped ? 'default' : 'info'" bordered="false">{{ gv.rows.length }} 节点</n-tag>
              </div>
              <n-empty v-if="!gv.rows.length" description="该分组暂无节点" size="small" style="padding:10px 0;" />
              <div v-for="row in gv.rows" :key="row.node.id" class="topo-row" :class="{ off: row.off }">
                <span class="topo-node client">👤 客户端</span>
                <span class="topo-arrow">→</span>
                <span class="topo-node entry">
                  <b>{{ row.node.name || '—' }}</b>
                  <span class="topo-proto">{{ nodeProtocol(row.node) }}</span>
                  <template v-if="row.ib">
                    <span class="topo-port">:{{ row.ib.listen_port }}</span>
                    <span class="topo-loc">@ {{ serverName(row.ib.server_id) }}</span>
                  </template>
                </span>
                <span v-if="row.kind === 'external'" class="topo-arrow relay">⇢ 外部线路 ⇢</span>
                <span v-else-if="row.kind === 'broken'" class="topo-arrow relay warn">⇢ 入站已失效 ⇢</span>
                <template v-else>
                <template v-for="(seg, si) in row.segs" :key="si">
                  <template v-if="seg.kind === 'landing'">
                    <span class="topo-arrow relay">⇢ 中转 ⇢</span>
                    <span class="topo-node landing">
                      <b>{{ seg.ib.tag }}</b>
                      <span class="topo-proto">{{ (seg.ib.type || '').toUpperCase() }}</span>
                      <span class="topo-loc">@ {{ serverName(seg.ib.server_id) }}</span>
                      <span v-if="seg.off" class="topo-loc">· 已停用</span>
                    </span>
                  </template>
                  <template v-else-if="seg.kind === 'egress'">
                    <span class="topo-arrow egress">⇢ 代理出口 ⇢</span>
                    <span class="topo-node egress">
                      <b>{{ seg.eg.name }}</b>
                      <span class="topo-proto">{{ (seg.eg.type || '').toUpperCase() }}</span>
                      <span class="topo-loc">{{ showTopoIp ? seg.eg.host : maskHost(seg.eg.host) }}</span>
                    </span>
                  </template>
                  <span v-else-if="seg.kind === 'broken-landing'" class="topo-arrow relay warn">⇢ 落地已失效 ⇢</span>
                  <span v-else-if="seg.kind === 'broken-egress'" class="topo-arrow relay warn">⇢ 出口已失效 ⇢</span>
                  <span v-else-if="seg.kind === 'loop'" class="topo-arrow relay warn">⇢ 链路成环 ⇢</span>
                  <!-- 原本该走中转，落地被删后降级成本机直连；出口 IP 已经变了。 -->
                  <span v-else-if="seg.kind === 'downgraded'" class="topo-arrow relay warn"
                        title="原落地入站已被删除，此中转已降级为从本机直连出网；编辑并保存该入站可消除此提示">
                    ⇢ 原落地已删除 · 现本机直连 ⇢
                  </span>
                </template>
                </template>
                <span class="topo-arrow">→</span>
                <span class="topo-node inet">🌐 互联网</span>
                <span class="topo-actions">
                  <!-- 降级提示要么靠重新指定落地/出口来消除，要么在这里明确「就这样」。
                       不给出口就成了一条永远消不掉的红字，久了没人再看。 -->
                  <n-button v-if="row.ib?.upstream_broken" size="tiny" quaternary
                            :loading="acking === row.ib.id" @click="ackUpstream(row.ib)">已知晓</n-button>
                  <n-button size="tiny" quaternary @click="openNode(row.node)">编辑节点</n-button>
                </span>
              </div>
            </div>
            <p class="topo-tip">
              节点的中转 / 代理出口在「sing-box 配置」页的入站上配置，这里只做展示；外部节点的后续链路由对方线路决定，面板不可见。
            </p>
          </div>
          <n-empty v-else-if="!loading" description="暂无节点，无法展示链路" style="padding:40px 0;" />
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
                <span style="display:flex;gap:4px;flex-shrink:0;">
                  <!-- 停用的源后台不会去拉，节点数会一直停在上次的值。不标出来的话，
                       它和「拉到 0 个」长得一模一样。 -->
                  <n-tag v-if="!r.enabled" size="tiny" type="warning" bordered="false">已停用</n-tag>
                  <n-tag size="tiny" :type="r.last_error ? 'error' : 'default'" bordered="false">{{ r.last_count || 0 }} 节点</n-tag>
                </span>
              </div>
              <div class="lc-meta" style="word-break:break-all;"><span class="kv">{{ r.url }}</span></div>
              <div class="lc-meta">
                <span class="kv">上次拉取 <b>{{ r.last_fetched ? timeAgo(r.last_fetched) : '从未' }}</b></span>
                <span v-if="r.last_fetched" class="kv" style="color:var(--text-3);">{{ fmtDateTime(r.last_fetched) }}</span>
              </div>
              <!-- 后台每隔一段自动拉一次，失败时节点会被清成 0，订阅里也就没了。
                   错误只进了 last_error 和服务端日志，面板上必须说出来。 -->
              <div v-if="r.last_error" class="src-err" :title="r.last_error">拉取失败：{{ r.last_error }}</div>
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
            <n-input v-model:value="nodeForm.share_link" type="textarea" :rows="3" placeholder="vless://... 或订阅链接" />
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
  NSelect, NSwitch, NRadioGroup, NRadio, NTag, NSpin, NEmpty, NCard, useMessage
} from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { timeAgo, fmtDateTime } from '@/utils/format'

const message = useMessage()
const tab = ref('nodes')
const loading = ref(false)
const saving = ref(false)
const reordering = ref(false)

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
const servers = ref<any[]>([])
const egresses = ref<any[]>([])
// 出口列表是否真的取到了。取不到时 egressById 是空的，此时不能把「查不到出口」
// 当成「出口已失效」——那会把一次加载失败画成满屏红字。
const egressesLoaded = ref(true)

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

// 统一视图：每个分组一张卡片，卡片内是该组的节点；未归任何分组的节点放到「未分组」卡片。
// 一个节点可属于多个分组，会在每个所属分组的卡片里各出现一次。
const groupedView = computed(() => {
  const out: any[] = []
  const sorted = [...groups.value].sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0) || a.id - b.id)
  for (const g of sorted) {
    out.push({
      key: 'g' + g.id, name: g.name, description: g.description || '', group: g, isUngrouped: false,
      nodes: nodes.value.filter(n => (n.group_ids || []).includes(g.id)),
    })
  }
  const ungrouped = nodes.value.filter(n => !n.group_ids || n.group_ids.length === 0)
  if (ungrouped.length) {
    out.push({ key: 'ungrouped', name: '未分组', description: '', group: null, isUngrouped: true, nodes: ungrouped })
  }
  return out
})

// ========== 链路拓扑 ==========
// 自建节点绑定的是某个入站，出网路径（多级中转 / 代理出口）挂在入站上，
// 因此这里按节点分组把每个节点对应入站的链路展开，与 sing-box 配置页同一套画法。
const showTopoIp = ref(false) // 默认对 IP 打码，避免截图/分享泄露真实地址
const inboundById = computed(() => new Map<number, any>(inbounds.value.map(i => [i.id, i])))
const egressById = computed(() => new Map<number, any>(egresses.value.map(e => [e.id, e])))

function serverName(id: number) { if (!id) return '本机'; const s = servers.value.find(s => s.id === id); return s ? s.name : '#' + id }

// maskHost 对 IP/域名打码：IPv4 保留首尾段，域名/IPv6 保留首尾各两位。
function maskHost(h: string): string {
  if (!h) return h
  const v4 = h.match(/^(\d{1,3})\.\d{1,3}\.\d{1,3}\.(\d{1,3})$/)
  if (v4) return `${v4[1]}.***.***.${v4[2]}`
  if (!/[.:]/.test(h)) return h
  if (h.length <= 6) return '***'
  return h.slice(0, 2) + '***' + h.slice(-2)
}

// 节点绑定的入站（仅自建节点；tag 找不到说明入站已被删除/改名）。
function inboundOfNode(n: any): any | null {
  if (!n || n.type === 'external' || !n.inbound_tag) return null
  return inboundMap.value.get(n.inbound_tag) || null
}

// 从某入站沿 upstream 链走到头，返回拓扑段列表（多级中转 + 末端出口），防环。
function chainOf(ib: any) {
  const segs: any[] = []
  const seen = new Set<number>([ib.id])
  let cur = ib
  while (cur.upstream_inbound_id) {
    const next = inboundById.value.get(cur.upstream_inbound_id)
    // 成环和落地被删是两种完全不同的配置错误，分开报，不要都说成「落地已失效」。
    if (seen.has(next?.id)) { segs.push({ kind: 'loop' }); return segs }
    if (!next) { segs.push({ kind: 'broken-landing' }); return segs }
    seen.add(next.id)
    segs.push({ kind: 'landing', ib: next, off: !next.enabled })
    cur = next
  }
  // 落地被删时后端会清掉 upstream 并置 upstream_broken：链路看着是直连，
  // 实际是「本该走中转，现在从本机出网」，这行提示是它唯一的痕迹。
  if (cur.upstream_broken) segs.push({ kind: 'downgraded' })
  if (cur.egress_id) {
    const e = egressById.value.get(cur.egress_id)
    // 出口列表没加载出来时不要报「已失效」——那是个假告警。load() 会另外提示。
    if (e) segs.push({ kind: 'egress', eg: e })
    else if (egressesLoaded.value) segs.push({ kind: 'broken-egress' })
  }
  return segs
}

// 每个节点一行：kind 区分「自建且入站存在」「自建但入站失效」「外部节点」。
function topoRow(n: any) {
  if (n.type === 'external') return { node: n, ib: null, kind: 'external', segs: [] as any[] }
  const ib = inboundOfNode(n)
  if (!ib) return { node: n, ib: null, kind: 'broken', segs: [] as any[] }
  // 入站自己被停用时整行置灰：链路画得再完整，这条也是不通的。
  return { node: n, ib, kind: 'ok', off: !n.enabled || !ib.enabled, segs: chainOf(ib) }
}
const topoGroups = computed(() => groupedView.value.map(gv => ({
  key: gv.key, name: gv.name, isUngrouped: gv.isUngrouped, rows: gv.nodes.map(topoRow),
})))

// 节点卡片上的一行链路摘要：只在「有中转/出口」或「入站失效」时出现，
// 直连节点不加这行噪音。模板里要用三次（v-if / class / 文本），
// 所以按 node.id 预先算好，不要每次渲染都重走一遍链路。
const chainSummaries = computed(() => {
  const out = new Map<number, { text: string; broken: boolean }>()
  for (const gv of topoGroups.value) {
    for (const row of gv.rows) {
      const s = summarize(row)
      if (s) out.set(row.node.id, s)
    }
  }
  return out
})
function chainSummary(n: any) { return chainSummaries.value.get(n.id) || null }

// 节点落在哪台机器上——卡片上原先完全看不到这件事。链路摘要只在有中转/出口时才
// 出现，而直连节点（最常见的那种）因此一个字都不显示，要知道它在哪台机跑得先去
// sing-box 页翻入站。自建节点由入站的 server_id 决定；外部节点不在我们的机器上，
// 入站找不到时更无从谈起，两种情况都返回 null 由模板决定怎么说。
// 与 chainSummaries 同样预先算好：模板里要读多次，不该每次渲染都重查一遍入站。
const nodeServers = computed(() => {
  const out = new Map<number, string>()
  for (const n of nodes.value) {
    const ib = inboundOfNode(n)
    if (ib) out.set(n.id, serverName(ib.server_id))
  }
  return out
})
function nodeServer(n: any) { return nodeServers.value.get(n.id) || null }

// 确认「原落地已删除、现本机直连」这件事已知晓。只清提示，不动配置。
const acking = ref<number | null>(null)
async function ackUpstream(ib: any) {
  acking.value = ib.id
  try { await apiPost(`/api/admin/sb/inbounds/${ib.id}/ack-upstream`); await load() }
  catch (e: any) { message.error(e.message) }
  finally { acking.value = null }
}

function summarize(row: any): { text: string; broken: boolean } | null {
  if (row.kind === 'external') return null
  if (row.kind === 'broken') return { text: `入站「${row.node.inbound_tag || '未绑定'}」不存在`, broken: true }
  if (!row.segs.length) return null
  const parts: string[] = []
  let broken = false
  for (const seg of row.segs) {
    switch (seg.kind) {
      case 'landing': parts.push(`中转 ${seg.ib.tag} @ ${serverName(seg.ib.server_id)}${seg.off ? '（已停用）' : ''}`); break
      case 'egress': parts.push(`出口 ${seg.eg.name}`); break
      case 'downgraded': parts.push('原落地已删除 · 现本机直连'); broken = true; break
      case 'loop': parts.push('链路成环'); broken = true; break
      case 'broken-egress': parts.push('出口已失效'); broken = true; break
      default: parts.push('落地已失效'); broken = true
    }
  }
  return { text: parts.join(' ⇢ ') + ' ⇢ 互联网', broken }
}

// --- Nodes ---
const showNode = ref(false)
const editingNode = ref<any>(null)
// share_link must match store.Node's JSON tag — it was `link`, so the field was
// never sent: created external nodes had no link at all, and saving an existing
// one blanked its stored link.
const nodeForm = reactive({ name: '', type: 'self_built', inbound_tag: '', share_link: '', group_ids: [] as number[], enabled: true })
function openNode(n?: any) {
  editingNode.value = n || null
  if (n) {
    Object.assign(nodeForm, { name: n.name, type: n.type || 'self_built', inbound_tag: n.inbound_tag || '', share_link: n.share_link || '', group_ids: n.group_ids || [], enabled: n.enabled })
  } else {
    Object.assign(nodeForm, { name: '', type: 'self_built', inbound_tag: '', share_link: '', group_ids: [], enabled: true })
  }
  showNode.value = true
}
// 从分组卡片直接添加节点，预选该分组。
function openNodeInGroup(g: any | null) {
  openNode()
  if (g) nodeForm.group_ids = [g.id]
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

// 调整节点在分组内（及订阅/列表中）的顺序。节点按全局 sort_order 排，分组只是按
// 归属过滤，因此「组内前移/后移」= 把该节点与组内相邻节点在全局数组里对调位置，
// 再把完整 id 顺序提交后端。乐观更新，失败回滚重载。
async function moveNodeInGroup(gv: any, idx: number, dir: -1 | 1) {
  const target = idx + dir
  if (target < 0 || target >= gv.nodes.length || reordering.value) return
  const aId = gv.nodes[idx].id
  const bId = gv.nodes[target].id
  const arr = [...nodes.value]
  const gi = arr.findIndex(n => n.id === aId)
  const gj = arr.findIndex(n => n.id === bId)
  if (gi < 0 || gj < 0) return
  ;[arr[gi], arr[gj]] = [arr[gj], arr[gi]]
  nodes.value = arr
  reordering.value = true
  try {
    await apiPost('/api/admin/nodes/reorder', { ids: arr.map(n => n.id) })
  } catch (e: any) {
    message.error(e.message || '排序失败'); await load()
  } finally { reordering.value = false }
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
  // 服务器/出口只用于链路拓扑的展示，取不到时降级为空数组，不影响节点管理主流程。
  // 但降级要说出来：出口列表为空会让每条带出口的链路都显示成「出口已失效」，
  // 那是个假告警——拿不到就别画，并明确告诉管理员拓扑是不完整的。
  let degraded = ''
  try {
    const [n, g, s, i, sv, eg] = await Promise.all([
      apiList('/api/admin/nodes'), apiList('/api/admin/node-groups'),
      apiList('/api/admin/node-sources'), apiList('/api/admin/inbounds'),
      apiList('/api/admin/servers').catch(() => { degraded = '服务器列表'; return [] }),
      apiList('/api/admin/sb/egresses').catch(() => { degraded = degraded ? degraded + '、代理出口' : '代理出口'; return [] }),
    ])
    nodes.value = n; groups.value = g; sources.value = s; inbounds.value = i
    servers.value = sv; egresses.value = eg
    egressesLoaded.value = !degraded.includes('代理出口')
    if (degraded) message.warning(`${degraded}加载失败，链路拓扑显示不完整`)
  } catch {} finally { loading.value = false }
}
</script>

<style scoped>
/* 拉取失败要能一眼看到：这条源现在等于没有节点，用户订阅里也少了这一批 */
.src-err {
  font-size: 12px; color: var(--danger); background: var(--danger-soft, #fdf2f2);
  border-radius: 6px; padding: 6px 8px; line-height: 1.5;
  overflow: hidden; text-overflow: ellipsis; display: -webkit-box;
  -webkit-line-clamp: 2; -webkit-box-orient: vertical;
}
.page-title { font-size: 21px; margin-bottom: 16px; }
:deep(.n-drawer-content-body) { display: flex; flex-direction: column; }
.group-card { margin-bottom: 14px; }
.group-actions { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.group-desc { color: var(--text-2); font-size: 12px; margin-bottom: 10px; }

/* 节点卡片上的链路摘要 */
.lc-chain { font-size: 12px; color: var(--text-3, #999); line-height: 1.5; margin-top: 2px; word-break: break-word; }
.lc-chain.warn { color: #d03050; }

/* 链路拓扑（与 sing-box 配置页同一套画法） */
.topo { display: flex; flex-direction: column; gap: 18px; padding: 4px 2px; }
.topo-legend { display: flex; flex-wrap: wrap; gap: 14px; font-size: 12px; color: var(--text-3, #888); }
.topo-legend span { display: inline-flex; align-items: center; gap: 5px; }
.topo-legend .dot { width: 10px; height: 10px; border-radius: 3px; display: inline-block; }
.dot.client { background: #909399; }
.dot.entry { background: #2080f0; }
.dot.landing { background: #f0a020; }
.dot.egress { background: #7c3aed; }
.dot.inet { background: #18a058; }
.topo-machine { border: 1px solid var(--n-border-color, #e6e6ec); border-radius: 8px; padding: 12px 14px; }
.topo-mhead { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.topo-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding: 6px 0; }
.topo-row + .topo-row { border-top: 1px dashed var(--n-border-color, #ececf2); }
.topo-row.off { opacity: 0.42; }
.topo-node { display: inline-flex; align-items: baseline; gap: 6px; padding: 4px 10px; border-radius: 7px; font-size: 13px; border: 1px solid transparent; white-space: nowrap; }
.topo-node.client { background: rgba(144, 147, 153, 0.14); color: var(--text-2, #666); }
.topo-node.entry { background: rgba(32, 128, 240, 0.12); border-color: rgba(32, 128, 240, 0.35); }
.topo-node.landing { background: rgba(240, 160, 32, 0.13); border-color: rgba(240, 160, 32, 0.4); }
.topo-node.egress { background: rgba(124, 58, 237, 0.11); border-color: rgba(124, 58, 237, 0.38); }
.topo-node.inet { background: rgba(24, 160, 88, 0.12); color: #18a058; }
.topo-node b { font-weight: 650; }
.topo-proto { font-size: 11px; opacity: 0.75; }
.topo-port, .topo-loc { font-size: 11px; opacity: 0.6; }
.topo-arrow { color: var(--text-3, #aaa); font-size: 13px; user-select: none; }
.topo-arrow.relay { color: #f0a020; font-weight: 600; font-size: 12px; }
.topo-arrow.egress { color: #7c3aed; font-weight: 600; font-size: 12px; }
.topo-arrow.relay.warn { color: #d03050; }
.topo-actions { margin-left: auto; display: inline-flex; gap: 6px; }
.topo-tip { font-size: 12px; color: var(--text-3, #999); line-height: 1.6; margin: 0; }
.machine-name { font-weight: 650; font-size: 15px; color: var(--text); }
@media (max-width: 600px) { .topo-actions { margin-left: 0; } }
</style>
