<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { t } from "../i18n";
import { DocumentArchive, errorMessage, loadReading, saveReading, type ConsumptionEntry } from "../consumption";
import { ComicImageCache, comicRasterType, naturalComicPages, type ComicDirection, type ComicMode, type ComicPage } from "../reader-comic";

const props = defineProps<{
  url: string;
  entry: ConsumptionEntry;
  identity: string;
  pages?: ComicPage[];
  initialId?: string;
  resolveImage?: (id: string) => Promise<string>;
  releaseImage?: (url: string) => void;
}>();
interface Position { id: string; index: number; mode: ComicMode; direction: ComicDirection; fraction: number }
const pages = ref<ComicPage[]>([]);
const current = ref(0);
const mode = ref<ComicMode>("single");
const direction = ref<ComicDirection>("ltr");
const loading = ref(false);
const failure = ref("");
const surface = ref<HTMLElement>();
const viewport = ref({ width: 800, height: 600 });
const scrollTop = ref(0);
const ratios = ref<Record<string, number>>({});
const revision = ref(0);
let archive: DocumentArchive | undefined;
let controller: AbortController | undefined;
let cache: ComicImageCache | undefined;
let resize: ResizeObserver | undefined;
let generation = 0;
let disposed = false;
let stateIdentity = "";
let ready = false;
let saveTimer: ReturnType<typeof setTimeout> | undefined;

