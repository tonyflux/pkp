<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Thumb from "./Thumb.vue";
import Orbit from "./Orbit.vue";
import type { Entry, PreparationEntry } from "../../bindings/github.com/tonyflux/pkp/internal/lanshare/models";
import { LANService, lanError } from "../lanstore";
import { accounts, appLocked } from "../store";
import { t } from "../i18n";

const props = defineProps<{ spaceId: string; spaceName: string; opener?: HTMLElement }>();
const emit = defineEmits<{ close: []; tasks: [] }>();
const dialog = ref<HTMLElement>();
const entries = ref<PreparationEntry[]>([]);
const trail = ref<{ id: string; name: string }[]>([]);
const selected = ref<{ id: string; name: string; path: string; error: string; ready: boolean; desktopCompatibleReady: boolean }[]>([]);
const kind = ref<"original" | "compatible">("original");
const isReady = (entry: { ready: boolean; desktopCompatibleReady: boolean; needsVerification?: boolean }) => kind.value === "original" ? entry.ready && !entry.needsVerification : entry.desktopCompatibleReady;
const pending = computed(() => selected.value.filter(file => !isReady(file)));
const loading = ref(false), busy = ref(false), error = ref("");
const submitted = ref(false);
let generation = 0, alive = true;
let opener: HTMLElement | null = null;
const layout = ref<"list" | "grid">("list");
const entriesBox = ref<HTMLElement>();
const scrollBox = ref<HTMLElement>();
const selectionHeading = ref<HTMLElement>();
const thumbnails = ref<Record<string, string>>({});
let observer: IntersectionObserver | undefined;
let thumbnailGeneration = 0, active = 0;
let queue: PreparationEntry[] = [];
const visible = new Set<string>();
const requested = new Set<string>();
type DirectoryPage = { entries: PreparationEntry[]; thumbnails: Record<string, string>; scroll: Record<"list" | "grid", number> };
const directoryCache = new Map<string, DirectoryPage>();
let currentPage: DirectoryPage | undefined;

function saveScroll(view = layout.value) {
  if (currentPage && scrollBox.value) currentPage.scroll[view] = scrollBox.value.scrollTop;
}
async function restoreScroll(current: number) {
  await nextTick();
  if (alive && current === generation && scrollBox.value) scrollBox.value.scrollTop = currentPage?.scroll[layout.value] ?? 0;
}
function rememberThumbnail(id: string, src: string) {
  thumbnails.value[id] = src;
  const ids = Object.keys(thumbnails.value);
  if (ids.length > 64) delete thumbnails.value[ids[0]!];
}
watch(layout, (_view, previous) => { saveScroll(previous); void restoreScroll(generation); }, { flush: "sync" });
function showSelection() {
  selectionHeading.value?.scrollIntoView({ block: "start" });
  selectionHeading.value?.focus({ preventScroll: true });
}

function category(entry: Entry): string {
  if (entry.isDir) return "folder";
  const mime = entry.mime.toLowerCase();
  const ext = entry.name.split(".").pop()?.toLowerCase() ?? "";
  if (mime.startsWith("image/") || /^(jpg|jpeg|png|gif|webp|bmp|heic|heif|avif|tiff?)$/.test(ext)) return "image";
  if (mime.startsWith("video/") || /^(mp4|mkv|avi|mov|webm|m4v|ts|mpeg|mpg)$/.test(ext)) return "video";
  if (mime.startsWith("audio/") || /^(mp3|flac|aac|m4a|wav|ogg|opus)$/.test(ext)) return "audio";
  if (/^(zip|rar|7z|tar|gz|bz2|xz)$/.test(ext)) return "archive";
  if (mime.startsWith("text/") || /^(pdf|docx?|xlsx?|pptx?|txt|md)$/.test(ext)) return "document";
  return "file";
}
function resetThumbnails() {
  thumbnailGeneration++;
  observer?.disconnect(); observer = undefined;
  queue = []; visible.clear(); requested.clear();
}
function pumpThumbnails() {
  while (active < 4 && queue.length) {
    const entry = queue.shift()!;
    if (!visible.has(entry.id)) { requested.delete(entry.id); continue; }
    const current = thumbnailGeneration;
    active++;
    void LANService.PreparationThumbnail(props.spaceId, entry.id).then(src => {
      if (alive && current === thumbnailGeneration && !appLocked.value && !accounts.dialog && src) rememberThumbnail(entry.id, src);
    }).catch(() => {}).finally(() => { active--; pumpThumbnails(); });
  }
}
async function observeThumbnails() {
  resetThumbnails();
  const current = thumbnailGeneration;
  if (layout.value !== "grid" || appLocked.value || accounts.dialog) return;
  await nextTick();
  if (!alive || current !== thumbnailGeneration || !entriesBox.value) return;
  observer = new IntersectionObserver(changes => {
    if (!alive || current !== thumbnailGeneration) return;
    for (const change of changes) {
      const id = (change.target as HTMLElement).dataset.prepareCard!;
      if (!change.isIntersecting) { visible.delete(id); continue; }
      visible.add(id);
      const entry = entries.value.find(item => item.id === id);
      if (!entry || entry.isDir) continue;
      if (entry.thumbnail) rememberThumbnail(id, entry.thumbnail);
      else if (entry.ready && category(entry) === "image" && !thumbnails.value[id] && !requested.has(id)) {
        requested.add(id); queue.push(entry);
      }
    }
    pumpThumbnails();
  }, { root: scrollBox.value, rootMargin: "100px" });
  entriesBox.value.querySelectorAll<HTMLElement>("[data-prepare-card]").forEach(element => observer!.observe(element));
}
watch([layout, entries, appLocked, () => accounts.dialog], () => { void observeThumbnails(); }, { flush: "sync" });

