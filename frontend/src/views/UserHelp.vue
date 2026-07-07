<template>
  <div>
    <h2 class="page-title">帮助中心</h2>
    <p class="page-sub">常见问题与使用指南</p>

    <div class="help-layout">
      <n-card size="small" class="help-nav">
        <div
          v-for="doc in docs"
          :key="doc.id"
          class="help-nav-item"
          :class="{ active: activeId === doc.id }"
          @click="activeId = doc.id"
        >
          {{ doc.title }}
        </div>
        <n-empty v-if="docs.length === 0" description="暂无文档" size="small" />
      </n-card>

      <n-card v-if="activeDoc" size="small">
        <h3 style="margin-bottom: 16px;">{{ activeDoc.title }}</h3>
        <div class="md" v-html="mdToHtml(activeDoc.content)" />
      </n-card>
      <n-card v-else size="small">
        <n-empty description="选择一篇文档" />
      </n-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { NCard, NEmpty } from 'naive-ui'
import { apiList } from '@/api'
import { mdToHtml } from '@/utils/markdown'

const docs = ref<any[]>([])
const activeId = ref<number | null>(null)

const activeDoc = computed(() => docs.value.find(d => d.id === activeId.value))

onMounted(async () => {
  try { docs.value = await apiList('/api/help') || [] } catch {}
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
</style>
