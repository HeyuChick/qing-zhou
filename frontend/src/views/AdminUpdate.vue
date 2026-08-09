<template>
  <div>
    <h2 class="page-title">在线更新</h2>

    <n-spin :show="loading">
      <!-- 版本概览 -->
      <div class="ver-grid">
        <div class="ver-card">
          <div class="ver-label">当前版本</div>
          <div class="ver-value">{{ info?.current || '—' }}</div>
          <n-tag v-if="info?.dev" type="warning" size="tiny" bordered="false">开发构建</n-tag>
        </div>
        <div class="ver-arrow">→</div>
        <div class="ver-card" :class="{ hot: info?.update_available }">
          <div class="ver-label">最新版本</div>
          <div class="ver-value">{{ info?.latest || '—' }}</div>
          <n-tag v-if="info && info.update_available" type="success" size="tiny" bordered="false">可更新</n-tag>
          <n-tag v-else-if="info" type="default" size="tiny" bordered="false">已是最新</n-tag>
        </div>
      </div>

      <div class="toolbar">
        <n-button size="small" :loading="loading" @click="check">
          <template #icon><n-icon><RefreshOutline /></n-icon></template>
          检查更新
        </n-button>
        <a v-if="info?.url" :href="info.url" target="_blank" rel="noopener" class="release-link">
          在 GitHub 查看发布页 ↗
        </a>
        <span class="spacer" />
        <n-button
          v-if="info?.update_available"
          type="primary"
          :disabled="updating || !info.downloadable"
          :loading="updating"
          @click="confirmUpdate"
        >
          {{ updating ? '更新中…' : '立即更新' }}
        </n-button>
      </div>

      <n-alert v-if="info && info.update_available && !info.downloadable" type="warning" style="margin-top:8px;">
        最新发布未提供适配当前服务器架构的二进制（{{ info.asset_name }}），无法一键更新。请手动升级或补充对应架构的构建产物。
      </n-alert>

      <!-- 回滚：不联网，发布翻车时唯一还能走的路径 -->
      <div class="rb-box">
        <div class="rb-head">
          <span class="rb-title">回滚到上一个版本</span>
          <n-tag v-if="rollback?.available" type="success" size="tiny" bordered="false">可用</n-tag>
          <n-tag v-else type="default" size="tiny" bordered="false">不可用</n-tag>
        </div>
        <p class="rb-desc">
          <template v-if="rollback?.available">
            升级前的二进制还留在本机（<b>{{ rollback.version || '版本未知' }}</b>{{ rollback.saved_at ? '，保存于 ' + fmtDate(rollback.saved_at) : '' }}）。
            回滚<b>不需要联网</b>，也不重新下载 —— 新版本起不来、GitHub 连不上时，这是唯一还能走的路径。
            回滚后当前版本会成为新的回滚目标，所以点错了还能再点回来。
          </template>
          <template v-else>{{ rollback?.reason || '本机没有保留上一个版本。' }}</template>
        </p>
        <n-button size="small" :disabled="!rollback?.available || updating" @click="confirmRollback">
          {{ rollback?.version ? '回滚到 ' + rollback.version : '回滚到上一个版本' }}
        </n-button>
      </div>

      <!-- 指定版本安装（含降级） -->
      <div class="rel-box">
        <div class="rb-head">
          <span class="rb-title">安装指定版本</span>
          <n-button text size="tiny" :loading="relLoading" @click="loadReleases">
            {{ releases.length ? '刷新列表' : '加载版本列表' }}
          </n-button>
        </div>
        <p class="rb-desc">
          用于回到更早的版本 —— 出问题的版本已经不是最新、或本机没留上一个二进制时走这里。
          <b>降级不会回滚数据库</b>：库结构变更都是只增不减的，旧版本会忽略多出来的列，但跨大版本降级前
          请先到「系统设置 → 数据备份」下载一份快照。
        </p>
        <div v-if="releases.length" class="rel-pick">
          <n-select
            v-model:value="selectedTag"
            :options="releaseOptions"
            placeholder="选择版本"
            style="max-width:360px;"
          />
          <n-button
            type="primary"
            ghost
            :disabled="!selectedTag || updating || !selectedRelease?.downloadable"
            @click="confirmInstall"
          >安装此版本</n-button>
        </div>
        <n-alert v-if="selectedRelease && !selectedRelease.downloadable" type="warning" style="margin-top:8px;">
          该版本未提供适配当前架构的二进制（{{ info?.asset_name }}），无法一键安装。
        </n-alert>
        <n-alert v-else-if="selectedRelease?.relation === 'older'" type="warning" style="margin-top:8px;">
          这是一次<b>降级</b>：{{ info?.current }} → {{ selectedRelease.tag }}。请确认已了解上面关于数据库的说明。
        </n-alert>
        <n-alert v-else-if="selectedRelease?.prerelease" type="warning" style="margin-top:8px;">
          {{ selectedRelease.tag }} 是预发布版本，不建议用于生产。
        </n-alert>
      </div>

      <!-- 更新进度 -->
      <div v-if="updating || progress.status === 'failed'" class="progress-box">
        <div class="progress-head">
          <span class="phase">{{ phaseLabel }}</span>
          <span v-if="progress.target_version" class="target">→ {{ progress.target_version }}</span>
        </div>
        <n-progress
          type="line"
          :percentage="progress.percent || 0"
          :status="progress.status === 'failed' ? 'error' : 'default'"
          :processing="updating && progress.status !== 'failed'"
        />
        <div class="progress-msg" :class="{ err: progress.status === 'failed' }">{{ progress.message }}</div>
      </div>

      <!-- 变更日志 -->
      <div v-if="info?.notes" class="changelog">
        <div class="cl-head">
          <span class="cl-title">{{ info.name || ('版本 ' + info.latest) }}</span>
          <span v-if="publishedText" class="cl-date">{{ publishedText }}</span>
        </div>
        <div class="cl-body md" v-html="notesHtml"></div>
      </div>
    </n-spin>

    <n-alert type="info" style="margin-top:16px;">
      更新流程：从 GitHub Releases 下载对应架构二进制 → 校验 SHA-256 → 原子替换 → 进程自动重启。
      重启期间面板会短暂（约 1~2 秒）不可访问，完成后本页会自动刷新到新版本。
    </n-alert>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { NSpin, NButton, NIcon, NTag, NAlert, NProgress, NSelect, useMessage, useDialog } from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import { apiGet, apiPost } from '@/api'
