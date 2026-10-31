<script setup lang="ts">
// Cleanup submit dock: what the ticks add up to, what happens to them,
// and the action. It floats over the bottom of the tab's scroller, which
// keeps room under its last row so the dock never covers content.
import Icon from "./Icon.vue";
import { t } from "../i18n";

defineProps<{ title: string; hint: string; warn?: boolean }>();
</script>

<template>
  <footer class="dock glass" :aria-label="t('提交')">
    <span class="dock-mark" aria-hidden="true"><Icon name="shield" :size="20" /></span>
    <div class="copy">
      <strong class="tnum">{{ title }}</strong>
      <small :class="{ warn }">{{ hint }}</small>
    </div>
    <div class="actions">
      <slot />
    </div>
  </footer>
</template>

<style scoped>
.dock {
  position: absolute;
  left: var(--pad);
  right: var(--pad);
  bottom: 14px;
  z-index: 3;
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 70px;
  padding: 10px 12px 10px 14px;
  border-radius: 20px;
}

.dock-mark {
  flex: none;
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
  color: var(--fg-dim);
}

.copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* The longer languages take a second line rather than being cut. */
.copy strong {
  font-size: 15px;
  font-weight: 650;
  text-wrap: balance;
}

.copy small {
  font-size: 12px;
  line-height: 1.4;
  color: var(--fg-dim);
  text-wrap: pretty;
}

.copy small.warn {
  color: var(--warning);
}

.actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
}

.actions :deep(button) {
  min-height: 42px;
  padding: 0 18px;
  border-radius: 12px;
  font-size: 14px;
}

@container (max-width: 650px) {
  .dock {
    left: 10px;
    right: 10px;
    bottom: 10px;
    gap: 10px;
  }

  .dock-mark {
    display: none;
  }
}
</style>
