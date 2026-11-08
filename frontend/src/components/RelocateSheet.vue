<script setup lang="ts">
// Move to / Copy to: a folder picker on its own trail, so choosing a
// destination never navigates the page behind it. A picked folder cannot be
// entered (the server would accept moving a folder into itself and then fail
// in the background), and the folder the entries already live in cannot be
// chosen (the server refuses that outright). The same picker chooses the
// folder AI Assistant · Film and TV Organization works on (mode organize, no entries) and the one
// Cleanup scans (mode cleanup), each with everything below it. The search box
// finds folders anywhere in the drive; New Folder makes one in the level on
// screen and walks into it (not for Cleanup: a new folder has nothing to scan).
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import {
  accountName,
  appLocked,
  closeRelocate,
  confirmRelocate,
  createRelocateFolder,
  enterRelocateResult,
  relocate,
  relocateTo,
  searchRelocateFolders,
  trailPath,
} from "../store";
import { t } from "../i18n";
import type { Entry } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const sheet = ref<HTMLElement>();
let sharePickerFocus: HTMLElement | null = null;
watch(() => relocate.mode === "shareImport", async open => {
  if (!open) { if (!appLocked.value && sharePickerFocus?.isConnected) sharePickerFocus.focus(); sharePickerFocus = null; return; }
  sharePickerFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  await nextTick();
  if (relocate.mode === "shareImport" && !appLocked.value) sheet.value?.querySelector<HTMLInputElement>('input[type="search"]')?.focus();
});
const open = computed(() => relocate.mode !== "");
// Cleanup picks a folder to scan, with everything below it.
const scoping = computed(() => relocate.mode === "cleanup");
// The Cloud Add sheet's Save to: a folder to save into, nothing to move.
const saving = computed(() => relocate.mode === "cloudAdd" || relocate.mode === "shareImport");
// Whole sentences per mode: the verb sits differently in every language.
const words = computed(() => {
  const name = targetName.value;
  switch (relocate.mode) {
    case "sync":
    case "shareImport":
      return { empty: t("这里没有子文件夹，可以选择这里。"), note: trailPath(relocate.trail), working: t("正在加载"), go: t("选择这个文件夹") };
    case "cleanup":
      return { empty: t("这里没有子文件夹，可以直接扫描这里"), note: t("将扫描「{name}」及子文件夹", { name }), working: t("扫描中…"), go: t("扫描这里") };
    case "cloudAdd":
      return { empty: t("这里没有子文件夹，可以直接保存到这里"), note: t("将保存到「{name}」", { name }), working: t("保存中…"), go: t("保存到这里") };
    case "copy":
      return { empty: t("这里没有子文件夹，可以直接复制到这里"), note: t("将复制到「{name}」", { name }), working: t("复制中…"), go: t("复制到这里"), self: t("不能复制到所选文件夹自身") };
    default:
      return { empty: t("这里没有子文件夹，可以直接移动到这里"), note: t("将移动到「{name}」", { name }), working: t("移动中…"), go: t("移动到这里"), self: t("不能移动到所选文件夹自身") };
  }
});
const title = computed(() => {
  if (relocate.mode === "sync") return t("选择云端文件夹");
  if (relocate.mode === "cleanup") return t("选择扫描范围");
  if (saving.value) return t("选择保存位置");
  return relocate.mode === "copy" ? t("复制到") : t("移动到");
});
const subject = computed(() => {
  if (relocate.mode === "sync" || relocate.mode === "shareImport") return `${t("所属账号")} · ${accountName(relocate.account)}`;
  if (scoping.value) return t("包含所有子文件夹");
  if (relocate.mode === "cloudAdd") return t("云添加的任务保存在这里");
  if (saving.value) return t("云添加的任务保存在这里；确认时还可以在其中按作品新建文件夹");
  const items = relocate.items;
  return items.length === 1 ? t("「{name}」", { name: items[0].name }) : t("「{name}」等 {n} 项", { name: items[0]?.name ?? "", n: items.length });
});
// The picked name is the part that gives way: the words and the count around
// it stay whole however long the name is.
const subjectParts = computed(() => {
  const name = relocate.items[0]?.name;
  if (scoping.value || saving.value || relocate.mode === "cloudAdd" || !name) return null;
  const n = relocate.items.length;
  const [before, after] = (n === 1 ? t("「{name}」", { name: "\u0000" }) : t("「{name}」等 {n} 项", { name: "\u0000", n })).split("\u0000");
  return { before, name, after };
});
// The Cloud Add sheet sends no parent for All Files: the server files the task in its own download folder.
const toDefault = computed(() => relocate.mode === "cloudAdd" && !relocate.trail.length);

