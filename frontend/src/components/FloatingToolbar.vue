<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import AccountAvatar from "./AccountAvatar.vue";
import AccountPopover from "./AccountPopover.vue";
import SearchFilters from "./SearchFilters.vue";
import { WORKSPACE_QUERY, modKey, shortcutText, useMedia } from "../dom";
import { maskLogin, shownPath } from "../format";
import { parseSearch } from "../searchquery";
import { t } from "../i18n";
import { useCommand } from "../shortcuts";
import { sync } from "../syncstore";
import type { AISearchHit } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import {
  accountLabel,
  accounts,
  aiReady,
  aiSettings,
  askAssistant,
  assistant as assistantPage,
  canGoBack,
  cleanup,
  closeViewer,
  currentAccount,
  drive,
  goBack,
  indexReady,
  localSearchShown,
  openAISettings,
  openViewer,
  play,
  rescanCleanup,
  refresh,
  revealFile,
  currentId,
  searchPreview,
  savedHistoryFocus,
  setPanelOpen,
  videoWorksShown,
  ui,
  viewFile,
} from "../store";

const search = ref<HTMLInputElement>();

const backTitle = computed(() =>
  t("{name}（{note}）", {
    name: localSearchShown.value ? t("返回文件列表") : ui.view === "all" && ui.scope === "drive" ? t("返回当前文件夹") : t("返回上一级"),
    note: shortcutText("["),
  }),
);


// The context column's switch, on file browsing only. Under 960px the
// column is the Info page, so the same button turns between the two pages.
const browsing = computed(() => ui.route === "files" || ui.route === "viewer");
// AI Assistant has a toolbar of its own: its name, the
// account it works in, the model it would ask; no file search, sort or Cloud add.
const assistant = computed(() => ui.route === "assistant");
const assistantAccount = computed(
  () => accountLabel(currentAccount.value) || maskLogin(drive.status?.account ?? "") || t("当前账号"),
);
// The page the assistant shows, in the toolbar's name: Plan preview, Receipt.
const assistantTitle = computed(() =>
  assistantPage.screen === "plan" ? t("AI 助手 · 计划预览") : assistantPage.screen === "receipt" ? t("AI 助手 · 回执") : t("AI 助手"),
);
// Cleanup: the account and the range it scans, that the
// scan reads only this computer's index, and Rescan.
const cleanupPage = computed(() => ui.route === "cleanup");
const cleanupScope = computed(() => {
  const at = cleanup.scope.at(-1);
  return at ? t("清理「{name}」", { name: at.name }) : t("全盘清理");
});
const narrow = useMedia(WORKSPACE_QUERY);
const panelShown = computed(() => (narrow.value ? ui.workspace === "info" : ui.panelOpen));
const panelTitle = computed(() => {
  if (narrow.value) return panelShown.value ? t("返回文件") : t("查看信息（{key}I）", { key: modKey });
  return panelShown.value ? t("收起信息栏") : t("显示信息栏（{key}I）", { key: modKey });
});

function togglePanel() {
  if (narrow.value) ui.workspace = panelShown.value ? "files" : "info";
  else setPanelOpen(!ui.panelOpen);
}

// Under 960px the Switch account entry moves here from the nav: the
// avatar at the leading edge, and the panel drops under it.
const accountButton = ref<HTMLElement | null>(null);
const accountsHere = computed(() => narrow.value && ui.accountsOpen);
const accountTitle = computed(() => {
  const a = currentAccount.value;
  const name = accountLabel(a) || maskLogin(drive.status?.account ?? "") || t("未登录");
  const login = maskLogin(a?.login || drive.status?.account || "");
  let text = accountLabel(a) ? t("切换账号，当前「{name}」", { name }) : t("切换账号，当前{name}", { name });
  if (login) text = t("{text}，{login}", { text, login });
  if (accounts.switching) return t("{text}，正在切换", { text });
  if (drive.status?.needsPassword || a?.needsPassword) return t("{text}，需要重新登录", { text });
  return text;
});

