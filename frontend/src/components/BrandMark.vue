<template>
  <img
    class="qz-brand-mark"
    src="/qingzhou-mark.svg"
    alt=""
    aria-hidden="true"
    :width="size"
    :height="size"
  >
</template>

<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 36 })
</script>

<style scoped>
.qz-brand-mark {
  display: block;
  flex-shrink: 0;
  object-fit: contain;
  filter: drop-shadow(0 4px 7px rgba(24, 76, 108, .14));
}
</style>
