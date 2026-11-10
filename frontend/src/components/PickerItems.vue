<script setup lang="ts">
defineProps<{ layout: "grid" | "list" }>();
</script>

<template>
  <div class="picker-items" :class="{ grid: layout === 'grid' }"><slot /></div>
</template>

<style scoped>
.picker-items { min-width: 0; min-height: 0; padding: 6px; }
.picker-items :deep(.picker-item) { position: relative; display: flex; align-items: center; justify-content: flex-start; gap: 10px; width: 100%; min-width: 0; min-height: 42px; padding: 8px 10px; border: 1px solid transparent; border-radius: 10px; background: transparent; color: var(--fg); text-align: left; cursor: pointer; }
.picker-items :deep(.picker-item:hover:not(:disabled)), .picker-items :deep(.picker-item.is-picked) { background: var(--accent-soft); }
.picker-items :deep(.picker-item:focus-visible), .picker-items :deep(.picker-item:focus-within) { outline: 2px solid var(--accent); outline-offset: -2px; }
.picker-items :deep(.picker-item:disabled) { opacity: .45; cursor: default; }
.picker-items :deep(.picker-thumb) { position: relative; display: grid; place-items: center; flex: none; width: 34px; height: 34px; border-radius: 8px; color: var(--accent); background: var(--glass-inset); overflow: hidden; }
.picker-items :deep(.picker-copy) { flex: 1; min-width: 0; display: grid; gap: 3px; }
.picker-items :deep(.picker-name) { display: block; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 500; line-height: 18px; }
.picker-items :deep(.picker-meta) { display: block; min-width: 0; color: var(--fg-dim); font-size: 11px; line-height: 1.5; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.picker-items :deep(.picker-chevron) { flex: none; color: var(--fg-dim); }
.picker-items :deep(.picker-check) { appearance: none; -webkit-appearance: none; position: relative; display: grid; place-items: center; flex: none; width: 18px; height: 18px; min-width: 0; min-height: 0; margin: 0; padding: 0; border: 1.5px solid var(--fg-dim); border-radius: 50%; background: transparent; cursor: pointer; }
.picker-items :deep(.picker-check:checked) { background: var(--primary) border-box; border-color: transparent; }
.picker-items :deep(.picker-check:checked::after) { content: ""; width: 5px; height: 9px; border: solid var(--on-primary); border-width: 0 1.8px 1.8px 0; transform: translateY(-.5px) rotate(45deg); }
.picker-items :deep(.picker-check:disabled) { opacity: .45; cursor: default; }
.picker-items.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 150px), 1fr)); grid-auto-rows: max-content; align-content: start; gap: 14px 12px; padding: 14px; }
.picker-items.grid :deep(.picker-item) { display: flex; flex-direction: column; align-items: stretch; gap: 8px; min-height: 0; padding: 0; border: 0; border-radius: 12px; }
.picker-items.grid :deep(.picker-item:hover:not(:disabled)), .picker-items.grid :deep(.picker-item.is-picked) { background: transparent; }
.picker-items.grid :deep(.picker-thumb) { flex: none; width: 100%; height: auto; aspect-ratio: 16 / 9; border: 1px solid var(--line); border-radius: 12px; }
.picker-items.grid :deep(.picker-thumb > .icon), .picker-items.grid :deep(.picker-thumb .fallback .icon) { width: 30px; height: 30px; }
.picker-items.grid :deep(.picker-item.is-picked .picker-thumb) { box-shadow: 0 0 0 2px var(--accent), 0 0 0 5px var(--accent-soft); }
.picker-items.grid :deep(.picker-copy) { flex: none; padding: 0 2px 2px; }
.picker-items.grid :deep(.picker-name) { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; min-height: 36px; white-space: normal; overflow-wrap: anywhere; word-break: break-all; font-size: 12px; }
.picker-items.grid :deep(.picker-chevron) { display: none; }
.picker-items.grid :deep(.picker-check) { position: absolute; z-index: 2; top: 8px; left: 8px; width: 20px; height: 20px; border-color: rgba(255, 255, 255, .86); background: rgba(6, 16, 28, .42); backdrop-filter: blur(6px); box-shadow: 0 1px 3px #0004; }
.picker-items.grid :deep(.picker-check:checked) { background: var(--primary) border-box; }
.picker-items :deep(.picker-state) { grid-column: 1 / -1; }
</style>
