<template>
  <div>
    <div class="flex items-center justify-between text-xs">
      <span>{{ label }}</span>
      <span class="font-mono">{{ value }} <span v-if="total" class="text-fg-subtle">({{ pct }}%)</span></span>
    </div>
    <div class="h-2 bg-bg-hover rounded mt-1 overflow-hidden">
      <div class="h-full rounded" :style="{ width: pct + '%' }" :class="toneBar" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{
  label: string
  value: number
  total: number
  tone?: 'accent' | 'success' | 'warning' | 'danger'
}>()
const pct = computed(() => props.total > 0 ? Math.round((props.value / props.total) * 100) : 0)
const toneBar = computed(() => {
  switch (props.tone) {
    case 'danger':  return 'bg-danger'
    case 'success': return 'bg-success'
    case 'warning': return 'bg-warning'
    default:        return 'bg-accent'
  }
})
</script>
