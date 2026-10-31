<script setup lang="ts">
// File Viewing: one shell for every file
// opened without the player — back and the file's name, the content stage,
// a bar holding only the controls this kind of file really has, and its
// facts in the context column beside it (PanelDetails). A picture
// fits and zooms; documents delegate to their bounded read-only consumers.
// Video and audio stay on the existing libmpv session.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Browser } from "@wailsio/runtime";
import { N_, t } from "../i18n";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import MarkdownDoc from "./MarkdownDoc";
import BookReader from "./BookReader.vue";
import ComicReader from "./ComicReader.vue";
import TableReader from "./TableReader.vue";
import OfficeReader from "./OfficeReader.vue";
import PdfReader from "./PdfReader.vue";
import TimedTextReader from "./TimedTextReader.vue";
import { DriveService } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import Thumb from "./Thumb.vue";
import { kindLabel } from "../format";
import { WORKSPACE_QUERY, isTyping, onWindows, useMedia } from "../dom";
import { parseMarkdown } from "../markdown";
import { fitScale, unsupportedCopy, viewerBadge, zoomIn, zoomLabel, zoomOut, type Zoom } from "../viewer";
import { closeViewer, drive, inViewerWindow, loadViewer, loadViewerFolder, notify, openViewer, popOutViewer, rememberViewerImage, returnViewerToMain, setTextWrap, stepViewer, toggleViewerNavigation, ui, viewer, wholeDrive, type Route } from "../store";
import type { Entry } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const entry = computed(() => viewer.entry);
const kind = computed(() => viewer.kind);
const format = computed(() => entry.value?.name.split(".").pop()?.toLowerCase() ?? "");
const rich = computed(() => ["pdf", "epub", "book", "comic", "table", "office", "timed"].includes(kind.value) || viewer.comic);
const comicPages = computed(() => viewer.siblings.filter(file => file.preview === "image"));
const releaseImage = (url: string) => { void DriveService.ClosePreview(url).catch(() => undefined); };
async function resolveImage(id: string) { return DriveService.PreviewFile(id); }
async function resolveMarkdownImage(relative: string): Promise<string | null> {
  let name: string;
  try { name = decodeURIComponent(relative); } catch { return null; }
  if (/^(?:[a-z][a-z\d+.-]*:|\/)/i.test(name) || /[\\?#\u0000-\u001f]/.test(name)) return null;
  name = name.replace(/^\.\//, "");
  if (name.includes("/")) return null;
  const image = viewer.siblings.find(file => file.preview === "image" && file.name === name && !/\.svg$/i.test(name));
  return image ? DriveService.PreviewFile(image.id) : null;
}

/* ---------- where it came from, and its neighbours ---------- */

const ROUTE_LABELS: Partial<Record<Route, string>> = {
  transfers: N_("传输中心"),
  shares: N_("我的分享"),
  assistant: N_("AI 助手"),
  cleanup: N_("清理"),
  settings: N_("设置"),
};

const from = computed(() => {
  if (viewer.returnRoute !== "files") return t(ROUTE_LABELS[viewer.returnRoute] ?? N_("文件"));
  if (ui.query.trim()) return t("搜索结果");
  if (ui.view === "starred") return t("星标");
  if (ui.view === "trash") return t("回收站");
  if (wholeDrive.value) return t("整个云盘");
  return drive.trail.at(-1)?.name ?? t("全部文件");
});

const index = computed(() => viewer.siblings.findIndex((e) => e.id === entry.value?.id));
const canPrev = computed(() => index.value > 0);
const canNext = computed(() => index.value >= 0 && index.value < viewer.siblings.length - 1);
const fileList = ref<HTMLElement>();
const navigationButton = ref<HTMLButtonElement>();
const backTitle = computed(() => inViewerWindow ? t("关闭查看窗口（Esc）") : t("返回{from}（Esc）", { from: from.value }));
watch(
  () => [viewer.entry?.id, viewer.navigation],
  async () => {
    await nextTick();
    fileList.value?.querySelector<HTMLElement>("[aria-current='true']")?.scrollIntoView({ block: "nearest" });
  },
);

function chooseFile(file: Entry) {
  if (file.id !== entry.value?.id) void openViewer(file);
  const root = fileList.value?.closest<HTMLElement>(".viewer");
  if (root && root.clientWidth <= 760) {
    toggleViewerNavigation();
    navigationButton.value?.focus();
  }
}

/* ---------- the stage ---------- */

const stage = ref<HTMLElement>();
const box = ref({ w: 0, h: 0 });
let sizer: ResizeObserver | undefined;

// What failed after the bytes arrived (the read itself failing is viewer.error).
const imageFailed = ref(false);
// The picture is decoded, or the PDF frame has loaded: the Receiving pill goes.
const arrived = ref(false);
const zoom = ref<Zoom>("fit");
const pictures = computed(() => {
  const images = viewer.images.map((image) => ({ url: image.url, name: image.name }));
  if (kind.value === "image" && viewer.url && !images.some((image) => image.url === viewer.url)) {
    images.push({ url: viewer.url, name: entry.value?.name ?? "" });
  }
  return images;
});

watch(
  () => viewer.url,
  () => {
    imageFailed.value = false;
    arrived.value = viewer.images.some((image) => image.url === viewer.url);
    zoom.value = "fit";
  },
);

// A new file starts at its top.
watch(
  () => entry.value?.id,
  async () => {
    tocOpen.value = false;
    await nextTick();
    stage.value?.scrollTo({ top: 0, left: 0 });
  },
);

/* ---------- pictures ---------- */

// Room the picture may take: the stage less its padding and the caption.
const IMAGE_PAD = 40;
const CAPTION = 40;
const fit = computed(() =>
  viewer.natural ? fitScale(viewer.natural, { w: box.value.w - 2 * IMAGE_PAD, h: box.value.h - 2 * IMAGE_PAD - CAPTION }) : 1,
);
const scale = computed(() => (zoom.value === "fit" ? fit.value : zoom.value));
const imageStyle = computed(() =>
  viewer.natural
    ? { width: `${Math.round(viewer.natural.w * scale.value)}px`, height: `${Math.round(viewer.natural.h * scale.value)}px` }
    : undefined,
);
// An SVG without its own size has no pixels to count: it only fits.
const zoomable = computed(() => kind.value === "image" && !!viewer.natural && !imageFailed.value);
const nextIn = computed(() => (zoomable.value ? zoomIn(zoom.value, fit.value) : null));
const nextOut = computed(() => (zoomable.value ? zoomOut(zoom.value, fit.value) : null));

function onImageLoad(event: Event) {
  const img = event.target as HTMLImageElement;
  if (img.getAttribute("src") !== viewer.url) return;
  rememberViewerImage(viewer.url, img.naturalWidth, img.naturalHeight);
  arrived.value = true;
}

// Zooming keeps the middle of what is on screen in the middle.
async function setZoom(next: Zoom | null) {
  if (next === null) return;
  const el = stage.value;
  const cx = el ? (el.scrollLeft + el.clientWidth / 2) / Math.max(el.scrollWidth, 1) : 0.5;
  const cy = el ? (el.scrollTop + el.clientHeight / 2) / Math.max(el.scrollHeight, 1) : 0.5;
  zoom.value = next;
  await nextTick();
  if (!el) return;
  el.scrollLeft = cx * el.scrollWidth - el.clientWidth / 2;
  el.scrollTop = cy * el.scrollHeight - el.clientHeight / 2;
}

// A click looks closer (at the picture's own size) and back.
function onImageClick() {
  if (!zoomable.value) return;
  if (zoom.value !== "fit") void setZoom("fit");
  else if (fit.value < 1) void setZoom(1);
}

/* ---------- Markdown ---------- */

const mode = ref<"read" | "source">("read");
const tocOpen = ref(false);
const doc = computed(() =>
  kind.value === "markdown" && viewer.text && viewer.text.encoding !== "unknown" ? parseMarkdown(viewer.text.text) : null,
);
// An outline only where the document has one: two headings or more.
const outline = computed(() => (doc.value?.headings ?? []).filter((h) => h.level <= 3));
const hasOutline = computed(() => outline.value.length >= 2);

function jumpTo(anchor: string) {
  const target = stage.value?.querySelector<HTMLElement>(`[data-anchor="${CSS.escape(anchor)}"]`);
  target?.scrollIntoView({ block: "start", behavior: "smooth" });
}

function followLink(href: string) {
  if (href.startsWith("#")) {
    let anchor = href.slice(1);
    try {
      anchor = decodeURIComponent(anchor);
    } catch {
      // A malformed escape is looked up as written.
    }
    jumpTo(anchor.toLowerCase());
    return;
  }
  void Browser.OpenURL(href).catch((err: unknown) =>
    notify(t("无法打开链接：{error}", { error: err instanceof Error ? err.message : String(err) }), "error"),
  );
}

/* ---------- text ---------- */

const lines = computed(() => {
  if (kind.value !== "text" || !viewer.text || viewer.text.encoding === "unknown") return [];
  const all = viewer.text.text.replace(/\r\n?/g, "\n").split("\n");
  // A file ending in a newline has no empty last line to number.
  if (all.length > 1 && all[all.length - 1] === "") all.pop();
  return all;
});
const digits = computed(() => String(lines.value.length).length);

const encodingName = computed(() => {
  const e = viewer.text?.encoding;
  return e === "utf-8" ? "UTF-8" : e === "utf-16le" ? "UTF-16 LE" : e === "utf-16be" ? "UTF-16 BE" : "";
});
const unreadable = computed(() => (kind.value === "markdown" || kind.value === "text") && viewer.text?.encoding === "unknown");

/* ---------- what the stage says ---------- */

const ready = computed(() => !viewer.loading && !viewer.error);
const unsupported = computed(() => (entry.value ? unsupportedCopy(entry.value) : null));

const caption = computed(() => {
  switch (kind.value) {
    case "image":
      return zoom.value === "fit" ? t("适合窗口 · 可放大") : t("已放大 · 滚动查看全图");
    case "pdf":
      return t("纸面阅读 · 保持原文比例");
    case "markdown":
      return mode.value === "read" ? t("阅读视图 · 可切换源码") : t("源码视图 · 同一份内容");
    case "text":
      return t("等宽阅读 · 保留换行");
    default:
      return kind.value === "unsupported" ? unsupported.value?.note ?? "" : "";
  }
});

const truncatedNote = computed(() => (viewer.text?.truncated ? t("只显示文件开头 512 KB") : ""));

/* ---------- keys ---------- */

// Hidden under the Info page (narrow) or the immersive player, the viewer
// is display:none: its keys rest, and its scroll is put back on return.
const narrow = useMedia(WORKSPACE_QUERY);
const shown = computed(() => ui.route === "viewer" && (inViewerWindow || !(narrow.value && ui.workspace === "info")));

function onKey(event: KeyboardEvent) {
  if (!shown.value || event.defaultPrevented || isTyping(event)) return;
  if (event.metaKey || event.ctrlKey || event.altKey) return;
  switch (event.key) {
    case "Escape":
      if (tocOpen.value) tocOpen.value = false;
      else if (viewer.navigation) {
        toggleViewerNavigation();
        navigationButton.value?.focus();
      }
      else closeViewer();
      break;
    case "ArrowLeft":
      if (rich.value) return;
      if (!canPrev.value) return;
      stepViewer(-1);
      break;
    case "ArrowRight":
      if (rich.value) return;
      if (!canNext.value) return;
      stepViewer(1);
      break;
    case "+":
    case "=":
      if (!nextIn.value) return;
      void setZoom(nextIn.value);
      break;
    case "-":
      if (!nextOut.value) return;
      void setZoom(nextOut.value);
      break;
    case "0":
      if (!zoomable.value || zoom.value === "fit") return;
      void setZoom("fit");
      break;
    default:
      return;
  }
  event.preventDefault();
}

let parked = { top: 0, left: 0 };
watch(
  shown,
  async (now) => {
    if (!now) {
      parked = { top: stage.value?.scrollTop ?? 0, left: stage.value?.scrollLeft ?? 0 };
      return;
    }
    await nextTick();
    stage.value?.scrollTo(parked);
  },
  { flush: "pre" },
);

// The stage comes and goes with the file (v-if): its size is watched on
// whichever element is there.
watch(stage, (el, old) => {
  if (old) sizer?.unobserve(old);
  if (el) sizer?.observe(el);
});

onMounted(() => {
  window.addEventListener("keydown", onKey);
  sizer = new ResizeObserver(([e]) => (box.value = { w: e.contentRect.width, h: e.contentRect.height }));
  if (stage.value) sizer.observe(stage.value);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKey);
  sizer?.disconnect();
});
</script>

<template>
  <section class="viewer" :class="[`kind-${kind}`, { windowed: inViewerWindow, inset: inViewerWindow && !onWindows }]" :aria-label="t('文件查看')">
    <header v-if="entry" class="head">
      <button v-if="!inViewerWindow || onWindows" class="back" :title="backTitle" :aria-label="backTitle" @click="closeViewer">
        <Icon :name="inViewerWindow ? 'close' : 'chevronLeft'" :size="18" />
      </button>
      <div class="title">
        <span class="crumb" :title="viewer.source">{{ viewer.source || from }}<i>/</i>{{ t("预览") }}</span>
        <h1 :title="entry.name">{{ entry.name }}</h1>
      </div>
      <button ref="navigationButton" class="head-action" :class="{ on: viewer.navigation }" :aria-expanded="viewer.navigation" aria-controls="viewer-files" :title="t('文件列表')" @click="toggleViewerNavigation">
        <Icon name="list" :size="17" /><span>{{ t("文件列表") }}</span>
      </button>
      <button v-if="inViewerWindow" class="head-action" :disabled="viewer.returning" :title="t('回到主窗口查看')" :aria-label="t('回到主窗口查看')" @click="returnViewerToMain">
        <Orbit v-if="viewer.returning" :size="17" /><Icon v-else name="popin" :size="17" />
      </button>
      <button v-if="!inViewerWindow" class="head-action" :disabled="viewer.detaching" :title="t('在独立窗口查看')" :aria-label="t('在独立窗口查看')" @click="popOutViewer">
        <Icon name="popout" :size="17" />
      </button>
      <div v-if="viewer.siblings.length > 1 && index >= 0" class="step" role="group" :aria-label="t('同一列表中的文件')">
        <button :disabled="!canPrev" :title="t('上一个（←）')" :aria-label="t('上一个')" @click="stepViewer(-1)">
          <Icon name="chevronLeft" :size="16" />
        </button>
        <span class="tnum">{{ index + 1 }} / {{ viewer.siblings.length }}</span>
        <button :disabled="!canNext" :title="t('下一个（→）')" :aria-label="t('下一个')" @click="stepViewer(1)">
          <Icon name="chevronRight" :size="16" />
        </button>
      </div>
    </header>

    <div v-if="entry" class="card">
      <div class="body">
        <nav v-if="viewer.navigation" id="viewer-files" ref="fileList" class="file-navigation" :aria-label="t('所在文件夹的文件')">
          <div class="navigation-head">
            <strong>{{ t("当前文件夹") }}</strong>
            <span class="tnum">{{ viewer.siblings.length }}</span>
            <button :title="t('收起文件列表')" :aria-label="t('收起文件列表')" @click="toggleViewerNavigation"><Icon name="chevronLeft" :size="15" /></button>
          </div>
          <div v-if="viewer.folderLoading" class="navigation-status" role="status"><Orbit :size="14" />{{ t("正在加载文件列表…") }}</div>
          <div v-if="viewer.folderError" class="navigation-status" role="alert">
            <span>{{ viewer.folderError }}</span><button @click="loadViewerFolder">{{ t("重试") }}</button>
          </div>
          <div class="navigation-items">
            <button
              v-for="file in viewer.siblings"
              :key="file.id"
              class="navigation-file"
              :class="{ current: file.id === entry.id, 'navigation-image': file.preview === 'image' }"
              :aria-current="file.id === entry.id ? 'true' : undefined"
              :title="file.name"
              @click="chooseFile(file)"
            >
              <span class="navigation-thumb">
                <Thumb v-if="file.preview === 'image'" :src="file.thumbnail" alt="" lazy><Icon name="image" :size="21" /></Thumb>
                <Icon v-else :name="file.preview === 'pdf' ? 'document' : 'text'" :size="21" />
              </span>
              <span class="navigation-name">{{ file.name }}</span>
            </button>
          </div>
        </nav>
        <nav v-if="kind === 'markdown' && mode === 'read' && hasOutline && tocOpen" class="toc" :aria-label="t('目录')">
          <button
            v-for="h in outline"
            :key="h.anchor"
            :class="`level-${h.level}`"
            :title="h.text"
            @click="jumpTo(h.anchor)"
          >
            {{ h.text }}
          </button>
        </nav>

        <div
          ref="stage"
          class="stage"
          :class="{ zoomed: kind === 'image' && zoom !== 'fit', rich }"
          tabindex="-1"
        >
          <div v-if="viewer.loading" class="state late">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取…") }}
          </div>

          <div v-else-if="viewer.error" class="fault" role="alert">
            <span class="mark"><Icon name="alert" :size="34" /></span>
            <h2>{{ t("内容没有读到") }}</h2>
            <p>{{ viewer.error }}</p>
            <button @click="loadViewer()">{{ t("重试") }}</button>
          </div>
          <ComicReader v-else-if="(kind === 'comic' || viewer.comic) && viewer.identity" :key="viewer.identity + ':comic'" :url="viewer.comic || entry.preview === 'comic' ? '' : viewer.url" :entry="entry" :identity="viewer.identity" :pages="viewer.comic || entry.preview === 'comic' ? comicPages : undefined" :initial-id="viewer.comic ? entry.id : undefined" :resolve-image="resolveImage" :release-image="releaseImage" />
          <BookReader v-else-if="(kind === 'book' || kind === 'epub') && viewer.url" :key="viewer.identity" :url="viewer.url" :entry="entry" :identity="viewer.identity" :format="kind === 'epub' ? 'epub' : 'text'" />
          <TableReader v-else-if="kind === 'table' && viewer.url" :key="viewer.identity" :url="viewer.url" :entry="entry" :identity="viewer.identity" :format="format as 'csv' | 'tsv' | 'xlsx'" />
          <OfficeReader v-else-if="kind === 'office' && viewer.url" :key="viewer.identity" :url="viewer.url" :entry="entry" :identity="viewer.identity" />
          <TimedTextReader v-else-if="kind === 'timed' && viewer.url" :key="viewer.identity" :url="viewer.url" :entry="entry" :identity="viewer.identity" :format="format as 'srt' | 'ass' | 'ssa' | 'vtt' | 'lrc'" />

          <!-- ---------- picture ---------- -->
          <template v-else-if="kind === 'image'">
            <div v-if="imageFailed" class="fault" role="alert">
              <span class="mark"><Icon name="image" :size="38" /></span>
              <h2>{{ t("这张图片无法显示") }}</h2>
              <p>{{ t("可能是系统不支持的格式，或文件已经损坏。文件信息在「详情」里。") }}</p>
              <button @click="loadViewer()">{{ t("重试") }}</button>
            </div>
          </template>

          <PdfReader v-else-if="kind === 'pdf' && viewer.url" :key="viewer.identity" :url="viewer.url" :entry="entry" :identity="viewer.identity" />

          <!-- ---------- text that is not UTF-8 / UTF-16 ---------- -->
          <div v-else-if="unreadable" class="fault" role="alert">
            <span class="mark"><Icon name="text" :size="36" /></span>
            <h2>{{ t("无法识别这份文本的编码") }}</h2>
            <p>{{ t("PKP 只直接显示 UTF-8 和带 BOM 的 UTF-16 文本；GBK 等编码不能可靠解码，这里不显示乱码。") }}</p>
          </div>

          <!-- ---------- Markdown ---------- -->
          <div v-else-if="kind === 'markdown' && viewer.text" class="paper-wrap">
            <article class="paper">
              <MarkdownDoc v-if="mode === 'read' && doc" :blocks="doc.blocks" :resolve-image="resolveMarkdownImage" :release-image="releaseImage" @link="followLink" />
              <pre v-else class="source">{{ viewer.text.text }}</pre>
              <p v-if="truncatedNote" class="cut">{{ truncatedNote }}</p>
            </article>
          </div>

          <!-- ---------- text and code ---------- -->
          <div v-else-if="kind === 'text' && viewer.text" class="code-wrap">
            <div class="code" :class="{ nowrap: !viewer.wrap }">
              <div class="code-head">
                <strong :title="entry.name">{{ entry.name }}</strong>
                <span>{{ t("{encoding} · {n} 行 · 只读预览", { encoding: encodingName, n: lines.length }) }}</span>
              </div>
              <ol :style="{ '--digits': digits }">
                <li v-for="(line, i) in lines" :key="i">{{ line }}</li>
              </ol>
              <p v-if="truncatedNote" class="cut">{{ truncatedNote }}</p>
            </div>
          </div>

          <!-- ---------- anything else ---------- -->
          <div v-else-if="kind === 'unsupported' && unsupported" class="fault plain">
            <span class="mark"><Icon :name="entry.category && entry.category !== 'other' ? entry.category : 'file'" :size="40" /></span>
            <span class="kicker">{{ unsupported.kicker }}</span>
            <h2>{{ unsupported.title }}</h2>
            <p>{{ unsupported.body }}</p>
            <div class="meta">
              <span :title="entry.name">{{ entry.name }}</span><i>·</i><span>{{ kindLabel(entry) }}</span>
            </div>
          </div>

          <!-- Retain decoded nodes even while another image or document is loading. -->
          <figure
            v-for="picture in pictures"
            :key="picture.url"
            v-show="kind === 'image' && !viewer.comic && ready && !imageFailed && picture.url === viewer.url"
            class="picture"
          >
            <img
              :src="picture.url"
              :alt="picture.name"
              :style="imageStyle"
              :class="{ arrived, fitted: !viewer.natural, zoomable, out: zoom !== 'fit' }"
              draggable="false"
              @load="onImageLoad"
              @error="picture.url === viewer.url && (imageFailed = true)"
              @click="onImageClick"
            />
            <figcaption v-if="arrived && zoom === 'fit'">
              {{ picture.name }}<template v-if="viewer.natural"><i>·</i><span class="tnum">{{ viewer.natural.w }} × {{ viewer.natural.h }}</span></template>
            </figcaption>
          </figure>

          <!-- Over a picture or a PDF while its bytes arrive. -->
          <div
            v-if="ready && viewer.url && !arrived && !imageFailed && kind === 'image' && !viewer.comic"
            class="streaming late"
          >
            <Orbit :size="16" :label="t('传输中')" />
            {{ t("正在接收图片…") }}
          </div>
        </div>
      </div>

      <footer class="bar">
        <div class="bar-left">
          <span class="badge">{{ viewerBadge(kind, entry.category) }}</span>
          <span class="caption">{{ caption }}</span>
        </div>

        <div class="controls">
          <template v-if="kind === 'image' && !viewer.comic">
            <button class="ctl" :disabled="!nextOut" :title="t('缩小（-）')" :aria-label="t('缩小')" @click="setZoom(nextOut)">
              <Icon name="minus" :size="16" />
            </button>
            <button
              class="ctl zoom tnum"
              :disabled="!zoomable || zoom === 'fit'"
              :title="t('适合窗口（0）')"
              @click="setZoom('fit')"
            >
              {{ zoomLabel(zoom) }}
            </button>
            <button class="ctl" :disabled="!nextIn" :title="t('放大（+）')" :aria-label="t('放大')" @click="setZoom(nextIn)">
              <Icon name="plus" :size="16" />
            </button>
          </template>


          <template v-else-if="kind === 'markdown' && doc">
            <button
              v-if="hasOutline && mode === 'read'"
              class="ctl choice"
              :class="{ on: tocOpen }"
              :aria-pressed="tocOpen"
              @click="tocOpen = !tocOpen"
            >
              <Icon name="list" :size="15" />{{ t("目录") }}
            </button>
            <div class="seg" role="group" :aria-label="t('查看方式')">
              <button class="ctl choice" :class="{ on: mode === 'read' }" :aria-pressed="mode === 'read'" @click="mode = 'read'">{{ t("阅读") }}</button>
              <button class="ctl choice" :class="{ on: mode === 'source' }" :aria-pressed="mode === 'source'" @click="mode = 'source'">{{ t("源码") }}</button>
            </div>
          </template>

          <template v-else-if="kind === 'text' && lines.length">
            <span class="note">{{ t("等宽字体") }}</span>
            <button class="ctl choice" :class="{ on: viewer.wrap }" :aria-pressed="viewer.wrap" @click="setTextWrap(!viewer.wrap)">
              {{ t("自动换行") }}
            </button>
          </template>

          <span v-else-if="kind === 'unsupported'" class="note">{{ t("没有可用的内容预览") }}</span>
        </div>
      </footer>
    </div>
  </section>
</template>

<style scoped>
/* Always the silver-blue dark of the design, whatever the theme: content
   reads against it the way a picture or a page reads in a dark room. */
.viewer {
  --fg: #edf4fa;
  --fg-dim: #a7bdcc;
  --fg-faint: #7f98ab;
  --line: rgba(207, 230, 245, 0.17);
  --line-soft: rgba(196, 228, 246, 0.1);
  --ctl: rgba(196, 228, 249, 0.09);
  --ctl-hover: rgba(196, 228, 249, 0.19);
  --pick: #a0d4ef;
  --on-pick: #15364d;
  --accent: #a9dfff;
  container: viewer / inline-size;
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 24px;
  border: 1px solid rgba(190, 221, 245, 0.14);
  color: var(--fg);
  color-scheme: dark;
  background:
    radial-gradient(ellipse 750px 500px at 22% 36%, #26425d 0%, transparent 85%),
    radial-gradient(ellipse 600px 530px at 80% 20%, #1e374e, transparent 86%),
    #0b192b;
  box-shadow: 0 24px 60px rgba(2, 10, 24, 0.4);
}

.stage.rich { padding: 0; overflow: hidden; display: flex; flex-direction: column; }
.stage.rich > :deep(*) { min-width: 0; }
.stage.rich > .state, .stage.rich > .fault { flex: 1; }

button {
  color: inherit;
}

button:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 3px;
}

i {
  font-style: normal;
}

/* ---------- header ---------- */

.head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 15px;
  min-height: 76px;
  padding: 12px 24px;
}

.back {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid rgba(207, 233, 250, 0.24);
  background: transparent;
}

.back:hover:not(:disabled) {
  background: var(--ctl-hover);
  border-color: rgba(207, 233, 250, 0.36);
}

.title {
  flex: 1;
  min-width: 0;
}

.crumb {
  display: block;
  font-size: 11px;
  letter-spacing: 0.045em;
  color: #99aec1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.crumb i {
  padding: 0 7px;
  color: #6e869c;
}

h1 {
  margin: 4px 0 0;
  font-size: 20px;
  font-weight: 640;
  letter-spacing: -0.025em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.step {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--fg-dim);
}

.step span {
  min-width: 52px;
  text-align: center;
}

.step button {
  width: 34px;
  height: 34px;
  min-height: 34px;
  padding: 0;
  border-radius: 10px;
  border: 0;
  background: var(--ctl);
}

.step button:hover:not(:disabled) {
  background: var(--ctl-hover);
}

/* ---------- the card: stage over bar ---------- */

.card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  --card-x: 16px;
  margin: 0 var(--card-x) var(--card-x);
  border: 1px solid var(--line);
  border-radius: 20px;
  overflow: hidden;
  background: #122237;
  box-shadow: inset 0 1px rgba(233, 246, 255, 0.1), 0 20px 60px rgba(3, 12, 24, 0.24);
}

.body {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
}

.head-action {
  flex: none;
  gap: 6px;
  min-width: 34px;
  padding: 0 9px;
  color: inherit;
  background: var(--ctl);
  border-color: var(--line);
}
.head-action.on { background: var(--pick); color: var(--on-pick); }
.file-navigation {
  flex: 0 0 216px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--line);
  background: #122237;
  overflow: hidden;
}
.navigation-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px;
  font-size: 12px;
}
.navigation-head strong { flex: 1; }
.navigation-head button { padding: 0; min-height: 28px; width: 28px; background: transparent; border: 0; }
.navigation-items {
  flex: 1;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding: 4px 8px 8px;
}
.navigation-file {
  width: 100%;
  min-width: 0;
  min-height: 56px;
  justify-content: flex-start;
  text-align: left;
  gap: 10px;
  border-radius: 10px;
  margin: 3px 0;
  padding: 7px;
  background: transparent;
  border: 1px solid transparent;
  transition: none;
}
.navigation-file.current { background: var(--pick); color: var(--on-pick); border-color: var(--accent); }
.navigation-file:hover:not(:disabled):not(.current) {
  background: var(--ctl);
  border-color: transparent;
}
.navigation-file:focus-visible {
  outline-offset: -2px;
}
.navigation-thumb {
  position: relative;
  flex: none;
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  overflow: hidden;
  border-radius: 6px;
  background: var(--ctl);
}
.navigation-image .navigation-thumb { width: 48px; height: 40px; }
.navigation-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  line-height: 1.5;
}
.navigation-status { display: flex; flex-wrap: wrap; gap: 6px; padding: 10px; font-size: 12px; color: var(--fg-dim); }