const pageWidth = computed(() => Math.max(120, Math.min(1100, viewport.value.width - 32)));
const offsets = computed(() => {
  const values = [0];
  for (const page of pages.value) values.push(values[values.length - 1] + Math.max(160, pageWidth.value * (ratios.value[page.id] ?? Math.SQRT2)) + 12);
  return values;
});
function indexAt(top: number) {
  let low = 0, high = pages.value.length;
  while (low < high) { const middle = (low + high) >>> 1; if (offsets.value[middle + 1] <= top) low = middle + 1; else high = middle; }
  return Math.min(Math.max(0, low), Math.max(0, pages.value.length - 1));
}
const visible = computed(() => {
  if (!pages.value.length) return [];
  if (mode.value !== "strip") return mode.value === "double" ? [current.value, current.value + 1].filter(index => index < pages.value.length) : [current.value];
  const first = indexAt(scrollTop.value);
  const last = indexAt(scrollTop.value + viewport.value.height);
  const start = Math.max(0, first - 1);
  return Array.from({ length: Math.min(8, pages.value.length - start, last - start + 2) }, (_, index) => start + index);
});
const displayed = computed(() => visible.value.map(index => ({ ...pages.value[index], index })));
function imageState(id: string) { void revision.value; return cache?.states.get(id); }
function persist() {
  if (!ready || !stateIdentity || !pages.value.length) return;
  const height = (offsets.value[current.value + 1] ?? 1) - (offsets.value[current.value] ?? 0);
  saveReading(`${stateIdentity}.comic`, { id: pages.value[current.value]?.id ?? "", index: current.value, mode: mode.value, direction: direction.value,
    fraction: mode.value === "strip" ? Math.max(0, (scrollTop.value - offsets.value[current.value]) / height) : 0 } satisfies Position);
}
function scheduleSave() { clearTimeout(saveTimer); saveTimer = setTimeout(persist, 250); }
function releaseResources() {
  controller?.abort(); controller = undefined;
  cache?.close(); cache = undefined;
  const previous = archive; archive = undefined;
  if (previous) void previous.close();
}
async function open() {
  persist();
  clearTimeout(saveTimer);
  ready = false;
  const ticket = ++generation;
  releaseResources();
  stateIdentity = props.identity;
  pages.value = [];
  ratios.value = {};
  current.value = 0;
  scrollTop.value = 0;
  failure.value = "";
  loading.value = true;
  const stored = loadReading<unknown>(`${props.identity}.comic`, null);
  const record = stored !== null && typeof stored === "object" ? stored as Record<string, unknown> : {};
  const saved: Position = {
    id: typeof record.id === "string" ? record.id : "",
    index: typeof record.index === "number" && Number.isInteger(record.index) && record.index >= 0 ? record.index : 0,
    mode: record.mode === "double" || record.mode === "strip" ? record.mode : "single",
    direction: record.direction === "rtl" ? "rtl" : "ltr",
    fraction: typeof record.fraction === "number" && Number.isFinite(record.fraction) ? Math.min(.99, Math.max(0, record.fraction)) : 0,
  };
  mode.value = saved.mode;
  direction.value = saved.direction;
  const resolver = props.resolveImage;
  const releaser = props.releaseImage;
  let opened: DocumentArchive | undefined;
  try {
    let sequence: ComicPage[];
    if (props.pages !== undefined || !props.url) sequence = naturalComicPages((props.pages ?? []).filter(page => !!comicRasterType(page.name)));
    else {
      controller = new AbortController();
      opened = await DocumentArchive.open(props.url, controller.signal);
      if (disposed || ticket !== generation) { await opened.close(); return; }
      archive = opened;
      sequence = naturalComicPages(opened.names().filter(name => !!comicRasterType(name) && !name.startsWith("__MACOSX/")).map(name => ({ id: name, name })));
    }
    if (disposed || ticket !== generation) return;
    const sourceArchive = opened;
    const byId = new Map(sequence.map(page => [page.id, page]));
    cache = new ComicImageCache(async (id) => {
      const page = byId.get(id);
      if (!page) throw new Error(t("漫画页面不存在"));
      if (sourceArchive) { const url = await sourceArchive.image(id); return { url, release: () => sourceArchive.releaseImage(url) }; }
      if (page.url) return { url: page.url, release: () => undefined };
      if (!resolver || !releaser) throw new Error(t("无法读取此漫画页面"));
      const url = await resolver(id);
      if (!url) throw new Error(t("无法读取此漫画页面"));
      return { url, release: () => releaser(url) };
    }, () => { revision.value++; });
    pages.value = sequence;
    const selected = sequence.findIndex(page => page.id === (props.initialId || saved.id));
    current.value = selected >= 0 ? selected : Math.min(sequence.length - 1, saved.index);
    if (current.value < 0) current.value = 0;
    ready = true;
    cache.setWanted(visible.value.map(index => sequence[index].id));
    loading.value = false;
    await nextTick();
    if (disposed || ticket !== generation) return;
    if (surface.value) {
      const fraction = props.initialId ? 0 : saved.fraction;
      surface.value.scrollTop = mode.value === "strip" ? offsets.value[current.value] + fraction * (offsets.value[current.value + 1] - offsets.value[current.value]) : 0;
      scrollTop.value = surface.value.scrollTop;
    }
  } catch (error) {
    if (!disposed && ticket === generation) { failure.value = errorMessage(error); releaseResources(); }
  } finally { if (!disposed && ticket === generation) loading.value = false; }
}
function jump(index: number) {
  if (!pages.value.length || !Number.isFinite(index)) return;
  current.value = Math.max(0, Math.min(pages.value.length - 1, Math.trunc(index)));
  if (surface.value) {
    surface.value.scrollTop = mode.value === "strip" ? offsets.value[current.value] : 0;
    scrollTop.value = surface.value.scrollTop;
  }
  scheduleSave();
}
function changeMode(event: Event) {
  mode.value = (event.target as HTMLSelectElement).value as ComicMode;
  void nextTick(() => { if (surface.value) { surface.value.scrollTop = mode.value === "strip" ? offsets.value[current.value] : 0; scrollTop.value = surface.value.scrollTop; } });
  scheduleSave();
}
function scroll() {
  if (!surface.value) return;
  scrollTop.value = surface.value.scrollTop;
  if (mode.value === "strip") { current.value = indexAt(scrollTop.value + 1); scheduleSave(); }
}
function loaded(id: string, event: Event) {
  const image = event.target as HTMLImageElement;
  if (!image.naturalWidth || !image.naturalHeight) return;
  const ratio = image.naturalHeight / image.naturalWidth;
  if (ratios.value[id] === ratio) return;
  const anchor = current.value;
  const delta = scrollTop.value - offsets.value[anchor];
  ratios.value = { ...ratios.value, [id]: ratio };
  if (mode.value === "strip") void nextTick(() => { if (surface.value) surface.value.scrollTop = offsets.value[anchor] + delta; });
}
function keyboard(event: KeyboardEvent) {
  if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) return;
  event.stopPropagation();
  if ((event.target as HTMLElement).closest("input,select,textarea,button")) return;
  if (event.altKey || event.ctrlKey || event.metaKey) return;
  if (mode.value === "strip" && ["ArrowUp", "ArrowDown", "PageUp", "PageDown", " "].includes(event.key)) return;
  event.preventDefault();
  if (event.key === "Home") jump(0);
  else if (event.key === "End") jump(pages.value.length - 1);
  else {
    let step = ["ArrowLeft", "ArrowUp", "PageUp"].includes(event.key) || (event.key === " " && event.shiftKey) ? -1 : 1;
    if (direction.value === "rtl" && ["ArrowLeft", "ArrowRight"].includes(event.key)) step *= -1;
    jump(current.value + step * (mode.value === "double" ? 2 : 1));
  }
}
watch(visible, indices => { cache?.setWanted(indices.map(index => pages.value[index].id)); });
watch([current, direction], scheduleSave);
watch(() => [props.url, props.identity, props.pages, props.initialId, props.resolveImage, props.releaseImage], () => { void open(); }, { immediate: true });
onMounted(() => {
  resize = new ResizeObserver(() => {
    if (surface.value) viewport.value = { width: surface.value.clientWidth, height: surface.value.clientHeight };
  });
  if (surface.value) { resize.observe(surface.value); viewport.value = { width: surface.value.clientWidth, height: surface.value.clientHeight }; }
});
onBeforeUnmount(() => { persist(); disposed = true; generation++; ready = false; clearTimeout(saveTimer); resize?.disconnect(); releaseResources(); });
</script>

