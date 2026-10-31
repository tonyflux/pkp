/*
 * Media organization L0: what the groups of media.ts are, read
 * from their names alone — no model, no network. The rules are guessit's in
 * spirit: release tags (resolution, codec, source, HDR, audio, language,
 * CRC) are lifted out first, because their digits are never an episode or a
 * year; then the season and episode marks, specials, the year; the title is
 * what comes before the first of those. The group pattern is read rather
 * than each name, so a mark that holds a varying {nK} tells which variable
 * is the episode; folder names fill in the title, season and year a bare
 * "05.mkv" lacks. A group is "high" only when nothing is left to guess; the
 * rest go to the model (if the user agrees) as before. TMDB (titleQueries,
 * applyMatches) then replaces the parsed title and year — it never travels
 * the other way, into a model request.
 */
import {
  languageTag,
  splitName,
  titleKey,
  type Identity,
  type IdentityType,
  type MediaGroup,
  type NumberRole,
} from "./media";
import { N_, t } from "./i18n";

export interface ParsedGroup {
  id: string;
  identity: Identity | null;
  /** "high": named without asking; "low": the model is asked, if allowed, and `why` says what is missing. */
  confidence: "high" | "low";
  why: string;
}

/* ---------------- reading one name ---------------- */

/** A number in a name: a group variable ({nK}, 0-based) or a literal. */
type Num = { variable: number } | { value: number };

interface Reading {
  title: string;
  year: number | null;
  season: Num | null;
  episode: Num | null;
  special: boolean;
  /** Release tags were seen: the name is a release, not a home video. */
  tagged: boolean;
  /** Variables the marks gave a role. */
  roles: Map<number, NumberRole>;
}

// {nK} stands in the text as one private-use character, so the marks match
// it wherever they would match digits.
const VAR0 = 0xe001;
const V = "\\uE001-\\uE0FF";
const NUM = `(\\d{1,4}|[${V}])`;
const SHORT = `(\\d{1,3}|[${V}])`;
const CN_NUM = `(\\d{1,4}|[${V}]|[〇零一二两三四五六七八九十百]+)`; // i18n-ignore: file name grammar
const L = "(?<![a-z0-9])";
const R = "(?![a-z0-9])";

