/*
 * Media Organization: the code's half. groupMedia sorts the files of a
 * scope into candidate groups by name shape; each group is then identified —
 * by the file-name rules (parse.ts), a TMDB exact match, or, for what the
 * rules cannot tell, the model (aiprompt.ts)
 * — and buildPlan turns those identities into a Change list named by the
 * user's templates. Nobody names a file or a folder directly: an identity
 * says what a group is; everything else is decided here, deterministically.
 */
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "./i18n";
import { nameKey, type Change, type Role, type Tree } from "./organizer";
import { renderTemplate, sanitizeValue, type Naming } from "./templates";

/* ---------------- files ---------------- */

export type FileClass = "video" | "subtitle" | "audio" | "other";

const VIDEO: Record<string, true> = Object.fromEntries(
  "mp4 mkv avi mov wmv flv webm m4v ts m2ts mts rmvb rm mpg mpeg 3gp vob ogv f4v divx asf".split(" ").map((e) => [e, true]),
);
const SUBTITLE: Record<string, true> = Object.fromEntries("srt ass ssa vtt sub idx sup".split(" ").map((e) => [e, true]));
const AUDIO: Record<string, true> = Object.fromEntries(
  "mp3 flac aac m4a wav ogg opus ape wma dts ac3 eac3 mka alac aiff".split(" ").map((e) => [e, true]),
);

/** splitName parts a name into what comes before its extension and the extension with its dot. */
export function splitName(name: string): { stem: string; ext: string } {
  const dot = name.lastIndexOf(".");
  if (dot <= 0 || !/^[A-Za-z0-9]{1,8}$/.test(name.slice(dot + 1))) return { stem: name, ext: "" };
  return { stem: name.slice(0, dot), ext: name.slice(dot) };
}

/** classify by extension first (a subtitle is "other" to the drive), the drive's category second. */
export function classify(e: Pick<Entry, "name" | "category">): FileClass {
  const ext = splitName(e.name).ext.slice(1).toLowerCase();
  if (SUBTITLE[ext]) return "subtitle";
  if (VIDEO[ext] || e.category === "video") return "video";
  if (AUDIO[ext] || e.category === "audio") return "audio";
  return "other";
}

/* ---------------- Coarse Grouping ---------------- */

export interface MediaFile {
  id: string;
  name: string;
  /** The folder it is in, as names below the scope. */
  dir: string[];
  /** Lower case, no dot. */
  ext: string;
  size: number;
  modified: string;
  /** The group's variables for this file, n1 first. */
  numbers: number[];
}

export interface MediaVariable {
  /** "n1", "n2", … as the pattern shows them. */
  name: string;
  min: number;
  max: number;
  /** How many distinct values. */
  distinct: number;
}

export interface MediaGroup {
  /** "g1", "g2", … */
  id: string;
  kind: FileClass;
  /** The names with their varying numbers as {n1}…: "[Group] Chronicles of Mountains and Seas - {n1} [1080p].mkv". */
  pattern: string;
  files: MediaFile[];
  dirs: string[][];
  /** Extension → file count. */
  exts: Record<string, number>;
  size: { min: number; max: number };
  variables: MediaVariable[];
  /** The few files whose names the model sees: first, last and the odd ones. */
  samples: MediaFile[];
}

interface Raw {
  file: MediaFile;
  kind: FileClass;
  texts: string[];
  runs: string[];
}

/** Samples per group: enough to show the naming and its exceptions; fewer when there are many groups. */
const SAMPLES = 4;
const SAMPLES_CROWDED = 2;
const CROWDED = 150;

function natural(a: string, b: string): number {
  return a.localeCompare(b, undefined, { numeric: true });
}

/**
 * groupMedia sorts the files of the tree — or, with `only`, those ids and
 * everything below the folders among them (Find Files → Hand to Organization) — into
 * candidate groups: the same file class, the same folder and the same name
 * once every digit run is "#". The folder is part of the key because one
 * identity is answered per group: Season1/[Series][01].mkv and
 * Season2/[Series][01].mkv share a shape but not a season. Single-file groups in
 * one folder whose names share the text before their first number are one
 * group, so episode-titled releases ("Series Title - 01 - Episode Title") do not become one
 * group per episode.
 */
