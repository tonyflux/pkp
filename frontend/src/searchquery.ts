/*
 * Search Box Filtering: the toolbar search syntax, its
 * chips and the AIQuery SearchIndex answers — all local, no model. A token
 * either is a condition (a chip) or a keyword; nothing is guessed, so the
 * same text always means the same query, and removing a chip edits the text
 * the user typed rather than a hidden state beside it.
 */

import { i18n, N_, t, type Locale } from "./i18n";

/** AIQuery is services.AIQuery (json names as the Go struct). */
export interface AIQuery {
  /** Local scope constraints; never included in model tool schemas. */
  ids?: string[];
  folderId?: string;
  keywords: string[];
  /** "" | video | image | document | other */
  category: string;
  /** Lower case, no dot. */
  ext: string[];
  /** Bytes, inclusive; 0 leaves the side open. */
  minSize: number;
  maxSize: number;
  /** "YYYY-MM-DD", inclusive days of this computer's zone. */
  createdAfter: string;
  createdBefore: string;
  modifiedAfter: string;
  modifiedBefore: string;
  /** A folder path from the root ("Media/Documentaries") or a folder name. */
  under: string;
  /** "" | yes | no */
  starred: string;
  played: Played;
  /** "" | file | folder */
  kind: string;
  /** "" (= modified) | modified | created | size | name | played */
  sort: string;
  asc: boolean;
  /** 1..200, 0 → 50 */
  limit: number;
  aggregate: AIAggregate | null;
}

export type Played = "" | "any" | "unfinished" | "finished";

export interface AIAggregate {
  /** folder | category | ext */
  by: string;
  /** size | count */
  measure: string;
  /** ≤ 50, 0 → 10 */
  limit: number;
}

export function emptyQuery(): AIQuery {
  return {
    keywords: [],
    category: "",
    ext: [],
    minSize: 0,
    maxSize: 0,
    createdAfter: "",
    createdBefore: "",
    modifiedAfter: "",
    modifiedBefore: "",
    under: "",
    starred: "",
    played: "",
    kind: "",
    sort: "",
    asc: false,
    limit: 0,
    aggregate: null,
  };
}

export interface SearchChip {
  /** One chip per key: removeChip drops every token with it. */
  key: string;
  /** 「Type: Video」「Playback: Unfinished」「Saved: Last Week」「Size: > 1 GB」 */
  label: string;
  /** The text the chip came from, as typed (several tokens joined by a space). */
  token: string;
}

export interface ParsedSearch {
  keywords: string[];
  query: AIQuery;
  chips: SearchChip[];
  /** Any condition besides keywords: the search goes to SearchIndex. */
  filtered: boolean;
}

/* ---------------- tokens ---------------- */

interface Token {
  raw: string;
  /** raw without the quotes. */
  value: string;
}

const OPEN_QUOTE: Record<string, string> = { '"': '"', "“": "”", "「": "」" };

/**
 * tokens splits on white space (full-width too); quotes keep spaces in one
 * token. Only double quotes decode JSON escapes; unquoted paths stay literal.
 */
