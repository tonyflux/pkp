// Shared number formatting. Each surface says something different for a
// missing value ("—" in a list, "Not provided" in the inspector, "" on a badge), so
// the label is a parameter rather than a fifth copy of the loop.
import { intlTag, t } from "./i18n";

/**
 * shownPath is a drive path from Go ("All Files / Videos") with its root named in
 * the reader's language. Go keeps the Simplified Chinese root because the AI tools and
 * the page read it as data; only what is shown changes.
 */
export function shownPath(path: string): string {
  const ROOT = "全部文件"; // i18n-ignore: Go's root segment
  return path === ROOT || path.startsWith(`${ROOT} / `) ? t("全部文件") + path.slice(ROOT.length) : path;
}

export function bytes(n: number, empty = "—", decimals = 2): string {
  if (!n) return empty;
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let value = n;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return `${value < 10 && i > 0 ? value.toFixed(decimals) : Math.round(value)} ${units[i]}`;
}

/** sizeParts splits bytes() into its figure and its unit, for a large figure beside a small unit. */
export function sizeParts(n: number, decimals = 1): { value: string; unit: string } {
  const [value, unit] = bytes(n, "0 B", decimals).split(" ");
  return { value, unit };
}

/**
 * maskLogin shows enough of a login to tell accounts apart without spelling
 * it out: an email keeps the first two characters of its name
 * and its domain, a phone number its first three and last four digits.
 */
export function maskLogin(login: string): string {
  const s = login.trim();
  if (!s) return "";
  const at = s.lastIndexOf("@");
  if (at > 0) {
    const name = [...s.slice(0, at)];
    return `${name.slice(0, name.length > 2 ? 2 : 1).join("")}••${s.slice(at)}`;
  }
  const phone = s.replace(/[\s-]/g, "");
  if (/^\+?\d{7,}$/.test(phone)) return `${phone.slice(0, 3)}••••${phone.slice(-4)}`;
  const chars = [...s];
  return chars.length <= 4 ? `${chars[0]}••` : `${chars.slice(0, 2).join("")}••${chars.slice(-2).join("")}`;
}

/**
 * share says what part of a whole `ratio` is, in whole percent. It never
 * rounds a sliver down to 0% or a nearly full disk up to 100%: both would
 * say something that is not so.
 */
export function share(ratio: number): string {
  if (!Number.isFinite(ratio) || ratio <= 0) return "0%";
  if (ratio >= 1) return "100%";
  if (ratio < 0.01) return t("不到 1%");
  return `${Math.min(99, Math.round(ratio * 100))}%`;
}

export function clock(seconds: number, empty = "0:00"): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return empty;
  const total = Math.floor(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return (h > 0 ? `${h}:` : "") + `${mm}:${String(s).padStart(2, "0")}`;
}

/** day renders a timestamp as 2026/10/08 (the locale's numeric date); an unparsable one is `empty`. */
export function day(ts: string | null | undefined, empty = "—"): string {
  const d = ts ? new Date(ts) : null;
  if (!d || Number.isNaN(d.getTime())) return empty;
  return new Intl.DateTimeFormat(intlTag(), { year: "numeric", month: "2-digit", day: "2-digit" }).format(d);
}

/** kindLabel names an entry's type the way the inspector and dock show it: "MP4 Video", "Folder", "File". */
export function kindLabel(e: { name: string; folder: boolean; category: string }): string {
  if (e.folder) return t("文件夹");
  const ext = e.name.includes(".") ? e.name.split(".").pop()!.toUpperCase() : "";
  const kind = { video: t("视频"), image: t("图片"), audio: t("音频"), document: t("文档"), archive: t("压缩包") }[e.category];
  return [ext, kind].filter(Boolean).join(" ") || t("文件");
}

/** ago says how long ago a timestamp was, coarsely, and falls back to the date after a week. */
export function ago(ts: string | null | undefined, empty = "—"): string {
  const d = ts ? new Date(ts) : null;
  if (!d || Number.isNaN(d.getTime())) return empty;
  const minutes = Math.floor((Date.now() - d.getTime()) / 60_000);
  if (minutes < 1) return t("刚刚");
  if (minutes < 60) return t("{n} 分钟前", { n: minutes });
  if (minutes < 24 * 60) return t("{n} 小时前", { n: Math.floor(minutes / 60) });
  if (minutes < 7 * 24 * 60) return t("{n} 天前", { n: Math.floor(minutes / (24 * 60)) });
  return day(ts, empty);
}

/**
 * stamp says when something happened, the way the transfer list reads it:
 * "Today 09:42", "Yesterday 21:18", "October 9 18:05" within the year, else the date.
 * Days are calendar days in local time, not 24-hour windows.
 */
export function stamp(ts: string | null | undefined, empty = "—", now = new Date()): string {
  const d = ts ? new Date(ts) : null;
  if (!d || Number.isNaN(d.getTime())) return empty;
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1).getTime();
  const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
  const at = d.getTime();
  if (at >= today && at < tomorrow) return t("今天 {time}", { time: hm });
  if (at >= yesterday && at < today) return t("昨天 {time}", { time: hm });
  if (d.getFullYear() === now.getFullYear()) {
    return `${new Intl.DateTimeFormat(intlTag(), { month: "short", day: "numeric" }).format(d)} ${hm}`;
  }
  return day(ts, empty);
}
