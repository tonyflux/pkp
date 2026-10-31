<script setup lang="ts">
// The right-hand island of Settings. Like
// the transfer details it is a full-height column of the content row beside
// the canvas: the canvas gives up the width instead of being covered, so
// every card keeps its action in view. The body scrolls inside once its
// content outgrows the row.
import Icon from "./Icon.vue";
import { t } from "../i18n";

defineProps<{ title: string; icon: string; lead?: string }>();
defineEmits<{ close: [] }>();
</script>

<template>
  <aside class="island glass glass-sheet" :aria-label="title">
    <header class="head">
      <Icon :name="icon" :size="24" />
      <h2>{{ title }}</h2>
      <button class="quiet icon close" :title="t('关闭')" @click="$emit('close')"><Icon name="close" :size="16" /></button>
    </header>
    <p v-if="lead" class="lead">{{ lead }}</p>
    <div class="body">
      <slot />
    </div>
  </aside>
</template>

<style scoped>
.island {
  position: relative;
  flex: none;
  align-self: stretch;
  z-index: 2;
  width: var(--inspector-w);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: var(--radius-sheet);
  animation: island-in var(--dur) var(--ease);
}

@keyframes island-in {
  from {
    opacity: 0;
    transform: translateX(14px);
  }
}

.head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 16px 8px 20px;
  color: var(--fg);
}

.head h2 {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 19px;
  font-weight: 600;
  /* A UI title, never cut: longer languages take a second line. */
  text-wrap: balance;
  overflow-wrap: break-word;
}

.close {
  width: 32px;
  height: 32px;
  min-height: 32px;
  background: var(--glass-control);
}

.lead {
  flex: none;
  margin: 0;
  padding: 2px 20px 16px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--fg-dim);
}

.body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 0 18px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
</style>