export function groupMedia(tree: Tree, only?: ReadonlySet<string>): MediaGroup[] {
  const raws: Raw[] = [];
  for (const e of tree.byId.values()) {
    if (e.folder) continue;
    if (only && !only.has(e.id) && !tree.ancestorsOf(e.id).some((a) => only.has(a))) continue;
    const { stem, ext } = splitName(e.name);
    const flat = stem.normalize("NFKC");
    const runs = flat.match(/\d+/g) ?? [];
    raws.push({
      file: {
        id: e.id,
        name: e.name,
        dir: tree.pathOf(e.parentId),
        ext: ext.slice(1).toLowerCase(),
        size: e.size || 0,
        modified: e.modifiedTime,
        numbers: [],
      },
      kind: classify(e),
      texts: flat.split(/\d+/),
      runs,
    });
  }
  raws.sort((a, b) => natural(a.file.dir.join("/"), b.file.dir.join("/")) || natural(a.file.name, b.file.name));

  const byShape = new Map<string, Raw[]>();
  for (const r of raws) {
    const key = `${r.kind}\u0000${r.file.dir.join("/")}\u0000${r.texts.join("#")}`;
    const list = byShape.get(key);
    if (list) list.push(r);
    else byShape.set(key, [r]);
  }
  const buckets: Raw[][] = [];
  const singles = new Map<string, Raw[]>();
  for (const list of byShape.values()) {
    const r = list[0];
    const prefix = r.texts[0].trim();
    if (list.length > 1 || !r.runs.length || prefix.length < 2) {
      buckets.push(list);
      continue;
    }
    const key = `${r.kind}\u0000${r.file.dir.join("/")}\u0000${nameKey(r.texts[0])}`;
    const same = singles.get(key);
    if (same) same.push(r);
    else singles.set(key, [r]);
  }
  for (const list of singles.values()) buckets.push(list);
  // Groups in the order their first file comes in the scope.
  const order = new Map(raws.map((r, i) => [r.file.id, i]));
  buckets.sort((a, b) => order.get(a[0].file.id)! - order.get(b[0].file.id)!);

  const per = buckets.length > CROWDED ? SAMPLES_CROWDED : SAMPLES;
  return buckets.map((list, i) => describe(`g${i + 1}`, list, per));
}

function describe(id: string, list: Raw[], samplesPer: number): MediaGroup {
  const k = Math.min(...list.map((r) => r.runs.length));
  const varying: number[] = [];
  for (let p = 0; p < k; p++) {
    if (new Set(list.map((r) => Number(r.runs[p]))).size > 1) varying.push(p);
  }
  const files = list.map((r) => ({ ...r.file, numbers: varying.map((p) => Number(r.runs[p])) }));

  // The pattern keeps what every name shares; "…" stands for text that differs.
  const first = list[0];
  let pattern = "";
  for (let p = 0; p <= k; p++) {
    const text = first.texts[p];
    const tail = p === k;
    const same = list.every((r) =>
      tail ? r.texts.slice(p).join("#") === first.texts.slice(p).join("#") && r.runs.length === first.runs.length : r.texts[p] === text,
    );
    pattern += same ? (tail ? interleave(first.texts.slice(p), first.runs.slice(p)) : text) : "…";
    if (!tail) {
      const v = varying.indexOf(p);
      pattern += v >= 0 ? `{n${v + 1}}` : first.runs[p];
    }
  }
  pattern = pattern.replace(/…+/g, "…");

  const exts: Record<string, number> = {};
  for (const f of files) exts[f.ext] = (exts[f.ext] ?? 0) + 1;
  const extList = Object.keys(exts);
  if (extList.length === 1) pattern += extList[0] ? `.${splitName(first.file.name).ext.slice(1)}` : "";
  else pattern += `.[${extList.join("|")}]`;

  const dirs: string[][] = [];
  const seen = new Set<string>();
  for (const f of files) {
    const key = f.dir.join("/");
    if (!seen.has(key)) {
      seen.add(key);
      dirs.push(f.dir);
    }
  }
  const sizes = files.map((f) => f.size);
  const variables = varying.map((_, v) => {
    const values = files.map((f) => f.numbers[v]);
    return { name: `n${v + 1}`, min: Math.min(...values), max: Math.max(...values), distinct: new Set(values).size };
  });
  return {
    id,
    kind: list[0].kind,
    pattern,
    files,
    dirs,
    exts,
    size: { min: Math.min(...sizes), max: Math.max(...sizes) },
    variables,
    samples: samplesOf(files, samplesPer),
  };
}