function tokens(text: string): Token[] {
  const out: Token[] = [];
  let raw = "";
  let value = "";
  let close = "";
  const flush = () => {
    if (raw) out.push({ raw, value });
    raw = "";
    value = "";
  };
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (close) {
      raw += ch;
      if (close === '"' && ch === "\\") {
        const escape = /^(?:["\\/bfnrt]|u[\da-fA-F]{4})/.exec(text.slice(i + 1));
        if (escape) {
          raw += escape[0];
          value += JSON.parse(`"\\${escape[0]}"`) as string;
          i += escape[0].length;
          continue;
        }
      }
      if (ch === close) close = "";
      else value += ch;
    } else if (OPEN_QUOTE[ch]) {
      raw += ch;
      close = OPEN_QUOTE[ch];
    } else if (/\s/u.test(ch)) {
      flush();
    } else {
      raw += ch;
      value += ch;
    }
  }
  flush();
  return out;
}

/* ---------------- dates ---------------- */

/** localDay is the date of d in this computer's zone, as AIQuery dates are written. */
export function localDay(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

interface Range {
  after: string;
  before: string;
  /** How the chip says it. */
  label: string;
}

/*
 * The words of the syntax, in every language of the interface. A row is an
 * id and the words that say it, a space apart: the first five are what the
 * Add Condition menu writes in Simplified Chinese, Traditional Chinese, English, Japanese and Korean
 * (LANGS), the rest more ways to say it. Words match in lower case. A Latin
 * word alone is a keyword (today is a word in file names); after its prefix
 * it is a condition (created:today).
 */
const LANGS: readonly Locale[] = ["zh-Hans", "zh-Hant", "en", "ja", "ko"];

type Rows<T extends string> = Record<T, string>;

/** aliases maps every word of the rows to its id. */
function aliases<T extends string>(rows: Rows<T>): Map<string, T> {
  const out = new Map<string, T>();
  for (const id of Object.keys(rows) as T[]) for (const w of rows[id].split(" ")) out.set(w.toLowerCase(), id);
  return out;
}

/** say is the word of a row the menu writes in the language in force. */
function say<T extends string>(rows: Rows<T>, id: T): string {
  const words = rows[id].split(" ");
  return words[LANGS.indexOf(i18n.locale)] ?? words[0];
}

/** bareWord: a word typed alone may say a condition unless it is Latin. */
function bareWord(v: string): boolean {
  return !/^[a-z]/i.test(v);
}

type Day = "today" | "yesterday" | "thisweek" | "lastweek" | "thismonth" | "lastmonth" | "thisyear" | "lastyear";

const DAY_ROWS: Rows<Day> = {
  today: "今天 今天 today 今日 오늘", // i18n-ignore: search syntax
  yesterday: "昨天 昨天 yesterday 昨日 어제", // i18n-ignore
  thisweek: "本周 本週 thisweek 今週 이번주 这周 這週", // i18n-ignore
  lastweek: "上周 上週 lastweek 先週 지난주", // i18n-ignore
  thismonth: "本月 本月 thismonth 今月 이번달 这个月 這個月", // i18n-ignore
  lastmonth: "上月 上月 lastmonth 先月 지난달 上个月 上個月", // i18n-ignore
  thisyear: "今年 今年 thisyear 今年 올해", // i18n-ignore
  lastyear: "去年 去年 lastyear 昨年 작년", // i18n-ignore
};

const DAY_WORDS = aliases(DAY_ROWS);

const DAY_LABEL: Record<Day, string> = {
  today: N_("今天"),
  yesterday: N_("昨天"),
  thisweek: N_("本周"),
  lastweek: N_("上周"),
  thismonth: N_("本月"),
  lastmonth: N_("上月"),
  thisyear: N_("今年"),
  lastyear: N_("去年"),
};

/** Last N days/weeks/months (Nd/Nw/Nm after a prefix); the words before the number say it alone. */
const RECENT = /^(最近|近|過去|直近|최근|last)?(\d{1,3})(天|日|週間|周|週|星期|个月|個月|か月|ヶ月|カ月|月|개월|일|주|d|w|m)$/;

const RECENT_UNITS = aliases<"day" | "week" | "month">({
  day: "天 日 일 d", // i18n-ignore
  week: "周 週 週間 星期 주 w", // i18n-ignore
  month: "个月 個月 か月 ヶ月 カ月 月 개월 m", // i18n-ignore
});

function validDay(y: number, m: number, d: number): boolean {
  const t = new Date(y, m - 1, d);
  return t.getFullYear() === y && t.getMonth() === m - 1 && t.getDate() === d;
}

/**
 * dateRange reads one date value against today (weeks start on Monday):
 * a word (today … last year, English without spaces), last N days/weeks/months (Nd/Nw/Nm),
 * a month 2026-10 or a day 2026-10-01. Bounds are inclusive days.
 */
function dateRange(value: string, today: Date, english: boolean): Range | null {
  const v = value.toLowerCase();
  const y = today.getFullYear();
  const m = today.getMonth();
  const d = today.getDate();
  const day = (yy: number, mm: number, dd: number) => localDay(new Date(yy, mm, dd));
  const word = DAY_WORDS.get(v);
  if (word && (english || bareWord(v))) {
    const monday = d - ((today.getDay() + 6) % 7);
    const label = t(DAY_LABEL[word]);
    switch (word) {
      case "today":
        return { after: day(y, m, d), before: day(y, m, d), label };
      case "yesterday":
        return { after: day(y, m, d - 1), before: day(y, m, d - 1), label };
      case "thisweek":
        return { after: day(y, m, monday), before: day(y, m, monday + 6), label };
      case "lastweek":
        return { after: day(y, m, monday - 7), before: day(y, m, monday - 1), label };
      case "thismonth":
        return { after: day(y, m, 1), before: day(y, m + 1, 0), label };
      case "lastmonth":
        return { after: day(y, m - 1, 1), before: day(y, m, 0), label };
      case "thisyear":
        return { after: day(y, 0, 1), before: day(y, 11, 31), label };
      default:
        return { after: day(y - 1, 0, 1), before: day(y - 1, 11, 31), label };
    }
  }
  const recent = RECENT.exec(v);
  if (recent && (english || (recent[1] !== undefined && recent[1] !== "last"))) {
    const n = Number(recent[2]);
    const unit = RECENT_UNITS.get(recent[3]) ?? "day";
    if (n < 1) return null;
    const label = unit === "day" ? t("最近 {n} 天", { n }) : unit === "week" ? t("最近 {n} 周", { n }) : t("最近 {n} 个月", { n });
    if (unit === "day") return { after: day(y, m, d - n + 1), before: day(y, m, d), label };
    if (unit === "week") return { after: day(y, m, d - 7 * n + 1), before: day(y, m, d), label };
    return { after: day(y, m - n, d + 1), before: day(y, m, d), label };
  }
  const month = /^(\d{4})-(\d{1,2})$/.exec(v);
  if (month) {
    const yy = Number(month[1]);
    const mm = Number(month[2]);
    if (mm < 1 || mm > 12) return null;
    return { after: day(yy, mm - 1, 1), before: day(yy, mm, 0), label: `${yy}-${String(mm).padStart(2, "0")}` };
  }
  const full = /^(\d{4})-(\d{1,2})-(\d{1,2})$/.exec(v);
  if (full) {
    const [yy, mm, dd] = [Number(full[1]), Number(full[2]), Number(full[3])];
    if (!validDay(yy, mm, dd)) return null;
    const one = day(yy, mm - 1, dd);
    return { after: one, before: one, label: one };
  }
  return null;
}

/* ---------------- conditions ---------------- */

/** What one token does: the chip key it belongs to, its own label and the change to the query. */
interface Condition {
  key: string;
  label: string;
  apply(q: AIQuery): void;
  /** Date bounds, so several tokens on one field can be labelled together. */
  range?: { field: "created" | "modified"; after: string; before: string };
}

type Category = "video" | "image" | "document" | "other";

const CATEGORY_ROWS: Rows<Category> = {
  video: "视频 影片 video 動画 동영상 videos", // i18n-ignore
  image: "图片 圖片 image 画像 이미지 照片 images photo 写真 사진", // i18n-ignore
  document: "文档 文件 document ドキュメント 문서 documents doc docs", // i18n-ignore
  other: "其他 其他 other その他 기타 其它", // i18n-ignore
};

const CATEGORY = aliases(CATEGORY_ROWS);

const CATEGORY_LABEL: Record<Category, string> = { video: N_("视频"), image: N_("图片"), document: N_("文档"), other: N_("其他") };

const PLAYED_ROWS: Rows<Exclude<Played, "">> = {
  unfinished: "没看完 沒看完 unfinished 視聴途中 이어보기 未看完", // i18n-ignore
  finished: "看过 看過 finished 視聴済み 시청함 看完了 看完 시청완료", // i18n-ignore
  any: "播放过 播放過 any 再生済み 재생함", // i18n-ignore
};

const PLAYED_WORDS = aliases(PLAYED_ROWS);

export const PLAYED_LABEL: Record<Exclude<Played, "">, string> = {
  get unfinished() {
    return t("没看完");
  },
  get finished() {
    return t("看过");
  },
  get any() {
    return t("播放过");
  },
};

const STARRED_ROWS: Rows<"yes"> = { yes: "星标 星號 starred スター 별표" }; // i18n-ignore
const FOLDER_ROWS: Rows<"folder"> = { folder: "文件夹 資料夾 folder フォルダ 폴더" }; // i18n-ignore
const STARRED_WORDS = aliases(STARRED_ROWS);
const FOLDER_WORDS = aliases(FOLDER_ROWS);
const FILE_WORDS = aliases({ file: "文件 檔案 file ファイル 파일" }); // i18n-ignore

type Sort = "size" | "name" | "modified" | "created" | "played";

const SORT_WORDS = aliases<Sort>({
  size: "大小 大小 size サイズ 크기", // i18n-ignore
  name: "名称 名稱 name 名前 이름 名字", // i18n-ignore
  modified: "修改时间 修改時間 modified 更新日時 수정일 时间 時間 更新", // i18n-ignore
  created: "保存时间 保存時間 created 保存日時 저장일", // i18n-ignore
  played: "播放 播放 played 再生 재생", // i18n-ignore
});

/** The chip's name for a sort and whether it runs up unless told. */
const SORT_INFO: Record<Sort, [string, boolean]> = {
  size: [N_("大小"), false],
  name: [N_("名称"), true],
  modified: [N_("修改时间"), false],
  created: [N_("保存时间"), false],
  played: [N_("最近播放"), false],
};

type By = "folder" | "category" | "ext";

/** The aggregates typed alone (By Folder); the Latin words are what group: takes in the menu. */
const AGGREGATE_ROWS: Rows<By> = {
  folder: "按文件夹统计 按資料夾統計 folder フォルダ別集計 폴더별통계", // i18n-ignore
  category: "按类型统计 按類型統計 type 種類別集計 유형별통계", // i18n-ignore
  ext: "按扩展名统计 按副檔名統計 ext 拡張子別集計 확장자별통계", // i18n-ignore
};

const AGGREGATES = aliases(AGGREGATE_ROWS);

const AGGREGATE_BY = aliases<By>({
  folder: "folder 文件夹 資料夾 フォルダ 폴더", // i18n-ignore
  category: "category type 类型 類型 種類 유형", // i18n-ignore
  ext: "ext 扩展名 副檔名 拡張子 확장자", // i18n-ignore
});

const AGGREGATE_LABEL: Record<By, string> = { folder: N_("按文件夹统计大小"), category: N_("按类型统计大小"), ext: N_("按扩展名统计大小") };

/** Size units as powers of 1024: 1G = 1 GiB, as bytes() shows sizes. */
const UNITS: Record<string, number> = { "": 0, k: 1, m: 2, g: 3, t: 4 };

const SIZE_OPS: Record<string, ">" | ">=" | "<" | "<="> = {
  ">": ">",
  ">=": ">=",
  "≥": ">=",
  大于: ">",
  大于等于: ">=",
  不小于: ">=",
  "<": "<",
  "<=": "<=",
  "≤": "<=",
  小于: "<",
  小于等于: "<=",
  不大于: "<=",
};

const SIZE_SIGNS: Record<string, string> = { ">": ">", ">=": "≥", "<": "<", "<=": "≤" };

type Field = "type" | "ext" | "in" | "played" | "is" | "modified" | "created" | "after" | "before" | "group" | "sort" | "size";

/** Prefixes (colon included) and the field they set. */
const PREFIX_ROWS: Rows<Field> = {
  type: "类型: 類型: type: 種類: 유형: タイプ:", // i18n-ignore
  ext: "扩展名: 副檔名: ext: 拡張子: 확장자:", // i18n-ignore
  in: "在: 在: in: 場所: 위치: under:", // i18n-ignore
  played: "播放: 播放: played: 再生: 재생:", // i18n-ignore
  is: "is:",
  modified: "修改: 修改: modified: 更新: 수정: 修改时间: 修改時間: 更新日時: 수정일:", // i18n-ignore
  created: "保存: 儲存: created: 保存: 저장: 保存时间: 保存時間: 儲存時間: 保存日: 保存日時: 저장일:", // i18n-ignore
  after: "after:",
  before: "before:",
  group: "统计: 統計: group: 集計: 통계:", // i18n-ignore
  sort: "排序: 排序: sort: 並べ替え: 정렬: 並び順:", // i18n-ignore
  size: "大小: 大小: size: サイズ: 크기:", // i18n-ignore
};

const PREFIXES = aliases(PREFIX_ROWS);

/** sizeCondition reads >1G <500M >=2GB greater than 1.5G (binary units); a number without a unit is a keyword. */
function sizeCondition(text: string): Condition | null {
  const m = /^(>=|<=|≥|≤|>|<|大于等于|小于等于|大于|小于|不小于|不大于)(\d+(?:\.\d+)?)([kmgt]?)(i?b)?$/i.exec(text);
  if (!m || (!m[3] && !m[4])) return null;
  const unit = m[3].toLowerCase();
  const bytes = Math.round(Number(m[2]) * 1024 ** UNITS[unit]);
  if (!Number.isSafeInteger(bytes)) return null;
  const op = SIZE_OPS[m[1]];
  const min = op.startsWith(">");
  // The index bounds are inclusive: a strict bound is one byte further in.
  const value = op === ">" ? bytes + 1 : op === "<" ? Math.max(0, bytes - 1) : bytes;
  return {
    key: min ? "minSize" : "maxSize",
    label: t("大小：{v}", { v: `${SIZE_SIGNS[op]} ${Number(m[2])} ${unit.toUpperCase()}B` }),
    apply: (q) => {
      if (min) q.minSize = value;
      else q.maxSize = value;
    },
  };
}

function dateCondition(field: "created" | "modified", r: Range): Condition {
  return {
    key: field,
    label: field === "created" ? t("保存时间：{v}", { v: r.label }) : t("修改时间：{v}", { v: r.label }),
    range: { field, after: r.after, before: r.before },
    apply: (q) => {
      if (field === "created") {
        if (r.after) q.createdAfter = r.after;
        if (r.before) q.createdBefore = r.before;
      } else {
        if (r.after) q.modifiedAfter = r.after;
        if (r.before) q.modifiedBefore = r.before;
      }
    },
  };
}

function playedCondition(p: Exclude<Played, "">): Condition {
  return { key: "played", label: t("播放：{v}", { v: PLAYED_LABEL[p] }), apply: (q) => (q.played = p) };
}

function starredCondition(v: "yes" | "no"): Condition {
  return { key: "starred", label: v === "yes" ? t("只看星标") : t("不含星标"), apply: (q) => (q.starred = v) };
}

function kindCondition(v: "folder" | "file"): Condition {
  return { key: "kind", label: v === "folder" ? t("只看文件夹") : t("只看文件"), apply: (q) => (q.kind = v) };
}

const QUOTED = /^["“「]/;

/** condition reads one token; null is a keyword. A quoted token is always a keyword. */
function condition(tok: Token, today: Date): Condition | null {
  if (QUOTED.test(tok.raw)) return null;
  const value = tok.value;

  if (bareWord(value)) {
    const lower = value.toLowerCase();
    const played = PLAYED_WORDS.get(lower);
    if (played) return playedCondition(played);
    if (STARRED_WORDS.has(lower)) return starredCondition("yes");
    if (FOLDER_WORDS.has(lower)) return kindCondition("folder");
    const agg = AGGREGATES.get(lower);
    if (agg) return aggregateCondition(agg);
  }
  if (/^\.[a-z0-9]{1,10}$/i.test(value)) return extCondition([value.slice(1).toLowerCase()]);
  const size = sizeCondition(value);
  if (size) return size;
  const date = dateRange(value, today, false);
  if (date) return dateCondition("created", date);

  // prefix:value, the full-width colon too; the value may be quoted (in:"US TV Series Collection").
  const colon = tok.raw.search(/[:：]/u);
  if (colon <= 0) return null;
  const field = PREFIXES.get(`${tok.raw.slice(0, colon).toLowerCase()}:`);
  if (!field) return null;
  const rest = tok.value.slice(colon + 1).trim();
  if (!rest) return null;
  const restLower = rest.toLowerCase();

  switch (field) {
    case "type": {
      const c = CATEGORY.get(restLower);
      if (!c) return null;
      return { key: "category", label: t("类型：{v}", { v: t(CATEGORY_LABEL[c]) }), apply: (q) => (q.category = c) };
    }
    case "ext": {
      const exts = restLower
        .split(/[,，]/)
        .map((e) => e.replace(/^\.+/, ""))
        .filter((e) => e);
      return exts.length && exts.every((e) => /^[a-z0-9]{1,10}$/.test(e)) ? extCondition(exts) : null;
    }
    case "in": {
      const quoted = tok.raw[colon + 1] === '"';
      const path = quoted ? rest : rest.replace(/[\\/]+/g, "/").replace(/^\/+|\/+$/g, "");
      if (!path) return null;
      return { key: "under", label: t("在：{v}", { v: path }), apply: (q) => (q.under = path) };
    }
    case "played": {
      const p = PLAYED_WORDS.get(restLower);
      return p ? playedCondition(p) : null;
    }
    case "is": {
      if (STARRED_WORDS.has(restLower)) return starredCondition("yes");
      if (restLower === "unstarred") return starredCondition("no");
      if (FOLDER_WORDS.has(restLower)) return kindCondition("folder");
      if (FILE_WORDS.has(restLower)) return kindCondition("file");
      return null;
    }
    case "created":
    case "modified": {
      const bounds = /^(\d{4}-\d{2}-\d{2})?\.\.(\d{4}-\d{2}-\d{2})?$/.exec(rest);
      if (bounds) {
        const after = bounds[1] ?? "";
        const before = bounds[2] ?? "";
        if ((!after && !before) || (after && !dateRange(after, today, true)) || (before && !dateRange(before, today, true))) return null;
        const label = after && before
          ? t("{a} 至 {b}", { a: after, b: before })
          : after ? t("{d} 之后", { d: after }) : t("{d} 之前", { d: before });
        return dateCondition(field, { after, before, label });
      }
      const r = dateRange(rest, today, true);
      return r ? dateCondition(field, r) : null;
    }
    case "after":
    case "before": {
      // A day, or a month read from its first (after) or last (before) day.
      const r = /^\d{4}-\d{1,2}(-\d{1,2})?$/.test(rest) ? dateRange(rest, today, false) : null;
      if (!r) return null;
      return dateCondition(
        "created",
        field === "after"
          ? { after: r.after, before: "", label: t("{d} 之后", { d: r.after }) }
          : { after: "", before: r.before, label: t("{d} 之前", { d: r.before }) },
      );
    }
    case "group": {
      const [by, measure = "size", limit = "10", ...extra] = restLower.split(":");
      const a = AGGREGATE_BY.get(by);
      if (!a || (measure !== "size" && measure !== "count") || !/^\d+$/.test(limit) || Number(limit) > 50 || extra.length) return null;
      return aggregateCondition(a, measure, Number(limit));
    }
    case "sort": {
      const desc = /[↓]$|-desc$/.test(restLower);
      const ascMark = /[↑]$|-asc$/.test(restLower);
      const s = SORT_WORDS.get(restLower.replace(/[↑↓]$|-(asc|desc)$/, ""));
      if (!s) return null;
      const [name, ascDefault] = SORT_INFO[s];
      const asc = ascMark || (ascDefault && !desc);
      return {
        key: "sort",
        label: t("排序：{v}", { v: t(name) + (ascMark || desc ? (asc ? " ↑" : " ↓") : "") }),
        apply: (q) => {
          q.sort = s;
          q.asc = asc;
        },
      };
    }
    case "size":
      return sizeCondition(rest);
  }
  return null;
}

/** extList lists extensions the way the language in force runs a list (mkv、mp4 · mkv, mp4). */
export function extList(exts: readonly string[]): string {
  return exts.join(i18n.locale === "en" || i18n.locale === "ko" ? ", " : "、");
}

function extCondition(exts: string[]): Condition {
  return {
    key: `ext:${exts.join(",")}`,
    label: t("扩展名：{v}", { v: extList(exts) }),
    apply: (q) => {
      for (const e of exts) if (!q.ext.includes(e)) q.ext.push(e);
    },
  };
}

function aggregateCondition(by: By, measure: "size" | "count" = "size", limit = 10): Condition {
  const label = measure === "size" && (limit === 10 || limit === 0)
    ? t(AGGREGATE_LABEL[by])
    : t("统计：{by} · {measure} · 最多 {n} 组", {
      by: by === "folder" ? t("文件夹") : by === "category" ? t("类型") : t("扩展名"),
      measure: measure === "count" ? t("数量") : t("大小"), n: limit || 10,
    });
  return { key: "aggregate", label, apply: (q) => (q.aggregate = { by, measure, limit }) };
}

/** A field set by several date tokens says the bounds they leave. */
function rangeLabel(field: "created" | "modified", q: AIQuery): string {
  const after = field === "created" ? q.createdAfter : q.modifiedAfter;
  const before = field === "created" ? q.createdBefore : q.modifiedBefore;
  const v =
    after && before
      ? after === before
        ? after
        : t("{a} 至 {b}", { a: after, b: before })
      : after
        ? t("{d} 之后", { d: after })
        : t("{d} 之前", { d: before });
  return field === "created" ? t("保存时间：{v}", { v }) : t("修改时间：{v}", { v });
}

/* ---------------- public ---------------- */

/**
 * searchText reopens an assistant query in the file search without changing its
 * filters. The file page owns its row limit; local IDs/folderId stay out of text.
 */
export function searchText(query: AIQuery): string {
  const out = query.keywords.map((keyword) => JSON.stringify(keyword));
  if (query.category) out.push(`type:${query.category}`);
  if (query.ext.length) out.push(`ext:${query.ext.join(",")}`);
  if (query.minSize > 0) out.push(`>=${query.minSize}B`);
  if (query.maxSize > 0) out.push(`<=${query.maxSize}B`);
  if (query.createdAfter || query.createdBefore) out.push(`created:${query.createdAfter}..${query.createdBefore}`);
  if (query.modifiedAfter || query.modifiedBefore) out.push(`modified:${query.modifiedAfter}..${query.modifiedBefore}`);
  if (query.under) out.push(`in:${JSON.stringify(query.under)}`);
  if (query.starred) out.push(`is:${query.starred === "yes" ? "starred" : "unstarred"}`);
  if (query.played) out.push(`played:${query.played}`);
  if (query.kind) out.push(`is:${query.kind}`);
  if (query.aggregate) {
    const { by, measure, limit } = query.aggregate;
    out.push(`group:${by}:${measure || "size"}:${limit}`);
  }
  // Explicit even for an empty query: use the index and do not infer played sort.
  out.push(`sort:${query.sort || "modified"}-${query.asc ? "asc" : "desc"}`);
  return out.join(" ");
}

/**
 * parseSearch reads the search box. Conditions of one key combine the way a
 * later word overrides an earlier one (Type:Video Type:Image is Image); dates
 * are relative to `today`. Keywords keep their order, repeats dropped.
 */
export function parseSearch(text: string, today: Date): ParsedSearch {
  const query = emptyQuery();
  const keywords: string[] = [];
  const byKey = new Map<string, { conditions: Condition[]; raws: string[] }>();
  for (const t of tokens(text)) {
    const c = condition(t, today);
    if (!c) {
      const k = t.value.trim();
      if (k && !keywords.includes(k)) keywords.push(k);
      continue;
    }
    const slot = byKey.get(c.key) ?? { conditions: [], raws: [] };
    slot.conditions.push(c);
    slot.raws.push(t.raw);
    byKey.set(c.key, slot);
  }
  const chips: SearchChip[] = [];
  for (const [key, slot] of byKey) {
    for (const c of slot.conditions) c.apply(query);
    const last = slot.conditions[slot.conditions.length - 1];
    const label = slot.conditions.length > 1 && last.range ? rangeLabel(last.range.field, query) : last.label;
    chips.push({ key, label, token: slot.raws.join(" ") });
  }
  // A play condition is about watching: newest play first unless a sort was asked for.
  if (query.played && !byKey.has("sort")) query.sort = "played";
  query.keywords = keywords;
  return { keywords, query, chips, filtered: chips.length > 0 };
}

/** removeChip is the text without the chip's tokens; the rest stays as typed, one space apart. */
export function removeChip(text: string, chip: Pick<SearchChip, "key">): string {
  // The key of a token does not depend on the day, so any date serves.
  const today = new Date(2000, 0, 1);
  return tokens(text)
    .filter((t) => condition(t, today)?.key !== chip.key)
    .map((t) => t.raw)
    .join(" ");
}

/**
 * addToken adds a condition from the Add Condition menu: it replaces a condition
 * of the same key (Type:Video → Type:Image) instead of piling up beside it.
 */
export function addToken(text: string, token: string): string {
  const today = new Date(2000, 0, 1);
  const added = tokens(token).map((t) => condition(t, today)?.key);
  const kept = tokens(text).filter((t) => {
    const key = condition(t, today)?.key;
    return key === undefined || !added.includes(key);
  });
  return [...kept.map((t) => t.raw), token.trim()].filter((s) => s).join(" ");
}

/** folderToken quotes paths when spaces, quotes or backslashes need protecting. */
export function folderToken(path: string): string {
  const p = path.trim().replace(/^\/+|\/+$/g, "");
  const prefix = say(PREFIX_ROWS, "in");
  return /[\s"“「\\]/u.test(p) ? `${prefix}${JSON.stringify(p)}` : `${prefix}${p}`;
}

// What a sentence says about when, read leniently: 「added from cloud last week」 means
// something added since last week, so the past periods run up to today.
// The sentence grammar is Simplified Chinese only; the tokens it makes read in every language.
const SENTENCE_DATES: [RegExp, (today: Date) => string][] = [
  [/今天/u, () => "今天"], // i18n-ignore
  [/昨天/u, () => "最近2天"], // i18n-ignore
  [/前天/u, () => "最近3天"], // i18n-ignore
  [/最近\s*(\d{1,3})\s*(天|周|个月)/u, () => ""],
  [/(本周|这周|这个星期|这礼拜)/u, () => "本周"], // i18n-ignore
  [/(上周|上星期|上个星期|上礼拜)/u, (t) => `after:${localDay(mondayBefore(t))}`],
  [/(本月|这个月)/u, () => "本月"], // i18n-ignore
  [/(上月|上个月)/u, (t) => `after:${localDay(new Date(t.getFullYear(), t.getMonth() - 1, 1))}`],
  [/今年/u, () => "今年"], // i18n-ignore
  [/去年/u, () => "去年"], // i18n-ignore
];

function mondayBefore(today: Date): Date {
  const d = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const sinceMonday = (d.getDay() + 6) % 7;
  d.setDate(d.getDate() - sinceMonday - 7);
  return d;
}

// Words that carry the request rather than what to look for.
const FILLER =
  /(帮我|帮忙|麻烦|请|把|将|给我|一下|整理|归类|找到|找出|找|查找|搜索|搜|看看|列出|所有|全部|那些|这些|这个|那个|一些|的|了|吧|呢|里面|里|中|放到|放进|移到|挪到|移动到|集数补零|补零|改名|重命名|云添加|添加|下载|保存|存的|文件夹|文件|先别动|别动|没把握|不确定)/gu;

/**
 * sentenceSearch reads a sentence the way the search box would read typed
 * conditions — for the AI page without a model, which still owes the user
 * a local result. Typed syntax (a space or a colon) is read as is; a
 * Chinese sentence gives up its time, play and type words as conditions,
 * a destination 「put in 「X」」 is dropped, and what remains after the
 * request's own words are the keywords.
 */
export function sentenceSearch(text: string, today: Date): ParsedSearch {
  const typed = text.trim();
  if (!typed || /[:：]/u.test(typed) || /\s/u.test(typed.replace(/[「」『』"“”]/gu, ""))) return parseSearch(typed, today);
  let rest = typed.replace(/(放到|放进|移到|挪到|移动到)\s*[「『"“]([^」』"”]+)[」』"”]\s*(里|中)?/gu, " ");
  const words: string[] = [];
  for (const [re, token] of SENTENCE_DATES) {
    const m = rest.match(re);
    if (!m) continue;
    words.push(m[1] && /^\d+$/u.test(m[1]) ? `最近${m[1]}${m[2]}` : token(today)); // i18n-ignore
    rest = rest.replace(re, " ");
    break;
  }
  // i18n-ignore: sentence grammar (next two lines)
  if (/(没看完|看了一半|没有看完)/u.test(rest)) words.push("没看完");
  else if (/(看过|看完了|看完的)/u.test(rest)) words.push("看过"); // i18n-ignore
  rest = rest.replace(/(没看完|看了一半|没有看完|看过|看完了|看完的)/gu, " ");
  if (/(视频|影片|电影|电视剧|剧集|纪录片|动漫|番剧)/u.test(rest)) words.push("类型:视频"); // i18n-ignore
  // A genre is a kind of video here, not a word in the file names.
  rest = rest.replace(/(视频|影片|电影|电视剧|剧集|纪录片|动漫|番剧)/gu, " ");
  const keywords = rest
    .replace(/[「」『』"“”]/gu, " ")
    .replace(FILLER, " ")
    .split(/[\s,，.。;；!！?？、/]+/u)
    .filter((k) => [...k].length >= 2);
  return parseSearch([...words.filter(Boolean), ...keywords].join(" "), today);
}

export interface FilterOption {
  label: string;
  token: string;
}

export interface FilterGroup {
  group: string;
  options: FilterOption[];
}

/** menuOption is a menu entry whose text and token follow the language in force. */
function menuOption(label: () => string, token: () => string): FilterOption {
  return {
    get label() {
      return label();
    },
    get token() {
      return token();
    },
  };
}

function menuGroup(name: () => string, options: FilterOption[]): FilterGroup {
  return {
    get group() {
      return name();
    },
    options,
  };
}

/** alone writes a word without its prefix unless it is Latin (created:today). */
function alone(prefix: string, word: string): string {
  return bareWord(word) ? word : prefix + word;
}

type DateOption = Day | "last7";

const DATE_OPTIONS: DateOption[] = ["today", "yesterday", "last7", "thisweek", "lastweek", "thismonth", "lastmonth", "thisyear", "lastyear"];

const DATE_ROWS: Rows<DateOption> = { ...DAY_ROWS, last7: "最近7天 最近7天 last7d 過去7日 최근7일" }; // i18n-ignore

function dateLabel(d: DateOption): string {
  return d === "last7" ? t("最近 {n} 天", { n: 7 }) : t(DAY_LABEL[d]);
}

/** FILTER_OPTIONS is the Add Condition menu; every token parses back to its chip, in the language in force. */
export const FILTER_OPTIONS: readonly FilterGroup[] = [
  menuGroup(
    () => t("类型"),
    (["video", "image", "document", "other"] as const).map((c) =>
      menuOption(
        () => t(CATEGORY_LABEL[c]),
        () => say(PREFIX_ROWS, "type") + say(CATEGORY_ROWS, c),
      ),
    ),
  ),
  menuGroup(
    () => t("大小"),
    [
      menuOption(() => t("大于 {v}", { v: "100 MB" }), () => ">100M"),
      menuOption(() => t("大于 {v}", { v: "1 GB" }), () => ">1G"),
      menuOption(() => t("大于 {v}", { v: "10 GB" }), () => ">10G"),
      menuOption(() => t("小于 {v}", { v: "10 MB" }), () => "<10M"),
    ],
  ),
  menuGroup(
    () => t("保存时间"),
    DATE_OPTIONS.map((d) =>
      menuOption(
        () => dateLabel(d),
        () => alone(say(PREFIX_ROWS, "created"), say(DATE_ROWS, d)),
      ),
    ),
  ),
  menuGroup(
    () => t("修改时间"),
    DATE_OPTIONS.map((d) =>
      menuOption(
        () => dateLabel(d),
        () => say(PREFIX_ROWS, "modified") + say(DATE_ROWS, d),
      ),
    ),
  ),
  menuGroup(
    () => t("filter|播放"),
    (["unfinished", "finished", "any"] as const).map((p) =>
      menuOption(
        () => PLAYED_LABEL[p],
        () => alone(say(PREFIX_ROWS, "played"), say(PLAYED_ROWS, p)),
      ),
    ),
  ),
  menuGroup(() => t("星标"), [menuOption(() => t("只看星标"), () => alone("is:", say(STARRED_ROWS, "yes")))]),
  menuGroup(() => t("只看文件夹"), [menuOption(() => t("只看文件夹"), () => alone("is:", say(FOLDER_ROWS, "folder")))]),
  menuGroup(
    () => t("统计"),
    (["folder", "category"] as const).map((by) =>
      menuOption(
        () => t(AGGREGATE_LABEL[by]),
        () => alone(say(PREFIX_ROWS, "group"), say(AGGREGATE_ROWS, by)),
      ),
    ),
  ),
];
