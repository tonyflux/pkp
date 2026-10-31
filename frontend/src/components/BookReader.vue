<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { t } from "../i18n";
import { errorMessage, loadReading, saveReading, type ConsumptionEntry } from "../consumption";
import { BOOK_ENCODINGS, cooperative, TextBook, textPageEnd, type BookEncoding } from "../reader-book-text";
import { epubPages, openEpub, type EpubBook, type EpubPage } from "../reader-book-epub";

const props = defineProps<{ url: string; entry: ConsumptionEntry; identity: string; format: "epub" | "text" }>();
interface Position { chapter: number; offset: number; scroll: number }
interface Bookmark extends Position { label: string }
interface ReadingState { position: Position; bookmarks: Bookmark[]; encoding: BookEncoding; font: number; line: number; theme: "paper" | "sepia" | "night" }
interface SearchHit { chapter: number; offset: number; label: string }
const defaults = (): ReadingState => ({ position: { chapter: 0, offset: 0, scroll: 0 }, bookmarks: [], encoding: "utf-8", font: 19, line: 1.9, theme: "paper" });
const state = ref<ReadingState>(defaults());
const content = ref<HTMLElement>();
const article = ref<HTMLElement>();
const loading = ref(false), navigating = ref(false), error = ref("");
const chapter = ref(0), offset = ref(0), section = ref(""), html = ref("");
const pageCount = ref(0), knownSegments = ref(0), textComplete = ref(false), textProgress = ref(0);
const toc = ref<{ title: string; chapter: number; offset: number; fragment?: string; depth: number }[]>([]);
const panel = ref<"toc" | "search" | "bookmarks" | "">("");
const query = ref(""), results = ref<SearchHit[]>([]), searching = ref(false), indexing = ref(false), searchDone = ref(false);
const hitPage = ref(0), tocPage = ref(0), searchScanned = ref(0), searchLimited = ref(false);
let textBook: TextBook | undefined, epub: EpubBook | undefined, pages: EpubPage[] = [], pagesChapter = -1;
let lifetime = new AbortController(), operation: AbortController | undefined;
let generation = 0, navigation = 0, disposed = false;
let imageUrls: string[] = [];
let persistTimer: ReturnType<typeof setTimeout> | undefined;
const stateKey = computed(() => `${props.identity}.book.${props.format}`);
let activeStateKey = "";
const themeStyle = computed(() => ({ fontSize: `${state.value.font}px`, lineHeight: String(state.value.line) }));
const resultSlice = computed(() => results.value.slice(hitPage.value * 80, (hitPage.value + 1) * 80));
const tocSlice = computed(() => toc.value.slice(tocPage.value * 100, (tocPage.value + 1) * 100));
const title = computed(() => toc.value.find(item => item.chapter === chapter.value)?.title ?? t("第 {n} 节", { n: chapter.value + 1 }));
const atStart = computed(() => chapter.value === 0 && offset.value === 0);
const atEnd = computed(() => props.format === "epub" ? chapter.value >= (epub?.chapters.length ?? 1) - 1 && offset.value >= pageCount.value - 1 : textComplete.value && chapter.value >= knownSegments.value - 1 && offset.value + section.value.length >= (textBook?.segments[chapter.value]?.length ?? 0));
const progress = computed(() => props.format === "epub" ? ((chapter.value + (offset.value / Math.max(1, pageCount.value))) / Math.max(1, epub?.chapters.length ?? 1)) * 100 : ((textBook?.segments[chapter.value]?.start ?? 0) / Math.max(1, textBook?.total ?? props.entry.size)) * 100);