function interleave(texts: readonly string[], runs: readonly string[]): string {
  return texts.map((t, i) => t + (runs[i] ?? "")).join("");
}

/**
 * samplesOf picks what tells the model most: the first and last by number,
 * then the odd ones out — another extension, another folder, a size far
 * from the rest.
 */
function samplesOf(files: readonly MediaFile[], cap: number): MediaFile[] {
  if (files.length <= cap) return files.slice();
  const byNumber = files
    .slice()
    .sort((a, b) => (a.numbers[0] ?? 0) - (b.numbers[0] ?? 0) || natural(a.name, b.name));
  const count = <T>(values: T[]) => {
    const m = new Map<T, number>();
    for (const v of values) m.set(v, (m.get(v) ?? 0) + 1);
    return m;
  };
  const extCount = count(files.map((f) => f.ext));
  const dirCount = count(files.map((f) => f.dir.join("/")));
  const sizes = files.map((f) => f.size).sort((a, b) => a - b);
  const median = sizes[Math.floor(sizes.length / 2)];
  const oddness = (f: MediaFile) =>
    (extCount.get(f.ext)! * 2 < files.length ? 1 : 0) +
    (dirCount.get(f.dir.join("/"))! * 2 < files.length ? 1 : 0) +
    (median > 0 && (f.size * 3 < median || f.size > median * 3) ? 1 : 0);
  const picked = [byNumber[0], byNumber[byNumber.length - 1]];
  const odd = byNumber.filter((f) => oddness(f) > 0).sort((a, b) => oddness(b) - oddness(a));
  for (const f of odd) {
    if (picked.length >= cap) break;
    if (!picked.includes(f)) picked.push(f);
  }
  return byNumber.filter((f) => picked.includes(f));
}

/* ---------------- what the model says ---------------- */

export type IdentityType = "series" | "movie" | "special" | "subtitle" | "junk" | "unknown";
export type NumberRole = "episode" | "season" | "other";

/** Where an identity came from: the file-name rules, a TMDB exact match, the model. */
export type IdentitySource = "rules" | "tmdb" | "model";

/** Identity is what one group is, already validated (parse.ts, aiprompt.ts parseIdentities). */
export interface Identity {
  /** The group, "g1". */
  id: string;
  type: IdentityType;
  title: string;
  year: number | null;
  /** "filename" and "tmdb" years are confirmed; a "guess" is used only once the user adopts it. */
  yearSource: "filename" | "tmdb" | "guess" | "none";
  source: IdentitySource;
  season: number | null;
  /** What each of the group's variables is, n1 first. */
  numbers: NumberRole[];
  /** The episode when no variable is the episode (a group of one). */
  episode: number | null;
  /** Subtitle language as the model reads it ("chs"); the file name's own tag wins. */
  language: string;
  /** A top folder the user asked for ("Anime"); "" for none. */
  folder: string;
  /** Why it is junk or unknown. */
  reason: string;
}

/* ---------------- names ---------------- */

const LANGUAGES: Record<string, true> = Object.fromEntries(
  (
    "chs cht sc tc zh zho chi zh-cn zh-tw zh-hk zh-hans zh-hant gb big5 eng en jpn ja jp kor ko kr " +
    // i18n-ignore
    "简体 繁体 简 繁 简中 繁中 中文 中字 简日 繁日 简英 繁英 中英 中日 双语 简繁"
  )
    .split(" ")
    .map((tag) => [tag, true]),
);

function isLanguage(token: string): boolean {
  const t = token.trim().toLowerCase();
  if (LANGUAGES[t]) return true;
  const parts = t.split(/[&+_]/);
  return parts.length > 1 && parts.every((p) => LANGUAGES[p]);
}

/**
 * languageTag is a subtitle's language as its name writes it: the last dot
 * segment before the extension (".chs.ass") or a bracketed tag ("[Simplified]");
 * "" when there is none. Latin tags come back lower case.
 */
