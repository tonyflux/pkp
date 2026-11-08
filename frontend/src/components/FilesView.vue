<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AccountIdentity from "./AccountIdentity.vue";
import ConfirmInline from "./ConfirmInline.vue";
import FilesSearch from "./FilesSearch.vue";
import SaveSearch from "./SaveSearch.vue";
import VideoWorksView from "./VideoWorksView.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu from "./SelectMenu.vue";
import SelectionBar from "./SelectionBar.vue";
import Thumb from "./Thumb.vue";
import { bytes, clock, day, shownPath, stamp } from "../format";
import { t } from "../i18n";
import { WORKSPACE_QUERY, isTyping, modKey, onWindows, useMedia, useOutsideClick } from "../dom";
import { vSlide } from "../slide";
import {
  accounts,
  clearSelection,
  copyDownloadLink,
  currentAccount,
  currentFolderId,
  currentId,
  deleteForever,
  downloadEntries,
  drive,
  emptyTrash,
  filesPlace,
  folderCovers,
  freedText,
  goToCrumb,
  hasMore,
  indexReady,
  isPicked,
  loadMore,
  localSearchShown,
  loadTrashSummary,
  nowPlaying,
  openCreateShare,
  openExtract,
  openFolder,
  openRelocate,
  openComic,
  openViewer,
  pickAll,
  pickRange,
  pickedEntries,
  play,
  player,
  relocate,
  removalSize,
  rename,
  renameSheet,
  restore,
  select,
  setLayout,
  setScope,
  setVideoLayout,
  videoWorksShown,
  shares,
  showCover,
  showInfo,
  sorts,
  toggleStar,
  togglePick,
  trash,
  trashSummary,
  ui,
  visibleFiles,
  wholeDrive,
  visibleFolders,
  type CategoryFilter,
  type FilesScope,
  type Cover,
  type FilesAnchor,
} from "../store";
import type { Entry } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const multiAccount = computed(() => accounts.list.length > 1);

const selected = computed(() => drive.entries.find((e) => e.id === drive.selectedId) ?? null);
// Two or more picked: checkboxes stay up and the batch bar floats over the list.
const multi = computed(() => pickedEntries.value.length > 1);
const workspaceNarrow = useMedia(WORKSPACE_QUERY);
// The server reads nothing inside the recycle bin (400 file_in_recycle_bin
// for any single entry), so play, preview, star and links are not offered
// there; opening an entry selects it, which brings up Restore and Delete permanently.
const inBin = computed(() => ui.view === "trash");

const chips: { key: CategoryFilter; label: string }[] = [
  { key: "all", get label() { return t("全部"); } },
  { key: "video", get label() { return t("视频"); } },
  { key: "audio", get label() { return t("chip|音频"); } },
  { key: "image", get label() { return t("图片"); } },
  { key: "document", get label() { return t("chip|文档"); } },
  { key: "other", get label() { return t("其他"); } },
];

// Browse scope: first where to look, then the kind of file.
const scopes: { key: FilesScope; label: string }[] = [
  { key: "folder", get label() { return t("当前文件夹"); } },
  { key: "drive", get label() { return t("整个云盘"); } },
];
// Only All files has one; a search across the drive replaces both.
const scopeShown = computed(() => ui.view === "all" && !(ui.query.trim() && indexReady.value));
const scopeHint = computed(() => {
  if (ui.scope === "folder") return videoWorksShown.value ? t("按作品聚合当前目录及子目录的视频") : t("只显示当前文件夹的直接子项");
  // Before the first full walk the tree comes from the server page by
  // page: what is drawn is never the whole drive yet.
  return indexReady.value ? t("来自不同文件夹的文件，在这里汇合") : t("本地索引建立中，结果可能不完整");
});

const sortLabel = computed(() => sorts.find((s) => s.key === ui.sortKey)?.label ?? sorts[0].label);

const heading = computed(() => {
  const query = ui.query.trim();
  if (query && indexReady.value) return { title: t("搜索结果"), sub: t("“{query}” · 全盘", { query }) };
  if (ui.view === "starred") return { title: t("星标"), sub: t("你标记过的文件与文件夹") };
  if (ui.view === "trash") return { title: t("回收站"), sub: t("删除的内容会保留一段时间") };
  if (wholeDrive.value) return { title: t("全部文件"), sub: t("跨文件夹浏览，不改变原来的文件夹结构") };
  const crumb = drive.trail.at(-1);
  // Inside a folder the breadcrumb is the subtitle: it already spells out
  // the full path, so a second copy would only repeat it. At the top the
  // subtitle says which space this is, not a slogan.
  return crumb ? { title: crumb.name, sub: "" } : { title: t("全部文件"), sub: t("云盘根目录") };
});

// Rows that carry where they live (search, the whole drive from the
// index) show it in place of the date.
const located = computed(() => !drive.live && (wholeDrive.value || (!!ui.query.trim() && indexReady.value)));

// Search results come from the whole drive, so each row says where it
// lives instead of when it changed.
const emptyText = computed(() => {
  if (wholeDrive.value && !ui.query) return ui.category === "all" ? t("云盘里还没有文件") : t("云盘里没有这类文件");
  if (!ui.query) return t("这里还没有内容");
  if (drive.live) return wholeDrive.value ? t("索引建立中，只能筛选已读取的文件") : t("索引建立中，暂时只能筛选当前文件夹");
  return t("没有匹配的文件");
});

// An indexed page knows the whole view's size; a live page only its own.
const countText = computed(() => {
  if (drive.listLoading && !drive.entries.length) return t("正在加载…");
  if (wholeDrive.value) {
    if (!drive.live && drive.total >= 0) return t("整个云盘 · {n} 个文件", { n: drive.total });
    return t("已读取 {n} 个文件 · 结果可能不完整", { n: drive.entries.length });
  }
  if (!drive.live && drive.total >= 0) return t("{n} 项", { n: drive.total });
  const parts: string[] = [];
  if (visibleFolders.value.length) parts.push(t("{n} 个文件夹", { n: visibleFolders.value.length }));
  parts.push(t("{n} 个文件", { n: visibleFiles.value.length }));
  return parts.join(" · ");
});

// The date beside a row: where it lives (search) or when it changed; in the
// bin, when the server will clear it — its delete_time, never a fixed
// number of days. A card spells that out; the list's column says it.
function when(entry: Entry, spelled = false): string {
  if (inBin.value) return spelled ? t("预计 {date} 清理", { date: stamp(entry.deleteTime) }) : stamp(entry.deleteTime);
  return entry.path ? shownPath(entry.path) : day(entry.modifiedTime);
}

// Two different waits. Opening a folder clears the rows, so an empty list
// plus the flag means "skeleton"; refreshing keeps the rows, so the same
// flag means "dim what is there and run the hairline".
const firstLoad = computed(() => drive.listLoading && !drive.entries.length);
const refreshing = computed(() => drive.listLoading && drive.entries.length > 0);
// Enough placeholders to fill a first screen, never a "page" of them.
const ghosts = { folders: 3, files: 8 };

// The mock colours folders in rotation; nothing in the data says which.
// Only the list's glyphs: a grid card is its cover.
const tints = ["blue", "purple", "amber"] as const;

/* ---------- folder covers ---------- */

// A card shows up to three of its folder's direct children from the index.
// The bin's folders keep the plain cover (the index's cover reads live rows
// only), as every card does until the index is ready.
function coverOf(folder: Entry): Cover | null {
  return inBin.value ? null : (folderCovers.get(folder.id) ?? null);
}

function coverClass(folder: Entry): string {
  const c = coverOf(folder);
  if (!c) return "cover-plain";
  return c.items.length ? `cover-n${c.items.length}` : "cover-empty";
}

function folderLine(folder: Entry): string {
  const c = coverOf(folder);
  const count = !c ? "" : c.children ? t("{n} 项", { n: c.children }) : t("暂无内容");
  return [count, when(folder, true)].filter(Boolean).join(" · ");
}

// A small tile names its entry without the extension; the large one, when
// it has no picture, spells the whole name.
function tileName(item: Entry, hero: boolean): string {
  if (hero || item.folder) return item.name;
  const dot = item.name.lastIndexOf(".");
  return dot > 0 ? item.name.slice(0, dot) : item.name;
}

function tileGlyph(item: Entry): "folder" | "audio" | "document" | "media" {
  if (item.folder) return "folder";
  if (item.category === "audio") return "audio";
  if (item.category === "video" || item.category === "image") return "media";
  return "document";
}

// Covers are asked for while their card is on screen; the store batches
// the cards entering together into one call.
const coverWatch = new IntersectionObserver((seen) => {
  for (const e of seen) {
    const id = (e.target as HTMLElement).dataset.entry;
    if (id) showCover(id, e.isIntersecting && !inBin.value);
  }
});
const vCover = {
  mounted: (el: HTMLElement) => coverWatch.observe(el),
  unmounted: (el: HTMLElement) => {
    coverWatch.unobserve(el);
    if (el.dataset.entry) showCover(el.dataset.entry, false);
  },
};
onBeforeUnmount(() => coverWatch.disconnect());

function isPlaying(entry: Entry): boolean {
  return player.presentation !== "off" && nowPlaying.value?.id === entry.id;
}

function activate(entry: Entry) {
  if (inBin.value) {
    select(entry);
    return;
  }
  if (entry.folder) {
    void openFolder(entry);
    return;
  }
  if (entry.playable) {
    void play(entry);
    return;
  }
  if (entry.category === "archive" && !entry.preview) {
    openExtract(entry);
    return;
  }
  // Everything else opens in the viewer: a picture, a document, text — or
  // the plain word that this kind cannot be shown.
  void openViewer(entry);
}

