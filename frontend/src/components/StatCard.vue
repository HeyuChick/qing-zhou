<template>
  <div class="stat-card" :class="{ clickable }" @click="$emit('click')">
    <div class="sc-top">
      <span class="sc-label">{{ label }}</span>
      <span v-if="badge" class="sc-badge" :style="{ background: badgeColor + '1a', color: badgeColor }">{{ badge }}</span>
    </div>
    <div class="sc-value" :style="valueColor ? { color: valueColor } : {}">{{ value }}</div>
    <div class="sc-sub">{{ sub }}</div>
    <div v-if="$slots.default" class="sc-extra"><slot /></div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  label: string
  value: string | number
  sub?: string
  badge?: string
  badgeColor?: string
  valueColor?: string
  clickable?: boolean
}>()
defineEmits<{ (e: 'click'): void }>()
</script>

<style scoped>
.stat-card {
  position: relative; overflow: hidden;
  background: var(--card); border: 1px solid var(--border); border-radius: var(--r-sm);
  padding: 14px 16px 12px;
  transition: box-shadow .18s, border-color .18s, transform .18s;
}
.stat-card.clickable { cursor: pointer; }
.stat-card.clickable:hover { box-shadow: var(--shadow); border-color: #d5d5d5; transform: translateY(-1px); }
.sc-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.sc-label { font-size: 12.5px; color: var(--text-2); font-weight: 550; }
.sc-badge { font-size: 10.5px; font-weight: 650; padding: 2px 8px; border-radius: 20px; letter-spacing: .01em; }
.sc-value {
  font-size: 26px; font-weight: 720; letter-spacing: -0.02em; line-height: 1.15;
  margin-top: 8px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.sc-sub { font-size: 11.5px; color: var(--text-3); margin-top: 4px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sc-extra { margin-top: 8px; }
</style>
