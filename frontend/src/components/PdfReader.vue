<script setup lang="ts">
import "../reader-pdf-compat";
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { getDocument, GlobalWorkerOptions, PasswordResponses, TextLayer } from "pdfjs-dist/legacy/build/pdf.mjs";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, PDFPageProxy, RenderTask } from "pdfjs-dist";
import pdfWorkerUrl from "pdfjs-dist/legacy/build/pdf.worker.min.mjs?url";
import { t } from "../i18n";
import { errorMessage, loadReading, readBounded, saveReading, type ConsumptionEntry } from "../consumption";
import { findPdfText, flattenPdfOutline, pdfText, PdfCMapFactory, PdfFontFactory, PdfWasmFactory, type PdfMatch, type PdfOutline, type PdfTextRun } from "../reader-pdf";

GlobalWorkerOptions.workerSrc = pdfWorkerUrl;
const props = defineProps<{ url: string; entry: ConsumptionEntry; identity: string }>();
const page = ref(1);
const pageInput = ref(1);
const count = ref(0);
const zoom = ref("fit");
const actualScale = ref(1);
const loading = ref(false);
const rendering = ref(false);
const error = ref("");
const renderError = ref("");
const stage = ref<HTMLElement>();
const pageHost = ref<HTMLElement>();
const outline = ref<PdfOutline[]>([]);
const outlineOpen = ref(false);
const passwordNeeded = ref(false);
const passwordIncorrect = ref(false);
const password = ref("");
let supplyPassword: ((password: string) => void) | undefined;
const query = ref("");
const matches = ref<PdfMatch[]>([]);
const matchIndex = ref(-1);
const searching = ref(false);
const searchedPages = ref(0);
const searchLimited = ref(false);
const searchError = ref("");
const activeMatch = computed(() => matches.value[matchIndex.value]);
let document: PDFDocumentProxy | undefined;
let task: PDFDocumentLoadingTask | undefined;
let controller: AbortController | undefined;
let generation = 0;
let renderGeneration = 0;
let searchGeneration = 0;
let renderTask: RenderTask | undefined;
let textLayer: TextLayer | undefined;
let canvas: HTMLCanvasElement | undefined;
let currentPage: PDFPageProxy | undefined;
let textRuns: PdfTextRun[] = [];
let searchTimer: ReturnType<typeof setTimeout> | undefined;
let observer: ResizeObserver | undefined;
let resizeTimer: ReturnType<typeof setTimeout> | undefined;
let previousWidth = 0;
let previousHeight = 0;
function remember() { saveReading(`${props.identity}.pdf`, { page: page.value, zoom: zoom.value }); }
function releasePage() {
  ++renderGeneration;
  renderTask?.cancel(); renderTask = undefined;
  textLayer?.cancel(); textLayer = undefined;
  if (canvas) { canvas.width = 0; canvas.height = 0; canvas = undefined; }
  pageHost.value?.replaceChildren();
  currentPage?.cleanup(); currentPage = undefined;
  textRuns = [];
}
function cancelSearch() { ++searchGeneration; clearTimeout(searchTimer); searching.value = false; }
async function dispose() {
  controller?.abort();
  cancelSearch();
  releasePage();
  rendering.value = false;
  supplyPassword = undefined;
  password.value = "";
  passwordNeeded.value = false;
  document = undefined;
  const oldTask = task;
  task = undefined;
  await oldTask?.destroy().catch(() => undefined);
}
async function load() {
  const run = ++generation;
  await dispose();
  if (run !== generation) return;
  loading.value = true; error.value = ""; renderError.value = ""; count.value = 0; outline.value = [];
  matches.value = []; matchIndex.value = -1; query.value = ""; searchError.value = "";
  const state = loadReading<{ page?: number; zoom?: string }>(`${props.identity}.pdf`, {});
  page.value = Number.isInteger(state.page) ? Math.max(1, state.page!) : 1;
  pageInput.value = page.value;
  zoom.value = state.zoom === "fit" || state.zoom === "width" || ["0.5", "0.75", "1", "1.25", "1.5", "2", "3", "4"].includes(state.zoom ?? "") ? state.zoom! : "fit";
  controller = new AbortController();
  try {
    const probe = await fetch(props.url, { headers: { Range: "bytes=0-0" }, signal: controller.signal });
    const ranged = probe.status === 206;
    const range = /^bytes 0-0\/(\d+)$/.exec(probe.headers.get("Content-Range") ?? "");
    const size = ranged ? Number(range?.[1]) : Number(probe.headers.get("Content-Length"));
    await probe.body?.cancel();
    if (!probe.ok) throw new Error(t("读取文件失败（HTTP {status}）", { status: probe.status }));
    if (ranged && (!range || !Number.isSafeInteger(size) || size < 1)) throw new Error(t("文件服务器没有返回有效的字节范围"));
    if (size > 256 * 1024 * 1024) throw new Error(t("内容超过安全读取上限"));
    const source = ranged ? { url: props.url, length: size, rangeChunkSize: 65536, disableAutoFetch: true, disableStream: true } : { data: await readBounded(props.url, 256 * 1024 * 1024, controller.signal) };
    if (run !== generation) return;
    const loadingTask = getDocument({ ...source, isEvalSupported: false, enableXfa: false, useWorkerFetch: false, CMapReaderFactory: PdfCMapFactory, StandardFontDataFactory: PdfFontFactory, WasmFactory: PdfWasmFactory, maxImageSize: 32000000, canvasMaxAreaInBytes: 64 * 1024 * 1024, useSystemFonts: true });
    task = loadingTask;
    loadingTask.onPassword = (update: (password: string) => void, reason: number) => {
      if (run !== generation) return;
      supplyPassword = update;
      passwordIncorrect.value = reason === PasswordResponses.INCORRECT_PASSWORD;
      passwordNeeded.value = true;
      password.value = "";
    };
    const opened = await loadingTask.promise;
    if (run !== generation) return;
    document = opened;
    count.value = opened.numPages;
    page.value = Math.min(page.value, count.value);
    pageInput.value = page.value;
    loading.value = false;
    await nextTick();
    if (run !== generation) return;
    void renderPage();
    void opened.getOutline().then(items => { if (run === generation) outline.value = flattenPdfOutline(items ?? []); }).catch(() => undefined);
  } catch (cause) {
    if (run === generation && !controller.signal.aborted) { error.value = errorMessage(cause); loading.value = false; }
  }
}
function submitPassword() {
  if (!supplyPassword) return;
  const update = supplyPassword;
  supplyPassword = undefined;
  passwordNeeded.value = false;
  update(password.value);
  password.value = "";
}
function cancelPassword() { void dispose(); ++generation; loading.value = false; error.value = t("pdf|已取消 PDF 密码输入"); }
function goPage(value: number) {
  if (!count.value || !Number.isFinite(value)) return;
  page.value = Math.max(1, Math.min(count.value, Math.floor(value)));
  pageInput.value = page.value;
  remember();
}
async function navigateOutline(item: PdfOutline) {
  const opened = document;
  if (!opened || !item.dest) return;
  try {
    const destination = typeof item.dest === "string" ? await opened.getDestination(item.dest) : item.dest;
    if (!destination?.length || opened !== document) return;
    const target = destination[0];
    const index = typeof target === "number" ? target : target && typeof target === "object" && "num" in target && "gen" in target ? await opened.getPageIndex(target as { num: number; gen: number }) : -1;
    if (opened === document && index >= 0) goPage(index + 1);
  } catch { renderError.value = t("pdf|无法定位此目录项"); }
}
function highlight() {
  if (!textLayer) return;
  const pageMatches = matches.value.filter(match => match.page === page.value);
  const active = activeMatch.value;
  textLayer.textDivs.forEach((div, index) => {
    const run = textRuns[index];
    if (!run) return;
    div.replaceChildren();
    let offset = 0;
    for (const match of pageMatches) {
      if (match.end <= run.start || match.start >= run.end) continue;
      const start = Math.max(0, match.start - run.start);
      const end = Math.min(run.text.length, match.end - run.start);
      div.append(run.text.slice(offset, start));
      const mark = window.document.createElement("mark");
      mark.textContent = run.text.slice(start, end);
      mark.className = match === active ? "pdf-hit active" : "pdf-hit";
      div.append(mark);
      offset = end;
    }
    div.append(run.text.slice(offset));
  });
}
async function renderPage() {
  releasePage();
  const run = renderGeneration;
  const opened = document;
  if (!opened || !pageHost.value || !stage.value) return;
  rendering.value = true; renderError.value = "";
  try {
    const pdfPage = await opened.getPage(page.value);
    if (run !== renderGeneration || opened !== document) { if (pdfPage !== currentPage) pdfPage.cleanup(); return; }
    currentPage = pdfPage;
    const original = pdfPage.getViewport({ scale: 1 });
    const width = Math.max(100, stage.value.clientWidth - 32);
    const height = Math.max(100, stage.value.clientHeight - 32);
    const wanted = zoom.value === "fit" ? Math.min(width / original.width, height / original.height) : zoom.value === "width" ? width / original.width : Number(zoom.value);
    const scale = Math.max(0.05, Math.min(wanted, 16384 / original.width, 16384 / original.height));
    actualScale.value = scale;
    const viewport = pdfPage.getViewport({ scale });
    const pixelRatio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(12000000 / Math.max(1, viewport.width * viewport.height)), 16384 / viewport.width, 16384 / viewport.height);
    const host = pageHost.value;
    host.style.width = `${viewport.width}px`; host.style.height = `${viewport.height}px`;
    host.style.setProperty("--total-scale-factor", String(viewport.scale));
    host.style.setProperty("--scale-round-x", "1px"); host.style.setProperty("--scale-round-y", "1px");
    const surface = window.document.createElement("canvas");
    canvas = surface;
    surface.width = Math.max(1, Math.floor(viewport.width * pixelRatio)); surface.height = Math.max(1, Math.floor(viewport.height * pixelRatio));
    surface.style.width = `${viewport.width}px`; surface.style.height = `${viewport.height}px`;
    surface.setAttribute("aria-label", t("pdf|第 {n} 页", { n: page.value }));
    host.append(surface);
    const draw = pdfPage.render({ canvas: surface, viewport, transform: pixelRatio === 1 ? undefined : [pixelRatio, 0, 0, pixelRatio, 0, 0] });
    renderTask = draw;
    await draw.promise;
    if (run !== renderGeneration) return;
    renderTask = undefined;
    const content = await pdfPage.getTextContent();
    if (run !== renderGeneration) return;
    const layerHost = window.document.createElement("div");
    layerHost.className = "pdf-text-layer";
    host.append(layerHost);
    const layer = new TextLayer({ textContentSource: content, container: layerHost, viewport });
    textLayer = layer;
    textRuns = pdfText(content).runs;
    await layer.render();
    if (run !== renderGeneration) return;
    if (viewport.rotation === 90) layerHost.style.transform = "rotate(90deg) translateY(-100%)";
    else if (viewport.rotation === 180) layerHost.style.transform = "rotate(180deg) translate(-100%, -100%)";
    else if (viewport.rotation === 270) layerHost.style.transform = "rotate(270deg) translateX(-100%)";
    highlight();
    if (activeMatch.value?.page === page.value) host.querySelector(".pdf-hit.active")?.scrollIntoView({ block: "center", inline: "nearest" });
  } catch (cause) { if (run === renderGeneration) renderError.value = errorMessage(cause); }
  finally { if (run === renderGeneration) rendering.value = false; }
}
async function search() {
  cancelSearch();
  const run = searchGeneration;
  const opened = document;
  const term = query.value.trim();
  matches.value = []; matchIndex.value = -1; searchedPages.value = 0; searchLimited.value = false; searchError.value = "";
  highlight();
  if (!opened || !term) return;
  searching.value = true;
  const found: PdfMatch[] = [];
  try {
    for (let number = 1; number <= opened.numPages; number++) {
      if (run !== searchGeneration || opened !== document) return;
      const target = await opened.getPage(number);
      const content = await target.getTextContent();
      if (run !== searchGeneration || opened !== document) { if (target !== currentPage) target.cleanup(); return; }
      const pageMatches = findPdfText(pdfText(content).text, term, number, 100000 - found.length);
      for (const match of pageMatches) found.push(match);
      if (target !== currentPage) target.cleanup();
      searchedPages.value = number;
      if (found.length >= 100000) { searchLimited.value = true; break; }
      const deferred = (Promise as PromiseConstructor & { withResolvers<T>(): { promise: Promise<T>; resolve: (value: T | PromiseLike<T>) => void } }).withResolvers<void>();
      setTimeout(() => deferred.resolve(), 0);
      await deferred.promise;
    }
    if (run !== searchGeneration) return;
    matches.value = found;
    if (found.length) { matchIndex.value = 0; goPage(found[0].page); }
    highlight();
  } catch (cause) { if (run === searchGeneration) searchError.value = errorMessage(cause); }
  finally { if (run === searchGeneration) searching.value = false; }
}
function stepMatch(direction: number) {
  if (!matches.value.length) return;
  matchIndex.value = (matchIndex.value + direction + matches.value.length) % matches.value.length;
  goPage(matches.value[matchIndex.value].page);
  highlight();
  pageHost.value?.querySelector(".pdf-hit.active")?.scrollIntoView({ block: "center", inline: "nearest" });
}
function onKey(event: KeyboardEvent) {
  if (event.key === "Escape") return;
  event.stopPropagation();
  if ((event.target as HTMLElement).matches("input,select,textarea")) return;
  if (["ArrowLeft", "PageUp", "ArrowRight", "PageDown", "Home", "End"].includes(event.key)) {
    event.preventDefault();
    goPage(event.key === "Home" ? 1 : event.key === "End" ? count.value : page.value + (["ArrowLeft", "PageUp"].includes(event.key) ? -1 : 1));
  }
}
watch(() => [props.url, props.identity], load, { immediate: true });
watch(page, () => { pageInput.value = page.value; stage.value?.scrollTo(0, 0); void renderPage(); });
watch(zoom, () => { if (document) { remember(); void renderPage(); } });
watch(query, () => { cancelSearch(); matches.value = []; matchIndex.value = -1; highlight(); searchTimer = setTimeout(() => void search(), 300); });
watch(stage, element => {
  observer?.disconnect();
  if (!element) return;
  observer = new ResizeObserver(entries => {
    const size = entries[0]?.contentRect;
    if (!size || (Math.abs(size.width - previousWidth) < 1 && Math.abs(size.height - previousHeight) < 1)) return;
    previousWidth = size.width; previousHeight = size.height;
    clearTimeout(resizeTimer);
    if (zoom.value === "fit" || zoom.value === "width") resizeTimer = setTimeout(() => void renderPage(), 100);
  });
  observer.observe(element);
});
onBeforeUnmount(() => { ++generation; clearTimeout(resizeTimer); observer?.disconnect(); void dispose(); });
</script>