/* ---------- picking ---------- */

// Finder's rules: a click picks one, ⌘/Ctrl adds or removes, ⇧ extends.
function pick(entry: Entry, ev: MouseEvent) {
  if (ev.metaKey || ev.ctrlKey) togglePick(entry);
  else if (ev.shiftKey) pickRange(entry);
  else select(entry);
}

// A double click while extending a selection is two picks, not an open.
function open(entry: Entry, ev: MouseEvent) {
  if (ev.metaKey || ev.ctrlKey || ev.shiftKey) return;
  activate(entry);
}

/* ---------- the ··· menu ---------- */

// One menu at a time, anchored to the button that opened it. It is
// teleported to <body> so the list's scroll clip and fade never cut it.
// It opens below the button, or above it when it does not fit below and
// there is more room above (a card at the window's foot); where neither
// side holds it, it scrolls within the larger one.
const MENU_GAP = 6;
const MENU_MARGIN = 12;
const menu = ref<{
  entry: Entry;
  x: number;
  top: number;
  bottom: number;
  up: boolean;
  room: number;
  confirming: boolean;
} | null>(null);
const menuEl = ref<HTMLElement>();
const renameName = ref("");
const renameField = ref<HTMLInputElement>();
const renaming = ref(false);
const renameWorking = ref(false);

async function startRename() {
  const entry = menu.value?.entry;
  if (!entry) return;
  renameName.value = entry.name;
  renaming.value = true;
  await nextTick();
  renameField.value?.focus();
  const dot = entry.folder ? -1 : entry.name.lastIndexOf(".");
  renameField.value?.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
}

async function submitRename() {
  const entry = menu.value?.entry;
  const name = renameName.value.trim();
  if (!entry || !name || renameWorking.value) return;
  if (name === entry.name) { closeMenu(); return; }
  renameWorking.value = true;
  let saved = false;
  try {
    saved = await rename(entry, name);
  } finally {
    renameWorking.value = false;
  }
  if (saved) closeMenu();
  else { await nextTick(); renameField.value?.focus(); }
}

function openMenu(entry: Entry, ev: MouseEvent) {
  if (renameWorking.value) return;
  if (menu.value?.entry.id === entry.id) {
    menu.value = null;
    return;
  }
  const rect = (ev.currentTarget as HTMLElement).getBoundingClientRect();
  renaming.value = false;
  menu.value = { entry, x: rect.right, top: rect.top, bottom: rect.bottom, up: false, room: 0, confirming: false };
}

// Measured once drawn, and again when Delete permanently's question changes its height.
watch(
  () => [menu.value?.entry.id, menu.value?.confirming, renaming.value],
  async () => {
    await nextTick();
    const m = menu.value;
    if (!m || !menuEl.value) return;
    const below = window.innerHeight - m.bottom - MENU_GAP - MENU_MARGIN;
    const above = m.top - MENU_GAP - MENU_MARGIN;
    m.up = menuEl.value.scrollHeight > below && above > below;
    m.room = Math.max(m.up ? above : below, 120);
  },
);

const menuStyle = computed(() => {
  const m = menu.value;
  if (!m) return {};
  return {
    right: `${Math.max(MENU_MARGIN, Math.min(window.innerWidth - m.x, window.innerWidth - (renaming.value ? 340 : 168) - MENU_MARGIN))}px`,
    ...(m.up ? { bottom: `${window.innerHeight - m.top + MENU_GAP}px` } : { top: `${m.bottom + MENU_GAP}px` }),
    ...(m.room ? { maxHeight: `${m.room}px` } : {}),
  };
});

function closeMenu() {
  if (renameWorking.value) return;
  renaming.value = false;
  menu.value = null;
}

function menuAction(fn: (entry: Entry) => unknown) {
  const entry = menu.value?.entry;
  menu.value = null;
  if (entry) void fn(entry);
}

useOutsideClick("[data-menu]", () => !!menu.value, closeMenu);

/* ---------- the recycle bin ---------- */

// Empty Trash asks with the index's count and size; before the index is
// ready it says what the list holds.
const emptying = ref(false);
const emptyTrashText = computed(() => {
  const count = trashSummary.ready ? trashSummary.count : drive.total >= 0 && !drive.live ? drive.total : drive.entries.length;
  const size = trashSummary.ready ? trashSummary.size : removalSize(drive.entries);
  return t("清空回收站？{n} 项 · {size}，无法撤销。", { n: count, size: bytes(size, "0 B") });
});

function askEmptyTrash() {
  emptying.value = true;
  void loadTrashSummary();
}

function confirmEmptyTrash() {
  emptying.value = false;
  void emptyTrash();
}

watch(
  [inBin, currentId],
  ([bin]) => {
    emptying.value = false;
    if (bin) void loadTrashSummary();
  },
  { immediate: true },
);

// Delete / ⌘⌫ in the bin deletes for good, so it asks first, in a bar where
// the batch bar floats; Keep takes focus (Delete then Enter never deletes)
// and Esc puts it away.
const keyConfirm = ref<Entry[] | null>(null);
const keyConfirmEl = ref<HTMLElement>();

watch(keyConfirm, async (entries) => {
  if (!entries) return;
  await nextTick();
  keyConfirmEl.value?.querySelector<HTMLButtonElement>("button.quiet")?.focus();
});
watch([() => drive.picked, () => ui.view], () => (keyConfirm.value = null));

function confirmKeyDelete() {
  const entries = keyConfirm.value;
  keyConfirm.value = null;
  if (entries) void deleteForever(entries);
}

// What Delete acts on: the picks, else the focused card or row.
function eraseTargets(): Entry[] {
  if (pickedEntries.value.length) return pickedEntries.value;
  const id = document.activeElement instanceof HTMLElement ? document.activeElement.closest<HTMLElement>("[data-entry]")?.dataset.entry : "";
  const focused = id ? drive.entries.find((e) => e.id === id) : null;
  return focused ? [focused] : selected.value ? [selected.value] : [];
}

/* ---------- keyboard ---------- */

function onKeydown(ev: KeyboardEvent) {
  // The Delete confirm owns the keys while it is up: Esc puts it away, the
  // rest reach its buttons.
  if (keyConfirm.value) {
    if (ev.key === "Escape") {
      ev.preventDefault();
      keyConfirm.value = null;
    }
    return;
  }
  // Esc closes the innermost thing first: the menu, then the selection.
  if (ev.key === "Escape" && menu.value) {
    closeMenu();
    return;
  }
  if (ev.key === "Escape" && emptying.value) {
    emptying.value = false;
    return;
  }
  if (isTyping(ev)) return;
  // Selection keys belong to the page only while it is in front and no
  // sheet is up; the page also sits, hidden, under the viewer.
  const sheetUp = !!relocate.mode || !!shares.targets.length || ui.cloudAddOpen || ui.newFolderOpen || renameSheet.entries.length > 0;
  const pageKeys = ui.route === "files" && !sheetUp && !localSearchShown.value && !videoWorksShown.value;
  const mod = (ev.metaKey || ev.ctrlKey) && !ev.altKey && !ev.shiftKey;
  if (pageKeys && mod && ev.key.toLowerCase() === "a") {
    ev.preventDefault();
    pickAll();
    return;
  }
  // Delete / ⌘⌫ (Ctrl+Backspace): to the bin; in the bin, for good, after asking.
  const plainDelete = ev.key === "Delete" && !ev.metaKey && !ev.ctrlKey && !ev.altKey && !ev.shiftKey;
  if (pageKeys && (plainDelete || (mod && ev.key === "Backspace"))) {
    const targets = eraseTargets();
    if (!targets.length) return;
    ev.preventDefault();
    if (inBin.value) keyConfirm.value = [...targets];
    else void trash(targets);
    return;
  }
  if (pageKeys && ev.key === "Escape" && drive.picked.length) {
    clearSelection();
    return;
  }

  // Space and Enter open what the list has picked: only while it is in front.
  if (ui.route !== "files") return;
  if (ev.key !== " " && ev.key !== "Enter") return;
  if (ev.metaKey || ev.ctrlKey || ev.altKey || localSearchShown.value || videoWorksShown.value) return;
  if (document.activeElement?.closest("[data-playback]")) return;
  const current = selected.value;
  if (!current) return;
  if (ev.key === " ") {
    if (current.folder || current.playable || inBin.value) return;
    ev.preventDefault();
    void openViewer(current);
    return;
  }
  ev.preventDefault();
  activate(current);
}

onMounted(() => window.addEventListener("keydown", onKeydown));
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown));

/* ---------- keeping the place when the layout moves ---------- */

// Picking never changes the grid (the context column is always there). Two
// things do: the column being put away or brought back, and the window
// being resized. Then the columns reflow, and the card the user is about —
// the focused or picked one while it is in view, else the first in view —
// keeps its height on screen with the least scroll that keeps the
// place. The browser's own scroll anchoring is off (`overflow-anchor`), so
// this is the only correction.
const scroller = ref<HTMLElement>();
let anchor: { id: string; offset: number } | null = null;
let anchorWidth = 0;

function cardOf(id: string): HTMLElement | null {
  return scroller.value?.querySelector<HTMLElement>(`[data-entry="${CSS.escape(id)}"]`) ?? null;
}

function inView(root: HTMLElement, el: HTMLElement): boolean {
  const box = root.getBoundingClientRect();
  const b = el.getBoundingClientRect();
  return b.bottom > box.top && b.top < box.bottom;
}