import { mdToHtml } from '@/utils/markdown'
import { fmtDate } from '@/utils/format'

interface ReleaseInfo {
  tag: string
  name: string
  published_at: string
  prerelease: boolean
  downloadable: boolean
  asset_size: number
  relation: 'current' | 'newer' | 'older' | 'unknown'
}

interface RollbackState {
  available: boolean
  version: string
  reason?: string
  size?: number
  saved_at?: number
}

interface UpdateInfo {
  current: string
  latest: string
  name: string
  notes: string
  url: string
  published_at: string
  update_available: boolean
  downloadable: boolean
  asset_name: string
  asset_size: number
  dev: boolean
}

const message = useMessage()
const dialog = useDialog()

const loading = ref(false)
const updating = ref(false)
const info = ref<UpdateInfo | null>(null)

const progress = reactive({
  status: 'idle' as string,
  message: '',
  percent: 0,
  target_version: '',
  current: '',
})

let pollTimer: number | undefined
let pollStart = 0

const notesHtml = computed(() => (info.value?.notes ? mdToHtml(info.value.notes) : ''))
const publishedText = computed(() => {
  const s = info.value?.published_at
  if (!s) return ''
  const t = Math.floor(new Date(s).getTime() / 1000)
  return Number.isFinite(t) ? fmtDate(t) : ''
})

const phaseLabels: Record<string, string> = {
  idle: '空闲',
  downloading: '下载中',
  verifying: '校验中',
  installing: '安装中',
  restarting: '重启中',
  failed: '更新失败',
}
const phaseLabel = computed(() => phaseLabels[progress.status] || progress.status)

