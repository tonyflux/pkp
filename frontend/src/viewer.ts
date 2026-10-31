// The file viewer's rules, kept apart from the
// component so they can be tested: which way a file is read, how pictures
// zoom, and what a failure says.
import { i18n, N_, t } from "./i18n";

/**
 * How the viewer reads a file. Video and audio are not here: they open in the
 * player on the existing libmpv session, never in a weaker second one.
 */
export type ViewerKind = "image" | "pdf" | "markdown" | "text" | "epub" | "book" | "comic" | "table" | "office" | "timed" | "unsupported";

const MARKDOWN_EXT: Record<string, true> = { md: true, markdown: true, mdown: true, mkd: true, mkdn: true };

export function viewerKind(entry: { name: string; preview: string; mimeType?: string }): ViewerKind {
  if (entry.preview === "image" || entry.preview === "pdf") return entry.preview;
  const ext = entry.name.split(".").pop()?.toLowerCase() ?? "";
  if (entry.preview) {
    if (ext === "epub") return "epub";
    if (ext === "txt") return "book";
    if (ext === "cbz" || entry.preview === "comic") return "comic";
    if (["csv", "tsv", "xlsx"].includes(ext)) return "table";
    if (ext === "docx") return "office";
    if (["srt", "ass", "ssa", "vtt", "lrc"].includes(ext)) return "timed";
  }
  switch (entry.preview) {
    case "image":
      return "image";
    case "pdf":
      return "pdf";
    case "text": {
      const dot = entry.name.lastIndexOf(".");
      const ext = dot > 0 ? entry.name.slice(dot + 1).toLowerCase() : "";
      return MARKDOWN_EXT[ext] || entry.mimeType === "text/markdown" ? "markdown" : "text";
    }
  }
  return "unsupported";
}

const KIND_BADGE: Record<Exclude<ViewerKind, "unsupported">, string> = {
  image: N_("图片"),
  pdf: "PDF",
  markdown: "Markdown",
  text: N_("文本"),
  epub: "EPUB",
  book: N_("阅读"),
  comic: N_("漫画"),
  table: N_("表格"),
  office: N_("文档"),
  timed: N_("字幕与歌词"),
};
const CATEGORY_BADGE: Record<string, string> = {
  video: N_("视频"),
  audio: N_("音频"),
  image: N_("图片"),
  document: N_("文档"),
  archive: N_("压缩包"),
};

/** The bar's badge: how the file is read, or for one that is not, what kind of file it is. */
export function viewerBadge(kind: ViewerKind, category: string): string {
  return t(kind === "unsupported" ? (CATEGORY_BADGE[category] ?? N_("其他")) : KIND_BADGE[kind]);
}

/* ---------- pictures ---------- */

/** "fit" follows the stage; a number is image pixels per CSS pixel. */
export type Zoom = "fit" | number;

export const ZOOM_STEPS = [0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 6, 8];
const EPS = 1e-3;

/**
 * fitScale is the scale that shows a whole picture in the box. It never
 * enlarges: a small picture stays at its own size rather than turning soft.
 */
export function fitScale(natural: { w: number; h: number }, box: { w: number; h: number }): number {
  if (natural.w <= 0 || natural.h <= 0 || box.w <= 0 || box.h <= 0) return 1;
  return Math.min(1, box.w / natural.w, box.h / natural.h);
}

/** zoomIn is the next step past what shows now; null at the largest. */
export function zoomIn(zoom: Zoom, fit: number): Zoom | null {
  const now = zoom === "fit" ? fit : zoom;
  return ZOOM_STEPS.find((s) => s > now + EPS) ?? null;
}

/** zoomOut is the step below; at or under the fitted size it is the fit itself, and null from there. */
export function zoomOut(zoom: Zoom, fit: number): Zoom | null {
  if (zoom === "fit") return null;
  const lower = [...ZOOM_STEPS].reverse().find((s) => s < zoom - EPS);
  return lower === undefined || lower <= fit + EPS ? "fit" : lower;
}

export function zoomLabel(zoom: Zoom): string {
  return zoom === "fit" ? t("适合窗口") : `${Math.round(zoom * 100)}%`;
}

/* ---------- failures ---------- */

/**
 * viewerError puts a failed read in words the reader can act on. The
 * service's own message follows only when it says something these do not
 * and is in the reader's language.
 */
export function viewerError(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err ?? "");
  const status = /upstream status (\d{3})/.exec(raw);
  if (status) return t("云盘没有返回文件内容（HTTP {status}），可以稍后再试", { status: status[1] });
  // Go's errNoDownloadLink (services/drive.go), in its source words or the language in force.
  const noLink = N_("这个文件暂时没有可用的下载地址，可能还没有处理完");
  if (raw.includes(noLink) || raw.includes(t(noLink))) return t(noLink);
  if (/file_in_recycle_bin/.test(raw)) return t("文件已移到回收站");
  if (/file_not_found|file_deleted/.test(raw)) return t("文件已不在云盘中");
  if (/timeout|deadline exceeded/i.test(raw)) return t("读取超时，网络可能较慢，可以重试");
  if (/connection refused|connection reset|no such host|network is unreachable|unexpected EOF|proxyconnect/i.test(raw)) {
    return t("网络连接中断，检查网络或代理后重试");
  }
  // Go's own errors arrive in the language in force. In Chinese, Japanese
  // or Korean anything without that script is a library's words; in English
  // the two can't be told apart, so the message is shown as it is.
  if (!raw) return t("原因未知");
  if (i18n.locale === "en" || /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u.test(raw)) return raw;
  return t("没有读到文件内容，可以稍后再试");
}

/** What the stage says about a file it cannot show (unsupported or not yet complete). */
export function unsupportedCopy(entry: { category: string; phase: string }): {
  kicker: string;
  title: string;
  body: string;
  note: string;
} {
  if (entry.phase && entry.phase !== "PHASE_TYPE_COMPLETE") {
    return {
      kicker: "NOT READY",
      title: t("文件还没有准备好"),
      body: t("云添加或上传完成后才能查看内容。文件信息和能做的操作在「详情」里。"),
      note: t("文件尚未就绪"),
    };
  }
  const kicker: Record<string, string> = {
    archive: "ARCHIVE FILE",
    document: "DOCUMENT",
    image: "IMAGE",
    video: "VIDEO",
    audio: "AUDIO",
  };
  return {
    kicker: kicker[entry.category] ?? "FILE",
    title: t("此格式暂不支持预览"),
    body: t("PKP 不会伪造它的内容。文件信息和能做的操作在「详情」里。"),
    note: entry.category === "archive" ? t("不伪造压缩包内容") : t("不伪造文件内容"),
  };
}
