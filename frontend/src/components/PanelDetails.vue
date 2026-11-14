<script setup lang="ts">
// Details: one entry in full; several summed up — counts, and
// only the sizes that can be added honestly; nothing picked, the folder on
// screen. A field that does not apply to the kind of entry is not drawn; one
// that applies but is still loading or could not be read says so in words.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { N_, t } from "../i18n";
import ConfirmInline from "./ConfirmInline.vue";
import Icon from "./Icon.vue";
import FolderStatsCard from "./FolderStatsCard.vue";
import Thumb from "./Thumb.vue";
import { bytes, clock, day, kindLabel, shownPath, stamp } from "../format";
import { useOutsideClick } from "../dom";
import {
  clearSelection,
  copyDownloadLink,
  deleteForever,
  downloadEntries,
  freedText,
  drive,
  openCreateShare,
  locateEntry,
  openExtract,
  openFolder,
  openRelocate,
  openViewer,
  pickedEntries,
  play,
  rename,
  restore,
  retryDetail,
  toggleStar,
  trash,
  ui,
  viewer,
  wholeDrive,
} from "../store";

const CATEGORY: Record<string, string> = { video: N_("视频"), image: N_("图片"), document: N_("文档"), audio: N_("音频"), other: N_("其他") };

const inBin = computed(() => ui.view === "trash");
const searching = computed(() => !!ui.query.trim());

/* ---------- where things are ---------- */

const folderPath = computed(() => [t("全部文件"), ...drive.trail.map((c) => c.name)].join(" / "));
// Search results and the whole drive carry their own path; elsewhere the
// view is the place. The whole drive read before the index is ready knows
// no paths: Locate containing folder still finds the folder from the server.
function placeOf(entry: { path: string }): string {
  if (entry.path) return shownPath(entry.path);
  if (ui.view === "starred") return t("星标");
  if (inBin.value) return t("回收站");
  if (wholeDrive.value) return t("本地索引建立后显示");
  return folderPath.value;
}

/* ---------- one entry ---------- */

// While the viewer is up, Details is the file being viewed —
// which a search hit or a transfer's file may be without being listed.
const viewing = computed(() => (ui.route === "viewer" ? viewer.entry : null));
const entry = computed(() => {
  const v = viewing.value;
  if (v) return drive.entries.find((e) => e.id === v.id) ?? v;
  return drive.picked.length === 1 ? (drive.entries.find((e) => e.id === drive.selectedId) ?? null) : null;
});
const detail = computed(() => {
  const id = viewing.value?.id ?? drive.selectedId;
  return drive.detail && drive.detail.id === id ? drive.detail : null;
});
const shown = computed(() => detail.value ?? entry.value);
const typeLabel = computed(() => (shown.value ? kindLabel(shown.value) : ""));
// Where the folder is not the page itself, the entry can be
// shown in its direct parent.
const canLocate = computed(() => !!entry.value && !inBin.value && (!!entry.value.path || wholeDrive.value));

// Media gets its real thumbnail at 16:9; anything else a type glyph beside
// the name. Folder thumbnails from the server are strips of their contents
// and read as a glitch when cropped, so they are never shown.
const showsPicture = computed(() => {
  const e = shown.value;
  return !!e && !e.folder && !!e.thumbnail && (e.category === "video" || e.category === "image");
});
const wantsDuration = computed(() => !!shown.value && (shown.value.category === "video" || shown.value.category === "audio"));
const wantsResolution = computed(() => !!shown.value && (shown.value.category === "video" || shown.value.category === "image"));
const duration = computed(() => detail.value?.duration || entry.value?.duration || 0);
// The server's dimensions, else what the viewer decoded of the picture.
const resolution = computed(() => {
  if (detail.value?.width && detail.value?.height) return `${detail.value.width} × ${detail.value.height}`;
  const n = viewing.value && viewer.natural;
  return n ? `${n.w} × ${n.h}` : "";
});
// Text in the viewer: the encoding it was read in, never assumed.
const encoding = computed(() => {
  if (!viewing.value || (viewer.kind !== "text" && viewer.kind !== "markdown") || !viewer.text) return "";
  const e = viewer.text.encoding;
  return e === "unknown" ? t("无法识别（不是 UTF-8 / UTF-16）") : e.toUpperCase();
});
// A folder's size would mean walking it; only the count of what is directly
// in it is shown, and only when the local index knows it.
const children = computed(() => (shown.value?.folder && detail.value && detail.value.children >= 0 ? detail.value.children : -1));