function anchorCard(): HTMLElement | null {
  const root = scroller.value;
  if (!root) return null;
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement.closest<HTMLElement>("[data-entry]") : null;
  if (focused && root.contains(focused) && inView(root, focused)) return focused;
  const picked = drive.pickAnchor || drive.picked[0];
  const card = picked ? cardOf(picked) : null;
  if (card && inView(root, card)) return card;
  const top = root.getBoundingClientRect().top;
  return Array.from(root.querySelectorAll<HTMLElement>("[data-entry]")).find((el) => el.getBoundingClientRect().bottom > top) ?? null;
}

function noteAnchor() {
  const root = scroller.value;
  const card = anchorCard();
  if (!root || !card) {
    anchor = null;
    return;
  }
  anchor = { id: card.dataset.entry ?? "", offset: card.getBoundingClientRect().top - root.getBoundingClientRect().top };
}

let noteFrame = 0;
function onScroll() {
  closeMenu();
  cancelAnimationFrame(noteFrame);
  noteFrame = requestAnimationFrame(noteAnchor);
}

// Set when the user shows or puts away the column: after that reflow the
// picked card is also brought into view if it left it, keeping it visible
// after View info. Done in the resize pass, after the anchor, so
// neither undoes the other.
let revealPick = false;

// After a reflow, before paint: put the anchor back where it was.
const resizes = new ResizeObserver(([change]) => {
  const width = change.contentRect.width;
  const moved = anchorWidth > 0 && width > 0 && width !== anchorWidth;
  const reveal = revealPick;
  revealPick = false;
  anchorWidth = width;
  const root = scroller.value;
  if (!moved || !root) return;
  const card = anchor ? cardOf(anchor.id) : null;
  if (anchor && card) root.scrollTop += card.getBoundingClientRect().top - root.getBoundingClientRect().top - anchor.offset;
  const id = drive.pickAnchor || drive.picked[0];
  if (reveal && id) cardOf(id)?.scrollIntoView({ block: "nearest" });
});

watch(scroller, (el, before) => {
  if (before) resizes.unobserve(before);
  anchorWidth = 0;
  if (el) resizes.observe(el);
});
watch(
  () => drive.picked,
  () => void nextTick(noteAnchor),
);
// The place is taken again just before the column moves.
watch(
  () => ui.panelOpen,
  () => {
    noteAnchor();
    revealPick = true;
  },
  { flush: "pre" },
);
onBeforeUnmount(() => {
  resizes.disconnect();
  cancelAnimationFrame(noteFrame);
});

// Switching accounts keeps each account's place in its list: the
// store asks for it on the way out and hands it back on the way in.
function savePlace(): FilesAnchor | null {
  const root = scroller.value;
  const card = anchorCard();
  if (!root || !card) return null;
  const id = card.dataset.entry;
  if (!id) return null;
  return { id, offset: card.getBoundingClientRect().top - root.getBoundingClientRect().top, top: root.scrollTop };
}

async function restorePlace(place: FilesAnchor | null) {
  await nextTick();
  const root = scroller.value;
  if (!root) return;
  const card = place ? cardOf(place.id) : null;
  if (place && card) root.scrollTop += card.getBoundingClientRect().top - root.getBoundingClientRect().top - place.offset;
  else root.scrollTop = place ? place.top : 0;
}

// Show in containing folder picks an entry that may sit far down its folder.
async function revealEntry(id: string) {
  await nextTick();
  cardOf(id)?.scrollIntoView({ block: "nearest" });
}

onMounted(() => {
  filesPlace.save = savePlace;
  filesPlace.restore = (place) => void restorePlace(place);
  filesPlace.reveal = (id) => void revealEntry(id);
});
onBeforeUnmount(() => {
  filesPlace.save = () => null;
  filesPlace.restore = () => {};
  filesPlace.reveal = () => {};
});

// Hidden under the viewer or the Info page, the list is display:none and a
// box that is not laid out has no scroll position to keep; it is put back
// when the list shows again.
const shown = computed(() => ui.route === "files" && !(ui.workspace === "info" && workspaceNarrow.value));
let parkedTop = 0;
watch(
  shown,
  async (now) => {
    if (!now) {
      parkedTop = scroller.value?.scrollTop ?? 0;
      return;
    }
    await nextTick();
    if (scroller.value) scroller.value.scrollTop = parkedTop;
  },
  { flush: "pre" },
);
</script>

