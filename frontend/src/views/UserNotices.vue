<template>
  <div>
    <div class="page-head"><div><h2 class="page-title">公告通知</h2><p class="page-sub">共 {{ notices.length }} 条公告 · 置顶 {{ notices.filter(n => n.pinned).length }} 条</p></div></div>
    <n-spin :show="loading">
    <n-card v-for="n in notices" :key="n.id" size="small" style="margin-bottom:12px;">
      <template #header>
        <span>{{ n.title }}</span>
        <n-tag v-if="n.pinned" size="tiny" type="warning" style="margin-left:6px;">置顶</n-tag>
      </template>
      <template #header-extra>
        <span style="font-size:12px;color:var(--text-3);">{{ fmtDate(n.created_at) }}</span>
      </template>
      <div class="md" v-html="mdToHtml(n.content)" />
    </n-card>
    <n-empty v-if="!loading && notices.length === 0" description="暂无公告" style="padding:60px 0;" />
    </n-spin>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { NCard, NTag, NEmpty, NSpin } from 'naive-ui'
import { apiList, apiPost } from '@/api'
import { fmtDate } from '@/utils/format'
import { mdToHtml } from '@/utils/markdown'

const notices = ref<any[]>([])
const loading = ref(false)

onMounted(async () => {
  loading.value = true
  try { notices.value = await apiList('/api/user/announcements') } catch {}
  finally { loading.value = false }
  // 标记为已读
  try { await apiPost('/api/user/announcements/read') } catch {}
})
</script>

<style scoped></style>