const openLabel = computed(() => {
  const e = shown.value;
  if (!e || viewing.value) return "";
  if (e.folder) return t("打开");
  if (e.playable) return t("播放");
  if (e.preview) return t("预览");
  if (e.category === "archive") return t("在线解压");
  return "";
});

function open() {
  const e = entry.value;
  if (!e) return;
  if (e.folder) void openFolder(e);
  else if (e.playable) void play(e);
  else if (e.preview) void openViewer(e);
  else if (e.category === "archive") openExtract(e);
}

/* ---------- several ---------- */

const many = computed(() => (drive.picked.length > 1 ? pickedEntries.value : []));
const manyFolders = computed(() => many.value.filter((e) => e.folder).length);
const manyFiles = computed(() => many.value.length - manyFolders.value);
const manySize = computed(() => many.value.reduce((sum, e) => sum + (e.folder ? 0 : e.size), 0));
const manyPlace = computed(() => {
  const places = new Set(many.value.map(placeOf));
  return places.size === 1 ? [...places][0] : t("多个位置");
});
const LISTED = 6;

/* ---------- nothing picked: the folder ---------- */

const folderTitle = computed(() => {
  if (searching.value) return t("搜索结果");
  if (ui.view === "starred") return t("星标");
  if (inBin.value) return t("回收站");
  if (wholeDrive.value) return t("整个云盘");
  return drive.trail.at(-1)?.name ?? t("全部文件");
});
const loadedText = computed(() => {
  if (!drive.live && drive.total >= 0) return t("共 {n} 项", { n: drive.total });
  const n = drive.entries.length;
  return drive.nextPageToken ? t("已加载 {n} 项，还有更多", { n }) : t("{n} 项", { n });
});

/* ---------- actions ---------- */

const menuOpen = ref(false);
// The actions end the column, so below More is often its bottom edge: the
// menu opens above when only there does it fit whole. Neither side holding
// it (a very short window), it opens below and the column scrolls to it.
const menuUp = ref(false);
const moreWrap = ref<HTMLElement | null>(null);
const menuEl = ref<HTMLElement | null>(null);
const MENU_GAP = 6;

watch(
  menuOpen,
  (open) => {
    const wrap = moreWrap.value;
    const menu = menuEl.value;
    if (!open || !wrap || !menu) return;
    const view = visibleBox(wrap);
    const at = wrap.getBoundingClientRect();
    const need = menu.offsetHeight + MENU_GAP;
    const roomBelow = view.bottom - at.bottom >= need;
    menuUp.value = !roomBelow && at.top - view.top >= need;
    if (!roomBelow && !menuUp.value) void nextTick(() => menuEl.value?.scrollIntoView({ block: "nearest" }));
  },
  { flush: "post" },
);

// visibleBox is the stretch of the window that el's nearest scrolling
// ancestor shows.
function visibleBox(el: HTMLElement): { top: number; bottom: number } {
  let top = 0;
  let bottom = window.innerHeight;
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/auto|scroll/.test(getComputedStyle(p).overflowY)) {
      const box = p.getBoundingClientRect();
      top = Math.max(top, box.top);
      bottom = Math.min(bottom, box.bottom);
      break;
    }
  }
  return { top, bottom };
}

const renaming = ref(false);
const confirming = ref(false);
const draft = ref("");


watch(
  [() => drive.selectedId, () => drive.picked],
  () => {
    menuOpen.value = false;
    renaming.value = false;
    confirming.value = false;
  },
);

function startRename() {
  draft.value = shown.value?.name ?? "";
  renaming.value = true;
  menuOpen.value = false;
}

