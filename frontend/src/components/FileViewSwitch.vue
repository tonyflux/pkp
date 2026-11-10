<script setup lang="ts">
import Icon from "./Icon.vue";
import { t } from "../i18n";
import { vSlide } from "../slide";

defineProps<{ modelValue: "grid" | "list"; disabled?: boolean; label?: string }>();
const emit = defineEmits<{ (event: "update:modelValue", value: "grid" | "list"): void }>();
</script>

<template>
  <div v-slide class="file-view-switch glass-well" role="group" :aria-label="label || t('文件视图')">
    <button type="button" :disabled="disabled" :class="{ on: modelValue === 'grid' }" :title="t('网格')" :aria-label="t('网格')" :aria-pressed="modelValue === 'grid'" @click="emit('update:modelValue', 'grid')"><Icon name="grid" :size="16" /></button>
    <button type="button" :disabled="disabled" :class="{ on: modelValue === 'list' }" :title="t('列表')" :aria-label="t('列表')" :aria-pressed="modelValue === 'list'" @click="emit('update:modelValue', 'list')"><Icon name="list" :size="16" /></button>
  </div>
</template>

<style scoped>
.file-view-switch { display: inline-flex; align-items: center; gap: 2px; flex: none; padding: 3px; border-radius: var(--radius-pill); isolation: isolate; }
button { display: grid; place-items: center; width: 36px; height: 30px; min-height: 30px; padding: 0; border: 0; background: transparent; box-shadow: none; color: var(--fg-dim); }
button:first-child { border-radius: 15px 9px 9px 15px; }
button:last-child { border-radius: 9px 15px 15px 9px; }
button:hover:not(:disabled):not(.on) { background: color-mix(in srgb, var(--fg) 7%, transparent); color: var(--fg); }
.file-view-switch::before { background: var(--accent-soft); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--glass-edge) 50%, transparent), inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 45%, transparent); }
button.on { background: transparent; color: var(--fg); }
button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
