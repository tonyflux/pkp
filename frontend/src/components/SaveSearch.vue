<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";
import Icon from "./Icon.vue";
import { useAnchoredList, useOutsideClick } from "../dom";
import { t } from "../i18n";
import {
  currentId,
  currentSavedSearch,
  discardSavedHistoryChanges,
  localSearch,
  savedHistory,
  savedHistoryCanSave,
  savedHistoryDirty,
  saveSavedHistoryAs,
  saveSavedHistoryChanges,
  ui,
} from "../store";

const id = useId();
const editing = ref<"save" | "as" | null>(null);
const name = ref("");
const input = ref<HTMLInputElement>();
const trigger = ref<HTMLButtonElement>();
const more = ref<HTMLButtonElement>();
const menu = ref<HTMLElement>();
const menuRows = ref<HTMLElement>();
const menuOpen = ref(false);
const range = computed(() => localSearch.range?.account === currentId.value ? localSearch.range : null);
const selectedRange = computed(() => range.value?.ids !== undefined);
const reason = computed(() => selectedRange.value ? t("所选项目范围不能保存为动态搜索，请先移除所选项范围") : "");
const canSave = computed(() => !!currentId.value && savedHistoryCanSave.value);
const { spot, place } = useAnchoredList(() => more.value, menu, menuRows, menuOpen, () => closeMenu());

watch([currentId, () => currentSavedSearch.value?.id], () => {
  editing.value = null;
  name.value = "";
  closeMenu();
});

function closeMenu(refocus = false) {
  menuOpen.value = false;
  if (refocus) more.value?.focus({ preventScroll: true });
}

async function showMenu() {
  if (menuOpen.value) return;
  spot.placed = false;
  menuOpen.value = true;
  await nextTick();
  if (!menuOpen.value) return;
  place();
  await nextTick();
  if (menuOpen.value) menu.value?.querySelector<HTMLElement>("[role=menuitem]")?.focus({ preventScroll: true });
}

function onMoreKey(event: KeyboardEvent) {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  void showMenu();
}

function onMenuKey(event: KeyboardEvent) {
  if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
    event.preventDefault();
    menu.value?.querySelector<HTMLElement>("[role=menuitem]")?.focus();
  } else if (event.key === "Escape" || event.key === "Tab") {
    event.preventDefault();
    closeMenu(true);
  }
}

function onMenuFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null;
  if (next && !menu.value?.contains(next) && !more.value?.contains(next)) closeMenu();
}

async function begin(mode: "save" | "as") {
  if (!canSave.value) return;
  closeMenu();
  if (editing.value !== mode) {
    name.value = (currentSavedSearch.value?.name ?? ui.query.trim()).slice(0, 100);
    editing.value = mode;
  }
  await nextTick();
  input.value?.focus();
  input.value?.select();
}

async function cancel(refocus = true) {
  editing.value = null;
  await nextTick();
  if (refocus) (currentSavedSearch.value ? more.value : trigger.value)?.focus({ preventScroll: true });
}

function saveAs() {
  if (!editing.value || !canSave.value || !name.value.trim()) return;
  if (saveSavedHistoryAs(name.value)) cancel();
}

function onRootKey(event: KeyboardEvent) {
  if (event.key !== "Escape" || (!menuOpen.value && !editing.value)) return;
  event.preventDefault();
  if (menuOpen.value) closeMenu(true);
  else cancel();
}

useOutsideClick(`[data-save-search-menu="${id}"]`, () => menuOpen.value, () => closeMenu());
function onResize() { closeMenu(); }
onMounted(() => window.addEventListener("resize", onResize));
onBeforeUnmount(() => window.removeEventListener("resize", onResize));
</script>