export function languageTag(name: string): string {
  const { stem } = splitName(name);
  const dot = stem.lastIndexOf(".");
  if (dot >= 0 && isLanguage(stem.slice(dot + 1))) return stem.slice(dot + 1).trim().toLowerCase();
  const brackets = stem.match(/[[(（【][^\])）】]+[\])）】]/g) ?? [];
  for (let i = brackets.length - 1; i >= 0; i--) {
    const inner = brackets[i].slice(1, -1);
    if (isLanguage(inner)) return inner.trim().toLowerCase();
  }
  return "";
}

const RESOLUTION = /(^|[^a-z0-9])(2160p|4k|uhd|1440p|1080p|1080i|720p|576p|480p)(?![a-z0-9])/;
const RESOLUTION_LABEL: Record<string, string> = { "4k": "2160p", uhd: "2160p", "1080i": "1080p" };
const MARKS: [RegExp, string][] = [
  [/(^|[^a-z0-9])remux(?![a-z0-9])/, "REMUX"],
  [/(^|[^a-z0-9])(blu-?ray|bdrip|bd)(?![a-z0-9])/, "BluRay"],
  [/(^|[^a-z0-9])web-?dl(?![a-z0-9])/, "WEB-DL"],
  [/(^|[^a-z0-9])web-?rip(?![a-z0-9])/, "WEBRip"],
  [/(^|[^a-z0-9])hdr(10|10plus)?(?![a-z0-9])/, "HDR"],
  [/(^|[^a-z0-9])(dv|dovi|dolby[ ._-]?vision)(?![a-z0-9])/, "DV"],
];

/** versionTag is what a file name says about its version: "2160p HDR", "1080p WEB-DL", "" for nothing. */
export function versionTag(name: string): string {
  const text = splitName(name).stem.normalize("NFKC").toLowerCase();
  const tags: string[] = [];
  const res = RESOLUTION.exec(text);
  if (res) tags.push(RESOLUTION_LABEL[res[2]] ?? res[2]);
  for (const [re, label] of MARKS) if (re.test(text)) tags.push(label);
  return tags.join(" ");
}

/** Titles that differ only in spacing, punctuation, width or case are one work. */
export function titleKey(title: string): string {
  return title.normalize("NFKC").toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, "") || title.toLowerCase();
}

/* ---------------- the plan ---------------- */

export interface PlanOptions {
  naming: Naming;
  /** MediaResolutions for resolutionIds: id → "2160p". */
  resolutions?: ReadonlyMap<string, string>;
  /** Series keys whose guessed year the user adopted (Add Guessed Year). */
  years?: ReadonlySet<string>;
  /** Why a group without an identity stays: default "No identification result"; "Token limit reached, not identified" after the limit stopped it. */
  missing?: string;
}

export interface PlanSeries {
  key: string;
  title: string;
  type: "series" | "movie";
  /** The year in the plan's names; null for none. */
  year: number | null;
  /** That year is the model's guess (adopted through opts.years). */
  yearGuessed: boolean;
  /** Where the series' identity came from; "tmdb" when any of its groups was matched there, so the plan can say 「Data from TMDB」. */
  source: IdentitySource;
  /** A guessed year the user may adopt; null when the names carry a year or there is no guess. */
  guess: number | null;
  /** The top folder the user asked for, "" for none. */
  folder: string;
  /** Source folders its files come from. */
  dirs: number;
  /** Changes in the plan for it. */
  items: number;
}

export interface MediaPlan {
  /** Moves for everything recognised, junk to trash (unticked), then source folders to trash when emptied. */
  changes: Change[];
  /** What stays as it is, locked with the reason. */
  kept: Change[];
  series: PlanSeries[];
  /** Videos that share an episode (or a movie) with another: ask MediaResolutions, then build again. */
  resolutionIds: string[];
}

interface Unit {
  file: MediaFile;
  entry: Entry;
  role: Role;
  title: string;
  movie: boolean;
  season: number;
  episode: number | null;
  year: number | null;
  yearConfirmed: boolean;
  source: IdentitySource;
  folder: string[];
  language: string;
  key: string;
  /** Filled while naming. */
  toPath: string[];
  stem: string;
}

