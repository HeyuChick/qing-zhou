<template>
  <div>
    <h2 class="page-title">帮助中心</h2>
    <p class="page-sub">共 {{ docs.length }} 篇指南，覆盖安装、订阅、连接与常见问题</p>

    <div class="help-layout">
      <n-card size="small" class="help-nav">
        <n-input v-model:value="query" size="small" clearable placeholder="搜索帮助文档" class="help-search" />
        <div
          v-for="doc in filteredDocs"
          :key="doc.id"
          class="help-nav-item"
          :class="{ active: activeId === doc.id }"
          @click="activeId = doc.id"
        >
          {{ doc.title }}
        </div>
        <n-empty v-if="!loading && filteredDocs.length === 0" :description="docs.length ? '没有匹配的文档' : '暂无文档'" size="small" />
      </n-card>

      <n-spin :show="loading">
        <n-card v-if="activeDoc" size="small" class="help-content">
          <h3>{{ activeDoc.title }}</h3>
          <div class="help-meta">文档编号 #{{ activeDoc.id }} · 内容会随管理员更新自动同步</div>
          <div class="md" v-html="mdToHtml(activeDoc.content)" />
        </n-card>
        <n-card v-else size="small"><n-empty :description="loading ? '正在加载文档' : '选择一篇文档'" /></n-card>
      </n-spin>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { NCard, NEmpty, NInput, NSpin } from 'naive-ui'
import { apiList } from '@/api'
import { mdToHtml } from '@/utils/markdown'

const docs = ref<any[]>([])
const activeId = ref<number | null>(null)
const query = ref('')
const loading = ref(false)

const activeDoc = computed(() => docs.value.find(d => d.id === activeId.value))
const filteredDocs = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return docs.value
  return docs.value.filter(d => `${d.title || ''} ${d.content || ''}`.toLowerCase().includes(q))
})
watch(filteredDocs, list => {
  if (list.length && !list.some(d => d.id === activeId.value)) activeId.value = list[0].id
})

onMounted(async () => {
  loading.value = true
  try { docs.value = await apiList('/api/help') || [] } catch {}
  finally { loading.value = false }
  if (docs.value.length) activeId.value = docs.value[0].id
})
</script>

<style scoped>
.page-title { font-size: 21px; margin-bottom: 4px; }
.page-sub { color: var(--text-2); margin-bottom: 22px; }
.help-layout { display: grid; grid-template-columns: 220px 1fr; gap: 16px; align-items: start; }
@media (max-width: 780px) { .help-layout { grid-template-columns: 1fr; } }
.help-nav-item {
  padding: 9px 12px;
  border-radius: 9px;
  cursor: pointer;
  font-size: 14px;
  color: var(--text-2);
  transition: 0.12s;
}
.help-nav-item:hover { background: var(--bg-soft); color: var(--text); }
.help-nav-item.active { background: var(--accent-soft); color: var(--accent-strong); font-weight: 600; }
.help-search { margin-bottom:10px; }
.help-content h3 { margin:0 0 3px; font-size:18px; }
.help-meta { margin-bottom:18px; padding-bottom:12px; border-bottom:1px solid var(--border); color:var(--text-3); font-size:11.5px; }
</style>
