/*
 * Naming template: where a recognised video lands and what it is
 * called, from {title} {year} {season} {episode} {version}. `/` separates
 * folder levels, `<…>` is a segment dropped whenever a variable in it is
 * empty, `{season:02}` pads, and a bare {episode} pads to the series' widest
 * episode number (at least 2). The extension is the caller's: templates never
 * write it.
 */
import { t } from "./i18n";
import { nameError } from "./organizer";

export type NamingStyle = "plex" | "chinese" | "custom";
export type TemplateKind = "series" | "movie";

/**
 * Naming is one style's templates. Specials (SP, OVA, theatrical release) render `special`
 * with season 0: Plex and custom reuse the series template (Season 00 /
 * S00E01), Chinese has its own Specials folder.
 */
export interface Naming {
  style: NamingStyle;
  series: string;
  movie: string;
  special: string;
}

export const PRESETS: Readonly<Record<"plex" | "chinese", Naming>> = {
  plex: {
    style: "plex",
    series: "{title}< ({year})>/Season {season:02}/{title} - S{season:02}E{episode}< - {version}>",
    movie: "{title}< ({year})>/{title}< ({year})>< - {version}>",
    special: "{title}< ({year})>/Season {season:02}/{title} - S{season:02}E{episode}< - {version}>",
  },
  chinese: {
    style: "chinese",
    series: "{title}/第{season}季/{title} · 第{episode}集< ({version})>", // i18n-ignore: names made on the drive
    movie: "{title}< ({year})>/{title}< ({year})>< ({version})>",
    special: "{title}/特别篇/{title} · SP{episode}< ({version})>", // i18n-ignore: names made on the drive
  },
};

/** A level count past this is a template mistake, not a library layout. */
export const MAX_LEVELS = 4;

export interface TemplateValues {
  title?: string;
  year?: number | string | null;
  season?: number | null;
  episode?: number | null;
  version?: string;
  /** Digits a bare {episode} pads to: the series' widest episode number. Never below 2. */
  episodeWidth?: number;
}

/** The sample the settings preview and templateError render. */
export const SAMPLE: Readonly<TemplateValues> = {
  get title() {
    return t("山海纪行");
  },
  year: 2023,
  season: 1,
  episode: 1,
  version: "2160p",
};

type Variable = "title" | "year" | "season" | "episode" | "version";
const VARIABLES: Record<string, true> = { title: true, year: true, season: true, episode: true, version: true };

type Part = { text: string } | { variable: Variable; width: number } | { optional: Part[] };

/** parse reads a template into parts, or says what is wrong with it. */
function parse(template: string): { parts: Part[]; error: string } {
  const parts: Part[] = [];
  let optional: Part[] | null = null;
  const fail = (error: string) => ({ parts: [], error });
  for (let i = 0; i < template.length; i++) {
    const ch = template[i];
    const into = optional ?? parts;
    if (ch === "{") {
      const end = template.indexOf("}", i);
      if (end < 0) return fail(t("「{」没有对应的「}」"));
      const body = template.slice(i + 1, end);
      const m = /^([a-z]+)(?::(\d{1,2}))?$/.exec(body);
      if (!m) return fail(t("「{token}」写法不对", { token: `{${body}}` }));
      if (!VARIABLES[m[1]]) return fail(t("没有变量 {token}，可用 {title} {year} {season} {episode} {version}", { token: `{${m[1]}}` }));
      const width = m[2] === undefined ? 0 : Number(m[2]);
      if (m[2] !== undefined && m[1] !== "season" && m[1] !== "episode") return fail(t("只有 {season} 和 {episode} 可以补零"));
      if (m[2] !== undefined && (width < 1 || width > 6)) return fail(t("「{token}」的位数要在 1 到 6 之间", { token: `{${body}}` }));
      into.push({ variable: m[1] as Variable, width });
      i = end;
    } else if (ch === "}") return fail(t("「}」没有对应的「{」"));
    else if (ch === "<") {
      if (optional) return fail(t("可选段 <…> 不能嵌套"));
      optional = [];
    } else if (ch === ">") {
      if (!optional) return fail(t("「>」没有对应的「<」"));
      parts.push({ optional });
      optional = null;
    } else if (ch === "/" && optional) return fail(t("可选段 <…> 里不能有 /"));
    else {
      const last = into[into.length - 1];
      if (last && "text" in last) last.text += ch;
      else into.push({ text: ch });
    }
  }
  if (optional) return fail(t("「<」没有对应的「>」"));
  return { parts, error: "" };
}