// The search is the page's own (ui.query, ui.shareQuery, ui.transferQuery):
// files and folders where files are browsed, the list on My shares and on
// Transfer center. On a page with no list of its own (Settings) it searches the files,
// and typing takes the page there, so a search never runs out of sight.
const searchModel = computed<string>({
  get: () => (ui.route === "history" ? ui.playQuery : ui.route === "sync" ? sync.query : ui.route === "shares" ? ui.shareQuery : ui.route === "transfers" ? ui.transferQuery : ui.query),
  set: (value) => {
    if (ui.route === "history") ui.playQuery = value;
    else if (ui.route === "sync") sync.query = value;
    else if (ui.route === "shares") ui.shareQuery = value;
    else if (ui.route === "transfers") ui.transferQuery = value;
    else {
      ui.query = value;
      // A search is a list: typing over the viewer goes back to the files.
      if (ui.route === "viewer" && value.trim()) closeViewer();
      if (!browsing.value && value.trim()) ui.route = "files";
    }
  },
});
const searchPlaceholder = computed(() => {
  if (ui.route === "history") return t("搜索播放记录");
  if (ui.route === "sync") return t("搜索同步文件夹");
  if (ui.route === "shares") return t("搜索分享");
  if (ui.route === "transfers") return t("搜索传输任务");
  if (ui.view === "all" && ui.scope === "drive") return t("搜索整个云盘的文件");
  return accounts.list.length > 1 ? t("搜索当前账号的文件") : t("搜索文件或文件夹");
});

/* ---------- ⌘K: the dropdown under the box ---------- */

// While the box has focus and text, a list drops under the toolbar: what
// the text reads as (chips), a few instant results from the index, then
// Filter all files (Enter) and Ask AI. Nothing in it asks a model; Ask AI only
// routes to the assistant, which asks before anything is sent.
const focused = ref(false);
const fileSearch = computed(() => !videoWorksShown.value && ui.route !== "history" && ui.route !== "shares" && ui.route !== "transfers" && ui.route !== "sync");
const dropText = computed(() => (fileSearch.value ? ui.query.trim() : ""));
const dropDismissed = ref(false);
const dropOpen = computed(() => focused.value && !!dropText.value && !dropDismissed.value);
const dropChips = computed(() => parseSearch(dropText.value, new Date()).chips);
const dropHits = ref<AISearchHit[]>([]);
const dropLoading = ref(false);
// The highlighted row: hits first, then Filter, then Ask AI; -1 = none (Enter filters).
const active = ref(-1);
const askIndex = computed(() => dropHits.value.length + 1);

let dropTimer: number | undefined;
let dropSerial = 0;
watch(dropText, (text) => {
  dropDismissed.value = false;
  active.value = -1;
  clearTimeout(dropTimer);
  const serial = ++dropSerial;
  if (!text || !indexReady.value) {
    dropHits.value = [];
    dropLoading.value = false;
    return;
  }
  dropLoading.value = true;
  dropTimer = setTimeout(async () => {
    const hits = await searchPreview(text);
    if (serial !== dropSerial) return;
    dropHits.value = hits;
    dropLoading.value = false;
  }, 160);
});

function closeDrop() {
  dropDismissed.value = true;
  active.value = -1;
}

function openHit(hit: AISearchHit) {
  closeDrop();
  search.value?.blur();
  if (hit.folder) void viewFile({ account: currentId.value, fileId: hit.id });
  else if (hit.playable) void play(hit, [hit]);
  else if (hit.preview) void openViewer(hit, [hit]);
  else void revealFile({ account: currentId.value, fileId: hit.id });
}

// Filter all files: the box already drives the file page; this shows it.
function filterAll() {
  closeDrop();
  search.value?.blur();
  ui.route = "files";
}

function askAI() {
  const text = dropText.value;
  closeDrop();
  search.value?.blur();
  void askAssistant(text);
}