async function commitRename() {
  const target = entry.value;
  renaming.value = false;
  const name = draft.value.trim();
  if (target && name && name !== target.name) await rename(target, name);
}

function menuAction(fn: () => unknown) {
  menuOpen.value = false;
  void fn();
}

useOutsideClick("[data-detail-menu]", () => menuOpen.value, () => (menuOpen.value = false));

// An open menu or a rename owns Esc, ahead of the page (which would clear
// the selection on the same key).
function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || !(menuOpen.value || renaming.value || confirming.value)) return;
  event.stopImmediatePropagation();
  menuOpen.value = false;
  renaming.value = false;
  confirming.value = false;
}

onMounted(() => window.addEventListener("keydown", onEscape, true));
onBeforeUnmount(() => window.removeEventListener("keydown", onEscape, true));
</script>

<template>
  <!-- ---------- one entry ---------- -->
  <div v-if="shown" class="details">
    <p class="kicker">{{ viewing ? t("当前查看") : t("已选 1 项") }}</p>

    <div v-if="showsPicture" class="picture">
      <Thumb :src="shown.thumbnail" :alt="shown.name">
        <Icon :name="shown.category" :size="30" />
      </Thumb>
    </div>

    <div class="heading">
      <span v-if="!showsPicture" class="glyph" :class="shown.folder ? 'folder' : shown.category">
        <Icon :name="shown.folder ? 'folder' : shown.category" :size="26" :filled="shown.folder" />
      </span>
      <div class="heading-text">
        <input
          v-if="renaming"
          v-model="draft"
          :aria-label="t('新名称')"
          autofocus
          @keyup.enter="commitRename"
          @blur="commitRename"
        />
        <h2 v-else :title="shown.name">{{ shown.name }}</h2>
        <small>{{ typeLabel }}</small>
      </div>
    </div>

    <dl class="facts" :aria-busy="drive.detailLoading">
      <div>
        <dt>{{ t("类型") }}</dt>
        <dd>{{ typeLabel }}</dd>
      </div>
      <div v-if="!shown.folder">
        <dt>{{ t("大小") }}</dt>
        <dd class="tnum">{{ bytes(shown.size, "0 B") }}</dd>
      </div>
      <div v-if="children >= 0">
        <dt>{{ t("包含") }}</dt>
        <dd class="tnum">{{ t("{n} 项", { n: children }) }}</dd>
      </div>
      <div v-if="wantsDuration">
        <dt>{{ t("时长") }}</dt>
        <dd v-if="duration" class="tnum">{{ clock(duration) }}</dd>
        <dd v-else-if="drive.detailLoading" class="skel pending" style="width: 56px" />
        <dd v-else class="missing">{{ t("未获取") }}</dd>
      </div>
      <div v-if="wantsResolution">
        <dt>{{ t("尺寸") }}</dt>
        <dd v-if="resolution" class="tnum">{{ resolution }}</dd>
        <dd v-else-if="drive.detailLoading" class="skel pending" style="width: 84px" />
        <dd v-else class="missing">{{ t("未获取") }}</dd>
      </div>
      <div v-if="encoding">
        <dt>{{ t("编码") }}</dt>
        <dd>{{ encoding }}</dd>
      </div>
      <div v-if="!shown.folder">
        <dt>{{ t("保存时间") }}</dt>
        <dd v-if="detail?.createdTime">{{ day(detail.createdTime) }}</dd>
        <dd v-else-if="drive.detailLoading" class="skel pending" style="width: 96px" />
        <dd v-else class="missing">{{ t("未获取") }}</dd>
      </div>
      <div v-if="shown.modifiedTime">
        <dt>{{ t("修改时间") }}</dt>
        <dd>{{ day(shown.modifiedTime) }}</dd>
      </div>
      <div v-if="inBin && entry?.deleteTime">
        <dt>{{ t("预计清理时间") }}</dt>
        <dd>{{ stamp(entry.deleteTime) }}</dd>
      </div>
      <!-- The listed row has the path; the detail read for it carries none. -->
      <div>
        <dt>{{ t("位置") }}</dt>
        <dd>{{ placeOf(entry ?? shown) }}</dd>
      </div>
      <div v-if="detail?.hash">
        <dt>{{ t("内容校验") }}</dt>
        <dd class="mono">{{ detail.hash.slice(0, 16) }}…</dd>
      </div>
    </dl>

    <button v-if="canLocate" class="locate" @click="locateEntry(entry!)">
      <Icon name="folder" :size="16" />{{ t("定位到所在文件夹") }}<Icon class="chevron" name="chevronRight" :size="14" />
    </button>

    <p v-if="drive.detailError" class="error" role="status">
      {{ t("详情没有读到：{error}", { error: drive.detailError }) }}
      <button class="small" @click="retryDetail">{{ t("重试") }}</button>
    </p>

    <ConfirmInline
      v-if="confirming"
      class="confirm"
      :message="t('彻底删除「{name}」？无法撤销，{freed}。', { name: shown.name, freed: freedText([shown]) })"
      :confirm-label="t('确认删除')"
      :cancel-label="t('保留')"
      danger
      @cancel="confirming = false"
      @confirm="confirming = false; deleteForever([shown])"
    />
    <div v-else class="actions" data-detail-menu>
      <template v-if="inBin">
        <button @click="restore([shown])"><Icon name="restore" :size="16" />{{ t("还原") }}</button>
        <button class="danger" @click="confirming = true"><Icon name="trash" :size="16" />{{ t("彻底删除") }}</button>
      </template>
      <template v-else>
        <button v-if="openLabel" @click="open">
          <Icon :name="shown.folder ? 'folder' : shown.playable ? 'play' : shown.preview ? 'eye' : 'archive'" :size="16" />{{ openLabel }}
        </button>
        <button @click="openCreateShare([shown])"><Icon name="share" :size="16" />{{ t("分享") }}</button>
        <button :aria-pressed="shown.starred" @click="toggleStar(shown)">
          <Icon name="star" :size="16" :filled="shown.starred" />{{ shown.starred ? t("已加星标") : t("星标") }}
        </button>
        <span ref="moreWrap" class="more-wrap">
          <button :title="t('更多')" aria-haspopup="menu" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
            <Icon name="more" :size="16" filled />{{ t("更多") }}
          </button>
          <div v-if="menuOpen" ref="menuEl" class="menu glass glass-sheet" :class="{ up: menuUp }" role="menu">
            <button class="quiet" role="menuitem" @click="menuAction(() => downloadEntries([shown!]))">
              <Icon name="download" :size="16" />{{ t("下载到电脑") }}
            </button>
            <button v-if="!shown.folder" class="quiet" role="menuitem" @click="menuAction(() => copyDownloadLink(shown!))">
              <Icon name="link" :size="16" />{{ t("复制直链") }}
            </button>
            <button class="quiet" role="menuitem" @click="startRename"><Icon name="text" :size="16" />{{ t("重命名") }}</button>
            <button class="quiet" role="menuitem" @click="menuAction(() => openRelocate('move', [shown!]))">
              <Icon name="move" :size="16" />{{ t("移动到…") }}
            </button>
            <button class="quiet" role="menuitem" @click="menuAction(() => openRelocate('copy', [shown!]))">
              <Icon name="copy" :size="16" />{{ t("复制到…") }}
            </button>
            <button
              class="quiet danger"
              role="menuitem"
              :title="t('可以在回收站还原；空间要等彻底删除后才释放')"
              @click="menuAction(() => trash([shown!]))"
            >
              <Icon name="trash" :size="16" />{{ t("移到回收站") }}
            </button>
          </div>
        </span>
      </template>
    </div>
    <FolderStatsCard v-if="shown.folder && !inBin" :folder-id="shown.id" :folder-name="shown.name" />
  </div>

  <!-- ---------- several ---------- -->
  <div v-else-if="many.length" class="details">
    <p class="kicker">{{ t("批量选择") }}</p>
    <div class="heading">
      <span class="count tnum">{{ many.length }}</span>
      <div class="heading-text">
        <h2>{{ t("已选 {n} 项", { n: many.length }) }}</h2>
        <small>{{ t("{folders} 个文件夹 · {files} 个文件", { folders: manyFolders, files: manyFiles }) }}</small>
      </div>
    </div>

    <dl class="facts">
      <div>
        <dt>{{ t("位置") }}</dt>
        <dd>{{ manyPlace }}</dd>
      </div>
      <div>
        <dt>{{ manyFolders ? t("已选文件大小") : t("大小") }}</dt>
        <dd v-if="manyFiles" class="tnum">{{ manyFolders ? t("{size}；文件夹未计入", { size: bytes(manySize, "0 B") }) : bytes(manySize, "0 B") }}</dd>
        <dd v-else class="missing">{{ t("文件夹大小未统计") }}</dd>
      </div>
    </dl>

    <ul class="names">
      <li v-for="e in many.slice(0, LISTED)" :key="e.id">
        <Icon :name="e.folder ? 'folder' : e.category" :size="15" :filled="e.folder" />
        <span :title="e.name">{{ e.name }}</span>
      </li>
      <li v-if="many.length > LISTED" class="more-names">{{ t("还有 {n} 项", { n: many.length - LISTED }) }}</li>
    </ul>

    <!-- The bin's two verbs, as for one entry (the batch bar has them too):
         a selection is acted on from where it is described. -->
    <ConfirmInline
      v-if="inBin && confirming"
      class="confirm"
      :message="t('彻底删除 {n} 项？无法撤销，{freed}。', { n: many.length, freed: freedText(many) })"
      :confirm-label="t('确认删除')"
      :cancel-label="t('保留')"
      danger
      @cancel="confirming = false"
      @confirm="confirming = false; deleteForever(many)"
    />
    <div v-else class="actions">
      <template v-if="inBin">
        <button @click="restore(many)"><Icon name="restore" :size="16" />{{ t("还原") }}</button>
        <button class="danger" @click="confirming = true"><Icon name="trash" :size="16" />{{ t("彻底删除") }}</button>
      </template>
      <button @click="clearSelection"><Icon name="close" :size="15" />{{ t("取消选择") }}</button>
    </div>
  </div>

  <!-- ---------- nothing picked ---------- -->
  <div v-else class="details">
    <p class="kicker">{{ wholeDrive ? t("浏览范围") : t("当前文件夹") }}</p>
    <div class="heading">
      <span class="glyph folder"><Icon :name="searching ? 'search' : ui.view === 'starred' ? 'star' : inBin ? 'trash' : 'folder'" :size="26" :filled="!searching && ui.view === 'all'" /></span>
      <div class="heading-text">
        <h2>{{ folderTitle }}</h2>
        <small>{{ searching ? `“${ui.query.trim()}”` : wholeDrive ? t("按文件类型浏览") : ui.view === "all" ? t("文件夹") : t("视图") }}</small>
      </div>
    </div>
    <dl class="facts">
      <div v-if="ui.view === 'all' && !searching">
        <dt>{{ t("位置") }}</dt>
        <dd>{{ wholeDrive ? t("全部文件及其中所有文件夹") : folderPath }}</dd>
      </div>
      <div>
        <dt>{{ wholeDrive ? t("文件") : t("项目") }}</dt>
        <dd class="tnum">{{ drive.listLoading && !drive.entries.length ? t("正在读取…") : loadedText }}</dd>
      </div>
      <div v-if="ui.category !== 'all'">
        <dt>{{ t("筛选") }}</dt>
        <dd>{{ t("只看{kind}", { kind: t(CATEGORY[ui.category]) }) }}</dd>
      </div>
    </dl>
    <p class="note">{{ wholeDrive ? t("选择一个文件，查看它来自哪个文件夹。") : t("选择文件后，在这里查看它的属性。") }}</p>
    <FolderStatsCard v-if="ui.view === 'all' && !searching && !wholeDrive" :folder-id="drive.trail.at(-1)?.id ?? ''" :folder-name="folderTitle" />
  </div>