function persist() {
  if (!activeStateKey) return;
  state.value.position = { chapter: chapter.value, offset: offset.value, scroll: state.value.position.scroll };
  saveReading(activeStateKey, state.value);
}
function onScroll() {
  const el = content.value;
  if (!el || navigating.value) return;
  state.value.position.scroll = el.scrollTop / Math.max(1, el.scrollHeight - el.clientHeight);
  clearTimeout(persistTimer);
  persistTimer = setTimeout(persist, 250);
}
function cancelOperation() {
  operation?.abort(); operation = undefined;
  searching.value = false; indexing.value = false;
}
function syncText() {
  if (!textBook) return;
  knownSegments.value = textBook.segments.length;
  textComplete.value = textBook.complete;
  textProgress.value = (textBook.segments.at(-1)?.end ?? 0) / Math.max(1, textBook.total) * 100;
  toc.value = textBook.chapters.map(item => ({ title: item.title, chapter: item.segment, offset: item.offset, depth: 0 }));
}
async function go(targetChapter: number, targetOffset = 0, fragment = "", scroll = 0) {
  const current = generation, request = ++navigation;
  navigating.value = true; error.value = "";
  try {
    if (textBook) {
      const source = textBook;
      targetChapter = Math.max(0, targetChapter);
      let text = await source.segment(targetChapter);
      if (current !== generation || request !== navigation) return;
      syncText();
      if (!source.segments[targetChapter] && source.segments.length) {
        targetChapter = source.segments.length - 1;
        text = await source.segment(targetChapter);
        if (current !== generation || request !== navigation) return;
      }
      if (!source.segments[targetChapter]) return;
      chapter.value = targetChapter;
      offset.value = Math.min(Math.max(0, targetOffset), Math.max(0, text.length - 1));
      if (offset.value && /[\uDC00-\uDFFF]/.test(text[offset.value])) offset.value--;
      section.value = text.slice(offset.value, textPageEnd(text, offset.value));
      html.value = "";
    } else if (epub) {
      const book = epub;
      targetChapter = Math.max(0, Math.min(book.chapters.length - 1, targetChapter));
      const loaded = pagesChapter === targetChapter ? pages : await epubPages(book, targetChapter);
      if (current !== generation || request !== navigation) return;
      pages = loaded; pagesChapter = targetChapter; pageCount.value = pages.length;
      const anchorPage = fragment ? pages.findIndex(page => page.ids.includes(fragment)) : -1;
      chapter.value = targetChapter; offset.value = anchorPage >= 0 ? anchorPage : Math.max(0, Math.min(pages.length - 1, targetOffset));
      for (const url of imageUrls) book.archive.releaseImage(url);
      imageUrls = [];
      html.value = pages[offset.value].html;
      section.value = "";
    }
    state.value.position = { chapter: chapter.value, offset: offset.value, scroll };
    await nextTick();
    if (current !== generation || request !== navigation) return;
    content.value?.scrollTo({ top: scroll * Math.max(0, content.value.scrollHeight - content.value.clientHeight) });
    if (fragment && article.value) Array.from(article.value.querySelectorAll<HTMLElement>("[id]")).find(element => element.id === fragment)?.scrollIntoView({ block: "start" });
    if (epub && article.value) {
      const book = epub;
      for (const image of Array.from(article.value.querySelectorAll<HTMLImageElement>("img[data-book-image]"))) {
        try {
          const url = await book.archive.image(image.dataset.bookImage!);
          if (current !== generation || request !== navigation) { book.archive.releaseImage(url); continue; }
          imageUrls.push(url); image.src = url;
        } catch (failure) { if (current === generation && request === navigation && !lifetime.signal.aborted) image.alt = image.alt || errorMessage(failure); }
      }
    }
    if (current === generation && request === navigation) persist();
  } catch (failure) {
    if (current === generation && request === navigation && !lifetime.signal.aborted) error.value = errorMessage(failure);
  } finally { if (current === generation && request === navigation) navigating.value = false; }
}
async function step(direction: number) {
  if (navigating.value) return;
  if (props.format === "epub") {
    const next = offset.value + direction;
    if (next >= 0 && next < pageCount.value) await go(chapter.value, next);
    else if (direction > 0 && !atEnd.value) await go(chapter.value + 1);
    else if (direction < 0 && chapter.value > 0) await go(chapter.value - 1, Number.MAX_SAFE_INTEGER);
  } else if (textBook) {
    if (direction > 0) {
      const next = offset.value + section.value.length;
      if (next < textBook.segments[chapter.value].length) await go(chapter.value, next);
      else if (!atEnd.value) await go(chapter.value + 1);
    } else if (offset.value > 0) await go(chapter.value, Math.max(0, offset.value - 8000));
    else if (chapter.value > 0) await go(chapter.value - 1, Math.max(0, textBook.segments[chapter.value - 1].length - 8000));
  }
}
async function seek(event: Event | number) {
  const value = (typeof event === "number" ? event : Number((event.target as HTMLInputElement).value)) / 100;
  if (epub) { await go(Math.min(epub.chapters.length - 1, Math.floor(value * epub.chapters.length))); return; }
  if (!textBook) return;
  const book = textBook, current = generation;
  navigating.value = true;
  try {
    const target = Math.floor(value * book.total);
    while (!book.complete && (book.segments.at(-1)?.end ?? 0) <= target) {
      await book.extend();
      if (current !== generation) return;
      syncText(); await cooperative(lifetime.signal);
    }
    const found = book.segments.findIndex(part => part.end > target);
    const index = found < 0 ? Math.max(0, book.segments.length - 1) : found;
    const part = book.segments[index];
    const targetOffset = part ? value === 1 ? Math.max(0, part.length - 8000) : Math.floor(Math.max(0, target - part.start) / Math.max(1, part.end - part.start) * part.length) : 0;
    await go(index, targetOffset);
  } catch (failure) { if (current === generation && !lifetime.signal.aborted) error.value = errorMessage(failure); }
  finally { if (current === generation) navigating.value = false; }
}
function bookmark() {
  const exists = state.value.bookmarks.findIndex(item => item.chapter === chapter.value && item.offset === offset.value);
  if (exists >= 0) state.value.bookmarks.splice(exists, 1);
  else state.value.bookmarks.push({ chapter: chapter.value, offset: offset.value, scroll: state.value.position.scroll, label: `${title.value} · ${offset.value + 1}` });
  persist();
}
function removeBookmark(index: number) { state.value.bookmarks.splice(index, 1); persist(); }
async function scan(indexOnly = false) {
  cancelOperation();
  const controller = new AbortController(), current = generation;
  operation = controller;
  const needle = query.value.trim().toLowerCase();
  if (!indexOnly && !needle) return;
  indexing.value = indexOnly; searching.value = !indexOnly; searchDone.value = false;
  if (!indexOnly) { results.value = []; hitPage.value = 0; searchScanned.value = 0; searchLimited.value = false; }
  let carry = "", previousChapter = 0, previousLength = 0;
  const visit = (text: string, index: number) => {
    if (indexOnly) { syncText(); return; }
    // TXT search keeps an overlap so a match spanning Range segments is found.
    const prefix = props.format === "text" ? carry : "";
    const source = (prefix + text).toLowerCase();
    let found = source.indexOf(needle);
    while (found >= 0) {
      if (results.value.length >= 10000) { searchLimited.value = true; break; }
      const crosses = found < prefix.length;
      results.value.push({ chapter: crosses ? previousChapter : index, offset: crosses ? previousLength - prefix.length + found : found - prefix.length, label: (prefix + text).slice(Math.max(0, found - 30), found + needle.length + 60).replace(/\s+/g, " ") });
      found = source.indexOf(needle, found + Math.max(1, needle.length));
    }
    carry = needle.length > 1 ? text.slice(-(needle.length - 1)) : ""; previousChapter = index; previousLength = text.length;
    searchScanned.value++;
  };
  try {
    if (textBook) await textBook.scan(controller.signal, visit);
    else if (epub) {
      const book = epub;
      for (let index = 0; index < book.chapters.length; index++) {
        controller.signal.throwIfAborted();
        const chapterPages = await epubPages(book, index);
        controller.signal.throwIfAborted();
        const before = results.value.length;
        visit(chapterPages.map(page => page.text).join(""), index);
        for (let match = before; match < results.value.length; match++) {
          let remaining = results.value[match].offset, page = 0;
          while (page < chapterPages.length - 1 && remaining >= chapterPages[page].text.length) { remaining -= chapterPages[page].text.length; page++; }
          results.value[match].offset = page;
        }
        await cooperative(controller.signal);
      }
    }
    if (current === generation && !controller.signal.aborted) { syncText(); searchDone.value = true; }
  } catch (failure) { if (current === generation && !controller.signal.aborted && !lifetime.signal.aborted) error.value = errorMessage(failure); }
  finally { if (operation === controller) { searching.value = false; indexing.value = false; operation = undefined; } }
}
function link(event: MouseEvent) {
  const target = (event.target as Element).closest<HTMLAnchorElement>("a");
  if (!target) return;
  event.preventDefault(); event.stopPropagation();
  const index = epub?.chapters.findIndex(item => item.path === target.dataset.bookPath) ?? -1;
  if (index >= 0) void go(index, 0, target.dataset.bookFragment ?? "");
}
function keyboard(event: KeyboardEvent) {
  const target = event.target as HTMLElement;
  if (target.closest("input,select,textarea,button,a")) { if (event.key !== "Escape") event.stopPropagation(); return; }
  if (["ArrowLeft", "ArrowRight", "PageDown", "PageUp", "Home", "End", " "].includes(event.key)) {
    event.stopPropagation(); event.preventDefault();
    if (event.key === "Home") void go(0);
    else if (event.key === "End") { if (epub) void go(epub.chapters.length - 1, Number.MAX_SAFE_INTEGER); else void seek(100); }
    else void step(["ArrowLeft", "PageUp"].includes(event.key) ? -1 : 1);
  }
}
async function load() {
  persist(); clearTimeout(persistTimer); cancelOperation(); lifetime.abort();
  const old = epub; epub = undefined; textBook = undefined;
  if (old) void old.archive.close();
  lifetime = new AbortController(); const signal = lifetime.signal, current = ++generation;
  ++navigation; imageUrls = []; pages = []; pagesChapter = -1;
  html.value = ""; section.value = ""; error.value = ""; toc.value = []; results.value = []; searchDone.value = false;
  chapter.value = 0; offset.value = 0; knownSegments.value = 0; textComplete.value = false; textProgress.value = 0; tocPage.value = 0;
  loading.value = true; navigating.value = false;
  activeStateKey = stateKey.value;
  const stored = loadReading<unknown>(activeStateKey, null);
  const saved = stored && typeof stored === "object" ? stored as Record<string, unknown> : {};
  const rawPosition = saved.position && typeof saved.position === "object" ? saved.position as Record<string, unknown> : {};
  const bookmarks: Bookmark[] = [];
  if (Array.isArray(saved.bookmarks)) for (const item of saved.bookmarks.slice(0, 1000)) {
    if (!item || typeof item !== "object") continue;
    const mark = item as Record<string, unknown>;
    if (typeof mark.chapter !== "number" || !Number.isSafeInteger(mark.chapter) || mark.chapter < 0 || typeof mark.offset !== "number" || !Number.isSafeInteger(mark.offset) || mark.offset < 0 || typeof mark.label !== "string") continue;
    bookmarks.push({ chapter: mark.chapter, offset: mark.offset, label: mark.label.slice(0, 300), scroll: Math.max(0, Math.min(1, Number(mark.scroll) || 0)) });
  }
  state.value = { ...defaults(), position: { chapter: Number(rawPosition.chapter) || 0, offset: Number(rawPosition.offset) || 0, scroll: Number(rawPosition.scroll) || 0 }, bookmarks,
    encoding: BOOK_ENCODINGS.includes(saved.encoding as BookEncoding) ? saved.encoding as BookEncoding : "utf-8", font: Number(saved.font) || 19, line: Number(saved.line) || 1.9,
    theme: saved.theme === "sepia" || saved.theme === "night" ? saved.theme : "paper" };
  state.value.font = Math.max(12, Math.min(40, Number(state.value.font) || 19));
  state.value.line = Math.max(1.2, Math.min(2.8, Number(state.value.line) || 1.9));
  if (!BOOK_ENCODINGS.includes(state.value.encoding)) state.value.encoding = "utf-8";
  if (!["paper", "sepia", "night"].includes(state.value.theme)) state.value.theme = "paper";
  const position = { ...state.value.position };
  try {
    if (props.format === "text") { textBook = new TextBook(props.url, state.value.encoding, props.entry.size, signal); await textBook.extend(); if (current !== generation) return; state.value.encoding = textBook.encoding; syncText(); }
    else {
      const book = await openEpub(props.url, signal);
      if (current !== generation) { await book.archive.close(); return; }
      epub = book; toc.value = book.toc.map(item => ({ title: item.title, chapter: book.chapters.findIndex(chapter => chapter.path === item.path), offset: 0, fragment: item.fragment, depth: item.depth }));
    }
    loading.value = false;
    await go(Number.isSafeInteger(position.chapter) && position.chapter >= 0 ? position.chapter : 0, Number.isSafeInteger(position.offset) && position.offset >= 0 ? position.offset : 0, "", Math.max(0, Math.min(1, Number(position.scroll) || 0)));
  } catch (failure) { if (current === generation && !signal.aborted) error.value = errorMessage(failure); }
  finally { if (current === generation) loading.value = false; }
}
function changeEncoding() { state.value.position = { chapter: 0, offset: 0, scroll: 0 }; chapter.value = 0; offset.value = 0; persist(); void load(); }
watch(() => [props.url, props.identity, props.format], () => { if (!disposed) void load(); }, { immediate: true });
onBeforeUnmount(() => { disposed = true; persist(); clearTimeout(persistTimer); cancelOperation(); lifetime.abort(); ++generation; void epub?.archive.close(); });
</script>