interface Work {
  key: string;
  movie: boolean;
  titles: Map<string, number>;
  confirmed: Map<number, number>;
  guessed: Map<number, number>;
  folder: string[];
  sources: Set<IdentitySource>;
  dirs: Set<string>;
  units: Unit[];
}

/** The source a work is shown with when its groups came from several: TMDB data wins. */
const SOURCE_RANK: readonly IdentitySource[] = ["tmdb", "model", "rules"];

function mostCommon<T>(counts: ReadonlyMap<T, number>): T | null {
  let best: T | null = null;
  let n = 0;
  for (const [value, count] of counts) {
    if (count > n) {
      best = value;
      n = count;
    }
  }
  return best;
}

function bump<T>(counts: Map<T, number>, value: T, by = 1) {
  counts.set(value, (counts.get(value) ?? 0) + by);
}

function keepChange(entry: Entry, from: string[], role: Role, reason: string): Change {
  return {
    item: entry,
    from,
    op: "move",
    toPath: from.slice(),
    name: entry.name,
    proposed: entry.name,
    selected: false,
    locked: reason,
    note: "",
    series: "",
    role,
  };
}

/**
 * buildPlan names what the groups were identified as: every
 * series under one {title} however many groups and folders it came from;
 * every version of an episode kept with its own {version}; years from file
 * names and TMDB used, guessed ones only for series in opts.years; subtitles named
 * after the video they belong to; junk proposed for the trash unticked;
 * source folders that end up empty proposed for the trash ticked.
 */