const rollback = ref<RollbackState | null>(null)
const releases = ref<ReleaseInfo[]>([])
const relLoading = ref(false)
const selectedTag = ref<string | null>(null)

const selectedRelease = computed(() =>
  releases.value.find(r => r.tag === selectedTag.value) || null)

const relationLabel: Record<string, string> = {
  current: '当前', newer: '更新', older: '更旧', unknown: '',
}
const releaseOptions = computed(() => releases.value.map(r => {
  const bits = [r.tag]
  if (relationLabel[r.relation]) bits.push(relationLabel[r.relation])
  if (r.prerelease) bits.push('预发布')
  if (!r.downloadable) bits.push('无本架构产物')
  return {
    label: bits.join(' · '),
    value: r.tag,
    // 当前版本没有「安装」的意义，禁掉避免误触发一次无谓的重启。
    disabled: r.relation === 'current',
  }
}))

async function check() {
  loading.value = true
  try {
    info.value = await apiGet<UpdateInfo>('/api/admin/update/check')
  } catch (e: any) {
    message.error(e?.message || '检查更新失败')
  } finally {
    loading.value = false
  }
}

// 回滚状态是纯本地判断（看 .prev 在不在），GitHub 挂了也要能拿到——所以独立
// 于 check() 请求，不能因为检查更新失败就连回滚按钮都点不了。
async function loadRollback() {
  try {
    rollback.value = await apiGet<RollbackState>('/api/admin/update/rollback')
  } catch { /* 非致命：按钮保持不可用 */ }
}

async function loadReleases() {
  relLoading.value = true
  try {
    const data = await apiGet<{ current: string; releases: ReleaseInfo[] }>('/api/admin/update/releases')
    releases.value = data?.releases || []
    if (!releases.value.length) message.warning('该仓库没有可用的发布版本')
  } catch (e: any) {
    message.error(e?.message || '获取版本列表失败')
  } finally {
    relLoading.value = false
  }
}

function confirmRollback() {
  const v = rollback.value?.version || '上一个版本'
  dialog.warning({
    title: '确认回滚',
    content: `将把面板换回 ${v} 并重启服务。不会下载任何东西，也不会改动数据库。继续？`,
    positiveText: '回滚',
    negativeText: '取消',
    onPositiveClick: () => { startRollback() },
  })
}

async function startRollback() {
  try {
    await apiPost('/api/admin/update/rollback')
  } catch (e: any) {
    message.error(e?.message || '无法启动回滚')
    return
  }
  beginProgress('正在回滚…', rollback.value?.version || '', 'verifying')
}

function confirmInstall() {
  const r = selectedRelease.value
  if (!r) return
  const downgrade = r.relation === 'older'
  dialog.warning({
    title: downgrade ? '确认降级' : '确认安装',
    content: downgrade
      ? `将从 ${info.value?.current} 降级到 ${r.tag}。数据库不会回滚，建议先下载一份备份。继续？`
      : `将安装 ${r.tag} 并重启服务，确定继续？`,
    positiveText: downgrade ? '仍然降级' : '开始安装',
    negativeText: '取消',
    onPositiveClick: () => { startUpdate(r.tag) },
  })
}

function confirmUpdate() {
  if (!info.value) return
  dialog.warning({
    title: '确认更新',
    content: `将从 ${info.value.current || '当前版本'} 更新到 ${info.value.latest}。更新过程中服务会短暂重启，确定继续？`,
    positiveText: '开始更新',
    negativeText: '取消',
    onPositiveClick: () => { startUpdate() },
  })
}

// tag 为空 = 装最新版（保持原行为）；给了 tag 就装指定版本，可以比当前旧。
async function startUpdate(tag?: string) {
  try {
    await apiPost('/api/admin/update/apply', tag ? { version: tag } : undefined)
  } catch (e: any) {
    message.error(e?.message || '无法启动更新')
    return
  }
  beginProgress('准备下载…', tag || '')
}

// phase 是初始阶段：升级从「下载中」起步，回滚不下载任何东西，直接是「安装中」。
function beginProgress(msg: string, target: string, phase = 'downloading') {
  updating.value = true
  progress.status = phase
  progress.percent = 0
  progress.message = msg
  progress.target_version = target
  pollStart = Date.now()
  poll()
}