<template>
  <section class="pdf-reader" :aria-label="entry.name" @keydown="onKey">
    <div v-if="count" class="pdf-toolbar">
      <button :aria-expanded="outlineOpen" @click="outlineOpen = !outlineOpen">{{ t("pdf|目录") }}</button>
      <button :disabled="page <= 1" :aria-label="t('上一页')" @click="goPage(page - 1)">‹</button>
      <form @submit.prevent="goPage(Number(pageInput))"><input v-model="pageInput" type="number" min="1" :max="count" :aria-label="t('pdf|页码')" /><span>/ {{ count }}</span><button type="submit">{{ t("pdf|前往") }}</button></form>
      <button :disabled="page >= count" :aria-label="t('下一页')" @click="goPage(page + 1)">›</button>
      <select v-model="zoom" :aria-label="t('pdf|缩放')">
        <option value="fit">{{ t("pdf|适合整页") }}</option><option value="width">{{ t("pdf|适合宽度") }}</option>
        <option v-for="scale in [0.5,0.75,1,1.25,1.5,2,3,4]" :key="scale" :value="String(scale)">{{ Math.round(scale * 100) }}%</option>
      </select>
      <span class="pdf-scale">{{ Math.round(actualScale * 100) }}%</span>
      <form class="pdf-find" @submit.prevent="search"><input v-model="query" type="search" :aria-label="t('pdf|查找 PDF 文本')" :placeholder="t('pdf|查找 PDF 文本')" /><button type="submit">{{ t("搜索") }}</button></form>
      <template v-if="searching"><span role="status">{{ t("pdf|正在查找 {n}/{total}", { n: searchedPages, total: count }) }}</span><button @click="cancelSearch">{{ t("取消") }}</button></template>
      <template v-else-if="query.trim()"><span role="status">{{ t("pdf|匹配 {n}/{total}", { n: matchIndex + 1, total: matches.length }) }}</span><button :disabled="!matches.length" :aria-label="t('pdf|上一个匹配')" @click="stepMatch(-1)">↑</button><button :disabled="!matches.length" :aria-label="t('pdf|下一个匹配')" @click="stepMatch(1)">↓</button></template>
    </div>
    <form v-if="passwordNeeded" class="pdf-password" @submit.prevent="submitPassword">
      <p>{{ passwordIncorrect ? t("pdf|密码不正确，请重试") : t("pdf|此 PDF 已加密，请输入打开密码") }}</p>
      <input v-model="password" type="password" autocomplete="off" autofocus :aria-label="t('pdf|PDF 打开密码')" />
      <button type="submit">{{ t("确定") }}</button><button type="button" @click="cancelPassword">{{ t("取消") }}</button>
    </form>
    <div v-else-if="loading" class="pdf-status" role="status">{{ t("正在加载…") }}</div>
    <div v-else-if="error" class="pdf-status" role="alert"><p>{{ error }}</p><button @click="load">{{ t("重试") }}</button></div>
    <div v-else class="pdf-body">
      <nav v-if="outlineOpen" class="pdf-outline" :aria-label="t('pdf|目录')">
        <p v-if="!outline.length">{{ t("pdf|此文档没有目录") }}</p>
        <button v-for="(item, index) in outline" :key="index" :disabled="!item.dest" :style="{ paddingInlineStart: `${12 + item.depth * 12}px` }" @click="navigateOutline(item)">{{ item.title }}</button>
      </nav>
      <div ref="stage" class="pdf-stage" tabindex="0">
        <p v-if="renderError" class="pdf-page-error" role="alert">{{ renderError }} <button @click="renderPage">{{ t("重试") }}</button></p>
        <p v-if="searchError" role="alert">{{ searchError }}</p>
        <p v-if="searchLimited" role="status">{{ t("pdf|匹配过多，仅显示前十万项") }}</p>
        <p v-if="rendering" class="pdf-rendering" role="status">{{ t("pdf|正在绘制页面…") }}</p>
        <div ref="pageHost" class="pdf-page"></div>
      </div>
    </div>
  </section>
