<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import { useOutsideClick } from "../dom";
import { t } from "../i18n";
import { currentId, currentSavedSearch, openSavedHistoryDialog, openSavedSearch, savedHistory, ui } from "../store";
import { loadSavedSearches, savedSearches, type SavedSearch } from "../savedsearches";

const props = defineProps<{ compact: boolean }>();
const emit = defineEmits<{ popover: [open: boolean] }>();
const collapsed = ref(false);
const open = ref(false);
const trigger = ref<HTMLButtonElement | null>(null);
const panel = ref<HTMLElement | null>(null);
const position = ref({ left: "0px", top: "0px" });
const menu = ref<{ account: string; id: string; name: string; anchor: HTMLButtonElement } | null>(null);
const menuPanel = ref<HTMLElement | null>(null);
const menuPosition = ref({ left: "0px", top: "0px" });
const items = computed(() => savedSearches.account === currentId.value ? savedSearches.items : []);
const storageError = computed(() => savedSearches.account === currentId.value ? savedSearches.error : "");

watch(currentId, (account) => {
  close();
  loadSavedSearches(account);
}, { immediate: true });
watch(() => props.compact, () => close());
watch(open, (value) => emit("popover", value));
watch(() => savedHistory.dialog, (dialog) => { if (dialog) close(); });
useOutsideClick("[data-saved-searches]", () => open.value, () => close());
useOutsideClick("[data-saved-history-menu], [data-saved-history-action]", () => !!menu.value, () => closeMenu());

function closeMenu(focus = false) {
  const anchor = menu.value?.anchor;
  menu.value = null;
  if (focus && anchor?.isConnected) anchor.focus({ preventScroll: true });
}

function close(focus = false) {
  closeMenu();
  open.value = false;
  if (focus) trigger.value?.focus({ preventScroll: true });
}

async function toggle() {
  if (!props.compact) { closeMenu(); collapsed.value = !collapsed.value; return; }
  if (open.value) { close(true); return; }
  const rect = trigger.value?.getBoundingClientRect();
  if (!rect) return;
  position.value = {
    left: `${Math.max(8, Math.min(rect.right + 12, window.innerWidth - 304))}px`,
    top: `${Math.max(8, Math.min(rect.top, window.innerHeight - 360))}px`,
  };
  open.value = true;
  await nextTick();
  panel.value?.focus({ preventScroll: true });
}

function run(item: SavedSearch) {
  if (savedSearches.account !== currentId.value) return;
  close(props.compact);
  openSavedSearch(item.id);
}

async function toggleMenu(item: SavedSearch, event: MouseEvent | KeyboardEvent) {
  if (savedSearches.account !== currentId.value) return;
  const anchor = event.currentTarget as HTMLButtonElement;
  if (menu.value?.id === item.id) { closeMenu(true); return; }
  const rect = anchor.getBoundingClientRect();
  const selected = { account: currentId.value, id: item.id, name: item.name, anchor };
  menu.value = selected;
  menuPosition.value = { left: `${rect.right}px`, top: `${rect.bottom + 4}px` };
  await nextTick();
  if (menu.value?.id !== selected.id || menu.value.account !== selected.account || !menuPanel.value) return;
  const box = menuPanel.value.getBoundingClientRect();
  menuPosition.value = {
    left: `${Math.max(8, Math.min(rect.right - box.width, window.innerWidth - box.width - 8))}px`,
    top: `${Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - box.height - 8))}px`,
  };
  menuPanel.value.querySelector<HTMLButtonElement>("button")?.focus({ preventScroll: true });
}

function act(kind: "edit" | "rename" | "delete") {
  const selected = menu.value;
  if (!selected || selected.account !== currentId.value) { close(); return; }
  closeMenu(true);
  if (props.compact) close(true);
  if (kind === "edit") openSavedSearch(selected.id, true);
  else openSavedHistoryDialog(kind, selected.id);
}

function onMenuKey(event: KeyboardEvent) {
  if (event.key === "Tab") { closeMenu(true); return; }
  const buttons = Array.from(menuPanel.value?.querySelectorAll<HTMLButtonElement>("button") ?? []);
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  let next: number;
  if (event.key === "ArrowDown") next = (index + 1) % buttons.length;
  else if (event.key === "ArrowUp") next = (index - 1 + buttons.length) % buttons.length;
  else if (event.key === "Home") next = 0;
  else if (event.key === "End") next = buttons.length - 1;
  else return;
  event.preventDefault();
  buttons[next]?.focus();
}

function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || savedHistory.dialog || (!menu.value && !open.value)) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (menu.value) closeMenu(true);
  else close(true);
}

function onResize() { close(); }
function onScroll(event: Event) {
  if (menu.value && !(event.target instanceof Node && menuPanel.value?.contains(event.target))) closeMenu();
}
function storage(event: StorageEvent) {
  if (event.key === null || event.key === `pkp.savedSearches.${currentId.value}`) {
    closeMenu();
    loadSavedSearches(currentId.value);
  }
}
onMounted(() => {
  window.addEventListener("storage", storage);
  window.addEventListener("resize", onResize);
  window.addEventListener("scroll", onScroll, true);
  window.addEventListener("keydown", onEscape, true);
});
onBeforeUnmount(() => {
  window.removeEventListener("storage", storage);
  window.removeEventListener("resize", onResize);
  window.removeEventListener("scroll", onScroll, true);
  window.removeEventListener("keydown", onEscape, true);
  emit("popover", false);
});
</script>

