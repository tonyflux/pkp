<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { errorMessage, loadReading, saveReading, type ConsumptionEntry } from "../consumption";
import { t } from "../i18n";
import { loadOfficePackage } from "../reader-office-archive";
import { createOfficeRenderer, officeFrameDocument, type OfficePage, type OfficeRenderer } from "../reader-office-render";

const props = defineProps<{ url: string; entry: ConsumptionEntry; identity: string }>();
const loading = ref(true), error = ref("");
const pageIndex = ref(0), count = ref(0), zoom = ref(1), fit = ref(true);
const page = ref<OfficePage>(), query = ref(""), result = ref("");
const thumbnails = ref(false);
const stage = ref<HTMLElement>(), thumbRail = ref<HTMLElement>(), searchInput = ref<HTMLInputElement>();
const width = ref(800), frame = ref<HTMLIFrameElement>();
const previews = ref<Record<number, OfficePage>>({});
let renderer: OfficeRenderer | undefined;
let controller: AbortController | undefined;
let sizer: ResizeObserver | undefined;
let frameSizer: ResizeObserver | undefined;
let thumbObserver: IntersectionObserver | undefined;
let generation = 0;
const scale = computed(() => fit.value ? Math.max(.1, Math.min(1.5, (width.value - 32) / (page.value?.width ?? 816))) : zoom.value);
const srcdoc = computed(() => page.value ? officeFrameDocument(page.value.html) : "");
const storageKey = () => `${props.identity}.office.docx`;
function remember() { saveReading(storageKey(), { page: pageIndex.value, zoom: zoom.value, fit: fit.value, thumbnails: thumbnails.value }); }
function close() {
  controller?.abort(); controller = undefined;
  thumbObserver?.disconnect(); thumbObserver = undefined;
  sizer?.disconnect(); sizer = undefined;
  frameSizer?.disconnect(); frameSizer = undefined;
  renderer?.close(); renderer = undefined;
  previews.value = {}; page.value = undefined;
}
async function load() {
  const current = ++generation;
  close(); loading.value = true; error.value = ""; count.value = 0; query.value = ""; result.value = "";
  const stored = loadReading<unknown>(storageKey(), null);
  const saved = stored && typeof stored === "object" ? stored as Record<string, unknown> : {};
  zoom.value = typeof saved.zoom === "number" && Number.isFinite(saved.zoom) ? Math.max(.25, Math.min(3, saved.zoom)) : 1;
  fit.value = saved.fit !== false; thumbnails.value = saved.thumbnails === true;
  const abort = controller = new AbortController();
  let created: OfficeRenderer | undefined;
  try {
    const pkg = await loadOfficePackage(props.url, abort.signal);
    if (current !== generation) return;
    created = await createOfficeRenderer(pkg, abort.signal);
    if (current !== generation) { created.close(); return; }
    renderer = created; count.value = created.count;
    pageIndex.value = typeof saved.page === "number" && Number.isInteger(saved.page) ? Math.max(0, Math.min(created.count - 1, saved.page)) : 0;
    showPage(pageIndex.value);
  } catch (cause) {
    created?.close();
    if (current === generation && !abort.signal.aborted) error.value = errorMessage(cause);
  } finally {
    if (current === generation) { loading.value = false; await nextTick(); observeSurface(); observeThumbnails(); }
  }
}
function showPage(index: number) {
  if (!renderer || index < 0 || index >= count.value) return;
  try {
    frameSizer?.disconnect();
    page.value = renderer.page(index); pageIndex.value = index;
    stage.value?.scrollTo({ top: 0, left: 0 }); remember();
  } catch (cause) { error.value = errorMessage(cause); }
}
function onPageInput(event: Event) {
  const value = Number((event.target as HTMLInputElement).value);
  if (Number.isInteger(value)) showPage(Math.max(0, Math.min(count.value - 1, value - 1)));
}
function changeZoom(delta: number) {
  zoom.value = Math.max(.25, Math.min(3, Math.round((scale.value + delta) * 100) / 100)); fit.value = false; remember();
}
function onKey(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f") { event.preventDefault(); event.stopPropagation(); searchInput.value?.focus(); return; }
  if ((event.target as HTMLElement).closest("input,textarea,button")) { event.stopPropagation(); return; }
  const step = event.key === "ArrowRight" || event.key === "PageDown" ? 1 : event.key === "ArrowLeft" || event.key === "PageUp" ? -1 : 0;
  if (step) { event.preventDefault(); event.stopPropagation(); showPage(pageIndex.value + step); }
}
function search(direction = 1) {
  const needle = query.value.trim().toLocaleLowerCase();
  if (!needle || !renderer) return;
  try {
    for (let offset = 1; offset <= count.value; offset++) {
      const index = (pageIndex.value + direction * offset + count.value * 2) % count.value;
      if (renderer.text(index).toLocaleLowerCase().includes(needle)) { showPage(index); result.value = t("office|已找到匹配内容"); return; }
    }
    result.value = t("office|未找到匹配内容");
  } catch (cause) { error.value = errorMessage(cause); }
}
function observeSurface() {
  sizer?.disconnect();
  if (!stage.value) return;
  width.value = stage.value.clientWidth;
  sizer = new ResizeObserver(entries => { width.value = entries[0]?.contentRect.width ?? width.value; });
  sizer.observe(stage.value);
}
function onFrameLoad() {
  frameSizer?.disconnect();
  const body = frame.value?.contentDocument?.body;
  if (!body || !page.value) return;
  const resize = () => {
    if (page.value) page.value = { ...page.value, height: Math.min(30000, Math.max(page.value.height, body.scrollHeight)) };
  };
  resize(); frameSizer = new ResizeObserver(resize); frameSizer.observe(body);
}
function observeThumbnails() {
  thumbObserver?.disconnect();
  if (!thumbnails.value || !thumbRail.value || !renderer) return;
  thumbObserver = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting || !renderer) continue;
      const index = Number((entry.target as HTMLElement).dataset.page);
      if (!previews.value[index]) {
        try {
          const next = { ...previews.value, [index]: renderer.page(index) };
          const keys = Object.keys(next).map(Number);
          if (keys.length > 16) delete next[keys.sort((a, b) => Math.abs(b - index) - Math.abs(a - index))[0]];
          previews.value = next;
        } catch (cause) { error.value = errorMessage(cause); }
      }
    }
  }, { root: thumbRail.value, rootMargin: "100px" });
  thumbRail.value.querySelectorAll("[data-page]").forEach(element => thumbObserver!.observe(element));
}
watch(thumbnails, async () => { remember(); await nextTick(); observeThumbnails(); observeSurface(); });
watch(() => [props.url, props.identity], () => { void load(); }, { immediate: true });
onBeforeUnmount(() => { generation++; close(); });
</script>

