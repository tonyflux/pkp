<script setup lang="ts">
// The bar a page raises while two or more of its rows are picked — the file
// page, the transfer list: how many, Select all, the page's own actions in the
// slot, and a way out. It floats over the bottom of the canvas it sits on,
// in place of the details island, which only ever describes one.
import { computed } from "vue";
import Icon from "./Icon.vue";
import { t } from "../i18n";

const props = defineProps<{ count: number; allPicked: boolean }>();
// The count stays bold inside a sentence whose word order differs by language.
const countParts = computed(() => t("已选 {n} 项", { n: "\u0000" }).split("\u0000"));
defineEmits<{ pickAll: []; clear: [] }>();
</script>

<template>
  <div class="bar glass glass-sheet" role="toolbar" :aria-label="t('批量操作')">
    <span class="count">{{ countParts[0] }}<strong>{{ props.count }}</strong>{{ countParts[1] }}</span>
    <button v-if="!allPicked" class="quiet text" @click="$emit('pickAll')">{{ t("全选") }}</button>
    <span class="divider" aria-hidden="true" />
    <slot />
    <span class="divider" aria-hidden="true" />
    <button class="quiet icon" :title="t('取消选择（Esc）')" @click="$emit('clear')"><Icon name="close" :size="16" /></button>
  </div>
</template>

<style scoped>
/* Centred by auto margins between 12px insets rather than left 50% plus a
   translate: that left the bar only half the canvas to lay out in. When the
   page's actions don't fit one row (en/ja labels on a canvas narrowed by
   the info panel) they wrap onto a second row inside the same glass, which
   keeps the one-row capsule's 25px corners. */
.bar {
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: 14px;
  z-index: 5;
  width: fit-content;
  margin-inline: auto;
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 2px 4px;
  padding: 6px 8px 6px 18px;
  border-radius: 25px;
  box-shadow: var(--shadow-sheet);
  white-space: nowrap;
  animation: rise var(--dur) var(--ease);
}

@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
}

.count {
  font-size: 14px;
  color: var(--fg-dim);
  padding-right: 4px;
}

.count strong {
  color: var(--fg);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.divider {
  flex: none;
  width: 1px;
  height: 22px;
  margin: 0 6px;
  background: var(--line-strong);
}

/* The actions come from the page through the slot. */
.bar :deep(button) {
  gap: 6px;
  min-height: 36px;
  padding: 0 12px;
  font-size: 14px;
}

.bar :deep(button.icon) {
  padding: 0;
}

.bar :deep(button.text) {
  color: var(--accent);
}

/* A confirm lies flat inside the bar: the message beside its two buttons,
   the count and the way out on either side. The bar is then one grid row,
   never a wrapped flex line (that left a divider dangling at a row's end):
   when the row is short of room the message wraps, balanced; the buttons
   never do. */
.bar:has(> .confirm) {
  display: grid;
  grid-auto-flow: column;
}

.bar :deep(.confirm) {
  flex: 0 1 auto;
  min-width: 0;
  flex-direction: row;
  align-items: center;
  gap: 4px;
  padding-top: 0;
  border-top: 0;
}

.bar :deep(.confirm p) {
  min-width: 0;
  font-size: 14px;
  line-height: 1.35;
  color: var(--danger);
  padding: 0 6px;
  white-space: normal;
  text-wrap: balance;
}

.bar :deep(.confirm-actions) {
  flex: none;
  flex-wrap: nowrap;
  gap: 4px;
}

.bar :deep(.confirm-actions button) {
  min-height: 36px;
  padding: 0 12px;
  font-size: 14px;
}

.bar :deep(.confirm-actions .quiet) {
  color: var(--accent);
}

/* A canvas narrowed by an island beside it: the actions
   keep their words and counts and give up their icons. Applies where the
   page's canvas is a size container, as the files canvas and the transfer
   list are. */
@container (max-width: 600px) {
  .bar :deep(button:not(.icon) > svg) {
    display: none;
  }
}

/* On a narrow canvas confirmation uses two rows: the count, select-all and
   close control stay above the question and its buttons. */
@container (max-width: 720px) {
  .bar:has(> .confirm) {
    grid-auto-flow: row;
    grid-template-columns: auto 1fr auto;
  }

  .bar:has(> .confirm) > .divider {
    display: none;
  }

  .bar:has(> .confirm) > .count {
    grid-area: 1 / 1;
  }

  .bar:has(> .confirm) > button.text {
    grid-area: 1 / 2;
    justify-self: start;
  }

  .bar:has(> .confirm) > button.icon:last-child {
    grid-area: 1 / 3;
  }

  .bar > :deep(.confirm) {
    grid-area: 2 / 1 / 3 / -1;
    justify-content: space-between;
  }
}
</style>