<template>
  <div class="save-search" :data-save-search-menu="id" @keydown.stop="onRootKey">
    <template v-if="currentSavedSearch">
      <span class="save-search-status" :class="{ dirty: savedHistoryDirty }" role="status">{{ savedHistoryDirty ? t("未保存") : t("已保存") }}</span>
      <span v-if="savedHistoryDirty" class="save-search-actions">
        <button type="button" :disabled="!canSave" :title="reason || undefined" @click="saveSavedHistoryChanges">{{ t("保存更改") }}</button>
        <button type="button" @click="discardSavedHistoryChanges">{{ t("放弃更改") }}</button>
      </span>
      <button
        ref="more"
        type="button"
        class="save-search-more"
        :aria-label="t('更多操作')"
        :title="t('更多操作')"
        aria-haspopup="menu"
        :aria-expanded="menuOpen"
        :aria-controls="menuOpen ? `${id}-menu` : undefined"
        @click="menuOpen ? closeMenu(true) : showMenu()"
        @keydown="onMoreKey"
      ><Icon name="more" :size="16" /></button>
    </template>
    <button
      v-else-if="!editing"
      ref="trigger"
      type="button"
      class="save-search-trigger"
      :disabled="!canSave"
      :title="reason || t('已保存历史')"
      @click="begin('save')"
    ><Icon name="plus" :size="14" />{{ t("保存搜索") }}</button>
    <form v-if="editing" class="save-search-form" @submit.prevent="saveAs">
      <label :for="`${id}-name`">{{ editing === "as" ? t("另存为…") : t("保存搜索") }}</label>
      <input :id="`${id}-name`" ref="input" v-model="name" maxlength="100" :aria-label="t('搜索名称')" :placeholder="t('搜索名称')" required />
      <span class="save-search-actions">
        <button type="submit" :disabled="!name.trim() || !canSave" :title="reason || undefined">{{ t("保存") }}</button>
        <button type="button" @click="cancel()">{{ t("取消") }}</button>
      </span>
      <span class="save-search-note">{{ t("仅保存在此电脑的当前账号中") }}</span>
    </form>
    <span v-if="reason" class="save-search-note">{{ reason }}</span>
    <span v-if="savedHistory.error && !savedHistory.dialog" class="save-search-error" role="alert">{{ savedHistory.error }}</span>
    <Teleport to="body">
      <div
        v-if="menuOpen"
        :id="`${id}-menu`"
        ref="menu"
        class="save-search-menu glass glass-sheet"
        role="menu"
        :aria-label="t('更多操作')"
        :data-save-search-menu="id"
        :style="{ top: `${spot.top}px`, left: `${spot.left}px`, maxHeight: `${spot.maxHeight}px`, visibility: spot.placed ? 'visible' : 'hidden' }"
        @keydown.stop="onMenuKey"
        @focusout="onMenuFocusOut"
      >
        <div ref="menuRows" class="save-search-menu-rows">
          <button type="button" role="menuitem" :aria-disabled="!canSave" :title="reason || undefined" @click="begin('as')">{{ t("另存为…") }}</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.save-search { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; min-width: 0; max-width: 100%; }
.save-search-form { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; min-width: 0; max-width: 100%; flex-basis: 100%; }
.save-search-form label { color: var(--fg-dim); font-size: 12px; }
.save-search-actions { display: inline-flex; align-items: center; flex-wrap: wrap; gap: 2px; }
.save-search button, .save-search-menu button { min-height: 32px; border: 0; border-radius: 9px; padding: 0 10px; color: var(--accent); background: transparent; font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; }
.save-search button:hover:not(:disabled), .save-search-menu button:hover:not([aria-disabled="true"]) { background: var(--accent-soft); }
.save-search button:active:not(:disabled), .save-search-menu button:active:not([aria-disabled="true"]) { transform: scale(.97); }
.save-search button:disabled, .save-search-menu button[aria-disabled="true"] { opacity: .5; cursor: default; }
.save-search-trigger { display: inline-flex; align-items: center; gap: 5px; }
.save-search .save-search-more { display: inline-flex; align-items: center; justify-content: center; width: 32px; padding: 0; color: var(--fg-dim); }
.save-search-status { color: var(--fg-dim); font-size: 12px; white-space: nowrap; }
.save-search-status.dirty { color: var(--accent); }
.save-search input { width: 180px; max-width: 100%; min-width: 0; min-height: 32px; box-sizing: border-box; padding: 6px 9px; border: 1px solid var(--line); border-radius: 8px; background: var(--glass-control); color: var(--fg); font: inherit; font-size: 12px; }
.save-search-note { color: var(--fg-dim); font-size: 11px; overflow-wrap: anywhere; }
.save-search-error { color: var(--danger); font-size: 12px; flex-basis: 100%; overflow-wrap: anywhere; }
.save-search-menu { position: fixed; z-index: 310; min-width: 144px; max-width: calc(100vw - 16px); border-radius: 12px; overflow: hidden; }
.save-search-menu-rows { padding: 4px; overflow-y: auto; max-height: inherit; }
.save-search-menu button { display: block; width: 100%; text-align: start; color: var(--fg); }
.save-search button:focus-visible, .save-search-menu button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