const pickedIds = computed(() => new Set(relocate.items.map((e) => e.id)));
const targetId = computed(() => relocate.trail.at(-1)?.id ?? "");
const targetName = computed(() => relocate.trail.at(-1)?.name ?? t("全部文件"));
const alreadyHere = computed(() => relocate.items.some((e) => e.parentId === targetId.value));
const searching = computed(() => !!relocate.query.trim());
const canCreate = computed(() => !scoping.value && relocate.mode !== "shareImport" && !searching.value);

function enter(folder: Entry) {
  if (pickedIds.value.has(folder.id)) return;
  void relocateTo([...relocate.trail, { id: folder.id, name: folder.name }]);
}

// The name starts as New Folder, selected, so typing replaces it and Enter alone accepts it.
const newName = ref("");
const nameField = ref<HTMLInputElement>();
watch(
  () => relocate.naming,
  async (naming) => {
    if (!naming) return;
    newName.value = t("新建文件夹");
    await nextTick();
    nameField.value?.select();
  },
);

async function submitName() {
  const name = newName.value.trim();
  if (!name || relocate.creating) return;
  // Refused (taken name, bad character): back to the name, to fix it.
  if (!(await createRelocateFolder(name)) && relocate.naming) nameField.value?.focus();
}

// Esc steps back one layer: the name row, then the search, then the sheet.
function onKeydown(ev: KeyboardEvent) {
  if (!open.value || appLocked.value) return;
  if (ev.key === "Tab" && relocate.mode === "shareImport") {
    const items = Array.from(sheet.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? []);
    const first = items[0], last = items.at(-1);
    if (ev.shiftKey && (document.activeElement === first || !sheet.value?.contains(document.activeElement))) { ev.preventDefault(); last?.focus(); }
    else if (!ev.shiftKey && (document.activeElement === last || !sheet.value?.contains(document.activeElement))) { ev.preventDefault(); first?.focus(); }
    return;
  }
  if (ev.key !== "Escape") return;
  ev.stopImmediatePropagation();
  if (relocate.naming) {
    if (!relocate.creating) relocate.naming = false;
  } else if (relocate.query) {
    searchRelocateFolders("");
  } else {
    closeRelocate();
  }
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown, true));
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="scrim" @click.self="closeRelocate">
      <section ref="sheet" class="sheet glass glass-sheet" role="dialog" :aria-label="title">
        <header>
          <div class="titles">
            <h2>{{ title }}</h2>
            <p v-if="subjectParts" class="subject" :title="subject">
              <span>{{ subjectParts.before }}</span><span class="picked">{{ subjectParts.name }}</span><span>{{ subjectParts.after }}</span>
            </p>
            <p v-else class="subject-text">{{ subject }}</p>
          </div>
          <button class="icon close" :title="t('关闭')" @click="closeRelocate"><Icon name="close" :size="16" /></button>
        </header>

        <label class="search">
          <Icon name="search" :size="17" />
          <input
            type="search"
            :value="relocate.query"
            :placeholder="t('搜索文件夹')"
            :aria-label="t('搜索全盘文件夹')"
            autocomplete="off"
            spellcheck="false"
            @input="searchRelocateFolders(($event.target as HTMLInputElement).value)"
          />
        </label>

        <div class="place">
          <nav v-if="!searching" class="crumbs" :aria-label="scoping ? t('文件夹位置') : t('目标位置')">
            <button class="quiet" :class="{ here: !relocate.trail.length }" @click="relocateTo([])">{{ t("全部文件") }}</button>
            <template v-for="(crumb, i) in relocate.trail" :key="crumb.id">
              <Icon name="chevronRight" :size="14" />
              <button
                class="quiet"
                :class="{ here: i === relocate.trail.length - 1 }"
                :title="crumb.name"
                @click="relocateTo(relocate.trail.slice(0, i + 1))"
              >
                {{ crumb.name }}
              </button>
            </template>
          </nav>
          <p v-else class="crumbs found">{{ t("搜索整个云盘的文件夹") }}</p>
          <button
            v-if="canCreate"
            class="quiet new"
            :title="t('新建到「{name}」', { name: targetName })"
            :disabled="relocate.loading || relocate.naming || relocate.working"
            @click="relocate.naming = true"
          >
            <Icon name="folderPlus" :size="16" />
            {{ t("新建文件夹") }}
          </button>
        </div>

        <p v-if="relocate.error" class="warning" role="alert">{{ relocate.error }}</p>
        <div class="list glass-well" :aria-busy="relocate.loading || relocate.searching">
          <template v-if="searching">
            <p v-if="relocate.searching" class="state"><Orbit :size="18" /> {{ t("正在搜索…") }}</p>
            <p v-else-if="!relocate.searchable" class="state">{{ t("云盘索引还在建立，暂时不能搜索全盘；可以逐层浏览。") }}</p>
            <p v-else-if="!relocate.results.length" class="state">{{ t("没有找到文件夹，试试其他名称") }}</p>
            <button
              v-for="folder in relocate.results"
              v-else
              :key="folder.id"
              class="quiet row"
              :title="trailPath(folder.trail ?? [])"
              @click="enterRelocateResult(folder)"
            >
              <Icon name="folder" :size="20" filled />
              <span class="name">{{ folder.name }}</span>
              <small class="path">{{ trailPath((folder.trail ?? []).slice(0, -1)) }}</small>
              <Icon name="chevronRight" :size="16" />
            </button>
          </template>
          <template v-else>
            <form v-if="relocate.naming" class="naming" @submit.prevent="submitName">
              <Icon name="folder" :size="20" filled />
              <input
                ref="nameField"
                v-model="newName"
                :aria-label="t('文件夹名称')"
                :placeholder="t('文件夹名称')"
                :disabled="relocate.creating"
                autocomplete="off"
                spellcheck="false"
              />
              <button type="button" class="quiet" :disabled="relocate.creating" @click="relocate.naming = false">{{ t("取消") }}</button>
              <button type="submit" class="primary" :class="{ working: relocate.creating }" :disabled="!newName.trim() || relocate.creating">
                <Orbit v-if="relocate.creating" :size="14" />
                {{ relocate.creating ? t("创建中…") : t("创建") }}
              </button>
            </form>
            <div v-if="relocate.loading" class="state"><Orbit :size="18" /> {{ t("正在加载文件夹…") }}</div>
            <p v-else-if="!relocate.folders.length && !relocate.naming" class="state">
              {{ words.empty }}
            </p>
            <button
              v-for="folder in relocate.folders"
              v-else
              :key="folder.id"
              class="quiet row"
              :disabled="pickedIds.has(folder.id)"
              :title="pickedIds.has(folder.id) ? words.self : folder.name"
              @click="enter(folder)"
            >
              <Icon name="folder" :size="20" filled />
              <span class="name">{{ folder.name }}</span>
              <small v-if="pickedIds.has(folder.id)">{{ t("已选") }}</small>
              <Icon v-else name="chevronRight" :size="16" />
            </button>
          </template>
        </div>

        <footer>
          <span class="note" :class="{ warn: alreadyHere }">
            {{
              alreadyHere
                ? t("所选项目已在这个文件夹里，请选择其他位置")
                : toDefault
                  ? t("将保存到服务端的默认下载文件夹")
                  : words.note
            }}
          </span>
          <button @click="closeRelocate">{{ t("取消") }}</button>
          <button
            class="primary"
            :class="{ working: relocate.working }"
            :disabled="alreadyHere || relocate.loading || relocate.working || relocate.creating || relocate.naming || !!relocate.error || (relocate.mode === 'sync' && searching)"
            @click="confirmRelocate"
          >
            <Orbit v-if="relocate.working" :size="15" />
            {{ relocate.working ? words.working : words.go }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(620px, calc(100vw - 80px));
  height: min(640px, calc(100vh - 100px));
  gap: 16px;
  padding: 28px 32px;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.titles {
  min-width: 0;
}

header h2 {
  margin: 0;
  font-size: 26px;
  font-weight: 600;
  line-height: 34px;
}

header p {
  margin: 4px 0 0;
  font-size: 14px;
  color: var(--fg-dim);
}

.subject {
  display: flex;
  min-width: 0;
  white-space: nowrap;
}

.subject > span {
  flex: none;
}

.subject > .picked {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.subject-text {
  text-wrap: balance;
}

.close {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
}

/* Same field as Online Extract's folder browser: glyph inside, the frame takes the focus colour. */
.search {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
  height: 44px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: 12px;
  background: var(--canvas);
  color: var(--fg-dim);
  transition: border-color var(--dur) var(--ease);
}

.search:focus-within {
  border-color: var(--accent);
}

.search input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 14px;
}

/* The trail gives way to New Folder, which keeps its whole label. */
.place {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  min-height: 36px;
}

.crumbs {
  display: flex;
  align-items: center;
  flex: 1;
  gap: 2px;
  min-width: 0;
  margin: 0;
  overflow-x: auto;
  color: var(--fg-dim);
}

.crumbs.found {
  padding: 0 10px;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: block;
}

.crumbs button {
  flex: none;
  max-width: 180px;
  min-height: 32px;
  padding: 0 10px;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: block;
}

.crumbs button.here {
  color: var(--fg);
  font-weight: 600;
}

.new {
  flex: none;
  gap: 6px;
  min-height: 32px;
  padding: 0 12px;
  font-size: 14px;
  color: var(--accent);
  white-space: nowrap;
}

/* The name row sits where the new folder will be listed. */
.naming {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 52px;
  margin-bottom: 4px;
  padding: 0 6px 0 12px;
  border: 1px solid var(--accent);
  border-radius: 10px;
  background: var(--canvas);
}

.naming > svg {
  flex: none;
  color: var(--accent);
}

.naming input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 15px;
  font-weight: 500;
}

