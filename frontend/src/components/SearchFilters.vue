<script setup lang="ts">
import { nextTick, ref, useId, watch } from "vue";
import Icon from "./Icon.vue";
import { useAnchoredList, useOutsideClick } from "../dom";
import { t } from "../i18n";
import { FILTER_OPTIONS, addToken } from "../searchquery";
import { currentId, currentSavedSearch, searchParse, ui } from "../store";

defineProps<{ iconOnly?: boolean }>();
const id = useId();
const open = ref(false);
const trigger = ref<HTMLButtonElement>();
const menu = ref<HTMLElement>();
const rows = ref<HTMLElement>();
const { spot, place } = useAnchoredList(() => trigger.value, menu, rows, open, () => close());
useOutsideClick(`[data-search-filters="${id}"]`, () => open.value, () => close());
watch([currentId, () => ui.route, () => ui.view, () => currentSavedSearch.value?.id], () => close());

function close(refocus = false) {
  open.value = false;
  if (refocus) trigger.value?.focus({ preventScroll: true });
}

async function show(last = false) {
  spot.placed = false;
  open.value = true;
  await nextTick();
  if (!open.value) return;
  place();
  await nextTick();
  const items = rows.value?.querySelectorAll<HTMLButtonElement>("[role=menuitem]");
  if (open.value && items?.length) items[last ? items.length - 1 : 0].focus({ preventScroll: true });
}

function onTriggerKey(event: KeyboardEvent) {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  void show(event.key === "ArrowUp");
}

function onMenuKey(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    close(true);
  } else if (event.key === "Tab") {
    event.preventDefault();
    close(true);
  } else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
    event.preventDefault();
    const items = Array.from(rows.value?.querySelectorAll<HTMLButtonElement>("[role=menuitem]") ?? []);
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const index = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
    items[index]?.focus();
  }
}

function add(token: string) {
  ui.query = addToken(ui.query, token);
  close(true);
}

function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null;
  if (next && !menu.value?.contains(next) && !trigger.value?.contains(next)) close();
}
</script>

<template>
  <div class="search-filters" :data-search-filters="id" style="--wails-draggable: no-drag" @keydown.stop>
    <button
      ref="trigger"
      type="button"
      class="filter-trigger"
      :class="{ 'icon-only': iconOnly, active: iconOnly && (open || searchParse.chips.length > 0) }"
      :title="t('添加条件')"
      :aria-label="t('添加条件')"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="open ? `${id}-menu` : undefined"
      @click="open ? close(true) : show()"
      @keydown="onTriggerKey"
    >
      <Icon :name="iconOnly ? 'filter' : 'plus'" :size="iconOnly ? 20 : 14" />
      <span v-if="!iconOnly">{{ t("添加条件") }}</span>
    </button>
    <Teleport to="body">
      <div
        v-if="open"
        :id="`${id}-menu`"
        ref="menu"
        class="filter-menu glass glass-sheet"
        role="menu"
        :aria-label="t('搜索条件')"
        :data-search-filters="id"
        :style="{ top: `${spot.top}px`, left: `${spot.left}px`, maxHeight: `${Math.min(360, spot.maxHeight)}px`, visibility: spot.placed ? 'visible' : 'hidden' }"
        @keydown.stop="onMenuKey"
        @focusout="onFocusOut"
      >
        <div ref="rows" class="filter-rows">
          <div v-for="group in FILTER_OPTIONS" :key="group.group" class="filter-group" role="group" :aria-label="group.group">
            <span>{{ group.group }}</span>
            <div>
              <button v-for="option in group.options" :key="option.token" type="button" role="menuitem" @click="add(option.token)">{{ option.label }}</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.search-filters { display: flex; flex: none; }
.filter-trigger { min-height: 32px; padding: 0 10px; border: 0; border-radius: 9px; background: transparent; color: var(--accent); font-size: 12px; font-weight: 600; }
.filter-trigger:hover:not(:disabled) { background: var(--accent-soft); }
.filter-trigger.icon-only { width: 52px; height: 44px; min-height: 44px; padding: 0; border: 1px solid var(--line); border-radius: 18px; background: var(--glass-control); color: var(--fg); }
.filter-trigger.active { background: var(--accent-soft); border-color: color-mix(in srgb, var(--accent) 35%, transparent); color: var(--accent); }
.filter-menu { position: fixed; z-index: 310; width: 340px; max-width: calc(100vw - 16px); border-radius: 14px; overflow: hidden; }
.filter-rows { max-height: inherit; overflow-y: auto; padding: 10px; }
.filter-group + .filter-group { margin-top: 8px; }
.filter-group > span { display: block; margin: 0 4px 4px; color: var(--fg-dim); font-size: 11px; }
.filter-group > div { display: flex; flex-wrap: wrap; gap: 4px; }
.filter-group button { min-height: 28px; padding: 0 10px; border-radius: 8px; font-size: 12px; }
.filter-trigger:active:not(:disabled), .filter-group button:active:not(:disabled) { transform: scale(.97); }
.filter-trigger:focus-visible, .filter-group button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 959px) {
  .filter-trigger.icon-only { width: 36px; }
}
</style>
