<template>
  <div>
    <h2 class="page-title">帮助文档管理</h2>
    <div class="page-toolbar">
      <span class="spacer" />
      <n-button type="primary" @click="openCreate">新建文档</n-button>
    </div>
    <n-spin :show="loading">
      <div v-if="docs.length" class="card-grid">
        <div v-for="d in docs" :key="d.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ d.title || '—' }}</span>
            <n-tag :type="d.published ? 'success' : 'default'" size="tiny" bordered="false">{{ d.published ? '已发布' : '草稿' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">排序 <b>{{ d.sort_order ?? 0 }}</b></span>
            <span class="kv">{{ fmtDate(d.updated_at) }}</span>
          </div>
          <div class="lc-foot">
            <n-button size="tiny" @click="openEdit(d)">编辑</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(d.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无文档" style="padding:40px 0;" />
    </n-spin>

    <!-- 编辑器 -->
    <n-modal v-model:show="showForm" preset="card" :title="editing ? '编辑文档' : '新建文档'" style="max-width:900px;">
      <n-form label-placement="left" label-width="60">
        <n-form-item label="标题"><n-input v-model:value="form.title" /></n-form-item>
        <n-form-item label="排序"><n-input-number v-model:value="form.sort_order" :min="0" style="width:120px;" /></n-form-item>
        <n-form-item label="发布"><n-switch v-model:value="form.published" /></n-form-item>
      </n-form>

      <!-- 工具栏 -->
      <div class="md-toolbar">
        <button v-for="btn in toolbar" :key="btn.label" @click="insertMd(btn)" :title="btn.label">{{ btn.icon }}</button>
        <span class="sep" />
        <button @click="editorMode='write'" :class="{on:editorMode==='write'}">编辑</button>
        <button @click="editorMode='split'" :class="{on:editorMode==='split'}">分栏</button>
        <button @click="editorMode='preview'" :class="{on:editorMode==='preview'}">预览</button>
        <span style="margin-left:auto;font-size:11px;color:var(--text-3);">{{ form.content.length }} 字</span>
      </div>

      <!-- 编辑区 -->
      <div class="editor-body" :class="editorMode">
        <textarea v-if="editorMode!=='preview'" ref="taRef" v-model="form.content" class="editor-ta" @keydown="handleTab" placeholder="Markdown 内容..." />
        <div v-if="editorMode!=='write'" class="editor-pv md" v-html="mdToHtml(form.content)" />
      </div>

      <div style="margin-top:12px;">
        <n-button type="primary" block :loading="saving" @click="handleSave">保存</n-button>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, nextTick } from 'vue'
import { NSpin, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSwitch, NTag, NEmpty, useMessage } from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtDate } from '@/utils/format'
import { mdToHtml } from '@/utils/markdown'

const message = useMessage()
const docs = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const editing = ref<any>(null)
const form = reactive({ title: '', content: '', sort_order: 0, published: true })
const editorMode = ref<'write' | 'split' | 'preview'>('split')
const taRef = ref<HTMLTextAreaElement | null>(null)

const toolbar = [
  { label: '标题', icon: 'H', insert: '## ' },
  { label: '粗体', icon: 'B', before: '**', after: '**' },
  { label: '斜体', icon: 'I', before: '*', after: '*' },
  { label: '链接', icon: '🔗', before: '[', after: '](url)' },
  { label: '代码', icon: '`', before: '`', after: '`' },
  { label: '代码块', icon: '```', insert: '```\n\n```' },
  { label: '列表', icon: '• ', insert: '- ' },
  { label: '有序列表', icon: '1.', insert: '1. ' },
  { label: '引用', icon: '❝', insert: '> ' },
  { label: '表格', icon: '⊞', insert: '| 列1 | 列2 |\n|------|------|\n| 内容 | 内容 |' },
  { label: '分割线', icon: '—', insert: '\n---\n' },
]

function insertMd(btn: any) {
  const ta = taRef.value
  if (!ta) { form.content += btn.insert || ''; return }
  const start = ta.selectionStart; const end = ta.selectionEnd; const sel = form.content.slice(start, end)
  if (btn.insert) {
    form.content = form.content.slice(0, start) + btn.insert + form.content.slice(end)
    nextTick(() => { ta.selectionStart = ta.selectionEnd = start + btn.insert.length; ta.focus() })
  } else if (btn.before) {
    const text = btn.before + (sel || '文本') + btn.after
    form.content = form.content.slice(0, start) + text + form.content.slice(end)
    nextTick(() => { ta.selectionStart = start + btn.before.length; ta.selectionEnd = start + btn.before.length + (sel || '文本').length; ta.focus() })
  }
}

function handleTab(e: KeyboardEvent) {
  if (e.key === 'Tab') {
    e.preventDefault()
    const ta = taRef.value!; const s = ta.selectionStart; const end = ta.selectionEnd
    form.content = form.content.slice(0, s) + '  ' + form.content.slice(end)
    nextTick(() => { ta.selectionStart = ta.selectionEnd = s + 2 })
  }
}

function openCreate() { editing.value = null; Object.assign(form, { title: '', content: '', sort_order: 0, published: true }); showForm.value = true }
function openEdit(d: any) { editing.value = d; Object.assign(form, { title: d.title, content: d.content, sort_order: d.sort_order || 0, published: d.published }); showForm.value = true }

async function handleSave() {
  saving.value = true
  try {
    if (editing.value) await apiPut(`/api/admin/help/${editing.value.id}`, form)
    else await apiPost('/api/admin/help', form)
    message.success('保存成功'); showForm.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

async function handleDelete(id: number) {
  try { await apiDelete(`/api/admin/help/${id}`); message.success('已删除'); await load() } catch (e: any) { message.error(e.message) }
}

async function load() { loading.value = true; try { docs.value = await apiList('/api/admin/help') } catch {} finally { loading.value = false } }
onMounted(load)
</script>

<style scoped>
.md-toolbar { display: flex; flex-wrap: wrap; gap: 4px; padding: 6px; border: 1px solid var(--border); border-bottom: none; border-radius: 10px 10px 0 0; background: var(--bg-soft); }
.md-toolbar button { min-width: 28px; height: 26px; padding: 0 6px; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--text-2); font-size: 12px; font-weight: 600; cursor: pointer; font-family: inherit; }
.md-toolbar button:hover { background: #fff; border-color: var(--border); }
.md-toolbar button.on { background: var(--accent); color: #fff; }
.md-toolbar .sep { width: 1px; height: 18px; background: var(--border); margin: 0 2px; }
.editor-body { display: flex; border: 1px solid var(--border); border-radius: 0 0 10px 10px; overflow: hidden; min-height: 45vh; }
.editor-body.write .editor-ta { flex: 1; }
.editor-body.split .editor-ta { flex: 1; border-right: 1px solid var(--border); }
.editor-body.split .editor-pv { flex: 1; }
.editor-body.preview .editor-pv { flex: 1; }
.editor-ta { border: none; resize: none; width: 100%; min-height: 45vh; padding: 14px; font-size: 13px; line-height: 1.65; font-family: 'SF Mono', ui-monospace, Menlo, Consolas, monospace; background: #fff; outline: none; }
.editor-pv { min-height: 45vh; max-height: 55vh; overflow: auto; padding: 16px; background: var(--bg-soft); }
</style>