<template>
  <section class="files canvas">
    <!-- Conditions in the search box (type, size, playback…): the index answers them
         on a page of its own; plain words keep the list as it always was. -->
    <FilesSearch v-if="localSearchShown" />
    <template v-else>
    <header class="head" :class="{ 'with-account': multiAccount && currentAccount }">
      <span v-if="multiAccount && currentAccount" class="account" :title="t('当前账号')">
        <AccountIdentity :account="currentAccount" :size="24" dense />
      </span>
      <h1 class="title" :title="heading.title">{{ heading.title }}</h1>
      <nav v-if="drive.trail.length && !wholeDrive" class="crumbs">
        <button class="quiet" @click="goToCrumb(0)">{{ t("全部文件") }}</button>
        <template v-for="(crumb, i) in drive.trail" :key="crumb.id">
          <Icon name="chevronRight" :size="14" />
          <button class="quiet" :title="crumb.name" @click="goToCrumb(i + 1)">{{ crumb.name }}</button>
        </template>
      </nav>
      <p v-else class="sub">{{ heading.sub }}</p>
    </header>

    <div v-if="scopeShown" class="scope-row">
      <div v-slide class="segments scope glass-well" role="group" :aria-label="t('浏览范围')">
        <button
          v-for="s in scopes"
          :key="s.key"
          :class="{ on: ui.scope === s.key }"
          :aria-pressed="ui.scope === s.key"
          @click="setScope(s.key)"
        >
          {{ s.label }}
        </button>
      </div>
      <span class="scope-hint" :class="{ warn: ui.scope === 'drive' && !indexReady }" :title="scopeHint">{{ scopeHint }}</span>
    </div>

    <div class="controls" :class="{ 'after-scope': scopeShown }">
      <!-- One continuous well cut into the canvas, not five separate pills. -->
      <div v-slide class="segments kinds glass-well" role="tablist" :aria-label="t('分类')">
        <button
          v-for="chip in chips"
          :key="chip.key"
          role="tab"
          :aria-selected="ui.category === chip.key"
          :class="{ on: ui.category === chip.key }"
          @click="ui.category = chip.key"
        >
          {{ chip.label }}
        </button>
      </div>
      <div v-if="ui.category === 'video' && !inBin" v-slide class="segments glass-well video-layout" role="group" :aria-label="t('视频视图')">
        <button :class="{ on: !videoWorksShown }" :aria-pressed="!videoWorksShown" @click="setVideoLayout('files')">{{ t("按文件") }}</button>
        <button :class="{ on: videoWorksShown }" :aria-pressed="videoWorksShown" @click="setVideoLayout('works')">{{ t("按作品") }}</button>
      </div>

      <span class="spacer" />
      <SaveSearch v-if="ui.query.trim() && indexReady && ui.view !== 'trash'" />

      <div v-if="!videoWorksShown" v-slide class="segments compact glass-well">
        <button :class="{ on: ui.layout === 'grid' }" :title="t('网格')" :aria-pressed="ui.layout === 'grid'" @click="setLayout('grid')">
          <Icon name="grid" :size="18" />
        </button>
        <button :class="{ on: ui.layout === 'list' }" :title="t('列表')" :aria-pressed="ui.layout === 'list'" @click="setLayout('list')">
          <Icon name="list" :size="18" />
        </button>
      </div>

      <div v-if="!videoWorksShown" class="sort glass-well">
        <SelectMenu
          v-model="ui.sortKey"
          class="picker"
          :title="t('排序方式')"
          :label="t('排序方式')"
          :options="sorts.map((s) => ({ value: s.key, label: s.label }))"
        >
          <span>{{ sortLabel }}</span>
          <Icon name="chevronDown" :size="16" />
        </SelectMenu>
        <span class="divider" aria-hidden="true" />
        <button class="quiet icon dir" :class="{ asc: ui.sortAsc }" :title="ui.sortAsc ? t('升序') : t('降序')" @click="ui.sortAsc = !ui.sortAsc">
          <Icon name="sort" :size="18" />
        </button>
      </div>

      <!-- Trash's one page-wide action ends the control row at the wells'
           height, as the other pages put theirs beside their controls. -->
      <button
        v-if="inBin"
        class="purge"
        :class="{ on: emptying }"
        :disabled="!drive.entries.length"
        :aria-expanded="emptying"
        @click="emptying ? (emptying = false) : askEmptyTrash()"
      >
        <Icon name="trash" :size="17" />{{ t("清空回收站") }}
      </button>
    </div>

    <VideoWorksView
      v-if="videoWorksShown"
      :folder-id="!ui.query.trim() && ui.view === 'all' && ui.scope === 'folder' ? currentFolderId : undefined"
      :starred="ui.view === 'starred'"
      :query="ui.query"
    />
    <template v-else>
    <ConfirmInline
      v-if="inBin && emptying"
      class="purge-confirm"
      :message="emptyTrashText"
      :confirm-label="t('清空回收站')"
      :cancel-label="t('保留')"
      danger
      @cancel="emptying = false"
      @confirm="confirmEmptyTrash"
    />

    <!-- Finder drops land here (main.go's WindowFilesDropped handler reads
         data-upload-parent). Only the browsable tree accepts them: starred,
         trash, search results and the whole drive are not folders to upload
         into. -->
    <div
      class="body"
      v-bind="
        ui.view === 'all' && ui.scope === 'folder'
          ? { 'data-file-drop-target': '', 'data-drop-label': t('拖放到这里上传'), 'data-upload-parent': currentFolderId, 'data-upload-account': currentId }
          : {}
      "
    >
      <div v-if="refreshing" class="hairline late" aria-hidden="true" />

      <div v-if="firstLoad" class="scroll ghost late" aria-busy="true" :aria-label="t('正在加载')">
        <!-- The whole drive lays out files only. -->
        <template v-if="!wholeDrive">
        <div class="skel ghost-h2" />
        <div v-if="ui.layout === 'grid'" class="folders">
          <div v-for="i in ghosts.folders" :key="i" class="folder ghost-folder" :style="{ '--i': i }">
            <span class="folder-cover cover-plain"><span class="skel ghost-fill" /></span>
            <span class="folder-meta">
              <span class="folder-text">
                <span class="skel ghost-line" style="width: 62%" />
                <span class="skel ghost-line thin" style="width: 38%" />
              </span>
            </span>
          </div>
        </div>
        <div v-else class="ghost-rows">
          <div v-for="i in ghosts.folders" :key="i" class="ghost-row" :style="{ '--i': i }">
            <span class="skel ghost-icon small" />
            <span class="skel ghost-line" :style="{ width: 24 + ((i * 23) % 30) + '%' }" />
            <span class="spacer" />
            <span class="skel ghost-line thin" style="width: 84px" />
          </div>
        </div>
        </template>
        <div class="skel ghost-h2" />
        <div v-if="ui.layout === 'grid'" class="grid">
          <div v-for="i in ghosts.files" :key="i" class="card ghost-card" :style="{ '--i': i + ghosts.folders }">
            <div class="thumb skel" />
            <div class="meta">
              <span class="skel ghost-line" :style="{ width: 48 + ((i * 37) % 40) + '%' }" />
              <span class="skel ghost-line thin" style="width: 40%" />
            </div>
          </div>
        </div>
        <div v-else class="ghost-rows">
          <div v-for="i in ghosts.files" :key="i" class="ghost-row" :style="{ '--i': i + ghosts.folders }">
            <span class="skel ghost-icon small" />
            <span class="skel ghost-line" :style="{ width: 30 + ((i * 29) % 40) + '%' }" />
            <span class="spacer" />
            <span class="skel ghost-line thin" style="width: 64px" />
            <span class="skel ghost-line thin" style="width: 84px" />
          </div>
        </div>
      </div>

      <div v-else ref="scroller" class="scroll" :class="{ stale: refreshing, selecting: multi }" @scroll.passive="onScroll">
        <template v-if="visibleFolders.length">
          <h2>{{ t("section|文件夹") }} <span class="count">({{ visibleFolders.length }})</span></h2>
          <div v-if="ui.layout === 'grid'" class="folders">
            <button
              v-for="folder in visibleFolders"
              :key="folder.id"
              v-cover
              :data-entry="folder.id"
              class="folder"
              :class="{ picked: isPicked(folder), starred: folder.starred }"
              :title="folder.name"
              :aria-pressed="isPicked(folder)"
              @dblclick="open(folder, $event)"
              @click="pick(folder, $event)"
            >
              <span class="check" :class="{ on: isPicked(folder) }" aria-hidden="true" @click.stop="togglePick(folder)" @dblclick.stop>
                <Icon name="check" :size="13" />
              </span>
              <!-- The cover: one large tile and two small ones of the
                   folder's direct children. A tile without a
                   picture, or whose picture fails, draws its kind. -->
              <span class="folder-cover" :class="coverClass(folder)" aria-hidden="true">
                <template v-if="coverOf(folder)?.items.length">
                  <span
                    v-for="(item, k) in coverOf(folder)!.items"
                    :key="item.id"
                    class="cover-tile"
                    :class="[`glyph-${tileGlyph(item)}`, { hero: k === 0 }]"
                  >
                    <Thumb :src="item.folder ? '' : item.thumbnail" alt="" lazy>
                      <span class="tile-glyph">
                        <span v-if="tileGlyph(item) === 'folder'" class="mini-folder" />
                        <span v-else-if="tileGlyph(item) === 'audio'" class="sound-bars"><i /><i /><i /><i /><i /></span>
                        <Icon v-else-if="tileGlyph(item) === 'media'" :name="item.category" :size="k === 0 ? 30 : 22" />
                        <span v-else class="mini-document"><i /><i /><i /></span>
                        <small>{{ tileName(item, k === 0) }}</small>
                      </span>
                    </Thumb>
                    <span v-if="k === 0 && item.category === 'video' && item.thumbnail" class="play-mark">
                      <Icon name="play" :size="12" filled />
                    </span>
                  </span>
                </template>
                <span v-else-if="coverOf(folder)" class="empty-symbol">
                  <span class="mini-folder" />
                  <span>{{ t("等待放入文件") }}</span>
                </span>
                <span v-else class="mini-folder" />
              </span>
              <span v-if="folder.starred" class="star-mark" role="img" :aria-label="t('已加星标')" :title="t('已加星标')">
                <Icon name="star" :size="12" filled />
              </span>
              <span class="folder-meta">
                <span class="folder-mark" aria-hidden="true" />
                <span class="folder-text">
                  <strong>{{ folder.name }}</strong>
                  <small :title="folder.path">{{ folderLine(folder) }}</small>
                </span>
                <Icon class="chevron" name="chevronRight" :size="16" />
              </span>
            </button>
          </div>

          <table v-else class="rows" :class="{ located, bin: inBin }">
            <thead>
              <tr><th>{{ t("名称") }}</th><th class="num">{{ t("大小") }}</th><th class="dim">{{ inBin ? t("预计清理时间") : located ? t("column|位置") : t("修改时间") }}</th><th class="actions"></th></tr>
            </thead>
            <tbody>
              <tr
                v-for="(folder, i) in visibleFolders"
                :key="folder.id"
                :data-entry="folder.id"
                :class="{ picked: isPicked(folder) }"
                :aria-selected="isPicked(folder)"
                @click="pick(folder, $event)"
                @dblclick="open(folder, $event)"
              >
                <td class="name">
                  <div class="cell">
                    <span class="check" :class="{ on: isPicked(folder) }" aria-hidden="true" @click.stop="togglePick(folder)" @dblclick.stop>
                      <Icon name="check" :size="12" />
                    </span>
                    <Icon class="folder-icon" :class="tints[i % tints.length]" name="folder" :size="18" filled />
                    <span class="label" :title="folder.name">{{ folder.name }}</span>
                  </div>
                </td>
                <td class="num">{{ bytes(folder.size) }}</td>
                <td class="dim" :title="folder.path">{{ when(folder) }}</td>
                <td class="actions">
                  <div v-if="!inBin" class="cell end">
                    <button
                      class="quiet icon"
                      :class="{ starred: folder.starred }"
                      :title="folder.starred ? t('取消星标') : t('action|星标')"
                      :aria-pressed="folder.starred"
                      @click.stop="toggleStar(folder)"
                      @dblclick.stop
                    >
                      <Icon name="star" :size="16" :filled="folder.starred" />
                    </button>
                    <!-- The file rows' More sits here: keeps the stars of both tables in one column. -->
                    <span class="slot" aria-hidden="true" />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-if="visibleFiles.length">
          <h2>{{ t("section|文件") }} <span class="count">({{ visibleFiles.length }})</span></h2>

          <div v-if="ui.layout === 'grid'" class="grid">
            <article
              v-for="file in visibleFiles"
              :key="file.id"
              :data-entry="file.id"
              class="card"
              :class="{ picked: isPicked(file) }"
              @click="pick(file, $event)"
              @dblclick="open(file, $event)"
            >
              <div class="thumb">
                <span class="check" :class="{ on: isPicked(file) }" aria-hidden="true" @click.stop="togglePick(file)" @dblclick.stop>
                  <Icon name="check" :size="13" />
                </span>
                <span v-if="file.starred" class="star-mark" role="img" :aria-label="t('已加星标')" :title="t('已加星标')">
                  <Icon name="star" :size="12" filled />
                </span>
                <Thumb :src="file.thumbnail" :alt="file.name" lazy>
                  <Icon :name="file.category" :size="28" />
                </Thumb>
                <span v-if="file.duration" class="duration">{{ clock(file.duration) }}</span>
                <span v-if="isPlaying(file)" class="playing" role="img" :aria-label="t('正在播放')">
                  <Icon :name="player.state.paused ? 'play' : 'pause'" :size="13" filled />
                </span>
                <template v-if="!inBin">
                  <button v-if="file.playable" class="play" :title="t('播放')" @click.stop="play(file)">
                    <Icon name="play" :size="16" filled />
                  </button>
                  <button v-else-if="file.preview" class="play" :title="t('预览')" @click.stop="openViewer(file)">
                    <Icon name="eye" :size="16" />
                  </button>
                </template>
              </div>
              <div class="meta">
                <div class="name-row">
                  <strong :title="file.name">{{ file.name }}</strong>
                  <button
                    class="quiet icon more"
                    :class="{ open: menu?.entry.id === file.id }"
                    :title="t('更多')"
                    aria-haspopup="menu"
                    :aria-expanded="menu?.entry.id === file.id"
                    data-menu
                    @click.stop="openMenu(file, $event)"
                    @dblclick.stop
                  >
                    <Icon name="more" :size="18" filled />
                  </button>
                </div>
                <!-- The whole drive: the line under the name says where it lives. -->
                <small v-if="wholeDrive && file.path" class="path" :title="shownPath(file.path)">{{ shownPath(file.path) }}</small>
                <small v-else :title="file.path">{{ bytes(file.size) }} · {{ when(file, true) }}</small>
              </div>
            </article>
          </div>

          <table v-else class="rows" :class="{ located, bin: inBin }">
            <thead>
              <tr><th>{{ t("名称") }}</th><th class="num">{{ t("大小") }}</th><th class="dim">{{ inBin ? t("预计清理时间") : located ? t("column|位置") : t("修改时间") }}</th><th class="actions"></th></tr>
            </thead>
            <tbody>
              <tr
                v-for="file in visibleFiles"
                :key="file.id"
                :data-entry="file.id"
                :class="{ picked: isPicked(file) }"
                :aria-selected="isPicked(file)"
                @click="pick(file, $event)"
                @dblclick="open(file, $event)"
              >
                <td class="name">
                  <div class="cell">
                    <span class="check" :class="{ on: isPicked(file) }" aria-hidden="true" @click.stop="togglePick(file)" @dblclick.stop>
                      <Icon name="check" :size="12" />
                    </span>
                    <span v-if="isPlaying(file)" class="playing inline" role="img" :aria-label="t('正在播放')">
                      <Icon :name="player.state.paused ? 'play' : 'pause'" :size="13" filled />
                    </span>
                    <Icon v-else :name="file.category" :size="18" />
                    <span class="label" :title="file.name">{{ file.name }}</span>
                  </div>
                </td>
                <td class="num">{{ bytes(file.size) }}</td>
                <td class="dim" :title="file.path">{{ when(file) }}</td>
                <td class="actions">
                  <div class="cell end">
                  <template v-if="!inBin">
                  <button v-if="file.playable" class="quiet icon" :title="t('播放')" @click.stop="play(file)">
                    <Icon name="play" :size="16" />
                  </button>
                  <button v-else-if="file.preview" class="quiet icon" :title="t('预览')" @click.stop="openViewer(file)">
                    <Icon name="eye" :size="16" />
                  </button>
                  <button
                    class="quiet icon"
                    :class="{ starred: file.starred }"
                    :title="file.starred ? t('取消星标') : t('action|星标')"
                    :aria-pressed="file.starred"
                    @click.stop="toggleStar(file)"
                    @dblclick.stop
                  >
                    <Icon name="star" :size="16" :filled="file.starred" />
                  </button>
                  </template>
                  <button
                    class="quiet icon"
                    :class="{ open: menu?.entry.id === file.id }"
                    :title="t('更多')"
                    aria-haspopup="menu"
                    :aria-expanded="menu?.entry.id === file.id"
                    data-menu
                    @click.stop="openMenu(file, $event)"
                    @dblclick.stop
                  >
                    <Icon name="more" :size="18" filled />
                  </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>

        <p v-if="!visibleFolders.length && !visibleFiles.length && !drive.listLoading" class="empty">
          <Icon name="folder" :size="28" />
          {{ emptyText }}
          <small v-if="ui.view === 'all' && ui.scope === 'folder' && !ui.query">{{ t("用左侧「新建」上传或新建文件夹，也可以把文件直接拖进来") }}</small>
        </p>

        <div v-if="hasMore" class="more-row">
          <button :class="{ working: drive.loadingMore }" :disabled="drive.loadingMore" @click="loadMore">
            <Orbit v-if="drive.loadingMore" :size="15" />
            {{ drive.loadingMore ? t("正在加载…") : t("加载更多") }}
          </button>
        </div>
      </div>

      <SelectionBar v-if="multi && !keyConfirm" />
      <div v-if="keyConfirm" ref="keyConfirmEl" class="key-confirm glass glass-sheet" role="alertdialog" :aria-label="t('彻底删除')">
        <ConfirmInline
          :message="
            keyConfirm.length === 1
              ? t('彻底删除「{name}」？无法撤销，{freed}。', { name: keyConfirm[0].name, freed: freedText(keyConfirm) })
              : t('彻底删除 {n} 项？无法撤销，{freed}。', { n: keyConfirm.length, freed: freedText(keyConfirm) })
          "
          :confirm-label="t('确认删除')"
          :cancel-label="t('保留')"
          danger
          @cancel="keyConfirm = null"
          @confirm="confirmKeyDelete"
        />
      </div>
    </div>

    <!-- The count and selection row is always present, so picking does not shift adjacent content. -->
    <footer class="foot">
      <span class="tnum">{{ countText }}</span>
      <span class="picks">
        <template v-if="pickedEntries.length">
          <span class="tnum">{{ t("已选 {n} 项", { n: pickedEntries.length }) }}</span>
          <button class="quiet clear" @click="clearSelection">{{ t("取消选择") }}</button>
        </template>
        <span v-else>{{ t("未选择文件") }}</span>
      </span>
    </footer>
    </template>
    </template>

    <Teleport to="body">
      <div
        v-if="menu"
        ref="menuEl"
        class="menu glass-sheet"
        :class="{ confirming: menu.confirming, renaming, up: menu.up }"
        :role="renaming ? 'dialog' : 'menu'"
        :aria-label="renaming ? t('重命名') : undefined"
        data-menu
        :style="menuStyle"
        @click.stop
      >
        <ConfirmInline
          v-if="!renaming && menu.confirming"
          :message="t('彻底删除「{name}」？无法撤销，{freed}。', { name: menu.entry.name, freed: freedText([menu.entry]) })"
          :confirm-label="t('确认删除')"
          :cancel-label="t('保留')"
          danger
          @cancel="closeMenu"
          @confirm="menuAction((e) => deleteForever([e]))"
        />
        <form v-if="renaming" class="rename-form" @submit.prevent="submitRename" @keydown.stop @keydown.esc.stop.prevent="closeMenu">
          <label for="file-rename">{{ t("新名称") }}</label>
          <input id="file-rename" ref="renameField" v-model="renameName" autocomplete="off" spellcheck="false" :disabled="renameWorking" />
          <div class="rename-actions">
            <button type="button" :disabled="renameWorking" @click="closeMenu">{{ t("取消") }}</button>
            <button type="submit" class="primary" :disabled="!renameName.trim() || renameWorking"><Orbit v-if="renameWorking" :size="14" />{{ t("保存") }}</button>
          </div>
        </form>
        <template v-else-if="!menu.confirming && inBin">
          <button class="quiet" role="menuitem" @click="menuAction((e) => { select(e); showInfo(); })">
            <Icon name="info" :size="17" /> {{ t("查看信息") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction((e) => restore([e]))">
            <Icon name="restore" :size="17" /> {{ t("还原") }}
          </button>
          <button class="quiet danger" role="menuitem" @click="menu.confirming = true">
            <Icon name="trash" :size="17" /> {{ t("彻底删除") }}
          </button>
        </template>
        <template v-else-if="!menu.confirming">
          <button class="quiet" role="menuitem" @click="menuAction((e) => { select(e); showInfo(); })">
            <Icon name="info" :size="17" /> {{ t("查看信息") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction((e) => openCreateShare([e]))">
            <Icon name="share" :size="17" /> {{ t("分享") }}
          </button>
          <button v-if="menu.entry.preview" class="quiet" role="menuitem" @click="menuAction(openViewer)">
            <Icon name="eye" :size="17" /> {{ t("预览") }}
          </button>
          <button v-if="menu.entry.folder || menu.entry.preview === 'image'" class="quiet" role="menuitem" @click="menuAction(openComic)">
            <Icon name="image" :size="17" /> {{ t("作为漫画阅读") }}
          </button>
          <button v-if="menu.entry.playable" class="quiet" role="menuitem" @click="menuAction(play)">
            <Icon name="play" :size="17" /> {{ t("播放") }}
          </button>
          <button v-if="menu.entry.category === 'archive' && !menu.entry.folder" class="quiet" role="menuitem" @click="menuAction(openExtract)">
            <Icon name="archive" :size="17" /> {{ t("在线解压") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction(toggleStar)">
            <Icon name="star" :size="17" :filled="menu.entry.starred" /> {{ menu.entry.starred ? t("取消星标") : t("action|星标") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction(copyDownloadLink)">
            <Icon name="link" :size="17" /> {{ t("复制直链") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction((e) => downloadEntries([e]))">
            <Icon name="download" :size="17" /> {{ t("下载到电脑") }}
          </button>
          <button class="quiet" role="menuitem" @click="startRename">
            <Icon name="text" :size="17" /> {{ t("重命名") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction((e) => openRelocate('move', [e]))">
            <Icon name="move" :size="17" /> {{ t("移动到…") }}
          </button>
          <button class="quiet" role="menuitem" @click="menuAction((e) => openRelocate('copy', [e]))">
            <Icon name="copy" :size="17" /> {{ t("复制到…") }}
          </button>
          <button
            class="quiet danger"
            role="menuitem"
            :title="t('可以在回收站还原；空间要等彻底删除后才释放（{key}）', { key: onWindows ? 'Delete' : `${modKey}⌫` })"
            @click="menuAction((e) => trash([e]))"
          >
            <Icon name="trash" :size="17" /> {{ t("移到回收站") }}
          </button>
        </template>
      </div>
    </Teleport>
  </section>
</template>

<style scoped>
.files {
  --surface: color-mix(in srgb, var(--canvas-flat) 91%, white 9%);
  --surface-hover: color-mix(in srgb, var(--canvas-flat) 84%, white 16%);
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  /* Keep room for the reading canvas beside the navigation and information panel. */
  padding: 26px 24px 0;
  overflow: hidden;
  container: files / inline-size;
}

/* The canvas foot: counts on the left, the selection on the right. */
.foot {
  flex: none;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 -24px;
  padding: 0 24px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  color: var(--fg-dim);
}

.picks {
  display: flex;
  align-items: center;
  gap: 8px;
}

.picks .clear {
  min-height: 40px;
  margin: 0 -8px;
  padding: 0 16px;
  font-size: 12px;
  color: var(--accent);
}

.head {
  min-width: 0;
}

/* The account on screen, at the title's right: no extra row.
   Only the title shares its line; the subtitle below runs the full width. */
.head.with-account {
  position: relative;
}

.head.with-account .title {
  padding-right: 236px;
}

.head .account {
  position: absolute;
  top: 5px;
  right: 0;
  max-width: 220px;
  display: flex;
}

/* Under 960px the toolbar's avatar names the account and the
   title needs the width. */
@media (max-width: 959px) {
  .head.with-account .title {
    padding-right: 0;
  }

  .head .account {
    display: none;
  }
}

.title {
  margin: 0;
  font-size: 28px;
  line-height: 34px;
  font-weight: 600;
  letter-spacing: -0.01em;
  /* Folder names run to a full sentence; the name belongs on one line and
     Details shows it whole. */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* The subtitle or breadcrumb shares one fixed-height line, so changing scope
   does not move the content below. */
.sub {
  display: flex;
  align-items: center;
  min-height: 28px;
  margin: 2px 0 0;
  font-size: 15px;
  line-height: 20px;
  color: var(--fg-dim);
}

.crumbs {
  display: flex;
  align-items: center;
  gap: 2px;
  /* Room for the first crumb's hover fill, which reaches 2px past the text
     column (the buttons' negative margin); the clip would cut it. */
  margin: 2px -2px 0;
  height: 28px;
  padding: 0 2px;
  color: var(--fg-dim);
  overflow: hidden;
}

.crumbs button {
  /* Buttons are flex boxes globally; text-overflow needs inline content. */
  display: block;
  flex: 0 1 auto;
  min-width: 0;
  max-width: 244px;
  min-height: 28px;
  margin: 0 -2px;
  padding: 0 10px;
  font-size: 15px;
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.crumbs button:last-child {
  color: var(--fg);
}

.crumbs svg {
  flex: none;
}

/* ---------- Browse scope ---------- */

.scope-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 14px;
  min-width: 0;
  margin-top: 20px;
}

.segments.scope {
  flex: none;
}

.segments.scope button {
  padding: 0 16px;
  font-size: 14px;
}

/* A lighter chip than the Type lens below: one primary pill on screen. */
.segments.scope::before {
  background: var(--accent-soft);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 40%, transparent);
}

.segments.scope button.on {
  color: var(--fg);
}

/* Beside the switch while it has room for two short lines; under it once
   it would be squeezed narrower than that. */
.scope-hint {
  flex: 1 1 14em;
  min-width: 0;
  font-size: 13px;
  line-height: 17px;
  text-wrap: balance;
  color: var(--fg-dim);
}

.scope-hint.warn {
  color: var(--warning);
}

/* ---------- controls ---------- */

.controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin: 22px 0 20px;
  transition: padding-right var(--dur) var(--ease);
}

.controls.after-scope {
  margin-top: 14px;
}

@media (max-height: 720px) {
  .files {
    padding-top: 20px;
  }

  .scope-row {
    margin-top: 12px;
  }

  .controls {
    margin: 14px 0 12px;
  }

  .controls.after-scope {
    margin-top: 10px;
  }
}

.spacer {
  flex: 1;
}

.segments,
.sort {
  display: flex;
  align-items: center;
  height: 44px;
  padding: 4px;
  border-radius: var(--radius-pill);
}

.segments button {
  min-height: 36px;
  padding: 0 20px;
  border: none;
  border-radius: var(--radius-pill);
  background: none;
  box-shadow: none;
  color: var(--fg-dim);
  font-size: 15px;
  font-weight: 500;
}

.segments button:hover:not(.on) {
  background: color-mix(in srgb, var(--fg) 7%, transparent);
  color: var(--fg);
}

.segments::before {
  background: var(--primary);
  box-shadow: inset 0 1px 0 var(--primary-rim), 0 4px 12px var(--primary-glow);
}

.segments button.on {
  background: transparent;
  color: var(--on-primary);
  font-weight: 600;
}

.video-layout { flex: none; }
.video-layout::before { background: var(--accent-soft); box-shadow: inset 0 1px 0 var(--glass-rim); }
.video-layout button.on { color: var(--fg); }

.segments.compact button {
  width: 40px;
  padding: 0;
  border-radius: 11px;
}

.segments.compact button:first-child {
  border-radius: 18px 11px 11px 18px;
}

.segments.compact button:last-child {
  border-radius: 11px 18px 18px 11px;
}

/* The layout toggle marks its state with a lighter chip, not the cobalt
   lens: only one primary pill per row (mock 01/02). */
.segments.compact::before {
  background: var(--accent-soft);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 40%, transparent);
}

.segments.compact button.on {
  color: var(--fg);
}

/* Keep filters reachable as the reading canvas narrows, including translated labels. */
@container files (max-width: 679px) {
  .segments.kinds {
    min-width: 0;
    max-width: 100%;
    min-height: 44px;
    height: auto;
    flex-wrap: wrap;
  }

  .segments.kinds button {
    flex: none;
    min-width: 44px;
    padding-inline: 10px;
  }
}

.sort {
  gap: 2px;
  padding-left: 8px;
}

.picker {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 36px;
  padding: 0 8px 0 10px;
  border-radius: var(--radius-pill);
  font-size: 15px;
  font-weight: 500;
  color: var(--fg);
  white-space: nowrap;
}

.picker svg {
  color: var(--fg-dim);
}

.divider {
  width: 1px;
  height: 20px;
  margin: 0 2px;
  background: var(--line-strong);
}

.dir svg {
  transition: transform var(--dur) var(--ease);
}

.dir.asc svg {
  transform: scaleY(-1);
}

/* ---------- body ---------- */

.body {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* The runtime toggles this class while a Finder drag hovers the target. */
.body.file-drop-target-active::after {
  content: attr(data-drop-label);
  position: absolute;
  inset: 0;
  z-index: 2;
  display: grid;
  place-items: center;
  border-radius: var(--radius-lg, 18px);
  border: 2px dashed var(--accent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  font-size: 16px;
  font-weight: 600;
  pointer-events: none;
}

.body > .hairline {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  z-index: 1;
}

.scroll {
  flex: 1;
  overflow-y: auto;
  margin: 0 -8px;
  padding: 0 8px 20px;
  /* Short fade instead of a hard cut. */
  mask-image: linear-gradient(to bottom, transparent 0, #000 10px, #000 calc(100% - 14px), transparent 100%);
  transition: opacity var(--dur) var(--ease);
  /* The layout anchor above is the one scroll correction (WebKit has none
     of its own; Chromium's would correct twice). */
  overflow-anchor: none;
}

/* A refresh keeps the rows you were reading; they step back a little
   while the hairline runs, and nothing jumps when the answer lands. */
.scroll.stale {
  opacity: 0.55;
  pointer-events: none;
}

/* The batch bar floats over the bottom of the list; the last row must be
   able to scroll clear of it. */
.scroll.selecting {
  padding-bottom: 84px;
}

/* ---------- picking ---------- */

/* ⇧-click extends the selection; it must not also select the names' text. */
.folder,
.card,
.rows tbody tr {
  user-select: none;
  -webkit-user-select: none;
}

.folder {
  position: relative;
}

/* The pick box: shown on hover, and on every entry while two or more are
   picked, so adding one more needs no modifier key. The ring is a border:
   on a 1x screen WebKit draws a 1.5px inset-shadow ring lopsided (2px top
   and left, 1px bottom and right), while a border is snapped evenly. */
.check {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 2;
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border: 1.5px solid rgba(255, 255, 255, 0.9);
  border-radius: 50%;
  color: transparent;
  background: rgba(6, 16, 28, 0.32);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25);
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--dur) var(--ease);
}

/* Drawn at 22px (18 in the table), hit at 40. */
.check::before {
  content: "";
  position: absolute;
  inset: calc(50% - 20px);
  border-radius: 50%;
}

.folder:hover .check,
.card:hover .check,
.rows tbody tr:hover .check,
.selecting .check,
.check.on {
  opacity: 1;
}

/* border-box: the gradient runs under the ring instead of tiling into it. */
.check.on {
  color: var(--on-primary);
  background: var(--primary) border-box;
}

/* In the table the box sits in line, beside the type glyph. Relative only
   anchors the hit area: the overlay's top/left must not move the box. */
.rows .check {
  position: relative;
  inset: auto;
  flex: none;
  width: 18px;
  height: 18px;
  border-color: var(--fg-dim);
  background: transparent;
  box-shadow: none;
}

.rows .check.on {
  border-color: transparent;
  background: var(--primary) border-box;
}

/* ---------- ghosts: the list before the list ---------- */

/* Ghost rows occupy the exact final row positions, so initial loading and
   browsing-scope changes do not shift content. */
.ghost {
  overflow: hidden;
}

.ghost-h2 {
  width: 96px;
  height: 16px;
  margin: 7px 0 17px;
}

.ghost-folder,
.ghost-card {
  cursor: default;
}

.ghost-fill {
  position: absolute;
  inset: 4px;
  border-radius: 5px;
}

.ghost-folder .folder-text {
  flex: 1;
}

.ghost-icon {
  flex: none;
  width: 56px;
  height: 48px;
  border-radius: 10px;
}

.ghost-icon.small {
  width: 18px;
  height: 18px;
  border-radius: 5px;
}

.ghost-line {
  display: block;
  height: 13px;
  border-radius: 6px;
}

.ghost-line.thin {
  height: 10px;
  margin-top: 7px;
}

.ghost-card .meta {
  gap: 0;
}

.ghost-rows {
  display: flex;
  flex-direction: column;
}

.ghost-row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 46px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
}

.ghost-row .ghost-line.thin {
  margin-top: 0;
}

/* ---------- sections ---------- */

h2 {
  margin: 4px 0 14px;
  font-size: 17px;
  line-height: 22px;
  font-weight: 600;
}

h2 + .folders + h2,
.folders + h2,
.rows + h2 {
  margin-top: 26px;
}

.count {
  margin-left: 2px;
  font-weight: 500;
  color: var(--fg-dim);
}

/* ---------- folders ---------- */

/* Columns derive from the list width rather than window width, with a 216px
   card minimum and 16px gap. */
.folders {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(216px, 1fr));
  gap: 16px;
}

/* A card is a cover of the folder's direct children (one large
   tile, two small) inside a folder outline, then the name and the count. */
.folder {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  justify-content: flex-start;
  gap: 0;
  min-width: 0;
  padding: 13px 12px 10px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: var(--surface);
  box-shadow: none;
  text-align: left;
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}

.folder:hover:not(:disabled) {
  border-color: var(--line-strong);
  background: var(--surface-hover);
}

.folder.picked,
.folder.picked:hover {
  border-color: transparent;
  box-shadow: 0 0 0 2px var(--accent), 0 0 0 6px var(--accent-soft);
}

/* The pick box and the star sit in the cover's top corners, 4px inside the
   tiles, as a file card's do on its thumbnail (1 border + 12/13 padding +
   4 margin + 1 border + 4 padding + 4). */
.folder > .check {
  top: 26px;
  left: 21px;
}

.folder > .star-mark {
  top: 26px;
  right: 21px;
}

/* Light by default, the dark skin below (the mock's colours). */
.folders {
  --cover-bg: #dce9f3;
  --cover-line: rgba(78, 121, 154, 0.25);
  --cover-empty: rgba(72, 115, 157, 0.045);
  --tile-photo: #c8dae9;
  --tile-glyph: #c8dae9;
  --tile-document: #c8dae9;
  --tile-audio: #c8dae9;
  --tile-ink: #203c56;
}

:root[data-theme="dark"] .folders {
  --cover-bg: #1a2a3c;
  --cover-line: rgba(147, 188, 219, 0.19);
  --cover-empty: rgba(116, 151, 183, 0.065);
  --tile-photo: #29435b;
  --tile-glyph: #334c65;
  --tile-document: #3b4b64;
  --tile-audio: #273c54;
  --tile-ink: #dceaf5;
}

.folder-cover {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1.68fr) minmax(0, 1fr);
  grid-template-rows: repeat(2, minmax(0, 1fr));
  gap: 4px;
  flex: none;
  height: 119px;
  margin-top: 4px;
  padding: 4px;
  border: 1px solid var(--cover-line);
  border-radius: 10px;
  background: var(--cover-bg);
}


/* Fewer than three children: one fills the cover, two split it. */
.folder-cover.cover-n1 {
  grid-template-columns: minmax(0, 1fr);
}

.folder-cover.cover-n1 .hero,
.folder-cover.cover-n2 .cover-tile:not(.hero) {
  grid-row: span 2;
}

.cover-tile {
  position: relative;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  border-radius: 5px;
  background: var(--tile-photo);
}

.cover-tile.hero {
  grid-row: span 2;
}

.cover-tile :deep(img) {
  filter: saturate(0.84) contrast(1.04);
}

.cover-tile.glyph-folder {
  background: var(--tile-glyph);
}

.cover-tile.glyph-document {
  background: var(--tile-document);
}

.cover-tile.glyph-audio {
  background: var(--tile-audio);
}

.tile-glyph {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  /* Full width (it is centred in Thumb's grid), so the name below can
     measure against the tile instead of collapsing to its ellipsis. */
  width: 100%;
  color: var(--tile-ink);
}

.tile-glyph small {
  display: block;
  max-width: calc(100% - 6px);
  font-size: 10px;
  line-height: 14px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hero .tile-glyph {
  gap: 9px;
}

.hero .tile-glyph small {
  font-size: 11px;
}

.folder-mark,
.mini-folder {
  position: relative;
  display: block;
  flex: none;
  width: 20px;
  height: 15px;
  margin-top: 3px;
  border-radius: 2px 3px 3px 3px;
  background: linear-gradient(180deg, #8ac5ee, #528ac0);
  box-shadow: 0 2px 5px rgba(15, 40, 66, 0.22);
}

.folder-mark::before,
.mini-folder::before {
  content: "";
  position: absolute;
  top: -4px;
  left: 0;
  width: 10px;
  height: 5px;
  border-radius: 3px 3px 0 0;
  background: #669ccb;
}

.mini-folder {
  width: 25px;
  height: 18px;
}

.mini-folder::before {
  width: 13px;
}

.mini-document {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 23px;
  height: 29px;
  padding: 8px 4px 4px;
  border-radius: 3px 5px 3px 3px;
  background: #d9e4ec;
  box-shadow: 0 2px 5px rgba(10, 28, 45, 0.2);
}

.mini-document::before {
  content: "";
  position: absolute;
  top: 0;
  right: 0;
  width: 7px;
  height: 7px;
  background: #9eb4ca;
  clip-path: polygon(0 0, 100% 100%, 0 100%);
}

.mini-document i {
  height: 2px;
  border-radius: 2px;
  background: #809db6;
}

.mini-document i:last-child {
  width: 65%;
}

.hero .mini-document {
  width: 37px;
  height: 48px;
  gap: 6px;
  padding: 12px 7px 6px;
}

.hero .mini-document::before {
  width: 11px;
  height: 11px;
}

.sound-bars {
  display: flex;
  align-items: center;
  gap: 3px;
  height: 25px;
}

.sound-bars i {
  width: 3px;
  height: 13px;
  border-radius: 3px;
  background: #91c5e7;
}

:root[data-theme="light"] .sound-bars i {
  background: #4f88b8;
}

.sound-bars i:nth-child(2),
.sound-bars i:nth-child(4) {
  height: 22px;
}

.sound-bars i:nth-child(3) {
  height: 17px;
}

/* A video frame says so; a still does not. */
.play-mark {
  position: absolute;
  top: 50%;
  left: 50%;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding-left: 2px;
  transform: translate(-50%, -50%);
  border: 1px solid rgba(255, 255, 255, 0.68);
  border-radius: 50%;
  background: rgba(9, 23, 40, 0.48);
  color: #fff;
  pointer-events: none;
}

/* Only over a frame that arrived: a failed one shows its kind instead. */
.thumb-img:not(.loaded) + .play-mark {
  display: none;
}

/* Before the index can say (and in the bin): the outline and a folder. */
.folder-cover.cover-plain {
  display: grid;
  place-items: center;
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
}

.folder-cover.cover-plain > .mini-folder {
  opacity: 0.62;
  transform: scale(1.5);
}

.folder-cover.cover-empty {
  display: grid;
  place-items: center;
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
  border-style: dashed;
  background: var(--cover-empty);
}


.empty-symbol {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 13px;
  color: var(--fg-dim);
  font-size: 12px;
}

.empty-symbol .mini-folder {
  opacity: 0.62;
  transform: scale(1.5);
}

.folder-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  width: 100%;
  padding: 8px 2px 0;
}

.folder-meta .chevron {
  flex: none;
  color: var(--fg-dim);
  opacity: 0.8;
}

/* One column (a phone-narrow window): the cover takes the extra width as height. */
@media (max-width: 520px) {
  .folder-cover {
    height: 140px;
  }
}

/* Starred at a glance. A starred folder card wears the file cards' gold mark
   in its cover's top-right corner (the pick box has the other), its edge
   warmed to match and a faint gold light from that corner, so it stands
   out in a grid of plain cards. A file shows the mark in the thumbnail's
   free top corner (duration, play and the playing mark take the bottom),
   in the playing mark's dark glass so one gold reads on any frame. */
.folder.starred {
  border-color: color-mix(in srgb, var(--star) 32%, transparent);
  background:
    radial-gradient(160px 110px at 100% 0, color-mix(in srgb, var(--star) 13%, transparent), transparent 70%),
    var(--surface);
}

.folder.starred:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--star) 48%, transparent);
  background:
    radial-gradient(160px 110px at 100% 0, color-mix(in srgb, var(--star) 17%, transparent), transparent 70%),
    var(--surface-hover);
}

.folder.starred.picked {
  border-color: transparent;
}

.star-mark {
  position: absolute;
  z-index: 1;
  top: 10px;
  right: 10px;
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: 7px;
  color: #ffcf5a;
  background: rgba(6, 16, 28, 0.7);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.18);
  pointer-events: none;
}

/* In the list the row's own star button is the mark: gold when set. */
.rows .actions button.starred {
  color: var(--star);
}

.folder-icon.blue {
  --t0: #7cc4ff;
  --t1: #2f86f0;
}

.folder-icon.purple {
  --t0: #c9a6ff;
  --t1: #8c5ff2;
}

.folder-icon.amber {
  --t0: #ffd98a;
  --t1: #f0a63c;
}

.folder-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.folder-text strong,
.folder-text small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-text strong {
  font-size: 15px;
  line-height: 21px;
  font-weight: 600;
}

.folder-text small {
  margin-top: 1px;
  font-size: 12px;
  line-height: 18px;
  color: var(--fg-dim);
}

/* ---------- file grid ---------- */

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(216px, 1fr));
  gap: 20px 16px;
}

/* Three 216px cards need 680px including gaps; keep their count stable
   across that boundary while giving smaller canvases a readable card size. */
@container files (max-width: 679px) {
  .grid {
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 172px), 1fr));
  }
}