.stage {
  --wrap-x: 40px;
  position: relative;
  flex: 1;
  min-width: 0;
  overflow: auto;
  outline: none;
  scrollbar-color: #5e788d transparent;
}

.kind-image .stage,
.kind-unsupported .stage {
  background: radial-gradient(ellipse at 49% 52%, #29465b, #0f1e30 74%);
}

.kind-pdf .stage,
.kind-markdown .stage {
  background: radial-gradient(ellipse at 50% 20%, #344d60, #192c41 75%);
}

.kind-text .stage {
  background: radial-gradient(ellipse at 50% 30%, #23405a, #112136 72%);
}

.kind-pdf .stage {
  overflow: hidden;
}

/* ---------- waiting and failing ---------- */

.state {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  font-size: 14px;
  color: var(--fg-dim);
}

.streaming {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 16px 8px 13px;
  border-radius: 999px;
  font-size: 13px;
  color: var(--fg-dim);
  background: rgba(11, 26, 42, 0.86);
  border: 1px solid var(--line);
  pointer-events: none;
}

.fault {
  min-height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 45px;
  text-align: center;
}

.fault .mark {
  display: grid;
  place-items: center;
  width: 92px;
  height: 92px;
  border: 1px solid rgba(180, 219, 241, 0.3);
  border-radius: 25px;
  background: rgba(164, 206, 234, 0.1);
  color: #a7d1e9;
  box-shadow: inset 0 1px rgba(255, 255, 255, 0.15);
}

.fault[role="alert"] .mark {
  color: #f0c9a4;
  border-color: rgba(240, 201, 164, 0.3);
  background: rgba(240, 201, 164, 0.08);
}

.kicker {
  margin-top: 37px;
  font-size: 11px;
  letter-spacing: 0.18em;
  color: #a0c7db;
}

.fault h2 {
  margin: 13px 0 12px;
  font-size: 29px;
  letter-spacing: -0.03em;
  text-wrap: balance;
}

.fault[role="alert"] h2 {
  margin-top: 30px;
  font-size: 22px;
}

.fault p {
  max-width: 440px;
  margin: 0;
  font-size: 14px;
  line-height: 1.8;
  color: #adbfcb;
  overflow-wrap: anywhere;
  text-wrap: pretty;
}

.fault button {
  margin-top: 24px;
  min-width: 96px;
  border-color: var(--line);
  background: var(--ctl);
}

.fault button:hover:not(:disabled) {
  background: var(--ctl-hover);
}

.meta {
  display: flex;
  max-width: min(100%, 520px);
  margin-top: 32px;
  padding-top: 20px;
  border-top: 1px solid rgba(210, 234, 249, 0.18);
  font-size: 12px;
  color: #91aabd;
}

.meta span:first-child {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta span:last-child {
  flex: none;
}

.meta i {
  padding: 0 8px;
}

/* ---------- picture ---------- */

/* Grows with the picture: smaller than the stage it sits in the middle;
   larger, the stage scrolls over all of it. */
.picture {
  box-sizing: border-box;
  min-width: 100%;
  min-height: 100%;
  width: max-content;
  height: max-content;
  margin: 0;
  padding: 40px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 18px;
}

.picture img {
  display: block;
  max-width: none;
  border: 1px solid rgba(240, 249, 255, 0.25);
  border-radius: 5px;
  box-shadow: 0 32px 75px rgba(0, 9, 20, 0.5), 0 0 0 8px rgba(221, 237, 248, 0.025);
  user-select: none;
  -webkit-user-select: none;
  /* Shown whole once decoded: a progressive JPEG's banding is not
     something to watch. No fade: content never enters from transparent. */
  visibility: hidden;
}

.picture img.arrived {
  visibility: visible;
}

/* No size of its own (an SVG without one): it fits and nothing more. */
.picture img.fitted {
  width: min(calc(100cqw - 112px), 800px);
  height: auto;
}

.picture img.zoomable {
  cursor: zoom-in;
}

.picture img.zoomable.out {
  cursor: zoom-out;
}

.zoomed .picture img {
  border-radius: 0;
}

figcaption {
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #c0d4e0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

figcaption i {
  margin: 0 10px;
  color: #728da1;
}

/* ---------- PDF ---------- */

iframe {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  border: 0;
  background: #f6f5f1;
}

/* ---------- Markdown ---------- */

.paper-wrap {
  min-height: 100%;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 31px 40px 48px;
}

.paper {
  flex: none;
  width: min(100%, 760px);
  min-height: 100%;
  padding: 56px 68px 72px;
  background: #fbfaf7;
  color: #25384a;
  box-shadow: 0 22px 52px rgba(0, 10, 23, 0.26);
  color-scheme: light;
  user-select: text;
  -webkit-user-select: text;
}

.source {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 13px/1.9 ui-monospace, "SF Mono", Menlo, monospace;
  color: #344d5e;
}

.cut {
  margin: 32px 0 0;
  padding-top: 14px;
  border-top: 1px solid #d9dde0;
  font-size: 12px;
  color: #7b8e9b;
}

.paper :deep(.md) {
  font-size: 15px;
  line-height: 1.9;
  overflow-wrap: anywhere;
}

.paper :deep(.md > :first-child) {
  margin-top: 0;
}

.paper :deep(h1) {
  margin: 0 0 22px;
  font-size: 34px;
  line-height: 1.25;
  letter-spacing: -0.04em;
}

.paper :deep(h1:not(:first-child)) {
  margin-top: 44px;
}

.paper :deep(h2) {
  margin: 34px 0 12px;
  font-size: 22px;
  line-height: 1.35;
  letter-spacing: -0.025em;
}

.paper :deep(h3) {
  margin: 26px 0 10px;
  font-size: 18px;
}

.paper :deep(h4),
.paper :deep(h5),
.paper :deep(h6) {
  margin: 22px 0 8px;
  font-size: 15px;
}

.paper :deep(p) {
  margin: 0 0 15px;
}

.paper :deep(ul),
.paper :deep(ol) {
  margin: 0 0 15px;
  padding-left: 24px;
}

.paper :deep(li) {
  padding-left: 4px;
}

.paper :deep(li > ul),
.paper :deep(li > ol) {
  margin-bottom: 0;
}

.paper :deep(li.task) {
  list-style: none;
  margin-left: -20px;
}

.paper :deep(.box) {
  display: inline-block;
  width: 14px;
  height: 14px;
  margin-right: 8px;
  vertical-align: -2px;
  border: 1.5px solid #7aaac2;
  border-radius: 4px;
}

.paper :deep(.box.on) {
  background: #5e98b6 url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Cpath d='m4 8.5 2.5 2.5L12 5.5' fill='none' stroke='white' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E") center / 12px no-repeat;
  border-color: #5e98b6;
}

.paper :deep(blockquote) {
  margin: 22px 0;
  padding: 6px 0 6px 21px;
  border-left: 3px solid #7aaac2;
  color: #506c7d;
}

.paper :deep(blockquote > :last-child) {
  margin-bottom: 0;
}

.paper :deep(code) {
  padding: 1px 5px;
  border-radius: 5px;
  background: #eef0ef;
  font-size: 0.88em;
  color: #2f4a5c;
}

.paper :deep(.md-code) {
  margin: 0 0 18px;
  padding: 14px 18px;
  border-radius: 10px;
  background: #f0f1ee;
  overflow-x: auto;
  line-height: 1.7;
}

.paper :deep(.md-code code) {
  padding: 0;
  background: none;
  font-size: 13px;
}

.paper :deep(hr) {
  margin: 32px 0;
  border: 0;
  border-top: 1px solid #d6dbde;
}

.paper :deep(a) {
  color: #2f7aa6;
  text-decoration: underline;
  text-underline-offset: 3px;
}

.paper :deep(.md-link-off) {
  border-bottom: 1px dashed #9fb0bb;
}

.paper :deep(.md-image) {
  display: inline-block;
  padding: 2px 8px;
  border: 1px dashed #a9bac5;
  border-radius: 6px;
  font-size: 13px;
  color: #5d7383;
}

.paper :deep(.md-table) {
  margin: 0 0 18px;
  overflow-x: auto;
}

.paper :deep(table) {
  border-collapse: collapse;
  font-size: 14px;
  line-height: 1.6;
}

.paper :deep(th),
.paper :deep(td) {
  padding: 7px 12px;
  border: 1px solid #dde2e4;
  text-align: left;
}

.paper :deep(th) {
  background: #f1f3f2;
}

/* The outline of a long document, beside its page. */
.toc {
  flex: none;
  width: 220px;
  overflow-y: auto;
  padding: 18px 10px;
  border-right: 1px solid var(--line-soft);
  background: rgba(11, 26, 42, 0.6);
}

.toc button {
  display: block;
  width: 100%;
  min-height: 32px;
  padding: 6px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--fg-dim);
  font-size: 13px;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
}

.toc button:hover:not(:disabled) {
  background: var(--ctl);
  color: var(--fg);
}

.toc .level-1 {
  color: var(--fg);
  font-weight: 600;
}

.toc .level-3 {
  padding-left: 24px;
}

/* ---------- text and code ---------- */

/* Not a centring flex box: a sheet wider than the stage (lines unwrapped)
   would hang off its left edge, out of the scroll's reach. (Not `.sheet`:
   style.css has one for modal sheets.) */
.code-wrap {
  min-height: 100%;
  padding: 32px var(--wrap-x) 40px;
}

.code {
  width: min(100%, 900px);
  margin: 0 auto;
  border-radius: 13px;
  border: 1px solid rgba(205, 231, 247, 0.2);
  background: #122335;
  box-shadow: 0 18px 43px rgba(4, 12, 23, 0.3);
  overflow: hidden;
}

/* Unwrapped, the whole stage scrolls sideways; the header stays where it
   can be read (clip, not hidden: a hidden box would hold the sticky). */
.code.nowrap {
  width: max-content;
  min-width: min(900px, calc(100cqw - 2 * var(--card-x) - 2px - 2 * var(--wrap-x)));
  max-width: none;
  overflow: clip;
}

.code.nowrap .code-head {
  position: sticky;
  left: var(--wrap-x);
  width: calc(100cqw - 2 * var(--card-x) - 4px - 2 * var(--wrap-x));
}

.code-head {
  box-sizing: border-box;
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 26px;
  background: #17293c;
  border-bottom: 1px solid rgba(206, 232, 249, 0.12);
  font-size: 12px;
}

.code-head strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
  letter-spacing: 0.03em;
}

.code-head span {
  flex: none;
  color: #8da8bb;
}

.code ol {
  margin: 20px 24px 28px;
  padding: 0 0 0 calc(var(--digits) * 1ch + 18px);
  color: #cfe2ed;
  font: 13px/24px ui-monospace, "SF Mono", Menlo, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  user-select: text;
  -webkit-user-select: text;
}

.code.nowrap ol {
  white-space: pre;
  overflow-wrap: normal;
}

.code li {
  min-height: 24px;
  padding-left: 16px;
  border-left: 1px solid rgba(169, 204, 227, 0.14);
}

.code li::marker {
  color: #6f8da2;
  font-variant-numeric: tabular-nums;
}

.code .cut {
  margin: 0;
  padding: 14px 26px 18px;
  border-top: 1px solid rgba(206, 232, 249, 0.12);
  color: #8da8bb;
}

/* ---------- the bar ---------- */

.bar {
  flex: none;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 18px;
  min-height: 64px;
  padding: 11px 22px;
  background: rgba(11, 26, 42, 0.85);
  border-top: 1px solid rgba(209, 234, 249, 0.17);
}

.bar-left,
.controls,
.seg {
  display: flex;
  align-items: center;
  gap: 12px;
}

.bar-left {
  min-width: 0;
}

.controls {
  flex: none;
  gap: 8px;
}

.seg {
  gap: 4px;
}

.badge {
  flex: none;
  padding: 6px 10px;
  border: 1px solid rgba(163, 213, 242, 0.24);
  border-radius: 8px;
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.05em;
  color: #a9d9f3;
}

.caption {
  min-width: 0;
  font-size: 12px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.note {
  font-size: 12px;
  color: #cbdae4;
  white-space: nowrap;
}

.ctl {
  min-width: 36px;
  height: 36px;
  min-height: 36px;
  padding: 0 8px;
  border: 0;
  border-radius: 9px;
  background: var(--ctl);
  color: #d8eaf5;
  font-size: 12px;
}

.ctl:hover:not(:disabled) {
  border-color: transparent;
  background: var(--ctl-hover);
}

.ctl.zoom {
  min-width: 76px;
}

.ctl.zoom:disabled {
  opacity: 1;
  cursor: default;
  background: transparent;
}

.ctl.choice {
  gap: 6px;
  padding: 0 14px;
}

.ctl.choice.on {
  background: var(--pick);
  color: var(--on-pick);
  font-weight: 600;
}

/* ---------- narrow ---------- */

@container viewer (max-width: 760px) {
  .head-action span { display: none; }
  .file-navigation {
    position: absolute;
    z-index: 3;
    inset: 0 auto 0 0;
    width: min(260px, 80%);
    box-shadow: 12px 0 30px rgba(0, 0, 0, 0.3);
  }
  .head {
    min-height: 66px;
    padding: 10px 16px;
  }

  .card {
    --card-x: 10px;
    border-radius: 16px;
  }

  .caption,
  .toc {
    display: none;
  }

  .bar {
    padding: 9px 14px;
  }

  .paper-wrap {
    padding: 18px;
  }

  .stage {
    --wrap-x: 18px;
  }

  .paper {
    padding: 36px 34px 44px;
  }

  .picture {
    padding: 24px;
  }
}

@container viewer (max-width: 480px) {
  h1 {
    font-size: 17px;
  }

  .step span {
    display: none;
  }

  .paper {
    padding: 28px 22px 36px;
  }

  .paper :deep(h1) {
    font-size: 27px;
  }

  .stage {
    --wrap-x: 10px;
  }

  .code ol {
    margin: 14px 12px 20px;
    font-size: 11px;
  }

  .code-head span,
  .note {
    display: none;
  }

  .fault h2 {
    font-size: 22px;
  }
}

@media (prefers-contrast: more) {
  .viewer,
  .card {
    border-color: #d0ecff;
  }

  .caption,
  .crumb {
    color: #f0f9ff;
  }
}

/* Standalone viewers use the window as their frame, not another nested card. */
.viewer.windowed {
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.windowed .head {
  min-height: 72px;
  align-items: flex-start;
  padding: 10px 16px 12px;
  gap: 10px;
  --wails-draggable: drag;
}

.windowed.inset .head {
  padding-left: 96px;
}

.windowed .head button {
  --wails-draggable: no-drag;
}

.windowed .back,
.windowed .head-action {
  width: 32px;
  height: 32px;
  min-height: 32px;
  min-width: 32px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  background: transparent;
}

.windowed .head-action.on {
  background: var(--pick);
}

.windowed .head-action span {
  display: none;
}

.windowed .title {
  display: flex;
  flex-direction: column;
}

.windowed .crumb {
  line-height: 16px;
  margin-top: 2px;
}

.windowed h1 {
  order: -1;
  font-size: 16px;
  line-height: 32px;
  margin: 0;
}

.windowed .step {
  height: 32px;
}

.windowed .step button {
  width: 32px;
  height: 32px;
  min-height: 32px;
}

.windowed .card {
  --card-x: 0px;
  margin: 0;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  box-shadow: none;
}

.windowed .file-navigation {
  flex-basis: 200px;
}


.windowed.kind-text .stage {
  container-type: inline-size;
  --wrap-x: 0px;
  background: #122335;
}

.windowed .code-wrap {
  padding: 0;
}

.windowed .code {
  width: 100%;
  min-height: 100%;
  margin: 0;
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.windowed .code.nowrap {
  width: max-content;
  min-width: 100cqw;
}

.windowed .code-head,
.windowed .code.nowrap .code-head {
  position: sticky;
  top: 0;
  left: 0;
  width: 100cqw;
  padding: 10px 20px;
}

.windowed .code-head strong {
  display: none;
}

.windowed .code-head span {
  margin-left: auto;
}

.windowed .code ol {
  margin: 16px 20px 24px;
}

.windowed .paper-wrap {
  padding: 20px;
}

.windowed .paper {
  padding: 32px 40px 48px;
}

.windowed .bar {
  min-height: 48px;
  padding: 6px 16px;
  gap: 10px;
}

.windowed .badge {
  border: 0;
  padding: 4px 0;
}

.windowed .ctl {
  height: 32px;
  min-height: 32px;
}

@container viewer (max-width: 760px) {
  .windowed .paper-wrap {
    padding: 12px;
  }

  .windowed .paper {
    padding: 24px;
  }
}
</style>
