<script setup lang="ts">
import { t } from "../i18n";
// An in-place "are you sure" that replaces a modal: one line of consequence
// and two buttons, drawn where the action was asked for.
defineProps<{
  message: string;
  confirmLabel: string;
  cancelLabel?: string;
  danger?: boolean;
}>();

defineEmits<{ confirm: []; cancel: [] }>();
</script>

<template>
  <div class="confirm" @click.stop>
    <p>{{ message }}</p>
    <div class="confirm-actions">
      <button class="quiet" @click="$emit('cancel')">{{ cancelLabel ?? t("取消") }}</button>
      <button class="confirm-go" :class="{ danger }" @click="$emit('confirm')">{{ confirmLabel }}</button>
    </div>
  </div>
</template>

<style scoped>
.confirm {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--line);
}

.confirm p {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
}

/* Long labels (en/ja) in a narrow card drop the pair onto a second line
   instead of pushing past the card's edge. */
.confirm-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.confirm-actions button {
  min-height: 34px;
  padding: 0 14px;
  font-size: 13px;
}

/* Hover keeps the fill: the plain button:hover outranks this rule and would
   swap it for flat glass. border-box: see button.primary in style.css. */
.confirm-go:not(.danger),
.confirm-go:not(.danger):hover:not(:disabled) {
  background: var(--primary) border-box;
  border-color: transparent;
  color: var(--on-primary);
  font-weight: 600;
}

</style>