/* Match the forced icon rail in dom.ts: expanding it must not reduce columns. */
@media (max-width: 1079px) {
  .grid {
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 216px), 1fr));
  }
}

/* The mock puts the picture straight on the canvas: no card chrome, the
   selection ring hugs the thumbnail. */
.card {
  position: relative;
  min-width: 0;
  cursor: pointer;
}

/* content-visibility: off-screen thumbnails (picture, blurred chips, play
   button) are skipped, so a resize — the nav collapsing, the window
   being dragged — only lays out and paints the cards in view. The 16:9
   ratio sizes the box from its width, skipped or not, so no placeholder
   height is needed and the scroll height stays exact. */
.thumb {
  position: relative;
  aspect-ratio: 16 / 9;
  border-radius: var(--radius-card);
  overflow: hidden;
  background: color-mix(in srgb, var(--fg) 8%, transparent);
  display: grid;
  place-items: center;
  content-visibility: auto;
  transition: box-shadow var(--dur) var(--ease);
}

/* The rim sits over the picture; Thumb owns the sweep and fade. */
.thumb::after {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: inherit;
  box-shadow: inset 0 0 0 1px var(--line-strong), inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 30%, transparent);
  pointer-events: none;
}

.card.picked .thumb {
  box-shadow: 0 0 0 2px var(--accent), 0 0 0 6px var(--accent-soft);
}