.naming button {
  flex: none;
  gap: 6px;
  min-height: 36px;
  padding: 0 14px;
  font-size: 14px;
  font-weight: 600;
}

.list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
  border-radius: 14px;
}

.state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  height: 100%;
  margin: 0;
  font-size: 14px;
  color: var(--fg-dim);
}

.row {
  width: 100%;
  justify-content: flex-start;
  gap: 12px;
  min-height: 44px;
  padding: 0 12px;
  border-radius: 10px;
  color: var(--fg);
  font-size: 15px;
}

.row > :first-child {
  flex: none;
  color: var(--accent);
}

.row .name {
  flex: 1;
  min-width: 0;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row small {
  flex: none;
  font-size: 12px;
  color: var(--fg-dim);
}

/* A search hit's parent path tells same-named folders apart; the name keeps the room. */
.row small.path {
  flex: 0 1 auto;
  max-width: 45%;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row > :last-child:not(small) {
  color: var(--fg-dim);
}

footer {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* A sentence, not a label: it wraps beside the buttons instead of losing
   its end. */
.note {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  line-height: 1.35;
  color: var(--fg-dim);
  text-wrap: pretty;
}

.note.warn {
  color: var(--danger);
}

footer button {
  min-height: 46px;
  padding: 0 24px;
  font-size: 15px;
  font-weight: 600;
}
</style>
