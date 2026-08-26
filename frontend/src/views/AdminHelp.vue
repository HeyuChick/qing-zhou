<template>
  <main class="help-admin-page">
    <header class="page-head help-page-head">
      <div>
        <h2 class="page-title">帮助文档管理</h2>
        <p class="page-sub">编写与发布帮助内容，管理用户端的展示顺序</p>
      </div>
      <div class="page-actions">
        <n-button type="primary" size="large" @click="openCreate">
          <template #icon><n-icon><AddOutline /></n-icon></template>
          新建文档
        </n-button>
      </div>
    </header>

    <section class="resource-overview help-overview" aria-label="文档状态概览">
      <button
        v-for="metric in metrics"
        :key="metric.value"
        class="resource-metric metric-card"
        :class="[metric.tone, { active: statusFilter === metric.value }]"
        type="button"
        :aria-pressed="statusFilter === metric.value"
        @click="statusFilter = metric.value"
      >
        <span class="metric-icon"><n-icon><component :is="metric.icon" /></n-icon></span>
        <span class="metric-copy">
          <b>{{ metric.count }}</b>
          <span>{{ metric.label }}</span>
          <small>{{ metric.hint }}</small>
        </span>
      </button>
    </section>

    <section class="document-panel">
      <div class="document-toolbar">
        <div class="toolbar-copy">
          <h3>文档列表</h3>
          <span aria-live="polite">{{ filteredDocs.length }} 篇结果</span>
        </div>
        <div class="toolbar-controls">
          <n-input
            v-model:value="searchQuery"
            clearable
            class="search-input"
            placeholder="搜索文档标题或内容"
            aria-label="搜索帮助文档"
          >
            <template #prefix><n-icon><SearchOutline /></n-icon></template>
          </n-input>
          <div class="status-switch" aria-label="按发布状态筛选">
            <button
              v-for="option in statusOptions"
              :key="option.value"
              type="button"
              :class="{ active: statusFilter === option.value }"
              @click="statusFilter = option.value"
            >
              {{ option.label }}
            </button>
          </div>
        </div>
      </div>

      <n-spin :show="loading">
        <div v-if="filteredDocs.length" class="document-list">
          <article v-for="d in filteredDocs" :key="d.id" class="document-row">
            <div class="order-badge" :title="`展示顺序 ${d.sort_order ?? 0}`">
              <span>顺序</span>
              <strong>{{ d.sort_order ?? 0 }}</strong>
            </div>

            <div class="document-content">
              <div class="document-title-line">
                <h4>{{ d.title || '未命名文档' }}</h4>
                <n-tag :type="d.published ? 'success' : 'default'" size="small" :bordered="false" round>
                  <span class="status-dot" />{{ d.published ? '已发布' : '草稿' }}
                </n-tag>
              </div>
              <p>{{ excerpt(d.content) }}</p>
              <div class="document-meta">
                <span><n-icon><TimeOutline /></n-icon>更新于 {{ fmtDate(d.updated_at) }}</span>
                <span><n-icon><ReaderOutline /></n-icon>{{ contentLength(d.content) }} 字</span>
              </div>
            </div>

            <div class="document-actions">
              <n-button secondary type="primary" @click="openEdit(d)">
                <template #icon><n-icon><CreateOutline /></n-icon></template>
                编辑
              </n-button>
              <n-button
                quaternary
                type="error"
                :loading="deletingId === d.id"
                aria-label="删除文档"
                @click="confirmDelete(d)"
              >
                <template #icon><n-icon><TrashOutline /></n-icon></template>
                删除
              </n-button>
            </div>
          </article>
        </div>

        <div v-else-if="!loading" class="document-empty">
          <n-empty :description="emptyDescription">
            <template #extra>
              <n-button v-if="searchQuery || statusFilter !== 'all'" @click="resetFilters">清除筛选</n-button>
              <n-button v-else type="primary" @click="openCreate">创建第一篇文档</n-button>
            </template>
          </n-empty>
        </div>
      </n-spin>
    </section>

    <n-modal
      v-model:show="showForm"
      preset="card"
      class="help-editor-modal"
      :title="editing ? '编辑文档' : '新建文档'"
      :mask-closable="false"
      style="width:min(1120px, calc(100vw - 32px));"
    >
      <div class="editor-heading">
        <div>
          <strong>{{ editing ? '更新帮助内容' : '创建帮助内容' }}</strong>
          <p>支持 Markdown 语法，可在右侧实时检查用户端效果。</p>
        </div>
        <n-tag :type="form.published ? 'success' : 'default'" :bordered="false" round>
          {{ form.published ? '发布后用户可见' : '仅管理员可见' }}
        </n-tag>
      </div>

      <n-form label-placement="top">
        <div class="document-settings">
          <n-form-item label="文档标题" required>
            <n-input v-model:value="form.title" maxlength="80" show-count placeholder="例如：Windows 客户端使用指南" />
          </n-form-item>
          <n-form-item label="展示顺序">
            <n-input-number v-model:value="form.sort_order" :min="0" :precision="0" style="width:100%;" />
          </n-form-item>
          <n-form-item label="发布状态">
            <div class="publish-control">
              <div>
                <strong>{{ form.published ? '已开启发布' : '保存为草稿' }}</strong>
                <span>{{ form.published ? '保存后将展示给用户' : '完成编辑后再发布' }}</span>
              </div>
              <n-switch v-model:value="form.published" />
            </div>
          </n-form-item>
        </div>
      </n-form>

      <div class="editor-label-row">
        <label for="help-markdown-editor">正文内容</label>
        <span>{{ contentLength(form.content) }} 字</span>
      </div>

      <div class="md-toolbar">
        <div class="format-actions" aria-label="Markdown 格式工具">
          <button v-for="btn in toolbar" :key="btn.label" type="button" @click="insertMd(btn)" :title="btn.label">{{ btn.icon }}</button>
        </div>
        <div class="editor-modes" aria-label="编辑器视图">
          <button type="button" @click="editorMode='write'" :class="{on:editorMode==='write'}">编辑</button>
          <button type="button" @click="editorMode='split'" :class="{on:editorMode==='split'}">分栏</button>
          <button type="button" @click="editorMode='preview'" :class="{on:editorMode==='preview'}">预览</button>
        </div>
      </div>

      <div class="editor-body" :class="editorMode">
        <textarea
          v-if="editorMode !== 'preview'"
          id="help-markdown-editor"
          ref="taRef"
          v-model="form.content"
          class="editor-ta"
          @keydown="handleTab"
          placeholder="从这里开始编写帮助内容…"
        />
        <div v-if="editorMode !== 'write'" class="editor-pv md" v-html="mdToHtml(form.content)" />
      </div>

      <div class="editor-footer">
        <span>保存后将同步更新用户端帮助中心</span>
        <div>
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="handleSave">
            {{ editing ? '保存更改' : form.published ? '创建并发布' : '保存草稿' }}
          </n-button>
        </div>
      </div>
    </n-modal>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, nextTick } from 'vue'