/**
 * sanitizeValue makes a value (a title from a model, a version tag) safe
 * inside a name: characters no folder can hold become spaces, runs of space
 * collapse, and it never ends in a space or a dot.
 */
export function sanitizeValue(text: string): string {
  return text
    .replace(/[\\/:*?"<>|\x00-\x1F]/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/[.\s]+$/, "");
}

function valueOf(v: Variable, width: number, values: TemplateValues): string {
  switch (v) {
    case "title":
      return sanitizeValue(values.title ?? "");
    case "version":
      return sanitizeValue(values.version ?? "");
    case "year":
      return values.year === null || values.year === undefined ? "" : sanitizeValue(String(values.year));
    case "season":
    case "episode": {
      const n = values[v];
      if (n === null || n === undefined || !Number.isFinite(n) || n < 0) return "";
      const pad = width || (v === "episode" ? Math.max(2, values.episodeWidth ?? 2) : 0);
      return String(Math.floor(n)).padStart(pad, "0");
    }
  }
}

function renderParts(parts: readonly Part[], values: TemplateValues): string {
  let out = "";
  for (const p of parts) {
    if ("text" in p) out += p.text;
    else if ("variable" in p) out += valueOf(p.variable, p.width, values);
    else {
      const empty = p.optional.some((q) => "variable" in q && !valueOf(q.variable, q.width, values));
      if (!empty) out += renderParts(p.optional, values);
    }
  }
  return out;
}

/**
 * renderTemplate is the template filled in, one string per level: the
 * folders, then the name without its extension. [] when the template does
 * not parse (templateError says why).
 */
export function renderTemplate(template: string, values: TemplateValues): string[] {
  const { parts, error } = parse(template);
  if (error) return [];
  return renderParts(parts, values).split("/");
}

/**
 * templateError is why a custom template cannot be saved, "" when it can:
 * a series one names the episode; only known variables and
 * widths; at most MAX_LEVELS levels; and the sample — with and without year
 * and version — renders levels a folder can hold.
 */
export function templateError(template: string, kind: TemplateKind): string {
  if (!template.trim()) return t("请填写模板");
  const { parts, error } = parse(template);
  if (error) return error;
  const uses = (v: Variable, list: readonly Part[]): boolean =>
    list.some((p) => ("variable" in p && p.variable === v) || ("optional" in p && uses(v, p.optional)));
  if (kind === "series" && !uses("episode", parts)) return t("剧集模板必须包含 {episode}");
  if (template.split("/").length > MAX_LEVELS) return t("最多 {n} 层（文件夹加文件名）", { n: MAX_LEVELS });
  const base: TemplateValues = kind === "series" ? SAMPLE : { ...SAMPLE, season: null, episode: null };
  for (const values of [base, { ...base, year: null, version: "" }]) {
    for (const level of renderParts(parts, values).split("/")) {
      if (!level.trim()) return t("每一层都不能为空，检查多余的 / 或只有可选段的层");
      const bad = nameError(level);
      if (bad) return t("「{name}」：{error}", { name: level, error: bad });
    }
  }
  return "";
}

/**
 * namingFor is the style the settings hold. A custom template that no longer
 * passes templateError (settings.json edited by hand) falls back to the Plex
 * one of its kind rather than producing names nobody checked.
 */
export function namingFor(style: string, custom?: { series: string; movie: string }): Naming {
  if (style === "chinese") return PRESETS.chinese;
  if (style !== "custom" || !custom) return PRESETS.plex;
  const series = templateError(custom.series, "series") ? PRESETS.plex.series : custom.series;
  const movie = templateError(custom.movie, "movie") ? PRESETS.plex.movie : custom.movie;
  return { style: "custom", series, movie, special: series };
}

/**
 * previewPath is the settings preview: the sample without a year (guessed
 * years are off by default), levels joined by " / ", "" when it does not
 * parse. "Shanhai Journey / Season 01 / Shanhai Journey - S01E01 - 2160p.mkv".
 */
export function previewPath(template: string, kind: TemplateKind, ext = ".mkv"): string {
  const values: TemplateValues = { ...SAMPLE, year: null };
  if (kind === "movie") {
    values.season = null;
    values.episode = null;
  }
  const levels = renderTemplate(template, values);
  return levels.length ? `${levels.join(" / ")}${ext}` : "";
}