async function poll() {
  try {
    const st = await apiGet<any>('/api/admin/update/status')
    // apply() 会在返回前把状态置为非 idle，因此更新期间再见到 idle
    // 说明进程已完成 re-exec 并以新版本重启 —— 视为成功。
    if (updating.value && st.status === 'idle') {
      onSuccess(st.current)
      return
    }
    progress.status = st.status
    progress.message = st.message || phaseLabel.value
    progress.percent = st.percent || 0
    progress.target_version = st.target_version || progress.target_version
    if (st.status === 'failed') {
      updating.value = false
      message.error(st.message || '更新失败')
      return
    }
  } catch {
    // 重启期间连接会中断，属正常，继续轮询等待服务回来。
    if (progress.status !== 'restarting') {
      progress.message = '服务重启中，等待恢复…'
    }
  }
  if (Date.now() - pollStart > 180_000) {
    updating.value = false
    message.warning('更新耗时过长，请手动刷新页面确认结果')
    return
  }
  pollTimer = window.setTimeout(poll, 1500)
}

function onSuccess(newVer: string) {
  updating.value = false
  progress.status = 'idle'
  progress.percent = 100
  message.success(`已更新到 ${newVer || '最新版本'}，即将刷新页面`)
  window.setTimeout(() => window.location.reload(), 1500)
}

onMounted(() => { check(); loadRollback() })
onUnmounted(() => { if (pollTimer) window.clearTimeout(pollTimer) })
</script>

<style scoped>
.page-title { font-size: 20px; font-weight: 700; margin: 0 0 16px; }

.ver-grid {
  display: flex; align-items: stretch; gap: 12px; flex-wrap: wrap;
}
.ver-card {
  flex: 1; min-width: 160px;
  background: #fff; border: 1px solid var(--border); border-radius: 12px;
  padding: 16px 18px; display: flex; flex-direction: column; gap: 6px;
}
.ver-card.hot { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(0,0,0,.08); }
.ver-label { font-size: 12px; color: var(--text-3); }
.ver-value { font-size: 22px; font-weight: 750; letter-spacing: -0.02em; }
.ver-arrow { display: grid; place-items: center; color: var(--text-3); font-size: 20px; }

.toolbar { display: flex; align-items: center; gap: 12px; margin-top: 16px; }
.toolbar .spacer { flex: 1; }
.release-link { font-size: 13px; color: var(--accent-strong); text-decoration: none; }
.release-link:hover { text-decoration: underline; }

.progress-box {
  margin-top: 16px; padding: 14px 16px;
  background: var(--bg-soft, #faf9f5); border: 1px solid var(--border); border-radius: 12px;
}
.progress-head { display: flex; align-items: baseline; gap: 8px; margin-bottom: 8px; }
.progress-head .phase { font-weight: 650; }
.progress-head .target { font-size: 12px; color: var(--text-3); }
.progress-msg { margin-top: 8px; font-size: 12px; color: var(--text-2); }
.progress-msg.err { color: #d03050; }

.rb-box, .rel-box {
  margin-top: 16px; padding: 14px 16px;
  background: #fff; border: 1px solid var(--border); border-radius: 12px;
}
.rb-head { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.rb-title { font-weight: 650; }
.rb-desc { margin: 0 0 10px; font-size: 12px; line-height: 1.75; color: var(--text-3); }
.rel-pick { display: flex; gap: 10px; flex-wrap: wrap; align-items: center; }

.changelog {
  margin-top: 20px; background: #fff; border: 1px solid var(--border); border-radius: 12px;
  overflow: hidden;
}
.cl-head {
  display: flex; align-items: baseline; justify-content: space-between; gap: 10px;
  padding: 14px 18px; border-bottom: 1px solid var(--border); background: var(--bg-soft);
}
.cl-title { font-weight: 700; }
.cl-date { font-size: 12px; color: var(--text-3); }
.cl-body { padding: 8px 18px 18px; }
</style>