import {
  NSpin, NButton, NModal, NForm, NFormItem, NInput, NInputNumber,
  NSwitch, NTag, NEmpty, NIcon, useMessage, useDialog,
} from 'naive-ui'
import {
  AddOutline, CheckmarkCircleOutline, CreateOutline, DocumentsOutline,
  ReaderOutline, SearchOutline, TimeOutline, TrashOutline,
} from '@vicons/ionicons5'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtDate } from '@/utils/format'
import { mdToHtml } from '@/utils/markdown'

type StatusFilter = 'all' | 'published' | 'draft'

const message = useMessage()
const dialog = useDialog()
const docs = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const deletingId = ref<number | null>(null)
const showForm = ref(false)
const editing = ref<any>(null)
const searchQuery = ref('')
const statusFilter = ref<StatusFilter>('all')
const form = reactive({ title: '', content: '', sort_order: 0, published: true })
const editorMode = ref<'write' | 'split' | 'preview'>('split')
const taRef = ref<HTMLTextAreaElement | null>(null)

const statusOptions: { label: string; value: StatusFilter }[] = [
  { label: '全部', value: 'all' },
  { label: '已发布', value: 'published' },
  { label: '草稿', value: 'draft' },
]

const publishedCount = computed(() => docs.value.filter(d => d.published).length)
const draftCount = computed(() => docs.value.length - publishedCount.value)
const metrics = computed(() => [
  { label: '全部文档', hint: '帮助中心内容总数', value: 'all' as StatusFilter, count: docs.value.length, icon: DocumentsOutline, tone: '' },
  { label: '已发布', hint: '当前用户端可见', value: 'published' as StatusFilter, count: publishedCount.value, icon: CheckmarkCircleOutline, tone: 'success' },
  { label: '草稿', hint: '等待完善或发布', value: 'draft' as StatusFilter, count: draftCount.value, icon: CreateOutline, tone: 'draft' },
])