<template>
  <section class="saved-searches" :class="{ rail: compact }" data-saved-searches>
    <button ref="trigger" class="saved-heading" type="button" data-saved-history-heading :title="t('已保存历史')" :aria-label="t('已保存历史')" :aria-expanded="compact ? open : !collapsed" :aria-haspopup="compact ? 'dialog' : undefined" @click="toggle">
      <Icon :name="compact ? 'search' : collapsed ? 'chevronRight' : 'chevronDown'" :size="compact ? 20 : 12" />
      <span v-if="!compact">{{ t("已保存历史") }}</span>
    </button>
    <Teleport to="body" :disabled="!compact">
      <div v-if="!compact || open" v-show="compact || !collapsed" ref="panel" class="saved-panel" :class="{ 'saved-popover': compact, glass: compact }" :style="compact ? position : undefined" :role="compact ? 'dialog' : 'group'" :aria-label="t('已保存历史')" :tabindex="compact ? -1 : undefined" data-saved-searches @keydown.stop>
        <div v-if="compact" class="saved-popover-head"><strong>{{ t("已保存历史") }}</strong><button type="button" :aria-label="t('关闭')" @click="close(true)"><Icon name="close" :size="15" /></button></div>
        <p v-if="storageError" class="saved-error" role="alert">{{ t("读取已保存历史失败：{error}", { error: storageError }) }} <button type="button" @click="loadSavedSearches(currentId)">{{ t("重试") }}</button></p>
        <p v-else-if="!items.length" class="saved-empty">{{ t("搜索后选择“保存搜索”，在此快速打开。") }}</p>
        <div v-for="item in items" :key="item.id" class="saved-row">
          <button type="button" class="saved-open" :class="{ current: ui.route === 'files' && currentSavedSearch?.id === item.id }" :aria-current="ui.route === 'files' && currentSavedSearch?.id === item.id ? 'page' : undefined" :title="item.query" @click="run(item)"><Icon name="search" :size="14" /><span>{{ item.name }}</span></button>
          <button type="button" class="saved-action" data-saved-history-action :aria-label="t('管理已保存历史：{name}', { name: item.name })" :title="t('更多操作')" aria-haspopup="menu" :aria-expanded="menu?.id === item.id" @click="toggleMenu(item, $event)" @keydown.down.prevent.stop="toggleMenu(item, $event)"><Icon name="more" :size="15" /></button>
        </div>
      </div>
    </Teleport>
    <Teleport to="body">
      <div v-if="menu" ref="menuPanel" class="menu glass saved-history-menu" role="menu" :aria-label="t('管理已保存历史：{name}', { name: menu.name })" :style="menuPosition" data-saved-searches data-saved-history-menu @keydown.stop="onMenuKey">
        <button type="button" role="menuitem" @click="act('edit')"><Icon name="search" :size="15" />{{ t("编辑条件") }}</button>
        <button type="button" role="menuitem" @click="act('rename')"><Icon name="font" :size="15" />{{ t("重命名") }}</button>
        <button type="button" role="menuitem" class="danger" @click="act('delete')"><Icon name="trash" :size="15" />{{ t("删除") }}</button>
      </div>
    </Teleport>
  </section>
</template>

<style scoped>
.saved-searches { margin: 14px 0 0; }
.saved-heading { display: flex; align-items: center; gap: 7px; width: 100%; height: 30px; border: 0; padding: 0 10px; background: transparent; color: var(--fg-dim); font: inherit; font-size: 11px; text-align: left; cursor: pointer; }
.saved-heading span { white-space: nowrap; }
.rail .saved-heading { justify-content: center; width: 54px; height: 38px; padding: 0; border-radius: 12px; color: var(--fg-dim); }
.saved-panel { min-width: 0; }
.saved-panel button { color: var(--fg-dim); background: transparent; border: 0; border-radius: 6px; font: inherit; font-size: 11px; cursor: pointer; }
.saved-panel button:active, .saved-heading:active { transform: scale(.97); }
.saved-panel button:hover, .saved-heading:hover { background: var(--glass-inset); color: var(--fg); }
.saved-row { display: flex; align-items: center; min-width: 0; gap: 1px; }
.saved-open { display: flex; align-items: center; flex: 1; min-width: 0; height: 34px; gap: 7px; padding: 0 7px; text-align: left; }
.saved-open :deep(svg) { flex-shrink: 0; }
.saved-open span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.saved-open.current { background: var(--accent-soft); color: var(--accent); }
.saved-action { display: grid; place-items: center; width: 28px; height: 30px; flex-shrink: 0; padding: 0; }
.saved-empty, .saved-error { margin: 4px 8px 8px; font-size: 11px; line-height: 1.6; color: var(--fg-dim); }
.saved-error { color: var(--danger); overflow-wrap: anywhere; }
.saved-popover { position: fixed; z-index: 300; width: 280px; max-width: calc(100vw - 32px); max-height: min(340px, calc(100vh - 16px)); padding: 10px; border-radius: 16px; overflow: auto; }
.saved-popover-head { display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 4px 6px 8px; color: var(--fg); font-size: 13px; }
.saved-popover-head button { display: grid; place-items: center; width: 26px; height: 26px; }
.saved-history-menu { position: fixed; z-index: 310; min-width: 170px; max-width: calc(100vw - 16px); max-height: calc(100dvh - 16px); overflow-y: auto; }
.saved-history-menu > button { display: flex; align-items: center; gap: 9px; width: 100%; border: 0; padding: 7px 10px; background: transparent; font: inherit; font-size: 13px; text-align: left; cursor: pointer; }
.saved-history-menu > button:hover, .saved-history-menu > button:focus-visible { background: var(--glass-inset); }
</style>