<template>
  <section class="book-reader" :data-theme="state.theme" @keydown="keyboard">
    <header class="book-toolbar">
      <button type="button" :aria-label="t('阅读目录')" :aria-pressed="panel === 'toc'" @click="panel = panel === 'toc' ? '' : 'toc'">{{ t("book|目录") }}</button>
      <button type="button" :aria-label="t('书内搜索')" :aria-pressed="panel === 'search'" @click="panel = panel === 'search' ? '' : 'search'">{{ t("book|搜索") }}</button>
      <button type="button" :aria-label="t('阅读书签')" :aria-pressed="panel === 'bookmarks'" @click="panel = panel === 'bookmarks' ? '' : 'bookmarks'">{{ t("book|书签") }}</button>
      <button type="button" :title="t('切换当前位置书签')" :aria-label="t('切换当前位置书签')" :disabled="loading || !section && !html" @click="bookmark">☆</button>
      <label>{{ t("book|字号") }} <input v-model.number="state.font" type="number" min="12" max="40" @change="persist" /></label>
      <label>{{ t("book|行距") }} <select v-model.number="state.line" @change="persist"><option :value="1.5">1.5</option><option :value="1.9">1.9</option><option :value="2.3">2.3</option><option :value="2.7">2.7</option></select></label>
      <label>{{ t("book|主题") }} <select v-model="state.theme" @change="persist"><option value="paper">{{ t("reading-theme|纸白") }}</option><option value="sepia">{{ t("reading-theme|暖黄") }}</option><option value="night">{{ t("reading-theme|夜色") }}</option></select></label>
      <label v-if="format === 'text'">{{ t("book|编码") }} <select v-model="state.encoding" :disabled="loading" @change="changeEncoding"><option v-for="encoding in BOOK_ENCODINGS" :key="encoding" :value="encoding">{{ encoding.toUpperCase() }}</option></select></label>
    </header>
    <div class="book-body">
      <aside v-if="panel" class="book-panel">
        <template v-if="panel === 'toc'">
          <template v-if="format === 'text'">
            <button type="button" :disabled="indexing || loading || textComplete" @click="scan(true)">{{ t("扫描全书目录") }}</button>
            <button v-if="indexing" type="button" @click="cancelOperation">{{ t("停止扫描") }}</button>
            <p>{{ t("目录扫描进度 {percent}%", { percent: Math.floor(textProgress) }) }}</p>
          </template>
          <p v-if="!toc.length">{{ t("尚未发现章节标题，可使用底部进度跳转") }}</p>
          <button v-for="(item, index) in tocSlice" :key="index" type="button" class="book-list-item" :style="{ paddingInlineStart: `${12 + Math.min(5, item.depth) * 12}px` }" :aria-current="chapter === item.chapter ? 'location' : undefined" @click="go(item.chapter, item.offset, item.fragment)">{{ item.title }}</button>
          <div v-if="toc.length > 100" class="book-pager"><button :disabled="tocPage === 0" @click="tocPage--">{{ t("上一组") }}</button><span>{{ tocPage + 1 }} / {{ Math.ceil(toc.length / 100) }}</span><button :disabled="(tocPage + 1) * 100 >= toc.length" @click="tocPage++">{{ t("下一组") }}</button></div>
        </template>
        <template v-else-if="panel === 'search'">
          <form class="book-search" @submit.prevent="scan(false)"><input v-model="query" :aria-label="t('书内搜索词')" maxlength="200" /><button type="submit" :disabled="loading || !query.trim()">{{ t("搜索全书") }}</button></form>
          <button v-if="searching" type="button" @click="cancelOperation">{{ t("停止扫描") }}</button>
          <p v-if="searching">{{ t("正在扫描第 {n} 节", { n: searchScanned + 1 }) }}</p>
          <p v-if="searchDone && !results.length">{{ t("全书未找到匹配内容") }}</p>
          <p v-if="searchLimited">{{ t("结果达到一万条，请缩小搜索范围") }}</p>
          <button v-for="(hit, index) in resultSlice" :key="index" type="button" class="book-list-item" @click="go(hit.chapter, hit.offset)">{{ hit.label }}</button>
          <div v-if="results.length > 80" class="book-pager"><button :disabled="hitPage === 0" @click="hitPage--">{{ t("上一组") }}</button><span>{{ hitPage + 1 }} / {{ Math.ceil(results.length / 80) }}</span><button :disabled="(hitPage + 1) * 80 >= results.length" @click="hitPage++">{{ t("下一组") }}</button></div>
        </template>
        <template v-else>
          <p v-if="!state.bookmarks.length">{{ t("尚无阅读书签") }}</p>
          <div v-for="(item, index) in state.bookmarks" :key="index" class="book-bookmark"><button type="button" class="book-list-item" @click="go(item.chapter, item.offset, '', item.scroll)">{{ item.label }}</button><button type="button" :aria-label="t('删除阅读书签')" @click="removeBookmark(index)">×</button></div>
        </template>
      </aside>
      <div ref="content" class="book-scroll" tabindex="0" :aria-busy="loading || navigating" @scroll.passive="onScroll">
        <p v-if="loading" role="status" class="book-notice">{{ t("正在打开电子书…") }}</p>
        <div v-if="error" role="alert" class="book-notice"><p>{{ error }}</p><button type="button" @click="load">{{ t("重试阅读") }}</button></div>
        <p v-if="navigating && !loading" class="book-busy" role="status">{{ t("正在读取章节…") }}</p>
        <article v-if="!loading && section" class="book-text" :style="themeStyle">{{ section }}</article>
        <article v-if="!loading && html" ref="article" class="book-prose" :style="themeStyle" @click="link" v-html="html" />
      </div>
    </div>
    <footer class="book-navigation">
      <button type="button" :disabled="loading || navigating || atStart" @click="step(-1)">{{ t("上一阅读节") }}</button>
      <label class="book-progress"><span>{{ title }}</span><input type="range" min="0" max="100" step="0.1" :value="progress" :aria-label="t('全书阅读进度')" :disabled="loading || navigating" @change="seek" /></label>
      <span>{{ Math.floor(progress) }}%</span>
      <button type="button" :disabled="loading || navigating || atEnd" @click="step(1)">{{ t("下一阅读节") }}</button>
    </footer>
  </section>