const filteredDocs = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase()
  return docs.value
    .filter((doc) => {
      if (statusFilter.value === 'published' && !doc.published) return false
      if (statusFilter.value === 'draft' && doc.published) return false
      if (!query) return true
      return `${doc.title || ''} ${doc.content || ''}`.toLocaleLowerCase().includes(query)
    })
    .slice()
    .sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || (b.updated_at ?? 0) - (a.updated_at ?? 0))
})

const emptyDescription = computed(() => {
  if (searchQuery.value) return `没有找到与“${searchQuery.value}”相关的文档`
  if (statusFilter.value === 'published') return '暂无已发布文档'
  if (statusFilter.value === 'draft') return '暂无草稿文档'
  return '还没有帮助文档'
})

const toolbar = [
  { label: '标题', icon: 'H', insert: '## ' },
  { label: '粗体', icon: 'B', before: '**', after: '**' },
  { label: '斜体', icon: 'I', before: '*', after: '*' },
  { label: '链接', icon: '🔗', before: '[', after: '](url)' },
  { label: '行内代码', icon: '`', before: '`', after: '`' },
  { label: '代码块', icon: '```', insert: '```\n\n```' },
  { label: '无序列表', icon: '•', insert: '- ' },
  { label: '有序列表', icon: '1.', insert: '1. ' },
  { label: '引用', icon: '❝', insert: '> ' },
  { label: '表格', icon: '⊞', insert: '| 列1 | 列2 |\n|------|------|\n| 内容 | 内容 |' },
  { label: '分割线', icon: '—', insert: '\n---\n' },
]

function contentLength(content?: string) { return (content || '').trim().length }

