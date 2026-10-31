<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import { t } from "../i18n";
import {
  appLocked, closeSavedHistoryDialog, currentId, deleteSavedHistory, renameSavedHistory,
  resolveSavedHistoryLeave, savedHistory, savedHistoryCanSave,
} from "../store";

const dialog = computed(() => savedHistory.dialog);
const sheet = ref<HTMLElement | null>(null);
const nameField = ref<HTMLInputElement | null>(null);
const name = ref("");
const title = computed(() => {
  const current = dialog.value;
  if (!current) return "";
  if (current.kind === "rename") return t("重命名已保存历史");
  if (current.kind === "delete") return t("删除已保存历史“{name}”？", { name: current.name });
  return t("保存对“{name}”的更改？", { name: current.name });
});
let opener: HTMLElement | null = null;
let pressedScrim = false;

function dismiss() {
  if (dialog.value?.kind === "leave") resolveSavedHistoryLeave("stay");
  else closeSavedHistoryDialog();
}

function focusables(): HTMLElement[] {
  return Array.from(sheet.value?.querySelectorAll<HTMLElement>(
    "button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex='-1'])",
  ) ?? []).filter((element) => element.offsetParent !== null);
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const targets = focusables();
  const first = targets[0];
  const last = targets[targets.length - 1];
  if (!first) { event.preventDefault(); sheet.value?.focus(); return; }
  if (event.shiftKey && (document.activeElement === first || !sheet.value?.contains(document.activeElement))) {
    event.preventDefault();
    last?.focus();
  } else if (!event.shiftKey && (document.activeElement === last || document.activeElement === sheet.value)) {
    event.preventDefault();
    first.focus();
  }
}

function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || !dialog.value || appLocked.value) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  dismiss();
}

function onFocusIn(event: FocusEvent) {
  if (!dialog.value || !sheet.value || appLocked.value || sheet.value.contains(event.target as Node)) return;
  (sheet.value.querySelector<HTMLElement>("[data-cancel]") ?? sheet.value).focus({ preventScroll: true });
}

function onScrimDown(event: PointerEvent) { pressedScrim = event.target === event.currentTarget; }
function onScrimClick(event: MouseEvent) {
  if (pressedScrim && event.target === event.currentTarget) dismiss();
  pressedScrim = false;
}

watch(dialog, async (current, previous) => {
  if (!current) {
    const from = opener;
    opener = null;
    await nextTick();
    if (dialog.value || appLocked.value || (document.activeElement && document.activeElement !== document.body)) return;
    const target = from?.isConnected && !from.matches(":disabled") ? from : document.querySelector<HTMLElement>("[data-saved-history-heading]");
    target?.focus({ preventScroll: true });
    return;
  }
  if (!previous) opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  name.value = current.name;
  pressedScrim = false;
  await nextTick();
  if (dialog.value !== current || appLocked.value) return;
  if (current.kind === "rename") { nameField.value?.focus({ preventScroll: true }); nameField.value?.select(); }
  else sheet.value?.querySelector<HTMLButtonElement>("[data-cancel]")?.focus({ preventScroll: true });
}, { immediate: true });
watch(currentId, (account) => { if (dialog.value && dialog.value.account !== account) dismiss(); });
onMounted(() => {
  window.addEventListener("keydown", onEscape, true);
  document.addEventListener("focusin", onFocusIn);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onEscape, true);
  document.removeEventListener("focusin", onFocusIn);
});
</script>