<template>
  <section class="comic-reader" tabindex="0" :aria-label="t('漫画阅读器')" @keydown="keyboard">
    <header class="comic-toolbar" role="toolbar" :aria-label="t('漫画阅读设置')">
      <label>{{ t("漫画布局") }}
        <select :value="mode" @change="changeMode">
          <option value="single">{{ t("漫画单页") }}</option>
          <option value="double">{{ t("漫画双页") }}</option>
          <option value="strip">{{ t("漫画长条") }}</option>
        </select>
      </label>
      <label>{{ t("漫画阅读方向") }}
        <select v-model="direction">
          <option value="ltr">{{ t("从左到右") }}</option>
          <option value="rtl">{{ t("从右到左") }}</option>
        </select>
      </label>
      <div class="comic-navigation">
        <button type="button" :disabled="!pages.length || current === 0" :aria-label="t('漫画上一页')" @click="jump(current - (mode === 'double' ? 2 : 1))">{{ t("漫画上一页") }}</button>
        <label class="comic-jump">{{ t("漫画页码") }}
          <input type="number" :value="pages.length ? current + 1 : 0" :min="pages.length ? 1 : 0" :max="pages.length" :disabled="!pages.length" @change="jump(Number(($event.target as HTMLInputElement).value) - 1)" />
          <span>/ {{ pages.length }}</span>
        </label>
        <button type="button" :disabled="!pages.length || current + (mode === 'double' ? 2 : 1) >= pages.length" :aria-label="t('漫画下一页')" @click="jump(current + (mode === 'double' ? 2 : 1))">{{ t("漫画下一页") }}</button>
      </div>
    </header>
    <div ref="surface" class="comic-surface" :class="[mode, { rtl: direction === 'rtl' }]" :aria-busy="loading" @scroll.passive="scroll">
      <div v-if="loading" class="comic-message" role="status">{{ t("正在打开漫画") }}</div>
      <div v-else-if="failure" class="comic-message" role="alert">{{ failure }}<button type="button" @click="open">{{ t("重试") }}</button></div>
      <div v-else-if="!pages.length" class="comic-message" role="status">{{ t("没有可阅读的漫画图片") }}</div>
      <div v-else class="comic-pages" :style="mode === 'strip' ? { height: `${offsets[pages.length]}px` } : undefined">
        <article v-for="page in displayed" :key="page.id" class="comic-page" :aria-label="t('漫画第 {n} 页', { n: page.index + 1 })"
          :style="mode === 'strip' ? { top: `${offsets[page.index]}px`, height: `${offsets[page.index + 1] - offsets[page.index] - 12}px`, width: `${pageWidth}px` } : undefined">
          <img v-if="imageState(page.id)?.status === 'ready'" :src="imageState(page.id)?.image?.url" :alt="page.name" decoding="async" @load="loaded(page.id, $event)" @error="cache?.fail(page.id)" />
          <div v-else-if="imageState(page.id)?.status === 'error'" class="comic-page-error" role="status">
            <span>{{ t("漫画图片加载失败：{name}", { name: page.name }) }}</span>
            <button type="button" @click="cache?.retry(page.id)">{{ t("重试") }}</button>
          </div>
          <span v-else class="comic-page-loading" role="status">{{ t("正在读取漫画第 {n} 页", { n: page.index + 1 }) }}</span>
        </article>
      </div>
    </div>
  </section>