function choose(index: number) {
  if (index >= 0 && index < dropHits.value.length) openHit(dropHits.value[index]);
  else if (index === askIndex.value) askAI();
  else filterAll();
}

function onSearchKey(event: KeyboardEvent) {
  if (event.isComposing) return;
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    if (!dropText.value) return;
    event.preventDefault();
    if (!dropOpen.value) {
      dropDismissed.value = false;
      return;
    }
    const count = askIndex.value + 1;
    active.value = event.key === "ArrowDown" ? (active.value + 1) % count : (active.value - 1 + count) % count;
    return;
  }
  if (event.key === "Enter" && dropOpen.value) {
    event.preventDefault();
    choose(active.value);
    return;
  }
  // Esc puts the dropdown away first; the next one clears the box (onKey).
  if (event.key === "Escape" && dropOpen.value) {
    event.preventDefault();
    event.stopPropagation();
    closeDrop();
  }
}

// ⌘K / ⌘F (shortcuts.ts) put the cursor in the box.
useCommand("search", () => {
  search.value?.focus();
  search.value?.select();
});

watch(() => savedHistoryFocus.serial, async () => {
  await nextTick();
  search.value?.focus();
  search.value?.select();
});

function onKey(event: KeyboardEvent) {
  if (event.key === "Escape" && document.activeElement === search.value) {
    searchModel.value = "";
    search.value?.blur();
  }
}

onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <div class="toolbar glass" :class="{ raised: accountsHere || dropOpen }" style="--wails-draggable: drag">
    <button
      v-if="narrow"
      ref="accountButton"
      class="account"
      style="--wails-draggable: no-drag"
      data-account-trigger
      :title="accountTitle"
      :aria-label="accountTitle"
      aria-haspopup="dialog"
      :aria-expanded="ui.accountsOpen"
      @click="ui.accountsOpen = !ui.accountsOpen"
    >
      <AccountAvatar :account="currentAccount" :size="32" />
    </button>

    <template v-if="assistant">
      <div class="title">
        <Icon name="spark" :size="20" />
        <strong>{{ assistantTitle }}</strong>
        <span>{{ t("{account} · 我的云盘", { account: assistantAccount }) }}</span>
      </div>
      <button
        class="model-pill"
        :class="{ on: aiReady }"
        type="button"
        style="--wails-draggable: no-drag"
        :title="aiReady ? t('模型：{model}，点按打开设置 · AI', { model: aiSettings.value?.model ?? '' }) : t('还没有连接模型，点按打开设置 · AI')"
        @click="openAISettings"
      >
        <i aria-hidden="true" />
        <span>{{ aiReady ? t("已连接 · {model}", { model: aiSettings.value?.model ?? "" }) : t("未连接模型 · 本地功能可用") }}</span>
      </button>
      <button class="quiet ai-settings" type="button" style="--wails-draggable: no-drag" :aria-label="t('AI 设置')" :title="t('AI 设置')" @click="openAISettings">
        <Icon name="tune" :size="18" />
      </button>
    </template>

    <template v-else-if="cleanupPage">
      <div class="title cleanup-title">
        <i aria-hidden="true" />
        <strong>{{ assistantAccount }}</strong>
        <span>{{ cleanupScope }}</span>
      </div>
      <span class="local" :title="t('扫描只读这台电脑上的本地索引，不请求云端')">
        <Icon name="shield" :size="16" />
        {{ t("本地索引扫描") }}
      </span>
      <button
        class="quiet rescan"
        style="--wails-draggable: no-drag"
        :disabled="!!cleanup.running || !indexReady"
        :aria-label="t('重新扫描')"
        @click="rescanCleanup()"
      >
        <Icon name="restore" :size="18" />
        <span class="label">{{ t("重新扫描") }}</span>
      </button>
    </template>

    <template v-else>
      <!-- Back, refresh and sort act on the file list: only where it is. -->
      <div v-if="ui.route === 'files'" class="nav-buttons glass-well" style="--wails-draggable: no-drag">
        <button class="quiet icon" :disabled="!canGoBack" :title="backTitle" @click="goBack">
          <Icon name="chevronLeft" :size="20" />
        </button>
        <button class="quiet icon" :title="t('{name}（{note}）', { name: t('刷新'), note: shortcutText('R') })" @click="refresh()">
          <Orbit v-if="drive.listLoading" :label="t('刷新中')" />
          <Icon v-else name="restore" :size="20" />
        </button>
      </div>

      <div class="search-wrap" style="--wails-draggable: no-drag">
        <label class="search glass-well">
          <Icon name="search" :size="20" />
          <input
            ref="search"
            v-model="searchModel"
            type="text"
            :placeholder="searchPlaceholder"
            :aria-label="searchPlaceholder"
            spellcheck="false"
            role="combobox"
            aria-autocomplete="list"
            aria-controls="search-drop"
            :aria-expanded="dropOpen"
            :aria-activedescendant="dropOpen && active >= 0 ? `search-drop-${active}` : undefined"
            @focus="focused = true; dropDismissed = false"
            @blur="focused = false"
            @keydown="onSearchKey"
          />
          <button v-if="searchModel" class="quiet icon clear" :title="t('清除')" @click="searchModel = ''">
            <Icon name="close" :size="14" />
          </button>
          <span v-else class="keys" aria-hidden="true">
            <kbd>{{ modKey }}</kbd>
            <kbd>K</kbd>
          </span>
        </label>

        <!-- mousedown.prevent keeps focus in the box, so a click is a choice, not a blur. -->
        <div v-if="dropOpen" id="search-drop" class="search-drop" role="listbox" :aria-label="t('搜索建议')" @mousedown.prevent>
          <div v-if="dropChips.length" class="drop-chips" aria-hidden="true">
            <span v-for="c in dropChips" :key="c.key">{{ c.label }}</span>
          </div>
          <p v-if="!indexReady" class="drop-note">{{ t("本地索引建立中，暂时只能按名称筛选当前文件夹") }}</p>
          <p v-else-if="dropLoading && !dropHits.length" class="drop-note">{{ t("正在本地索引中查找…") }}</p>
          <p v-else-if="!dropHits.length" class="drop-note">{{ t("没有即时结果，按回车在全部文件中筛选") }}</p>
          <div
            v-for="(hit, i) in dropHits"
            :id="`search-drop-${i}`"
            :key="hit.id"
            class="drop-row"
            :class="{ active: active === i }"
            role="option"
            :aria-selected="active === i"
            @mouseenter="active = i"
            @click="openHit(hit)"
          >
            <Icon :name="hit.folder ? 'folder' : hit.category === 'video' ? 'film' : hit.category === 'image' ? 'image' : 'file'" :size="16" />
            <span class="drop-words">
              <strong>{{ hit.name }}</strong>
              <small>{{ hit.path ? shownPath(hit.path) : t("全部文件") }}</small>
            </span>
          </div>
          <div
            :id="`search-drop-${askIndex - 1}`"
            class="drop-row drop-action"
            :class="{ active: active === askIndex - 1 }"
            role="option"
            :aria-selected="active === askIndex - 1"
            @mouseenter="active = askIndex - 1"
            @click="filterAll"
          >
            <Icon name="filter" :size="16" />
            <span class="drop-words"><strong>{{ t("在全部文件中筛选") }}</strong></span>
            <kbd>↩</kbd>
          </div>
          <div
            :id="`search-drop-${askIndex}`"
            class="drop-row drop-action"
            :class="{ active: active === askIndex }"
            role="option"
            :aria-selected="active === askIndex"
            @mouseenter="active = askIndex"
            @click="askAI"
          >
            <Icon name="spark" :size="16" />
            <span class="drop-words"><strong>{{ t("问 AI：{text}", { text: dropText }) }}</strong><small>{{ t("发送前先确认数据范围") }}</small></span>
          </div>
        </div>
      </div>

      <SearchFilters v-if="ui.route === 'files' && !videoWorksShown" icon-only />

      <button
        v-if="browsing && !videoWorksShown"
        class="panel-toggle"
        :class="{ on: panelShown }"
        style="--wails-draggable: no-drag"
        data-panel-toggle
        :title="panelTitle"
        :aria-label="panelTitle"
        :aria-pressed="panelShown"
        @click="togglePanel"
      >
        <Icon name="panel" :size="20" />
      </button>

      <button v-if="ui.route !== 'sync'" class="primary cloud" style="--wails-draggable: no-drag" :aria-label="t('云添加')" @click="ui.cloudAddOpen = true">
        <Icon name="cloudAdd" :size="22" />
        <span class="label">{{ t("云添加") }}</span>
      </button>
    </template>

    <AccountPopover v-if="accountsHere" placement="toolbar" :trigger="accountButton" />
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 18px;
  height: 60px;
  padding: 0 8px;
  border-radius: var(--radius-island);
}