const LATIN_TAGS = new RegExp(
  `${L}(?:${[
    "\\d{3,4}x\\d{3,4}",
    "\\d{3,4}[pi]",
    "[248]k",
    "uhd",
    "fhd",
    "hd",
    "[xh]\\.?26[45]",
    "hevc",
    "avc",
    "av1",
    "vp9",
    "xvid",
    "divx",
    "\\d{1,2}-?bits?",
    "hi10p?",
    "ma10p",
    "web-?dl",
    "web-?rip",
    "blu-?ray",
    "bd-?rip",
    "br-?rip",
    "bd-?remux",
    "bd",
    "remux",
    "hdtv",
    "hd-?rip",
    "dvd-?rip",
    "dvd",
    "tv-?rip",
    "hdr10\\+",
    "hdr(?:10plus|10)?",
    "sdr",
    "dv",
    "dovi",
    "dolby[ ._-]?vision",
    "imax",
    "aac(?:[ ._]?\\d\\.\\d)?",
    "flac",
    "dts(?:-?hd)?(?:[ ._-]?ma)?",
    "truehd",
    "atmos",
    "e-?ac-?3",
    "ac-?3",
    "ddp?\\+?(?:[ ._]?\\d\\.\\d)?",
    "opus",
    "lpcm",
    "[257]\\.[01]",
    "\\d{1,2}ch",
    "\\d{2,3}fps",
    "proper",
    "repack",
    "internal",
    "limited",
    "extended",
    "uncut",
    "unrated",
    "complete",
    "dual[ ._-]?audio",
    "multi[ ._-]?subs?",
    "(?:chs|cht|gb|big5)[&_+](?:jpn|jp|eng|en)",
    "chs",
    "cht",
    "gb",
    "big5",
    "eng",
    "jpn",
    "jpsc",
    "jptc",
    "mp4",
    "mkv",
  ].join("|")})${R}`,
  "giu",
);
const VAR_RESOLUTION = new RegExp(`${L}([${V}])[pi]${R}`, "giu");
const CJK_EDGE = "\\s._\\-\\[\\]【】()（）&+/";
const CJK_TAGS = new RegExp( // i18n-ignore: file name grammar (next line)
  `(?<=^|[${CJK_EDGE}])(?:简繁内封|简繁外挂|简日内嵌|简日双语|繁日双语|中英双字|中文字幕|特效字幕|简体|繁体|简繁|繁简|简日|繁日|简英|繁英|简中|繁中|中字|中英|中日|双语|双字|内封|内嵌|外挂|国语|粤语|国粤|国英|国配|中配|字幕|高清|超清|蓝光|原盘|无删减|未删减|完整版|修复版)+(?=$|[${CJK_EDGE}])`,
  "gu",
);
const CRC = /[[(][0-9a-f]{8}[\])]/giu;
const SEASON_EPISODE = new RegExp(`${L}s${NUM}[ ._-]*ep?${NUM}(?:[-~]e?\\d{1,4})?(?![0-9])`, "giu");
const SEASON_CN = new RegExp(`第\\s*${CN_NUM}\\s*季`, "gu"); // i18n-ignore
const SEASON_WORD = new RegExp(`${L}season[ ._-]*${NUM}(?![0-9])`, "giu");
const SEASON_S = new RegExp(`${L}s(\\d{1,2}|[${V}])${R}`, "giu");
const EPISODE_CN = new RegExp(`第\\s*${CN_NUM}\\s*[集话話期回]`, "gu"); // i18n-ignore
const EPISODE_WORD = new RegExp(`${L}(?:episode[ ._-]?|ep[ ._-]?|e)${NUM}(?:v\\d)?${R}`, "giu");
const SPECIAL = new RegExp(`${L}(?:sp|ova|oad|specials?)(?:[ ._-]?${SHORT})?${R}`, "giu");
const SPECIAL_CN = new RegExp(`(?:剧场版|劇場版|特别篇|特別篇|番外篇?|总集篇)(?:\\s*${SHORT}(?![0-9]))?`, "gu"); // i18n-ignore
/** Words beside a special mark that say which cut or extra it is: never the title. */
const SPECIAL_DESCRIPTOR = new RegExp(
  `${L}(?:extended|preview|trailers?|behind[ ._-]?the[ ._-]?scenes|making[ ._-]?of)${R}|` + // i18n-ignore: file name grammar (next line)
    `(?<=^|[${CJK_EDGE}])(?:加长|先导|花絮|幕后|预告|割爱|未删减|导演剪辑)+(?:版|片|集)?(?=$|[${CJK_EDGE}])`,
  "giu",
);
/** What a file manager appends to a duplicate: " (2)", "（2）", " - Copy". Never an episode. */
const COPY_SUFFIX = /(?:\s*-\s*副本|\s*[(（]\d{1,2}[)）])+$/u;
const YEAR = new RegExp(`(?<![0-9a-z${V}])((?:19|20)\\d\\d)(?![0-9a-z${V}])`, "giu");
const EPISODE_BRACKET = new RegExp(`[\\[【]\\s*${NUM}(?:v\\d)?\\s*[\\]】]`, "giu");
const EPISODE_DASH = new RegExp(`(?<=\\s)[-–—]\\s*${NUM}(?:v\\d)?(?=\\s|$|[\\[(（【.])`, "giu");
const EPISODE_LOOSE = new RegExp(`(?<=^|[\\s._])([${V}])(?:v\\d)?(?=$|[\\s._])`, "gu");
const EPISODE_TRAILING = new RegExp(`([${V}])(?=[\\s._\\-]*$)`, "gu");
const ONLY_NUMBER = /^[\s._\-[\]【】()]*(\d{1,4})(?=[\s._\-[\]【】()]*$)/gu;
const LEADING_BRACKETS = /^(?:\s*[[【][^\]】]*[\]】])+/u;
const BRACKET = /[[【]([^\]】]*)[\]】]/gu;
const SEP = /[\s._\-–—~·:：,，|&+()（）[\]【】]/u;
const WEBSITE = /(?:www\.)?[a-z0-9-]+\.(?:com|net|org|cn|cc|tv|me|xyz|top|vip|la)(?![a-z])/giu;
const HAN = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;

