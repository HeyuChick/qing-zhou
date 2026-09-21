<template>
  <img
    class="qz-brand-mark"
    :src="brandIcon"
    alt=""
    aria-hidden="true"
    :width="size"
    :height="size"
  >
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useConfigStore } from '@/stores/config'

withDefaults(defineProps<{ size?: number }>(), { size: 36 })

const config = useConfigStore()
const brandIcon = computed(() => config.config.brand_icon_data_uri || '/qingzhou-mark.svg')
</script>

<style scoped>
.qz-brand-mark {
  display: block;
  flex-shrink: 0;
  object-fit: contain;
  filter: drop-shadow(0 4px 7px rgba(24, 76, 108, .14));
}
</style>