function excerpt(content?: string) {
  const plain = (content || '')
    .replace(/```[\s\S]*?```/g, ' 代码示例 ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[#>*_`|~-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  return plain || '暂无正文内容，可进入编辑器补充。'
}

function resetFilters() { searchQuery.value = ''; statusFilter.value = 'all' }

function insertMd(btn: any) {
  const ta = taRef.value
  if (!ta) { form.content += btn.insert || ''; return }
  const start = ta.selectionStart
  const end = ta.selectionEnd
  const selected = form.content.slice(start, end)
  if (btn.insert) {
    form.content = form.content.slice(0, start) + btn.insert + form.content.slice(end)
    nextTick(() => { ta.selectionStart = ta.selectionEnd = start + btn.insert.length; ta.focus() })
  } else if (btn.before) {
    const text = btn.before + (selected || '文本') + btn.after
    form.content = form.content.slice(0, start) + text + form.content.slice(end)
    nextTick(() => {
      ta.selectionStart = start + btn.before.length
      ta.selectionEnd = start + btn.before.length + (selected || '文本').length
      ta.focus()
    })
  }
}

function handleTab(event: KeyboardEvent) {
  if (event.key === 'Tab') {
    event.preventDefault()
    const ta = taRef.value!
    const start = ta.selectionStart
    const end = ta.selectionEnd
    form.content = form.content.slice(0, start) + '  ' + form.content.slice(end)
    nextTick(() => { ta.selectionStart = ta.selectionEnd = start + 2 })
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, { title: '', content: '', sort_order: 0, published: true })
  editorMode.value = 'split'
  showForm.value = true
}

function openEdit(doc: any) {
  editing.value = doc
  Object.assign(form, {
    title: doc.title || '', content: doc.content || '',
    sort_order: doc.sort_order ?? 0, published: Boolean(doc.published),
  })
  editorMode.value = 'split'
  showForm.value = true
}

async function handleSave() {
  if (!form.title.trim()) { message.warning('请填写文档标题'); return }
  saving.value = true
  try {
    const payload = { ...form, title: form.title.trim() }
    if (editing.value) await apiPut(`/api/admin/help/${editing.value.id}`, payload)
    else await apiPost('/api/admin/help', payload)
    message.success(editing.value ? '文档已更新' : form.published ? '文档已创建并发布' : '草稿已保存')
    showForm.value = false
    await load()
  } catch (error: any) {
    message.error(error.message)
  } finally { saving.value = false }
}

function confirmDelete(doc: any) {
  dialog.warning({
    title: '删除文档？',
    content: `“${doc.title || '未命名文档'}”删除后无法恢复。`,
    positiveText: '确认删除', negativeText: '取消',
    onPositiveClick: () => handleDelete(doc.id),
  })
}

async function handleDelete(id: number) {
  deletingId.value = id
  try {
    await apiDelete(`/api/admin/help/${id}`)
    message.success('文档已删除')
    await load()
  } catch (error: any) {
    message.error(error.message)
    throw error
  } finally { deletingId.value = null }
}

async function load() {
  loading.value = true
  try { docs.value = await apiList('/api/admin/help') }
  catch (error: any) { message.error(error.message || '文档加载失败') }
  finally { loading.value = false }
}

onMounted(load)
</script>

<style scoped>
.help-admin-page { max-width: 1320px; margin: 0 auto; }
.help-page-head { align-items: center; margin-bottom: 20px; }
.help-page-head .page-sub { max-width: 620px; }
.help-overview { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 20px; }
.metric-card { position: relative; display: flex; align-items: center; gap: 13px; padding: 15px 16px; border-color: transparent; overflow: hidden; }
.metric-card::after { content: ''; position: absolute; inset: auto 0 0; height: 3px; background: transparent; transition: background-color .2s ease; }
.metric-card.active { border-color: color-mix(in srgb, var(--accent) 34%, var(--border)); background: color-mix(in srgb, var(--accent) 4%, var(--card)); }
.metric-card.active::after { background: var(--accent); }
.metric-icon { display: grid !important; place-items: center; flex: 0 0 40px; width: 40px; height: 40px; margin: 0 !important; border-radius: 11px; color: var(--accent) !important; background: color-mix(in srgb, var(--accent) 10%, transparent); font-size: 20px !important; }
.metric-card.success .metric-icon { color: var(--success) !important; background: color-mix(in srgb, var(--success) 11%, transparent); }
.metric-card.draft .metric-icon { color: var(--text-2) !important; background: var(--bg-soft); }
.metric-copy { min-width: 0; text-align: left; }
.metric-copy b { font-size: 22px; }
.metric-copy > span { margin-top: 1px; color: var(--text); font-size: 12.5px; font-weight: 600; }
.metric-copy small { display: block; margin-top: 2px; color: var(--text-3); font-size: 11px; font-weight: 400; }

.document-panel { border: 1px solid var(--border); border-radius: 16px; background: var(--card); box-shadow: var(--shadow-sm); overflow: hidden; }
.document-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 16px 18px; border-bottom: 1px solid var(--border); background: color-mix(in srgb, var(--bg-soft) 64%, var(--card)); }
.toolbar-copy { display: flex; align-items: baseline; gap: 10px; white-space: nowrap; }
.toolbar-copy h3 { margin: 0; color: var(--text); font-size: 15px; font-weight: 650; }
.toolbar-copy span { color: var(--text-3); font-size: 12px; }
.toolbar-controls { display: flex; align-items: center; justify-content: flex-end; gap: 10px; width: min(100%, 560px); }
.search-input { width: min(300px, 100%); }
.status-switch { display: inline-flex; padding: 3px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg-soft); }
.status-switch button { height: 28px; padding: 0 11px; border: 0; border-radius: 6px; background: transparent; color: var(--text-2); font-family: inherit; font-size: 12px; font-weight: 500; line-height: 1; white-space: nowrap; cursor: pointer; }
.status-switch button:hover { color: var(--text); }
.status-switch button.active { background: var(--card); color: var(--accent); box-shadow: 0 1px 3px rgba(28,48,70,.12); font-weight: 650; }