.duration {
  position: absolute;
  right: 10px;
  bottom: 10px;
  padding: 3px 8px;
  border-radius: 8px;
  font-size: 12px;
  line-height: 16px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: #fff;
  background: rgba(6, 16, 28, 0.64);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  transition: opacity var(--dur) var(--ease);
}

/* The play affordance takes the badge's corner on hover, so the two never
   stack on the same pixels. */
.card:hover .duration {
  opacity: 0;
}

/* "This one is in the player": a different mark from selection, so both
   can show at once on different cards (mock 05). */
.playing {
  position: absolute;
  left: 10px;
  bottom: 10px;
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: 7px;
  color: #fff;
  background: rgba(6, 16, 28, 0.7);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.18);
}

.playing.inline {
  position: static;
  flex: none;
}

.play {
  position: absolute;
  right: 10px;
  bottom: 10px;
  width: 34px;
  height: 34px;
  min-height: 34px;
  padding: 0;
  border-radius: 50%;
  color: var(--on-primary);
  background: var(--primary) border-box;
  border-color: transparent;
  box-shadow: inset 0 1px 0 var(--primary-rim), 0 6px 14px rgba(0, 0, 0, 0.3);
  opacity: 0;
  transition: opacity var(--dur) var(--ease);
}

.card:hover .play,
.card:focus-within .play {
  opacity: 1;
}

