<template>
  <div class="card p-4">
    <div class="text-xs uppercase tracking-wide text-fg-muted mb-1">{{ label }}</div>
    <div class="text-2xl font-semibold" :class="toneClass">{{ value }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{
  label: string
  value: string | number
  tone?: 'accent' | 'success' | 'warning' | 'danger'
}>()
const toneClass = computed(() => {
  switch (props.tone) {
    case 'success': return 'text-success'
    case 'warning': return 'text-warning'
    case 'danger':  return 'text-danger'
    case 'accent':
    default:        return 'text-accent'
  }
})
</script>