<template>
  <section class="office-reader" tabindex="0" @keydown="onKey" :aria-label="entry.name">
    <div class="office-toolbar" v-if="!loading && !error">
      <button type="button" @click="thumbnails = !thumbnails" :aria-pressed="thumbnails">{{ t("office|缩略图") }}</button>
      <button type="button" :disabled="pageIndex === 0" @click="showPage(pageIndex - 1)" :aria-label="t('office|上一页')">‹</button>
      <label class="office-page-number"><input type="number" :min="1" :max="count" :value="pageIndex + 1" @change="onPageInput" :aria-label="t('office|页码')"><span>/ {{ count }}</span></label>
      <button type="button" :disabled="pageIndex + 1 >= count" @click="showPage(pageIndex + 1)" :aria-label="t('office|下一页')">›</button>
      <button type="button" @click="changeZoom(-.1)" :disabled="scale <= .25" :aria-label="t('office|缩小')">−</button>
      <button type="button" @click="fit = !fit; remember()" :aria-pressed="fit">{{ fit ? t("office|适合宽度") : `${Math.round(scale * 100)}%` }}</button>
      <button type="button" @click="changeZoom(.1)" :disabled="scale >= 3" :aria-label="t('office|放大')">+</button>
      <form class="office-search" @submit.prevent="search()">
        <input ref="searchInput" v-model="query" type="search" :placeholder="t('office|搜索文档')" :aria-label="t('office|搜索文档')" maxlength="200">
        <button type="submit" :disabled="!query.trim()">{{ t("office|查找下一处") }}</button>
      </form>
    </div>
    <p class="office-status" v-if="loading" role="status">{{ t("office|正在安全解析文档") }}</p>
    <div class="office-status" v-else-if="error" role="alert"><p>{{ error }}</p><button type="button" @click="load">{{ t("重试") }}</button></div>
    <template v-else>
      <p class="office-limits">{{ t("只读本地预览，不连接外部网络。使用本机字体，分页与复杂版式可能不同。不显示图表、动画、影音、宏、嵌入对象和外部内容。上限：压缩 32 MB／展开 96 MB，DOCX 300 页；需整份排版。") }}</p>
      <p v-if="result" class="office-search-result" role="status">{{ result }}</p>
      <div class="office-workspace">
        <aside v-if="thumbnails" ref="thumbRail" class="office-thumbnails" :aria-label="t('office|缩略图')">
          <button v-for="index in count" :key="index" type="button" :data-page="index - 1" :aria-current="index - 1 === pageIndex ? 'page' : undefined" @click="showPage(index - 1)" :aria-label="t('office|跳到第{page}页', { page: index })">
            <div class="office-thumb-image" :style="{ height: `${Math.min(200, 128 * (previews[index - 1]?.height ?? 540) / (previews[index - 1]?.width ?? 960))}px` }">
              <iframe v-if="previews[index - 1]" tabindex="-1" sandbox="allow-same-origin" referrerpolicy="no-referrer" :title="t('office|跳到第{page}页', { page: index })" :srcdoc="officeFrameDocument(previews[index - 1].html)" :style="{ width: `${previews[index - 1].width}px`, height: `${previews[index - 1].height}px`, transform: `scale(${128 / previews[index - 1].width})` }" />
            </div><span>{{ index }}</span>
          </button>
        </aside>
        <div ref="stage" class="office-stage">
          <div v-if="page" class="office-paper" :style="{ width: `${page.width * scale}px`, height: `${page.height * scale}px` }">
            <iframe ref="frame" sandbox="allow-same-origin" referrerpolicy="no-referrer" :title="t('office|跳到第{page}页', { page: pageIndex + 1 })" :srcdoc="srcdoc" :style="{ width: `${page.width}px`, height: `${page.height}px`, transform: `scale(${scale})` }" @load="onFrameLoad" />
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.office-reader{height:100%;min-height:0;display:flex;flex-direction:column;outline:none;color:var(--text-primary,inherit)}
.office-toolbar{display:flex;align-items:center;gap:5px;flex-wrap:wrap;padding:8px 10px;border-bottom:1px solid var(--border,#8883);flex-shrink:0}
.office-toolbar button,.office-status button{font:inherit;font-size:12px;border:1px solid var(--border,#8884);background:var(--surface,#8881);color:inherit;border-radius:6px;min-height:28px;padding:3px 8px;cursor:pointer;white-space:nowrap}.office-toolbar button:disabled{opacity:.4;cursor:default}
.office-toolbar button[aria-pressed=true]{background:var(--accent-soft,#6a8eff25)}.office-page-number{display:flex;align-items:center;gap:4px;font-size:12px;white-space:nowrap}.office-page-number input{width:54px}
.office-toolbar input{font:inherit;font-size:12px;min-height:30px;background:transparent;color:inherit;border:1px solid var(--border,#8884);border-radius:5px;padding:4px 6px;min-width:0}.office-search{display:flex;gap:5px;margin-left:auto}.office-search input{width:145px}.office-limits,.office-search-result{font-size:11px;line-height:1.5;margin:0;padding:6px 12px;color:var(--text-secondary,#888)}
.office-workspace{display:flex;flex:1;min-height:0;min-width:0}.office-stage{flex:1;min-width:0;min-height:0;overflow:auto;padding:16px;background:#8881}.office-paper{position:relative;margin:0 auto;background:white;box-shadow:0 2px 12px #0002;flex-shrink:0}.office-paper iframe,.office-thumb-image iframe{border:0;transform-origin:top left;position:absolute;left:0;top:0;background:white}
.office-thumbnails{width:156px;flex-shrink:0;overflow:auto;border-right:1px solid var(--border,#8883);padding:8px;display:flex;flex-direction:column;gap:8px}.office-thumbnails button{padding:4px;border:2px solid transparent;border-radius:6px;background:var(--surface,#8881);color:inherit;cursor:pointer}.office-thumbnails button[aria-current=page]{border-color:var(--accent,#698cff)}.office-thumb-image{position:relative;width:128px;overflow:hidden;background:white;pointer-events:none}.office-thumbnails span{font-size:11px;display:block;margin-top:4px}
.office-status{margin:auto;padding:24px;text-align:center;font-size:13px}
@media(max-width:760px){.office-toolbar{gap:4px;padding:7px}.office-toolbar button{padding:4px 6px}.office-search{margin-left:0;flex:1;min-width:170px}.office-search input{width:100%;flex:1}.office-thumbnails{width:112px}.office-thumb-image{width:128px;transform:scale(.68);transform-origin:top left;margin-bottom:-32px}.office-limits{font-size:10px}}
@media(max-width:520px){.office-workspace{flex-wrap:wrap}.office-stage{height:100%;order:1}.office-thumbnails{order:0;width:100%;height:105px;flex-direction:row;border-right:0;border-bottom:1px solid var(--border,#8883);padding:4px}.office-thumbnails button{flex-shrink:0;max-height:95px;width:100px;overflow:hidden}.office-thumbnails:has(+.office-stage) + .office-stage{height:calc(100% - 105px)}}
</style>