</template>

<style scoped>
.comic-reader { display: flex; flex-direction: column; height: 100%; width: 100%; min-height: 0; outline: none; color: var(--fg); }
.comic-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 10px 16px; padding: 10px 16px; border-bottom: 1px solid var(--line); flex: 0 0 auto; font-size: 12px; }
.comic-toolbar label { display: inline-flex; align-items: center; gap: 6px; }
.comic-reader button, .comic-reader select, .comic-reader input { font: inherit; color: inherit; border: 1px solid var(--line); border-radius: 6px; background: var(--glass-control); padding: 5px 8px; min-height: 30px; }
.comic-reader button, .comic-reader select { cursor: pointer; }
.comic-reader button:disabled { opacity: .4; cursor: default; }
.comic-reader button:active:not(:disabled) { transform: scale(.97); }
.comic-reader :is(button, select, input):focus-visible, .comic-reader:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.comic-navigation { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-left: auto; }
.comic-jump input { width: 65px; }
.comic-surface { flex: 1; min-height: 0; overflow: auto; background: var(--canvas-flat); overscroll-behavior: contain; }
.comic-pages { display: flex; justify-content: center; align-items: center; gap: 8px; padding: 16px; min-height: 100%; box-sizing: border-box; }
.comic-surface.rtl:not(.strip) .comic-pages { flex-direction: row-reverse; }
.comic-page { margin: 0; min-width: 0; display: flex; align-items: center; justify-content: center; }
.comic-page img { display: block; max-width: 100%; max-height: 100%; object-fit: contain; }
.single .comic-pages, .double .comic-pages { height: 100%; }
.single .comic-page { width: 100%; height: 100%; }
.double .comic-page { width: calc(50% - 4px); height: 100%; }
.strip .comic-pages { position: relative; display: block; padding: 0; min-height: 0; overflow-anchor: none; }
.strip .comic-page { position: absolute; left: 50%; transform: translateX(-50%); }
.strip .comic-page img { width: 100%; height: 100%; }
.comic-message { min-height: 100%; padding: 24px; box-sizing: border-box; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; color: var(--fg-dim); }
.comic-page-error { padding: 20px; display: flex; flex-direction: column; align-items: center; gap: 12px; text-align: center; overflow-wrap: anywhere; color: var(--fg-dim); }
.comic-page-loading { color: var(--fg-dim); font-size: 12px; text-align: center; }
@media (max-width: 700px) { .comic-toolbar { padding: 8px; gap: 8px; } .comic-navigation { margin-left: 0; width: 100%; justify-content: space-between; } .comic-pages { padding: 8px; gap: 4px; } }
</style>