/* While the account panel is open it must sit above the canvas, which
   follows the toolbar in the document. */
.toolbar.raised {
  z-index: 30;
}

/* A 40px hit around the 32px avatar. */
.account {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
  padding: 0;
  border-radius: 50%;
  border-color: transparent;
  background: transparent;
}

.account:hover:not(:disabled),
.account[aria-expanded="true"] {
  border-color: transparent;
  background: var(--accent-soft);
}

.nav-buttons {
  display: flex;
  align-items: center;
  gap: 0;
  height: 44px;
  padding: 0 2px;
  border-radius: var(--radius-pill);
  margin-right: 2px;
}

.nav-buttons button {
  width: 44px;
  height: 40px;
  min-height: 40px;
  color: var(--fg);
}

.nav-buttons button:first-child {
  border-right: 1px solid color-mix(in srgb, var(--glass-rim) 18%, transparent);
  border-radius: 999px 0 0 999px;
}

.nav-buttons button:last-child {
  border-radius: 0 999px 999px 0;
}

.search {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 0 10px 0 18px;
  height: 44px;
  border-radius: var(--radius-pill);
  color: var(--fg-dim);
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

/* Focus thickens the material under the field instead of lifting the content layer. */
.search:focus-within {
  background: color-mix(in srgb, var(--canvas) 70%, var(--glass-inset));
  border-color: color-mix(in srgb, var(--accent) 70%, transparent);
}

.search > svg {
  flex: none;
}

.search input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: none;
  background: none;
  color: var(--fg);
  font-size: 16px;
}

.search input:focus {
  background: none;
}

.search input::placeholder {
  color: var(--fg-dim);
}

.search-wrap {
  flex: 1;
  min-width: 0;
  position: relative;
  display: flex;
}

/* Solid like the account panel: it sits over the canvas's file names. */
.search-drop {
  position: absolute;
  z-index: 5;
  top: calc(100% + 14px);
  left: 0;
  right: 0;
  min-width: 300px;
  max-height: min(460px, calc(100dvh - 120px));
  overflow-y: auto;
  padding: 8px;
  border: 1px solid var(--glass-edge);
  border-radius: 18px;
  background: var(--canvas-flat);
  box-shadow: var(--shadow-sheet);
  color: var(--fg);
  animation: drop-in 0.14s var(--ease);
}

@keyframes drop-in {
  from {
    transform: translateY(-4px);
  }
}

.drop-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 8px 8px;
}

.drop-chips span {
  padding: 3px 9px;
  border-radius: 7px;
  background: var(--accent-soft);
  font-size: 12px;
  font-weight: 600;
}

.drop-note {
  margin: 0;
  padding: 8px 10px;
  color: var(--fg-dim);
  font-size: 12px;
}

.drop-row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 46px;
  padding: 6px 10px;
  border-radius: 10px;
  color: var(--fg-dim);
  cursor: pointer;
}

.drop-row.active {
  background: var(--accent-soft);
  color: var(--fg);
}

.drop-action {
  min-height: 40px;
}