</template>

<style scoped>
.book-reader{--book-paper:#faf9f5;--book-ink:#292923;--book-rule:#d9d6cc;display:flex;flex-direction:column;width:100%;height:100%;min-height:0;background:var(--book-paper);color:var(--book-ink)}
.book-reader[data-theme="sepia"]{--book-paper:#f0e4ce;--book-ink:#493c29;--book-rule:#cabea8}.book-reader[data-theme="night"]{--book-paper:#202322;--book-ink:#d3d7ce;--book-rule:#454b44}
.book-toolbar,.book-navigation{display:flex;align-items:center;gap:8px;padding:10px 14px;flex-wrap:wrap;border-bottom:1px solid var(--book-rule);font-size:12px}.book-toolbar label{display:flex;align-items:center;gap:5px}.book-reader button,.book-reader select,.book-reader input{font:inherit;color:inherit;background:transparent;border:1px solid var(--book-rule);border-radius:6px;padding:5px 7px}.book-reader button{cursor:pointer}.book-reader button:active{transform:scale(.97)}.book-reader button:disabled{opacity:.4;cursor:default}.book-reader button[aria-pressed="true"],.book-reader button[aria-current]{background:var(--book-rule)}.book-reader input[type="number"]{width:48px}.book-reader :focus-visible{outline:2px solid #6c947c;outline-offset:2px}.book-body{display:flex;flex:1;min-height:0;overflow:hidden}.book-panel{flex:0 0 270px;padding:12px;overflow:auto;border-right:1px solid var(--book-rule);font-size:13px;min-width:0}.book-panel p{line-height:1.7;opacity:.75}.book-list-item{display:block;text-align:start;width:100%;margin:4px 0;overflow-wrap:anywhere}.book-bookmark{display:flex;align-items:center;gap:4px}.book-bookmark>.book-list-item{flex:1;min-width:0}.book-search{display:flex;gap:5px}.book-search input{min-width:0;width:100%}.book-pager{display:flex;align-items:center;justify-content:space-between;gap:5px;margin-top:12px}.book-scroll{flex:1;min-width:0;min-height:0;overflow:auto;position:relative;overscroll-behavior:contain}.book-text,.book-prose{max-width:760px;margin:0 auto;padding:36px 32px 56px;overflow-wrap:anywhere;word-break:normal;font-family:ui-serif,"Songti SC","Noto Serif CJK SC",serif}.book-text{white-space:pre-wrap}.book-prose:deep(img){max-width:100%;height:auto;object-fit:contain}.book-prose:deep(table){border-collapse:collapse;display:block;max-width:100%;overflow:auto}.book-prose:deep(td),.book-prose:deep(th){border:1px solid var(--book-rule);padding:.4em .6em}.book-prose:deep(pre){white-space:pre-wrap}.book-prose:deep(a){color:inherit;text-decoration:underline}.book-prose:deep(h1),.book-prose:deep(h2),.book-prose:deep(h3){line-height:1.4}.book-notice{padding:30px;text-align:center;font-size:14px}.book-busy{position:sticky;top:0;margin:0;padding:6px 20px;background:var(--book-paper);text-align:center;font-size:12px}.book-navigation{border-top:1px solid var(--book-rule);border-bottom:0;flex-wrap:nowrap}.book-progress{flex:1;min-width:40px;display:flex;align-items:center;gap:10px}.book-progress span{max-width:160px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.book-progress input{width:100%;min-width:40px;accent-color:#6c947c;padding:0}.book-navigation>button{white-space:nowrap}
@media(max-width:720px){.book-panel{flex-basis:38%;padding:8px}.book-text,.book-prose{padding:24px 18px 40px}.book-progress span{display:none}.book-toolbar{gap:6px;padding:8px}.book-navigation{gap:5px;padding:8px}.book-search{flex-wrap:wrap}}
</style>