/* Keeps its fill on hover: the plain button:hover outranks `.play`. */
.play:hover:not(:disabled) {
  background: var(--primary) border-box;
  border-color: transparent;
}

.meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 2px 0;
  min-width: 0;
}

.name-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.name-row strong {
  flex: 1;
  min-width: 0;
  font-size: 15px;
  line-height: 20px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta small {
  font-size: 13px;
  line-height: 18px;
  color: var(--fg-dim);
}

.meta small.path {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: color-mix(in srgb, var(--accent) 55%, var(--fg-dim));
}

.more {
  width: 28px;
  height: 28px;
  min-height: 28px;
  margin-right: -4px;
  color: var(--fg-dim);
}

.more.open,
.actions .open {
  background: var(--accent-soft);
  color: var(--fg);
}

/* ---------- list ---------- */

.rows {
  width: 100%;
  table-layout: fixed;
  border-collapse: collapse;
  font-size: 15px;
}

.rows th,
.rows td {
  text-align: left;
  padding: 9px 12px;
  border-bottom: 1px solid var(--line);
}

.rows th {
  font-weight: 500;
  font-size: 13px;
  color: var(--fg-dim);
}

.rows tbody tr {
  cursor: pointer;
  transition: background var(--dur) var(--ease);
}

.rows tbody tr:hover {
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

.rows tbody tr.picked {
  background: var(--accent-soft);
  box-shadow: inset 2px 0 0 var(--accent);
}

.rows tbody td:first-child {
  border-top-left-radius: 10px;
  border-bottom-left-radius: 10px;
}

.rows tbody td:last-child {
  border-top-right-radius: 10px;
  border-bottom-right-radius: 10px;
}

.rows .cell {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  min-height: 32px;
}

.rows .cell.end {
  justify-content: flex-end;
  gap: 2px;
}

.rows .name {
  font-weight: 500;
}

.rows .name > .cell > svg {
  color: var(--fg-dim);
}

/* A folder keeps the tint it has in the grid. */
.rows .name > .cell > .folder-icon {
  color: var(--t1);
}

.rows .label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rows .num {
  width: 100px;
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.rows .dim {
  width: 118px;
  color: var(--fg-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* A path needs more room than a date. */
.rows.located .dim {
  width: 30%;
}

/* Beside the right panel a path, a size, a date's room and three buttons
   left the name a few letters: there the location replaces the size. */
@container files (max-width: 620px) {
  .rows.located .num {
    display: none;
  }
}

/* Room for Play/Preview, Starred and More at full size (3 × 32 + 2 × 2 gap + 24
   padding): at 116 they shrank to 29px and the stars of rows with and
   without a play button no longer lined up. */
.rows .actions {
  width: 124px;
  padding-top: 6px;
  padding-bottom: 6px;
}

/* The bin's rows carry More only; its date is a day and a time
   (Oct 27 03:32), wider than the day the other pages show. */
.rows.bin .actions {
  width: 56px;
}

.rows.bin .dim {
  width: 140px;
}

.rows .actions button {
  flex: none;
  width: 32px;
  height: 32px;
  min-height: 32px;
}

.rows .actions .slot {
  flex: none;
  width: 32px;
}

/* ---------- menu ---------- */

.menu {
  position: fixed;
  z-index: 60;
  min-width: 168px;
  overflow-y: auto;
  overscroll-behavior: contain;
  border: 1px solid var(--glass-edge);
  animation: menu-in 0.18s var(--ease) both;
}

/* Above the button it grows out of the button too: up, not down. */
.menu.up {
  animation-name: menu-in-up;
}

.menu button {
  gap: 10px;
  padding: 0 12px;
  font-size: 15px;
}

.menu.renaming {
  width: min(340px, calc(100vw - 24px));
  padding: 16px;
}

.rename-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.rename-form label {
  font-size: 14px;
  font-weight: 600;
}

.rename-form input {
  width: 100%;
  min-width: 0;
}

.rename-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.rename-actions button {
  min-height: 36px;
  font-size: 14px;
}

@keyframes menu-in {
  from {
    opacity: 0;
    transform: translateY(-4px) scale(0.98);
  }
}

@keyframes menu-in-up {
  from {
    opacity: 0;
    transform: translateY(4px) scale(0.98);
  }
}

/* ---------- empty / more ---------- */

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 72px 0;
  color: var(--fg-dim);
  font-size: 15px;
  text-align: center;
  text-wrap: balance;
}

.empty small {
  max-width: 32em;
  margin-top: -4px;
  font-size: 13px;
  text-align: center;
  text-wrap: balance;
  opacity: 0.8;
}

.more-row {
  display: flex;
  justify-content: center;
  padding: 20px 0 4px;
}

/* ---------- the recycle bin ---------- */

/* Empty recycle bin keeps the control row's geometry; colors use the global danger action. */
.purge {
  flex: none;
  gap: 8px;
  min-height: 44px;
  padding: 0 18px;
  font-size: 15px;
  font-weight: 500;
}


/* The question takes its own line under the row, so the controls never
   squeeze: the consequence on the left, the answer on the right. (`.files`
   so it outranks ConfirmInline's own column.) */
.files .purge-confirm {
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 16px;
  margin: -8px 0 18px;
  padding: 10px 12px 10px 16px;
  border: 1px solid color-mix(in srgb, var(--danger) 30%, transparent);
  border-radius: 14px;
  background: color-mix(in srgb, var(--danger) 8%, transparent);
}

.files .purge-confirm :deep(p) {
  font-size: 14px;
  color: var(--fg);
}

/* Delete in the bin asks here, where the batch bar floats. */
.key-confirm {
  position: absolute;
  left: 50%;
  bottom: 14px;
  z-index: 5;
  transform: translateX(-50%);
  max-width: calc(100% - 24px);
  padding: 6px 8px 6px 18px;
  border-radius: var(--radius-pill);
  box-shadow: var(--shadow-sheet);
}

.key-confirm .confirm {
  flex-direction: row;
  align-items: center;
  gap: 10px;
  padding-top: 0;
  border-top: 0;
}

.key-confirm .confirm :deep(p) {
  font-size: 14px;
  color: var(--danger);
}

.menu.confirming {
  max-width: 280px;
  padding: 12px;
}

.menu.confirming .confirm {
  padding-top: 0;
  border-top: 0;
}
</style>