.drop-row + .drop-action:not(.drop-action + .drop-action) {
  margin-top: 4px;
  border-top: 1px solid var(--line);
  border-radius: 0 0 10px 10px;
}

.drop-words {
  flex: 1;
  min-width: 0;
}

.drop-words strong,
.drop-words small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.drop-words strong {
  color: var(--fg);
  font-size: 13px;
  font-weight: 600;
}

.drop-words small {
  margin-top: 1px;
  font-size: 11px;
}

.keys {
  display: flex;
  gap: 4px;
  flex: none;
}

kbd {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  font-family: inherit;
  font-size: 12px;
  font-weight: 500;
  border-radius: 6px;
  border: 1px solid color-mix(in srgb, var(--glass-rim) 30%, transparent);
  background: color-mix(in srgb, var(--glass-control) 70%, transparent);
  color: var(--fg-dim);
}

.clear {
  width: 26px;
  height: 26px;
  min-height: 26px;
}


.panel-toggle {
  flex: none;
  width: 44px;
  height: 44px;
  min-height: 44px;
  padding: 0;
  border-radius: 18px;
  color: var(--fg);
}

.panel-toggle.on {
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--accent) 35%, transparent);
}

.cloud {
  flex: none;
  min-width: 180px;
  height: 46px;
  min-height: 46px;
  padding: 0 24px;
  gap: 10px;
  font-size: 16px;
}

/* AI Assistant: the page's name where the search would be;
   the title bar drag reaches through it. */
.title {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding-left: 12px;
  white-space: nowrap;
  overflow: hidden;
}

.title > svg {
  flex: none;
  color: var(--accent);
}

.title strong {
  font-size: 18px;
  font-weight: 600;
}

.title span {
  padding-left: 12px;
  border-left: 1px solid var(--line-strong);
  font-size: 13px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
}

/* AI Assistant's model state: a hollow warm ring while no model is
   connected — the local features work regardless — a filled green dot once
   one is; either way a way into Settings · AI. */
.model-pill {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 260px;
  min-height: 34px;
  padding: 0 12px;
  border-color: transparent;
  background: transparent;
  font-size: 12px;
  color: var(--fg-dim);
}

.model-pill span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.model-pill i {
  flex: none;
  box-sizing: border-box;
  width: 7px;
  height: 7px;
  border: 1.5px solid var(--warning);
  border-radius: 50%;
}

.model-pill.on {
  color: var(--fg);
}

.model-pill.on i {
  border: 0;
  background: var(--success);
}

.ai-settings {
  flex: none;
  width: 38px;
  height: 38px;
  min-height: 38px;
  margin-right: 6px;
  padding: 0;
  border-radius: 12px;
}

@media (max-width: 700px) {
  .model-pill {
    display: none;
  }
}

/* Cleanup: the account (a live dot), the range, and what the scan reads. */
.cleanup-title i {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--success);
}

.cleanup-title strong {
  font-size: 15px;
  font-weight: 600;
}

.local {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.rescan {
  flex: none;
  min-height: 40px;
  margin-right: 6px;
  font-size: 14px;
  font-weight: 500;
  color: var(--fg);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
  border-color: var(--line);
}

@media (max-width: 1279px) {
  .toolbar {
    gap: 12px;
  }

  .cloud {
    min-width: 0;
    padding: 0 18px;
  }
}

/* The compact workspace keeps search conditions reachable; shortcut hints
   and the cloud-add label give up their room first. */
@media (max-width: 959px) {
  .toolbar {
    gap: 8px;
    height: 52px;
    padding: 0 6px;
  }

  .keys,
  .cloud .label,
  .local,
  .rescan .label {
    display: none;
  }

  .title {
    padding-left: 4px;
  }

  .cloud {
    width: 46px;
    padding: 0;
  }
}

/* At 375px the leading account avatar would clip cloud add. The workspace
   tabs below already toggle the panel, so its toolbar control yields space. */
@media (max-width: 500px) {
  .panel-toggle {
    display: none;
  }
}
</style>