<template>
  <Teleport to="body">
    <div v-if="dialog" class="scrim saved-history-scrim" @pointerdown="onScrimDown" @click="onScrimClick">
      <section ref="sheet" class="sheet glass glass-sheet saved-history-sheet" role="dialog" aria-modal="true" aria-labelledby="saved-history-dialog-title" aria-describedby="saved-history-dialog-description" tabindex="-1" @keydown.stop="onKeydown">
        <header>
          <h2 id="saved-history-dialog-title">{{ title }}</h2>
          <button type="button" class="quiet icon close" :aria-label="t('关闭')" :title="t('关闭')" @click="dismiss"><Icon name="close" :size="17" /></button>
        </header>
        <form v-if="dialog.kind === 'rename'" @submit.prevent="renameSavedHistory(name)">
          <p id="saved-history-dialog-description" class="description">{{ t("只更改名称，不会更改保存的搜索条件。") }}</p>
          <label class="name-field">
            <span>{{ t("搜索名称") }}</span>
            <input ref="nameField" v-model="name" maxlength="100" required :placeholder="t('请输入搜索名称')" :aria-invalid="savedHistory.error ? true : undefined" :aria-describedby="savedHistory.error ? 'saved-history-dialog-error' : undefined" />
          </label>
          <p v-if="savedHistory.error" id="saved-history-dialog-error" class="error" role="alert">{{ savedHistory.error }}</p>
          <footer class="actions">
            <button type="button" data-cancel @click="dismiss">{{ t("取消") }}</button>
            <button type="submit" class="primary" :disabled="!name.trim()">{{ t("保存") }}</button>
          </footer>
        </form>
        <template v-else-if="dialog.kind === 'delete'">
          <p id="saved-history-dialog-description" class="description">{{ t("只删除保存的搜索条件，不会删除云盘文件。") }}</p>
          <p v-if="savedHistory.error" id="saved-history-dialog-error" class="error" role="alert">{{ savedHistory.error }}</p>
          <footer class="actions">
            <button type="button" data-cancel @click="dismiss">{{ t("取消") }}</button>
            <button type="button" class="destructive" @click="deleteSavedHistory">{{ t("删除") }}</button>
          </footer>
        </template>
        <template v-else>
          <p id="saved-history-dialog-description" class="description">{{ t("更改尚未保存。保存后离开，或放弃更改后继续。") }}</p>
          <p v-if="!savedHistoryCanSave" class="description">{{ t("当前条件无法保存，请继续编辑或放弃更改。") }}</p>
          <p v-if="savedHistory.error" id="saved-history-dialog-error" class="error" role="alert">{{ savedHistory.error }}</p>
          <footer class="actions leave-actions">
            <button type="button" data-cancel @click="resolveSavedHistoryLeave('stay')">{{ t("继续编辑") }}</button>
            <button type="button" @click="resolveSavedHistoryLeave('discard')">{{ t("放弃并离开") }}</button>
            <button type="button" class="primary" :disabled="!savedHistoryCanSave" @click="resolveSavedHistoryLeave('save')">{{ t("保存并离开") }}</button>
          </footer>
        </template>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.saved-history-scrim { z-index: 400; padding: 16px; overflow-y: auto; }
.saved-history-sheet { position: relative; box-sizing: border-box; width: min(480px, calc(100vw - 32px)); max-height: calc(100vh - 32px); max-height: calc(100dvh - 32px); overflow-y: auto; overscroll-behavior: contain; padding: 26px; color: var(--fg); }
.saved-history-sheet header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.saved-history-sheet h2 { margin: 0; font-family: var(--font-display); font-size: 20px; line-height: 1.4; font-weight: 600; overflow-wrap: anywhere; }
.saved-history-sheet .close { flex-shrink: 0; width: 28px; height: 28px; padding: 0; border-radius: 50%; }
.description { margin: 14px 0 0; color: var(--fg-dim); font-size: 13px; line-height: 1.7; }
.name-field { display: flex; flex-direction: column; gap: 8px; margin-top: 20px; font-size: 12px; }
.name-field input { width: 100%; min-width: 0; box-sizing: border-box; color: var(--fg); font: inherit; font-size: 14px; }
.error { margin: 14px 0 0; color: var(--danger); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.actions { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; margin-top: 24px; }
.actions button { min-height: 36px; padding: 8px 14px; border: 1px solid var(--line); border-radius: 10px; background: var(--glass-control); color: var(--fg); font: inherit; font-size: 13px; font-weight: 600; cursor: pointer; }
.actions button:hover:not(:disabled) { background: var(--glass-inset); }
.actions button:active:not(:disabled) { transform: scale(.98); }
.actions button:disabled { opacity: .5; cursor: default; }
.actions .primary { border-color: transparent; background: var(--accent); color: var(--on-accent); }
.actions .primary:hover:not(:disabled) { background: var(--accent); filter: brightness(1.08); }
</style>