</template>

<style scoped>
:global(canvas.hiddenCanvasElement){position:fixed;inset:0 auto auto 0;width:0;height:0;visibility:hidden;pointer-events:none}
.pdf-reader{display:flex;flex-direction:column;height:100%;min-height:0;width:100%;color:var(--text,#eee)}.pdf-toolbar{display:flex;align-items:center;gap:7px;flex-wrap:wrap;padding:10px;border-bottom:1px solid var(--border,#ffffff20);font-size:12px}.pdf-toolbar form{display:flex;align-items:center;gap:5px}.pdf-toolbar input[type=number]{width:58px}.pdf-toolbar input[type=search]{width:145px}.pdf-reader button,.pdf-reader input,.pdf-reader select{font:inherit;color:inherit;background:var(--surface,#252525);border:1px solid var(--border,#ffffff30);border-radius:6px;padding:5px 7px;min-width:0}.pdf-reader button:disabled{opacity:.4}.pdf-body{display:flex;flex:1;min-height:0;min-width:0}.pdf-stage{flex:1;min-width:0;min-height:0;overflow:auto;padding:16px;position:relative;box-sizing:border-box}.pdf-page{position:relative;margin:0 auto;background:#fff;box-shadow:0 3px 16px #0003;isolation:isolate}.pdf-page :deep(canvas){display:block}.pdf-outline{flex:0 0 220px;max-width:35%;overflow:auto;border-inline-end:1px solid var(--border,#ffffff20);font-size:12px}.pdf-outline button{display:block;text-align:start;width:100%;border:0;background:transparent;border-radius:0;padding:9px 12px;overflow-wrap:anywhere}.pdf-outline p{padding:12px;opacity:.65}.pdf-status,.pdf-password{padding:32px;text-align:center;overflow:auto}.pdf-password input{margin:0 8px}.pdf-password button{margin:0 4px}.pdf-rendering{position:absolute;top:12px;left:18px;z-index:2;background:var(--surface,#252525);padding:6px 10px;border-radius:8px;font-size:12px}.pdf-scale{opacity:.6}.pdf-reader :focus-visible{outline:2px solid var(--accent,#8ab4ff);outline-offset:2px}
.pdf-reader{flex:1 1 0%;min-width:0;max-height:100%;box-sizing:border-box;overflow:hidden}.pdf-toolbar{flex-shrink:0}
.pdf-page :deep(.pdf-text-layer){position:absolute;text-align:initial;inset:0;overflow:clip;opacity:1;line-height:1;text-size-adjust:none;-webkit-text-size-adjust:none;forced-color-adjust:none;transform-origin:0 0;caret-color:CanvasText;z-index:1}.pdf-page :deep(.pdf-text-layer span),.pdf-page :deep(.pdf-text-layer br){color:transparent;position:absolute;white-space:pre;cursor:text;transform-origin:0 0}.pdf-page :deep(.pdf-text-layer span.markedContent){top:0;height:0}.pdf-page :deep(.pdf-text-layer .pdf-hit){color:transparent;background:#ffd50088;position:static;padding:0}.pdf-page :deep(.pdf-text-layer .pdf-hit.active){background:#ff8700b0;outline:1px solid #d66c00}.pdf-page :deep(.pdf-text-layer ::selection){background:#3388ff66}
@media(max-width:600px){.pdf-outline{flex-basis:150px}.pdf-scale{display:none}.pdf-toolbar{gap:5px;padding:7px}}
</style>