export function buildPlan(
  tree: Tree,
  groups: readonly MediaGroup[],
  identities: ReadonlyMap<string, Identity> | readonly Identity[],
  opts: PlanOptions,
): MediaPlan {
  const ids: ReadonlyMap<string, Identity> =
    identities instanceof Map ? identities : new Map((identities as readonly Identity[]).map((i) => [i.id, i]));
  const kept: Change[] = [];
  const trash: Change[] = [];
  const units: Unit[] = [];

  for (const g of groups) {
    const who = ids.get(g.id);
    const seasonVar = who ? who.numbers.indexOf("season") : -1;
    const episodeVar = who ? who.numbers.indexOf("episode") : -1;
    const yearDigits = who?.year ? String(who.year) : "";
    const yearInNames =
      !!yearDigits && g.files.some((f) => f.name.includes(yearDigits) || f.dir.some((d) => d.includes(yearDigits)));
    for (const f of g.files) {
      const entry = tree.byId.get(f.id);
      if (!entry) continue;
      const role: Role = g.kind;
      const from = f.dir;
      if (!who) {
        kept.push(keepChange(entry, from, role, opts.missing || t("没有得到识别结果")));
        continue;
      }
      if (who.type === "junk") {
        trash.push({
          item: entry,
          from,
          op: "trash",
          toPath: from.slice(),
          name: entry.name,
          proposed: entry.name,
          selected: false,
          locked: "",
          note: who.reason || t("残留文件"),
          series: "",
          role,
        });
        continue;
      }
      if (who.type === "unknown") {
        kept.push(keepChange(entry, from, role, who.reason || t("无法识别")));
        continue;
      }
      if (role === "other") {
        kept.push(keepChange(entry, from, role, t("不是视频、音频或字幕")));
        continue;
      }
      if (who.type === "subtitle" && role !== "subtitle") {
        kept.push(keepChange(entry, from, role, t("识别结果与文件类型不符")));
        continue;
      }
      const title = sanitizeValue(who.title);
      if (!title) {
        kept.push(keepChange(entry, from, role, t("没有识别出片名")));
        continue;
      }
      const special = who.type === "special";
      const episode = episodeVar >= 0 ? f.numbers[episodeVar] : who.episode;
      const movie = who.type === "movie" || (who.type === "subtitle" && episode === null && !special);
      if (!movie && !special && episode === null) {
        kept.push(keepChange(entry, from, role, t("没有识别出集数")));
        continue;
      }
      let season = seasonVar >= 0 ? f.numbers[seasonVar] : who.season ?? 1;
      if (special) season = 0;
      const folder = who.folder
        .split("/")
        .map(sanitizeValue)
        .filter(Boolean)
        .slice(0, 2);
      const year = who.yearSource === "none" ? null : who.year;
      units.push({
        file: f,
        entry,
        role,
        title,
        movie,
        season,
        episode: movie ? null : episode,
        year,
        // A TMDB year is the work's own; a file-name year only counts where the names really carry it.
        yearConfirmed: year !== null && (who.yearSource === "tmdb" || (who.yearSource === "filename" && yearInNames)),
        source: who.source,
        folder,
        language: role === "subtitle" || role === "audio" ? languageTag(f.name) || sanitizeValue(who.language) : "",
        key: "",
        toPath: [],
        stem: "",
      });
    }
  }

  // Works: a series by its title; a movie by title and year, where one
  // without a year joins the only same-title movie that has one.
  const movieYears = new Map<string, Set<number>>();
  for (const u of units) {
    if (!u.movie || u.year === null) continue;
    const set = movieYears.get(titleKey(u.title)) ?? new Set<number>();
    set.add(u.year);
    movieYears.set(titleKey(u.title), set);
  }
  const works = new Map<string, Work>();
  for (const u of units) {
    const t = titleKey(u.title);
    if (!u.movie) u.key = `${t}|series`;
    else {
      const years = movieYears.get(t);
      const year = u.year ?? (years && years.size === 1 ? [...years][0] : null);
      u.key = `${t}|movie|${year ?? ""}`;
    }
    let w = works.get(u.key);
    if (!w) {
      w = {
        key: u.key,
        movie: u.movie,
        titles: new Map(),
        confirmed: new Map(),
        guessed: new Map(),
        folder: [],
        sources: new Set(),
        dirs: new Set(),
        units: [],
      };
      works.set(u.key, w);
    }
    w.units.push(u);
    bump(w.titles, u.title);
    if (u.year !== null) bump(u.yearConfirmed ? w.confirmed : w.guessed, u.year);
    if (!w.folder.length && u.folder.length) w.folder = u.folder;
    w.dirs.add(u.file.dir.join("/"));
    w.sources.add(u.source);
  }

  const changes: Change[] = [];
  const series: PlanSeries[] = [];
  const resolutionIds: string[] = [];
  for (const w of works.values()) {
    const title = mostCommon(w.titles)!;
    const confirmed = mostCommon(w.confirmed);
    const guess = confirmed === null ? mostCommon(w.guessed) : null;
    const adopted = guess !== null && !!opts.years?.has(w.key);
    const year = confirmed ?? (adopted ? guess : null);

    // Specials without a number follow the numbered ones, in name order.
    if (!w.movie) {
      const numbered = w.units.filter((u) => u.season === 0 && u.episode !== null).map((u) => u.episode!);
      let next = numbered.length ? Math.max(...numbered) + 1 : 1;
      const loose = w.units.filter((u) => u.season === 0 && u.episode === null);
      loose.sort((a, b) => natural(a.file.name, b.file.name));
      for (const u of loose) u.episode = next++;
    }
    const widest = Math.max(0, ...w.units.map((u) => u.episode ?? 0));
    const episodeWidth = Math.max(2, String(widest).length);

    const slots = new Map<string, Unit[]>();
    for (const u of w.units) {
      const slot = w.movie ? "" : `${u.season}|${u.episode}`;
      const list = slots.get(slot);
      if (list) list.push(u);
      else slots.set(slot, [u]);
    }
    for (const list of slots.values()) {
      const videos = list
        .filter((u) => u.role === "video")
        .sort((a, b) => b.file.size - a.file.size || natural(a.file.name, b.file.name));
      const versions = new Map<Unit, string>();
      if (videos.length > 1) {
        const used = new Map<string, number>();
        videos.forEach((u, i) => {
          resolutionIds.push(u.file.id);
          const label = opts.resolutions?.get(u.file.id) || versionTag(u.file.name) || `(${i + 1})`;
          const n = (used.get(label) ?? 0) + 1;
          used.set(label, n);
          versions.set(u, n > 1 ? `${label} (${n})` : label);
        });
      }
      const render = (u: Unit, version: string) => {
        const template = w.movie ? opts.naming.movie : u.season === 0 ? opts.naming.special : opts.naming.series;
        const levels = renderTemplate(template, { title, year, season: u.season, episode: u.episode, version, episodeWidth });
        return { toPath: [...w.folder, ...levels.slice(0, -1)], stem: levels[levels.length - 1] ?? "" };
      };
      for (const u of videos) Object.assign(u, render(u, versions.get(u) ?? ""));
      for (const u of list) {
        if (u.role === "video") continue;
        const dir = u.file.dir.join("/");
        const pair = videos.find((v) => v.file.dir.join("/") === dir) ?? videos[0];
        const base = pair ? { toPath: pair.toPath, stem: pair.stem } : render(u, "");
        u.toPath = base.toPath;
        u.stem = base.stem + (u.language ? `.${u.language}` : "");
      }
      for (const u of list) {
        const ext = splitName(u.entry.name).ext;
        if (!u.stem) {
          kept.push(keepChange(u.entry, u.file.dir, u.role, t("命名模板无效")));
          continue;
        }
        const name = u.stem + ext;
        changes.push({
          item: u.entry,
          from: u.file.dir,
          op: "move",
          toPath: u.toPath,
          name,
          proposed: name,
          selected: true,
          locked: "",
          note: "",
          series: w.key,
          role: u.role,
        });
      }
    }
    series.push({
      key: w.key,
      title,
      type: w.movie ? "movie" : "series",
      year,
      yearGuessed: adopted,
      source: SOURCE_RANK.find((s) => w.sources.has(s)) ?? "rules",
      guess,
      folder: w.folder.join("/"),
      dirs: w.dirs.size,
      items: w.units.length,
    });
  }

  const target = (c: Change) => [...c.toPath, c.name].join("/");
  changes.sort((a, b) => natural(target(a), target(b)));
  trash.sort((a, b) => natural([...a.from, a.item.name].join("/"), [...b.from, b.item.name].join("/")));
  kept.sort((a, b) => natural([...a.from, a.item.name].join("/"), [...b.from, b.item.name].join("/")));
  const all = [...changes, ...trash];
  return { changes: [...all, ...emptiedFolders(tree, all)], kept, series, resolutionIds };
}