async function browse(next: { id: string; name: string }[], refresh = false) {
  if (busy.value) return;
  saveScroll();
  const current = ++generation, key = next.at(-1)?.id ?? "";
  const cached = directoryCache.get(key);
  const scroll = cached?.scroll ?? { list: 0, grid: 0 };
  resetThumbnails();
  currentPage = undefined;
  trail.value = next; error.value = "";
  if (cached && !refresh) {
    directoryCache.delete(key); directoryCache.set(key, cached);
    currentPage = cached;
    thumbnails.value = cached.thumbnails; entries.value = cached.entries; loading.value = false;
    void restoreScroll(current);
    return;
  }
  directoryCache.delete(key);
  thumbnails.value = {}; entries.value = []; loading.value = true;
  try {
    const result = await LANService.ListPreparationEntries(props.spaceId, key);
    if (!alive || current !== generation) return;
    for (const entry of result ?? []) {
      const file = selected.value.find(item => item.id === entry.id);
      if (file) { file.ready = entry.ready && !entry.needsVerification; file.desktopCompatibleReady = entry.desktopCompatibleReady; }
    }
    currentPage = { entries: result ?? [], thumbnails: {}, scroll };
    directoryCache.set(key, currentPage);
    if (directoryCache.size > 16) directoryCache.delete(directoryCache.keys().next().value!);
    thumbnails.value = currentPage.thumbnails; entries.value = currentPage.entries;
    void restoreScroll(current);
  } catch (cause) {
    if (alive && current === generation) error.value = lanError(cause);
  } finally {
    if (alive && current === generation) loading.value = false;
  }
}
function toggle(entry: PreparationEntry) {
  const index = selected.value.findIndex(item => item.id === entry.id);
  if (index >= 0) selected.value.splice(index, 1);
  else if (!isReady(entry)) selected.value.push({ id: entry.id, name: entry.name, path: [...trail.value.map(item => item.name), entry.name].join(" / "), error: "", ready: entry.ready && !entry.needsVerification, desktopCompatibleReady: entry.desktopCompatibleReady });
}
function close() { if (!busy.value) emit("close"); }
async function start() {
  if (busy.value || !pending.value.length) return;
  busy.value = true;
  const files = [...pending.value];
  for (const file of files) {
    if (!alive || appLocked.value || accounts.dialog) break;
    try {
      await LANService.PrepareDesktop(props.spaceId, file.id, kind.value);
      if (!alive) break;
      selected.value = selected.value.filter(item => item.id !== file.id);
      submitted.value = true;
    } catch (cause) {
      if (!alive) break;
      file.error = lanError(cause);
    }
    await nextTick();
  }
  if (!alive) return;
  busy.value = false;
  if (!pending.value.length && submitted.value) emit("tasks");
}
function key(event: KeyboardEvent) {
  if (appLocked.value || accounts.dialog) return;
  if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); close(); return; }
  if (event.key !== "Tab") return;
  const items = Array.from(dialog.value?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled)") ?? []).filter(item => item.offsetParent !== null);
  const first = items[0], last = items.at(-1);
  if (!first) { event.preventDefault(); dialog.value?.focus(); }
  else if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.value)) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
}
function focus(event: FocusEvent) {
  if (appLocked.value || accounts.dialog) return;
  if (dialog.value && !dialog.value.contains(event.target as Node)) (dialog.value.querySelector<HTMLElement>("button:not(:disabled), input:not(:disabled)") ?? dialog.value).focus({ preventScroll: true });
}
onMounted(() => {
  opener = props.opener ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
  window.addEventListener("keydown", key, true); document.addEventListener("focusin", focus);
  void browse([]); void nextTick(() => dialog.value?.focus());
});
onBeforeUnmount(() => {
  alive = false; generation++; resetThumbnails(); window.removeEventListener("keydown", key, true); document.removeEventListener("focusin", focus);
  directoryCache.clear(); currentPage = undefined; thumbnails.value = {};
  if (!appLocked.value && !accounts.dialog && opener?.isConnected) opener.focus();
});
</script>