.document-list { min-height: 120px; }
.document-row { display: grid; grid-template-columns: 58px minmax(0, 1fr) auto; align-items: center; gap: 16px; padding: 17px 18px; border-bottom: 1px solid var(--border); transition: background-color .18s ease; }
.document-row:last-child { border-bottom: 0; }
.document-row:hover { background: color-mix(in srgb, var(--bg-soft) 72%, transparent); }
.order-badge { display: flex; flex-direction: column; align-items: center; justify-content: center; width: 50px; height: 50px; border: 1px solid var(--border); border-radius: 11px; background: var(--bg-soft); color: var(--text-3); }
.order-badge span { font-size: 9px; letter-spacing: .08em; }
.order-badge strong { margin-top: 1px; color: var(--text); font-size: 15px; font-variant-numeric: tabular-nums; }
.document-content { min-width: 0; }
.document-title-line { display: flex; align-items: center; gap: 9px; }
.document-title-line h4 { min-width: 0; overflow: hidden; margin: 0; color: var(--text); font-size: 14.5px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.status-dot { display: inline-block; width: 5px; height: 5px; margin: 0 5px 1px 0; border-radius: 50%; background: currentColor; }
.document-content > p { overflow: hidden; margin: 5px 0 7px; color: var(--text-2); font-size: 12.5px; line-height: 1.5; text-overflow: ellipsis; white-space: nowrap; }
.document-meta { display: flex; flex-wrap: wrap; gap: 13px; color: var(--text-3); font-size: 11.5px; }
.document-meta span { display: inline-flex; align-items: center; gap: 4px; }
.document-meta .n-icon { font-size: 13px; }
.document-actions { display: flex; align-items: center; gap: 4px; }
.document-empty { display: grid; place-items: center; min-height: 280px; padding: 30px; }

.editor-heading { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin: -4px 0 18px; padding: 13px 15px; border: 1px solid var(--border); border-radius: 11px; background: var(--bg-soft); }
.editor-heading strong { color: var(--text); font-size: 13px; }
.editor-heading p { margin: 3px 0 0; color: var(--text-3); font-size: 11.5px; }
.document-settings { display: grid; grid-template-columns: minmax(0, 1fr) 130px 220px; gap: 14px; }
.publish-control { display: flex; align-items: center; justify-content: space-between; gap: 12px; box-sizing: border-box; width: 100%; min-height: 34px; padding: 5px 10px; border: 1px solid var(--border); border-radius: 8px; }
.publish-control div { display: flex; flex-direction: column; min-width: 0; }
.publish-control strong { color: var(--text); font-size: 11.5px; }
.publish-control span { color: var(--text-3); font-size: 10px; white-space: nowrap; }
.editor-label-row { display: flex; align-items: center; justify-content: space-between; margin: 0 1px 7px; }
.editor-label-row label { color: var(--text); font-size: 13px; font-weight: 600; }
.editor-label-row span { color: var(--text-3); font-size: 11px; font-variant-numeric: tabular-nums; }

.md-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 6px; border: 1px solid var(--border); border-bottom: none; border-radius: 10px 10px 0 0; background: var(--bg-soft); }
.format-actions, .editor-modes { display: flex; align-items: center; gap: 3px; }
.md-toolbar button { min-width: 29px; height: 28px; padding: 0 7px; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--text-2); font-family: inherit; font-size: 12px; font-weight: 600; line-height: 1; cursor: pointer; }
.md-toolbar button:hover { border-color: var(--border); background: var(--card); color: var(--text); }
.editor-modes { padding-left: 7px; border-left: 1px solid var(--border); }
.editor-modes button { min-width: 42px; font-weight: 500; }
.md-toolbar button.on { border-color: color-mix(in srgb, var(--accent) 24%, transparent); background: color-mix(in srgb, var(--accent) 10%, transparent); color: var(--accent); font-weight: 650; }
.editor-body { display: flex; min-height: 390px; border: 1px solid var(--border); border-radius: 0 0 10px 10px; overflow: hidden; }
.editor-body.write .editor-ta, .editor-body.preview .editor-pv { flex: 1; }
.editor-body.split .editor-ta, .editor-body.split .editor-pv { flex: 1 1 50%; width: 50%; }
.editor-body.split .editor-ta { border-right: 1px solid var(--border); }
.editor-ta { width: 100%; min-height: 390px; padding: 17px; border: none; outline: none; resize: none; background: var(--card); color: var(--text); font: 13px/1.7 'SF Mono', ui-monospace, Menlo, Consolas, monospace; }
.editor-ta::placeholder { color: var(--text-3); }
.editor-pv { min-height: 390px; max-height: 52vh; overflow: auto; padding: 18px 20px; background: color-mix(in srgb, var(--bg-soft) 65%, var(--card)); }
.editor-pv:empty::before { content: '预览内容将显示在这里'; color: var(--text-3); font-size: 13px; }
.editor-footer { display: flex; align-items: center; justify-content: space-between; gap: 18px; margin-top: 14px; }
.editor-footer > span { color: var(--text-3); font-size: 11.5px; }
.editor-footer > div { display: flex; gap: 8px; }