/** Frame heights a varying number is when it is a resolution, not an episode. */
const RESOLUTIONS = new Set([360, 480, 540, 576, 720, 1080, 1440, 2160, 4320]);

/** Names that say nothing about the work: file and folder names that are no title. */
const GENERIC: Record<string, true> = Object.fromEntries(
  (
    "video|videos|vid|movie|movies|film|films|tv|tv shows|series|show|shows|anime|episode|episodes|ep|season|seasons|" +
    "download|downloads|new folder|untitled|temp|tmp|sample|samples|media|img|dsc|clip|clips|extras|extra|" + // i18n-ignore: file name grammar (next two lines)
    "featurettes|specials|special|bonus|my drive|视频|电影|电视剧|剧集|动漫|动画|番剧|影视|下载|新建文件夹|未命名|" + // i18n-ignore
    "未分类|待分类|全部文件|我的云盘|我的资源|资源|合集|全集|花絮|特别篇|番外"
  )
    .split("|")
    .map((t) => [t, true]),
);

const CN_DIGITS: Record<string, number> = { 〇: 0, 零: 0, 一: 1, 二: 2, 两: 2, 三: 3, 四: 4, 五: 5, 六: 6, 七: 7, 八: 8, 九: 9 };

/** numOf reads a matched number: a variable character, digits, or Chinese numerals up to nine hundred ninety-nine. */
function numOf(s: string): Num | null {
  const c = s.charCodeAt(0);
  if (s.length === 1 && c >= VAR0 && c <= 0xe0ff) return { variable: c - VAR0 };
  if (/^\d+$/.test(s)) return { value: parseInt(s, 10) };
  let total = 0;
  let digit = -1;
  for (const ch of s) {
    // i18n-ignore: Chinese numerals in file names
    if (ch === "百" || ch === "十") {
      total += (digit < 0 ? 1 : digit) * (ch === "百" ? 100 : 10); // i18n-ignore
      digit = -1;
    } else if (CN_DIGITS[ch] !== undefined) digit = CN_DIGITS[ch];
    else return null;
  }
  return { value: total + Math.max(0, digit) };
}

/** contentFrom is where text starts after `from`, past separators and blanked marks. */
function contentFrom(text: string, from: number): number {
  let i = from;
  while (i < text.length && SEP.test(text[i])) i++;
  return i;
}

/**
 * cleanTitle turns what is left of the title part into a title: release
 * groups and websites out, dots and underscores to spaces; a name that
 * carries a Chinese and a Latin title keeps the Chinese one. "" when only
 * numbers or a generic word remain.
 */