</template>

<style scoped>
.details {
  display: flex;
  flex-direction: column;
}

.kicker {
  margin: 0 0 16px;
  font-size: 12px;
  color: var(--fg-dim);
}

/* 16:9 at a fixed height: as a shrinkable flex item an aspect box gets
   squashed by long content below it. */
.picture {
  position: relative;
  flex: none;
  height: 140px;
  margin-bottom: 16px;
  border-radius: 12px;
  overflow: hidden;
  background: color-mix(in srgb, var(--fg) 10%, transparent);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--glass-rim) 45%, transparent);
}

.heading {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.glyph,
.count {
  flex: none;
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 14px;
  background: var(--accent-soft);
  color: var(--accent);
}

.count {
  font-size: 22px;
  font-weight: 600;
}

.heading-text {
  flex: 1;
  min-width: 0;
}

/* The whole name, wrapped: the card already shortens it; here is where it
   is read in full. */
.heading h2 {
  margin: 2px 0 0;
  font-size: 18px;
  line-height: 26px;
  font-weight: 600;
  overflow-wrap: anywhere;
  user-select: text;
  -webkit-user-select: text;
}

.heading small {
  display: block;
  margin-top: 4px;
  font-size: 13px;
  color: var(--fg-dim);
}

.heading input {
  width: 100%;
  min-height: 36px;
  padding: 0 10px;
  border-radius: 10px;
  border: 1px solid var(--accent);
  background: var(--glass-control);
  color: var(--fg);
  font: inherit;
  font-size: 15px;
  font-weight: 600;
}

.facts {
  margin: 20px 0 0;
}

.facts > div {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
}

.facts dt {
  font-size: 12px;
  color: var(--fg-dim);
}

.facts dd {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  overflow-wrap: anywhere;
  user-select: text;
  -webkit-user-select: text;
}

.facts dd.missing {
  color: var(--fg-dim);
}

.facts dd.pending {
  display: block;
  height: 12px;
  margin: 4px 0;
  border-radius: 6px;
}

/* Locate containing folder: a quiet accent line under Location. */
.locate {
  align-self: flex-start;
  min-height: 40px;
  margin-top: 14px;
  padding: 0 12px;
  gap: 6px;
  border: none;
  border-radius: 12px;
  background: var(--accent-soft);
  box-shadow: none;
  color: var(--accent);
  font-size: 13px;
}

.locate .chevron {
  margin-left: 2px;
}

.error {
  margin: 12px 0 0;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  font-size: 12px;
  color: var(--danger);
  overflow-wrap: anywhere;
}

.small {
  min-height: 32px;
  padding: 0 12px;
  font-size: 12px;
}

/* Compact and even: two to a row, so four actions are a tidy square in the
   288px column instead of three and a straggler. */
.actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 22px;
}