@media (max-width: 780px) {
  .help-overview { grid-template-columns: repeat(3, 1fr); }
  .metric-card { justify-content: center; padding: 12px 8px; }
  .metric-icon, .metric-copy small { display: none !important; }
  .metric-copy { text-align: center; }
  .metric-copy b { font-size: 19px; }
  .document-toolbar { align-items: stretch; flex-direction: column; gap: 12px; }
  .toolbar-controls { align-items: stretch; flex-direction: column; width: 100%; }
  .search-input { width: 100%; }
  .status-switch { align-self: flex-start; }
  .document-row { grid-template-columns: 46px minmax(0, 1fr); gap: 12px; padding: 15px 14px; }
  .order-badge { width: 42px; height: 46px; }
  .document-actions { grid-column: 2; justify-content: flex-start; }
  .document-settings { grid-template-columns: 1fr 1fr; }
  .document-settings > :first-child { grid-column: 1 / -1; }
}

@media (max-width: 560px) {
  .help-page-head .n-button { width: 100%; }
  .help-overview { gap: 7px; }
  .metric-card { min-height: 66px; border-radius: 10px; }
  .metric-copy > span { font-size: 11px; }
  .document-content > p { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; white-space: normal; }
  .document-actions { width: 100%; }
  .document-actions .n-button:first-child { flex: 1; }
  .document-settings { grid-template-columns: 1fr; }
  .document-settings > :first-child { grid-column: auto; }
  .editor-heading { align-items: flex-start; flex-direction: column; gap: 9px; }
  .md-toolbar { align-items: flex-start; flex-direction: column; }
  .format-actions { max-width: 100%; overflow-x: auto; }
  .editor-modes { width: 100%; padding: 5px 0 0; border-top: 1px solid var(--border); border-left: 0; }
  .editor-modes button { flex: 1; }
  .editor-body.split { flex-direction: column; max-height: none; }
  .editor-body.split .editor-ta, .editor-body.split .editor-pv { flex: none; width: 100%; min-height: 260px; }
  .editor-body.split .editor-ta { border-right: 0; border-bottom: 1px solid var(--border); }
  .editor-footer { align-items: stretch; flex-direction: column; }
  .editor-footer > span { display: none; }
  .editor-footer > div .n-button { flex: 1; }
}
</style>