function cleanTitle(raw: string, folder: boolean): string {
  let s = raw
    .replace(BRACKET, " ")
    .replace(WEBSITE, " ")
    .replace(new RegExp(`[._${V}…]+`, "gu"), " ")
    .replace(/\(\s*\)|（\s*）/gu, " ")
    .replace(/\s+/gu, " ")
    .replace(/^[\s\-–—~·:：,，|&+/]+|[\s\-–—~·:：,，|&+/(（[【]+$/gu, "")
    .trim();
  if (HAN.test(s) && /[a-z]{2}/i.test(s)) {
    const han = /^(.*?[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}].*?)\s+[a-z][a-z0-9 '&!:,-]*$/iu.exec(s);
    const latinFirst = /^[a-z][a-z0-9 '&!:,-]*?\s+([\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}].*)$/iu.exec(s);
    if (han) s = han[1].trim();
    else if (latinFirst) s = latinFirst[1].trim();
  }
  if (!/[\p{L}\p{N}]/u.test(s) || GENERIC[s.toLowerCase()]) return "";
  if (folder && /^[\d\s]+$/u.test(s)) return "";
  return s;
}

/**
 * read takes one name (without extension) apart. `episodic[k]` says whether
 * variable k could be an episode at all (not a year, not a resolution): the
 * strong marks (S01E03, Episode 3, EP03) take any variable, the loose ones
 * ([03], " - 03", ".03.", a trailing number) only such.
 */
function read(input: string, episodic: readonly boolean[] = [], folder = false): Reading {
  let text = input;
  const starts: number[] = [];
  const r: Reading = { title: "", year: null, season: null, episode: null, special: false, tagged: false, roles: new Map() };
  const scan = (re: RegExp, accept: (m: RegExpMatchArray) => boolean) => {
    for (const m of [...text.matchAll(re)]) {
      if (!accept(m)) continue;
      const i = m.index!;
      text = text.slice(0, i) + " ".repeat(m[0].length) + text.slice(i + m[0].length);
      starts.push(i);
    }
  };
  const free = (n: Num) => !("variable" in n) || !r.roles.has(n.variable);
  const loose = (n: Num) =>
    "variable" in n ? episodic[n.variable] === true && !r.roles.has(n.variable) : n.value < 1900 && !RESOLUTIONS.has(n.value);
  const setEpisode = (n: Num | null) => {
    if (!n || r.episode || !free(n)) return false;
    r.episode = n;
    if ("variable" in n) r.roles.set(n.variable, "episode");
    return true;
  };
  const setSeason = (n: Num | null) => {
    if (!n || r.season || !free(n)) return false;
    r.season = n;
    if ("variable" in n) r.roles.set(n.variable, "season");
    return true;
  };
  const prefixEnd = LEADING_BRACKETS.exec(input)?.[0].length ?? 0;

  scan(VAR_RESOLUTION, (m) => {
    const n = numOf(m[1]);
    if (n && "variable" in n) r.roles.set(n.variable, "other");
    return (r.tagged = true);
  });
  scan(LATIN_TAGS, () => (r.tagged = true));
  scan(CJK_TAGS, () => (r.tagged = true));
  scan(CRC, () => true);
  const tags = starts.length;
  scan(SEASON_EPISODE, (m) => {
    if (r.season || r.episode) return false;
    setSeason(numOf(m[1]));
    return setEpisode(numOf(m[2]));
  });
  scan(SEASON_CN, (m) => setSeason(numOf(m[1])));
  scan(SEASON_WORD, (m) => setSeason(numOf(m[1])));
  scan(SEASON_S, (m) => setSeason(numOf(m[1])));
  scan(EPISODE_CN, (m) => setEpisode(numOf(m[1])));
  scan(EPISODE_WORD, (m) => setEpisode(numOf(m[1])));
  const special = (m: RegExpMatchArray) => {
    r.special = true;
    if (m[1]) setEpisode(numOf(m[1]));
    return true;
  };
  scan(SPECIAL, special);
  scan(SPECIAL_CN, special);
  if (r.special) scan(SPECIAL_DESCRIPTOR, () => true);

  // A year that opens the title is the title ("1917", "2012"), unless it is all there is.
  const begin = contentFrom(text, prefixEnd);
  const years = [...text.matchAll(YEAR)];
  if (years.some((m) => m.index !== begin)) {
    scan(YEAR, (m) => {
      if (m.index === begin) return false;
      r.year = Number(m[1]);
      return true;
    });
  }

  const looseEpisode = (m: RegExpMatchArray) => {
    const n = numOf(m[1]);
    return !!n && !r.episode && loose(n) && setEpisode(n);
  };
  scan(EPISODE_BRACKET, looseEpisode);
  scan(EPISODE_DASH, looseEpisode);
  scan(EPISODE_LOOSE, looseEpisode);
  scan(EPISODE_TRAILING, looseEpisode);
  if (!folder) scan(ONLY_NUMBER, looseEpisode);

  // The title: from the first content after the leading brackets to the first mark.
  const from = contentFrom(text, prefixEnd);
  let end = text.length;
  for (const s of starts) if (s > from && s < end) end = s;
  const rest = text.slice(from, end);
  const stop = rest.search(new RegExp(`[${V}…]`, "u"));
  r.title = cleanTitle(stop >= 0 ? rest.slice(0, stop) : rest, folder);
  if (!r.title) {
    // "[Group][Title][01]": the title is the last bracket before the first mark that still says
    // something — but a lone first bracket is as likely the release group.
    const said = [...text.slice(0, Math.min(prefixEnd, ...starts.slice(tags))).matchAll(BRACKET)]
      .map((m, i) => ({ title: cleanTitle(m[1], folder), first: i === 0 }))
      .filter((b) => b.title);
    const last = said[said.length - 1];
    if (last && (said.length > 1 || !last.first || HAN.test(last.title) || last.title.includes(" "))) r.title = last.title;
  }
  return r;
}

/* ---------------- reading a group ---------------- */

const JUNK_EXT: Record<string, string> = {
  torrent: N_("种子文件，下载完成后无用"),
  url: N_("网址快捷方式"),
  lnk: N_("快捷方式"),
  html: N_("网页文件，多为发布者的推广"),
  htm: N_("网页文件，多为发布者的推广"),
  txt: N_("说明文本，多为发布者的推广"),
  nfo: N_("发布信息文件"),
  aria2: N_("下载工具的临时文件"),
  xltd: N_("下载工具的临时文件"),
  td: N_("下载工具的临时文件"),
  part: N_("下载工具的临时文件"),
};
const IMAGE_EXT: Record<string, true> = { jpg: true, jpeg: true, png: true, gif: true, bmp: true, webp: true };
/** An image this small beside videos is an ad or a preview, not a poster worth keeping. */
const TINY_IMAGE = 256 * 1024;
const EXTRAS = /^(?:extras?|featurettes?|behind[ ._-]?the[ ._-]?scenes|deleted[ ._-]?scenes|interviews?|trailers?|bonus|花絮|预告片?|幕后(?:花絮)?)$/iu;
const SAMPLE_DIR = /^samples?$/iu;
const SAMPLE_NAME = /(?:^|[^a-z])sample(?:[^a-z]|$)/iu;
const PROMO_NAME = /广告|宣传/u;

interface Facts {
  group: MediaGroup;
  /** The pattern without extension (and a subtitle's language tag), {nK} as variable characters. */
  stem: string;
  /** Junk with this reason. */
  junk: string;
  /** Not to be named, with this reason (not media, extras). */
  aside: string;
  title: string;
  year: number | null;
  season: number | null;
  /** The episode when no variable is (a literal in the name). */
  episode: number | null;
  episodeKnown: boolean;
  special: boolean;
  tagged: boolean;
  numbers: NumberRole[];
  /** Variables nothing explained. */
  free: number;
  language: string;
  /** A subtitle or audio track that belongs to a video group of the same name. */
  follows: boolean;
}

function dropLanguage(stem: string): string {
  const tag = languageTag(`${stem}.srt`);
  return tag && stem.toLowerCase().endsWith(`.${tag}`) ? stem.slice(0, stem.length - tag.length - 1) : stem;
}

function patternStem(g: MediaGroup): string {
  const exts = Object.keys(g.exts);
  let p = g.pattern;
  if (exts.length > 1) p = p.replace(/\.\[[^\]]*\]$/u, "");
  else if (exts[0]) p = p.slice(0, p.length - exts[0].length - 1);
  if (g.kind === "subtitle" || g.kind === "audio") p = dropLanguage(p);
  return p.replace(/\{n(\d+)\}/gu, (_, k: string) => String.fromCharCode(VAR0 + Number(k) - 1));
}

function fileStem(g: MediaGroup, name: string): string {
  const stem = splitName(name).stem.normalize("NFKC");
  return g.kind === "subtitle" || g.kind === "audio" ? dropLanguage(stem) : stem;
}

/**
 * fromFolders reads what the folders say, nearest first, the scope itself
 * last: the season of the nearest folder that has one, whether the nearest
 * is a specials folder, and the first folder that names something — with
 * its year only when it names the same work as the file (or the file names
 * none).
 */
function fromFolders(dir: readonly string[], scopeName: string, nameTitle: string) {
  const names = [...dir].reverse();
  const scope = scopeName.split("/").pop()?.trim();
  if (scope) names.push(scope);
  let title = "";
  let year: number | null = null;
  let season: number | null = null;
  let special = false;
  names.forEach((name, i) => {
    const r = read(name.normalize("NFKC"), [], true);
    if (i === 0 && r.special) special = true;
    if (season === null && r.season && "value" in r.season) season = r.season.value;
    if (title || !r.title) return;
    title = r.title;
    if (r.year !== null && (!nameTitle || titleKey(nameTitle) === titleKey(r.title))) year = r.year;
  });
  return { title, year, season, special };
}

function readGroup(g: MediaGroup, scopeName: string, videoDirs: ReadonlySet<string>): Facts {
  const dir = g.files[0]?.dir ?? [];
  const facts: Facts = {
    group: g,
    stem: patternStem(g),
    junk: "",
    aside: "",
    title: "",
    year: null,
    season: null,
    episode: null,
    episodeKnown: false,
    special: false,
    tagged: false,
    numbers: g.variables.map(() => "other"),
    free: 0,
    language: "",
    follows: false,
  };
  const exts = Object.keys(g.exts);
  const junkExt = exts.every((e) => JUNK_EXT[e]) ? JUNK_EXT[exts[0]] : "";
  if (g.kind === "other" && junkExt) facts.junk = t(junkExt);
  else if (dir.some((d) => SAMPLE_DIR.test(d.trim())) || g.files.every((f) => SAMPLE_NAME.test(splitName(f.name).stem))) {
    facts.junk = t("样片，不是正片");
  } else if (g.files.every((f) => PROMO_NAME.test(f.name))) facts.junk = t("广告或宣传文件");
  else if (g.kind === "other" && exts.every((e) => IMAGE_EXT[e]) && g.size.max < TINY_IMAGE && videoDirs.has(dir.join("/"))) {
    facts.junk = t("视频旁的小图片，多为广告或预览图");
  } else if (g.kind === "other") facts.aside = t("不是视频、音频或字幕");
  else if (dir.some((d) => EXTRAS.test(d.trim()))) facts.aside = t("花絮、预告等附加内容");
  if (facts.junk || facts.aside) return facts;

  const episodic = g.variables.map(
    (v, k) => v.min >= 0 && v.max < 1900 && !g.files.every((f) => RESOLUTIONS.has(f.numbers[k])),
  );
  const r = read(facts.stem.replace(COPY_SUFFIX, ""), episodic);
  // A pattern that hides the episode (names that differ around it) still
  // shows it name by name: the variable that equals every name's episode is it.
  if (!r.episode && g.variables.length) {
    const eps = g.files.map((f) => {
      const e = read(fileStem(g, f.name).replace(COPY_SUFFIX, "")).episode;
      return e && "value" in e ? e.value : null;
    });
    const k = g.variables.findIndex((_, k) => !r.roles.has(k) && g.files.every((f, i) => eps[i] !== null && f.numbers[k] === eps[i]));
    if (k >= 0) {
      r.roles.set(k, "episode");
      r.episode = { variable: k };
    }
  }
  const folders = fromFolders(dir, scopeName, r.title);
  facts.title = r.title || folders.title;
  facts.year = r.year ?? folders.year;
  facts.season = r.season && "value" in r.season ? r.season.value : r.season ? null : folders.season;
  facts.episode = r.episode && "value" in r.episode ? r.episode.value : null;
  facts.episodeKnown = r.episode !== null;
  facts.special = r.special || folders.special || facts.season === 0;
  facts.tagged = r.tagged;
  facts.numbers = g.variables.map((_, k) => r.roles.get(k) ?? "other");
  facts.free = g.variables.filter((_, k) => !r.roles.has(k)).length;
  if (g.kind === "subtitle" || g.kind === "audio") {
    const tags = new Set(g.files.map((f) => languageTag(f.name)));
    facts.language = tags.size === 1 ? [...tags][0] : "";
  }
  return facts;
}

/**
 * readAllSteps reads every group; a subtitle or audio group whose name is a video
 * group's plus a language tag, in the same folder, takes that video's title,
 * year and season, so the two cannot end up as two works. A special that
 * names no title (and has no folder that does) takes the one title of the
 * series beside it.
 */
function* readAllSteps(groups: readonly MediaGroup[], scopeName: string): Generator<void, Facts[]> {
  const videoDirs = new Set<string>();
  for (let i = 0; i < groups.length; i++) {
    const group = groups[i];
    if (group.kind === "video") videoDirs.add((group.files[0]?.dir ?? []).join("/"));
    if (i % 64 === 63) yield;
  }
  const all: Facts[] = [];
  for (let i = 0; i < groups.length; i++) {
    all.push(readGroup(groups[i], scopeName, videoDirs));
    if (i % 64 === 63) yield;
  }
  const videos = new Map<string, Facts>();
  for (let i = 0; i < all.length; i++) {
    if (i % 64 === 63) yield;
    const f = all[i];
    if (f.group.kind === "video" && !f.junk && !f.aside) videos.set(`${(f.group.files[0]?.dir ?? []).join("/")}\u0000${f.stem}`, f);
  }
  for (let i = 0; i < all.length; i++) {
    if (i % 64 === 63) yield;
    const f = all[i];
    if ((f.group.kind !== "subtitle" && f.group.kind !== "audio") || f.junk || f.aside) continue;
    const video = videos.get(`${(f.group.files[0]?.dir ?? []).join("/")}\u0000${f.stem}`);
    if (!video) continue;
    f.follows = true;
    f.title = video.title;
    f.year = video.year;
    f.season = video.season;
    f.special = video.special;
    f.tagged = video.tagged;
  }
  const series = new Map<string, Set<string>>();
  for (let i = 0; i < all.length; i++) {
    if (i % 64 === 63) yield;
    const f = all[i];
    if (f.group.kind !== "video" || f.junk || f.aside || f.special || !f.title) continue;
    const dir = (f.group.files[0]?.dir ?? []).join("/");
    series.set(dir, (series.get(dir) ?? new Set()).add(f.title));
  }
  for (let i = 0; i < all.length; i++) {
    if (i % 64 === 63) yield;
    const f = all[i];
    if (!f.special || f.title || f.junk || f.aside) continue;
    const titles = series.get((f.group.files[0]?.dir ?? []).join("/"));
    if (titles?.size === 1) f.title = [...titles][0];
  }
  return all;
}

function baseIdentity(f: Facts, type: IdentityType, reason = ""): Identity {
  return {
    id: f.group.id,
    type,
    title: f.title,
    year: f.year,
    yearSource: f.year === null ? "none" : "filename",
    source: "rules",
    season: f.season,
    numbers: f.numbers,
    episode: f.episode,
    language: f.language,
    folder: "",
    reason,
  };
}

function decide(f: Facts): ParsedGroup {
  const g = f.group;
  const high = (identity: Identity): ParsedGroup => ({ id: g.id, identity, confidence: "high", why: "" });
  if (f.junk) return high({ ...baseIdentity(f, "junk", f.junk), numbers: g.variables.map(() => "other") });
  if (f.aside) return high({ ...baseIdentity(f, "unknown", f.aside), numbers: g.variables.map(() => "other") });
  const subtitle = g.kind === "subtitle";
  let type: IdentityType = "unknown";
  let why = "";
  if (g.kind === "audio" && !f.follows) why = t("音频文件，无法确定所属作品");
  else if (f.special) {
    type = "special";
    // Only a number in the name tells specials apart; S00E01 for an unnumbered one would be made up.
    if (!f.episodeKnown) why = t("特别篇没有编号");
  } else if (f.episodeKnown) type = subtitle ? "subtitle" : "series";
  else if (f.free === 0 || f.year !== null) {
    type = subtitle ? "subtitle" : "movie";
    if (f.year === null && !f.tagged) why = t("无法确定是电影还是其他视频");
  } else why = t("文件名里没有集数");
  if (!f.title && !why) why = t("无法确定片名");
  if (why) return { id: g.id, identity: baseIdentity(f, "unknown", why), confidence: "low", why };
  return high(baseIdentity(f, type));
}

/**
 * parseGroups identifies groups by their names (L0): `identity.source` is
 * "rules"; a low group carries an "unknown" identity whose reason is `why`,
 * so a plan built without the model keeps it and says why.
 */
export function parseGroups(groups: readonly MediaGroup[], scopeName: string): ParsedGroup[] {
  const steps = parseGroupsSteps(groups, scopeName);
  for (;;) {
    const step = steps.next();
    if (step.done) return step.value;
  }
}

/** The library can yield between batches without losing cross-file special/sidecar inference. */
export function* parseGroupsSteps(groups: readonly MediaGroup[], scopeName: string): Generator<void, ParsedGroup[]> {
  const all = yield* readAllSteps(groups, scopeName);
  const parsed: ParsedGroup[] = [];
  for (let i = 0; i < all.length; i++) {
    parsed.push(decide(all[i]));
    if (i % 64 === 63) yield;
  }
  return parsed;
}

/* ---------------- TMDB exact match ---------------- */

/** TitleQuery is services.TitleQuery: what MatchTitles looks up. */
export interface TitleQuery {
  /** Opaque: applyMatches finds the identities a match is for by it. */
  key: string;
  title: string;
  /** 0 for none. */
  year: number;
  kind: "movie" | "tv";
}

/** TitleMatch is the part of services.TitleMatch applyMatches uses. */
export interface TitleMatch {
  key: string;
  found: boolean;
  title: string;
  year: number;
}

/**
 * The query an identity belongs to: a series by its title alone (its groups
 * may or may not carry the year), a movie by title and year (remakes share
 * titles). Only names the rules or the model read are looked up.
 */
function queryOf(i: Identity): { key: string; title: string; kind: "movie" | "tv" } | null {
  if (i.source === "tmdb") return null;
  if (i.type !== "series" && i.type !== "special" && i.type !== "movie" && i.type !== "subtitle") return null;
  const title = i.title.trim();
  if (!title) return null;
  const tv = i.type === "series" || i.type === "special" || (i.type === "subtitle" && (i.numbers.includes("episode") || i.episode !== null));
  const year = i.yearSource === "filename" && i.year ? i.year : 0;
  return tv
    ? { key: `tv|${titleKey(title)}`, title, kind: "tv" }
    : { key: `movie|${titleKey(title)}|${year}`, title, kind: "movie" };
}

/** titleQueries are the distinct lookups for MatchTitles; a series takes the first year its names carry. */
export function titleQueries(identities: readonly Identity[]): TitleQuery[] {
  const out = new Map<string, TitleQuery>();
  for (const i of identities) {
    const q = queryOf(i);
    if (!q) continue;
    const year = i.yearSource === "filename" && i.year ? i.year : 0;
    const had = out.get(q.key);
    if (!had) out.set(q.key, { ...q, year });
    else if (!had.year && year) had.year = year;
  }
  return [...out.values()];
}

/**
 * applyMatches puts TMDB's title (and year, when it has one) on every
 * identity a found match is for, marked source "tmdb" — so buildPlan takes
 * the year as confirmed and followUp never sends either to a model.
 */
export function applyMatches(identities: readonly Identity[], matches: readonly TitleMatch[]): Identity[] {
  const found = new Map(matches.filter((m) => m.found && m.title.trim()).map((m) => [m.key, m]));
  return identities.map((i) => {
    const q = queryOf(i);
    const m = q && found.get(q.key);
    if (!m) return i;
    const dated = m.year >= 1880 && m.year <= 2100;
    return {
      ...i,
      title: m.title.trim(),
      year: dated ? m.year : i.year,
      yearSource: dated ? "tmdb" : i.yearSource,
      source: "tmdb",
    };
  });
}