/**
 * emptiedFolders are the Move to Trash Only If Empty After Execution candidates for a plan: every
 * folder below the scope whose whole contents the ticked changes move out
 * (or trash, or that are such folders themselves) and that no ticked move
 * lands in. Ticked by default; the run trashes one only if it really is
 * empty by then. Recompute after the user ticks items to refresh them.
 */
export function emptiedFolders(tree: Tree, changes: readonly Change[]): Change[] {
  const gone = new Set<string>();
  const landing = new Set<string>();
  for (const c of changes) {
    if (!c.selected || c.locked || c.op === "trashIfEmpty") continue;
    if (c.op === "trash") {
      gone.add(c.item.id);
      continue;
    }
    const same = c.toPath.length === c.from.length && c.toPath.every((n, i) => nameKey(n) === nameKey(c.from[i]));
    // Walk the destination through existing folders, as the run will.
    let at = tree.scopeId;
    for (const layer of c.toPath) {
      const holders = tree.childrenOf(at).filter((e) => e.folder && nameKey(e.name) === nameKey(layer));
      if (holders.length !== 1) break;
      at = holders[0].id;
      landing.add(at);
    }
    if (!same) gone.add(c.item.id);
  }
  const empties = new Map<string, boolean>();
  const empty = (id: string): boolean => {
    const known = empties.get(id);
    if (known !== undefined) return known;
    const inside = tree.childrenOf(id);
    const result =
      !landing.has(id) && inside.length > 0 && inside.every((e) => gone.has(e.id) || (e.folder && empty(e.id)));
    empties.set(id, result);
    return result;
  };
  const out: Change[] = [];
  for (const e of tree.byId.values()) {
    if (!e.folder || !empty(e.id)) continue;
    const from = tree.pathOf(e.parentId);
    out.push({
      item: e,
      from,
      op: "trashIfEmpty",
      toPath: from.slice(),
      name: e.name,
      proposed: e.name,
      selected: true,
      locked: "",
      note: t("执行后为空才移到回收站"),
      series: "",
      role: "folder",
    });
  }
  return out.sort((a, b) => b.from.length - a.from.length || natural(a.item.name, b.item.name));
}