.actions button {
  width: 100%;
  min-height: 40px;
  padding: 0 12px;
  gap: 6px;
  border-radius: 12px;
  font-size: 13px;
}

/* The menu items are buttons too; they keep the menu's own look. */
.actions .menu button {
  justify-content: flex-start;
  min-height: 36px;
  border-radius: 9px;
}

.more-wrap {
  position: relative;
}

.more-wrap .menu {
  position: absolute;
  right: 0;
  top: calc(100% + 6px);
  min-width: 150px;
  z-index: 5;
}

/* More in the left column (no Open before it): the menu opens rightwards,
   into the column, not past its left edge. */
.more-wrap:nth-child(odd) .menu {
  right: auto;
  left: 0;
}

/* Above More when the column has no room for the menu below it. */
.more-wrap .menu.up {
  top: auto;
  bottom: calc(100% + 6px);
}

.confirm {
  margin-top: 22px;
}

.names {
  margin: 20px 0 0;
  padding: 0;
  list-style: none;
}

.names li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 0;
  border-bottom: 1px solid var(--line);
  font-size: 13px;
}

.names li > svg {
  flex: none;
  color: var(--fg-dim);
}

.names li span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.names .more-names {
  color: var(--fg-dim);
}

.note {
  margin: 16px 0 0;
  font-size: 12px;
  line-height: 20px;
  color: var(--fg-dim);
}
</style>