<template>
  <Teleport to="body">
    <div class="scrim prepare-scrim">
      <section ref="dialog" class="prepare-dialog" role="dialog" aria-modal="true" aria-labelledby="prepare-title" tabindex="-1">
        <header><div><h2 id="prepare-title">{{ t('lan|选择文件准备') }}</h2><p>{{ t('lan|共享空间：{name}', { name: spaceName }) }}</p></div><button type="button" :disabled="busy" :aria-label="t('lan|关闭对话框')" @click="close">×</button></header>
        <div ref="scrollBox" class="prepare-body">
          <p>{{ t('lan|仅在此空间内读取所选文件，不会创建同步、上传、删除或修改云盘文件。电脑准备不受访客准备权限开关影响。') }}</p>
          <div class="browser">
            <div class="browser-toolbar"><nav :aria-label="t('lan|授权根目录')"><button type="button" :disabled="busy || !trail.length" @click="browse(trail.slice(0, -1))">{{ t('lan|返回上级目录') }}</button><button type="button" :disabled="busy || !trail.length" @click="browse([])">{{ t('lan|共享空间根目录') }}</button><span v-for="crumb in trail" :key="crumb.id"> / {{ crumb.name }}</span></nav><button type="button" class="refresh-directory quiet" :disabled="loading || busy" :aria-label="t('刷新')" :title="t('刷新')" data-prepare-refresh @click="browse(trail, true)"><Icon name="repeat" :size="18"/></button><div class="segments glass-well" role="group" :aria-label="t('文件视图')"><button type="button" :disabled="busy" :class="{ on: layout === 'grid' }" :aria-label="t('网格')" :title="t('网格')" :aria-pressed="layout === 'grid'" @click="layout = 'grid'"><Icon name="grid" :size="18"/></button><button type="button" :disabled="busy" :class="{ on: layout === 'list' }" :aria-label="t('列表')" :title="t('列表')" :aria-pressed="layout === 'list'" @click="layout = 'list'"><Icon name="list" :size="18"/></button></div></div>
            <div ref="entriesBox" class="entries" :aria-busy="loading">
              <div v-if="loading" class="picker-loading" data-prepare-loading>
                <div class="loading-label"><Orbit :size="18" :label="t('正在加载')"/><span aria-hidden="true">{{ t('正在加载') }}</span></div>
                <div :class="layout === 'grid' ? 'grid' : 'loading-rows'" aria-hidden="true">
                  <div v-for="index in layout === 'grid' ? 8 : 4" :key="index" :class="layout === 'grid' ? 'card loading-card' : 'loading-row'">
                    <span :class="layout === 'grid' ? 'card-thumb skel' : 'loading-icon skel'" :style="{ '--i': index }"/>
                    <span class="loading-line skel" :style="{ width: `${54 + (index * 13) % 32}%`, '--i': index }"/>
                  </div>
                </div>
              </div>
              <p v-else-if="error" role="alert">{{ error }} <button type="button" @click="browse(trail, true)">{{ t('重试') }}</button></p>
              <p v-else-if="!entries.length">{{ t('lan|此目录没有文件或文件夹') }}</p>
              <div v-else :class="{ grid: layout === 'grid' }">
                <template v-for="entry in entries" :key="entry.id">
                  <button v-if="entry.isDir" :class="layout === 'grid' ? 'card' : 'entry quiet'" type="button" :data-prepare-card="entry.id" :data-prepare-folder="entry.id" :disabled="busy" @click="browse([...trail, { id: entry.id, name: entry.name }])"><span v-if="layout === 'grid'" class="card-thumb"><Thumb alt=""><Icon name="folder" :size="34" filled/></Thumb></span><Icon v-else name="folder" :size="18"/><span class="entry-name" :title="entry.name">{{ entry.name }}</span><Icon v-if="layout === 'list'" name="chevronRight" :size="16"/></button>
                  <label v-else :class="layout === 'grid' ? ['card', { chosen: selected.some(item => item.id === entry.id), unavailable: isReady(entry) && !selected.some(item => item.id === entry.id) }] : 'entry'" :data-prepare-card="entry.id"><input type="checkbox" :data-prepare-file="entry.id" :checked="selected.some(item => item.id === entry.id)" :disabled="busy || (isReady(entry) && !selected.some(item => item.id === entry.id))" @change="toggle(entry)"/><span v-if="layout === 'grid'" class="card-thumb"><Thumb :src="thumbnails[entry.id]" alt="" lazy><Icon :name="category(entry)" :size="34"/></Thumb></span><span class="entry-name" :title="entry.name">{{ entry.name }}</span><span v-if="(entry.ready && !entry.needsVerification) || entry.desktopCompatibleReady" class="readiness"><span v-if="entry.ready && !entry.needsVerification">{{ t('lan|原件已就绪') }}</span><span v-if="entry.desktopCompatibleReady">{{ t('lan|兼容准备已就绪') }}</span></span></label>
                </template>
              </div>
            </div>
          </div>
          <h3 ref="selectionHeading" tabindex="-1">{{ t('lan|已选择 {n} 个文件', { n: selected.length }) }}</h3>
          <ul v-if="selected.length" class="selection"><li v-for="file in selected" :key="file.id"><div><span>{{ file.path }}</span><span v-if="isReady(file)" class="selected-ready"> · {{ kind === 'original' ? t('lan|原件已就绪') : t('lan|兼容准备已就绪') }}</span><p v-if="file.error" class="error" role="alert">{{ file.error }}</p></div><button type="button" :disabled="busy" :aria-label="t('lan|取消选择 {name}', { name: file.name })" :data-prepare-remove="file.id" @click="selected = selected.filter(item => item.id !== file.id)">×</button></li></ul>
          <fieldset :disabled="busy"><legend>{{ t('lan|准备方式') }}</legend><label><input v-model="kind" type="radio" value="original"/> {{ t('lan|原件') }}</label><label><input v-model="kind" type="radio" value="compatible"/> {{ t('lan|常见设备兼容副本') }}</label></fieldset>
          <p>{{ t('lan|兼容准备以 H.264/AAC/MP4 为常见设备目标，不保证所有设备可用；原件已兼容时可能直接复用，HDR 或不支持的媒体可能失败。') }}</p>
          <p>{{ t('lan|索引较大的 MP4 可通过兼容准备无损重新封装，加快起播，原件保持不变。') }}</p>
          <p>{{ t('lan|兼容准备仅适用于受支持的媒体文件，其他文件会逐项报告失败；可改选原件。') }}</p>
          <p v-if="submitted && pending.length" role="status">{{ t('lan|部分文件已提交，未提交的文件仍保留在列表中，可继续准备或查看任务。') }}</p>
        </div>
        <footer><button type="button" class="selection-jump quiet" :disabled="!selected.length" :title="t('lan|查看已选文件')" @click="showSelection">{{ t('lan|已选择 {n} 个文件', { n: selected.length }) }}</button><div class="footer-actions"><button type="button" :disabled="busy" @click="close">{{ t('lan|暂不操作') }}</button><button v-if="submitted" type="button" :disabled="busy" data-prepare-tasks @click="emit('tasks')">{{ t('lan|查看准备任务') }}</button><button type="button" class="primary" data-prepare-start :disabled="busy || !pending.length" @click="start">{{ busy ? t('lan|正在处理') : submitted ? t('lan|继续准备所选文件') : t('lan|确认并开始准备') }}</button></div></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.prepare-scrim { background: #15242d66; }
.prepare-dialog { width: min(640px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); display: flex; flex-direction: column; overflow: hidden; padding: 22px; border-radius: 20px; background: var(--glass-panel); color: var(--fg); backdrop-filter: blur(24px); }
header, footer { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; flex: none; }
header > div { min-width: 0; flex: 1; }
header h2, header p { margin: 0 0 5px; overflow-wrap: anywhere; }
.prepare-body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; scroll-padding-top: 12px; scrollbar-width: none; }
.prepare-body::-webkit-scrollbar { display: none; }
p { color: var(--fg-dim); line-height: 1.5; }
button { padding: 8px 12px; border-radius: 10px; cursor: pointer; }
button:disabled { opacity: .45; cursor: default; }
button:focus-visible, input:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.primary { background: var(--primary); color: var(--on-primary); }
.browser { border: 1px solid var(--line); border-radius: 12px; min-width: 0; }
.browser-toolbar { position: sticky; top: 0; z-index: 3; display: flex; align-items: center; gap: 8px; padding-right: 8px; border-bottom: 1px solid var(--line); border-radius: 12px 12px 0 0; background: var(--glass-panel); }
nav { display: flex; flex: 1; min-width: 0; align-items: center; flex-wrap: wrap; gap: 6px; padding: 8px; overflow-wrap: anywhere; }
nav span { min-width: 0; overflow-wrap: anywhere; }
.segments { display: flex; flex: none; padding: 3px; border-radius: 10px; }
.segments button { display: grid; place-items: center; width: 36px; min-height: 34px; padding: 0; border: 0; border-radius: 8px; background: transparent; box-shadow: none; }
.segments button.on { background: var(--accent-soft); box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 40%, transparent); }
.entries { min-height: 80px; padding: 6px; }
.refresh-directory { display: grid; place-items: center; flex: none; width: 34px; min-height: 34px; padding: 0; }
.picker-loading { overflow: hidden; }
.loading-label { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 0 8px 6px; color: var(--fg-dim); font-size: 13px; }
.loading-card { pointer-events: none; }
.loading-row { display: flex; align-items: center; gap: 12px; min-height: 40px; padding: 8px; }
.loading-icon { flex: none; width: 20px; height: 20px; border-radius: 6px; }
.loading-line { display: block; height: 12px; max-width: calc(100% - 32px); border-radius: 6px; }
.loading-card .loading-line { margin-top: 2px; }
.entry { display: flex; align-items: center; justify-content: flex-start; gap: 8px; width: 100%; min-width: 0; text-align: left; padding: 8px; }
.entry .entry-name { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.entry .icon { flex: none; }
.readiness { display: flex; flex: none; flex-wrap: wrap; gap: 4px; color: var(--fg-dim); font-size: 11px; }
.readiness > span { padding: 2px 5px; border-radius: 5px; background: var(--accent-soft); color: var(--accent); }
.selected-ready { color: var(--fg-dim); font-size: 12px; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(132px, 100%), 1fr)); gap: 6px; }
.card { position: relative; display: flex; flex-direction: column; align-items: stretch; gap: 6px; min-width: 0; padding: 6px; border: 1px solid transparent; border-radius: 10px; text-align: left; cursor: pointer; background: transparent; color: inherit; }
.card.chosen .card-thumb { box-shadow: 0 0 0 2px var(--accent), 0 0 0 5px var(--accent-soft); }
.card.unavailable { cursor: default; }
.card:focus-within { outline: 2px solid var(--accent); outline-offset: 1px; }
.card input { position: absolute; z-index: 2; top: 10px; left: 10px; width: 20px; height: 20px; min-width: 0; min-height: 0; padding: 0; margin: 0; appearance: none; border: 1.5px solid rgba(255, 255, 255, .9); border-radius: 50%; background: rgba(6, 16, 28, .45); box-shadow: 0 1px 3px #0004; }
.card input:checked { background: var(--primary); }
.card input:checked::after { content: ""; position: absolute; left: 5px; top: 3px; width: 7px; height: 4px; border-left: 2px solid white; border-bottom: 2px solid white; transform: rotate(-45deg); }
.card-thumb { position: relative; display: block; width: 100%; aspect-ratio: 16 / 9; overflow: hidden; border-radius: 8px; background: color-mix(in srgb, var(--fg) 8%, transparent); }
.entry-name { display: block; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
h3 { font-size: 15px; margin: 15px 0 8px; }
.selection { padding: 0; margin: 0; list-style: none; }
.selection li { display: flex; justify-content: space-between; gap: 8px; align-items: start; padding: 5px 0; }
.selection li div { min-width: 0; overflow-wrap: anywhere; }
.selection p { margin: 3px 0; }
.error { color: var(--danger); }
fieldset { display: flex; flex-wrap: wrap; gap: 12px; border: 0; padding: 12px 0 0; margin: 0; }
footer { border-top: 1px solid var(--line); padding-top: 12px; margin-top: 10px; }
.selection-jump { padding-left: 0; font-size: 13px; color: var(--fg-dim); }
.footer-actions { display: flex; align-items: center; justify-content: flex-end; flex-wrap: wrap; gap: 8px; margin-left: auto; }
.footer-actions button { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
@media (max-width: 600px) { .prepare-dialog { padding: 16px; } .browser-toolbar { flex-wrap: wrap; padding: 6px; gap: 4px; } .browser-toolbar nav { flex-basis: 100%; padding: 0; } .browser-toolbar .segments { margin-left: auto; } }
@media (prefers-reduced-motion: reduce) { .picker-loading .skel::after { animation: none; } }
</style>
