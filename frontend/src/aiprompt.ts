/*
 * AI Assistant: the Media Organization model requests are built here,
 * in the webview, and every answer is parsed here. Go only adds the key and
 * the provider envelope (AIComplete), so the Authorization Card counts and the token
 * estimate come from the very object that is sent. Model output is
 * untrusted: the parsers take nothing on faith — no casts, unknown fields and
 * ids dropped, numbers clamped, enums whitelisted — and throw ModelOutputError
 * with a reason the page can show when the answer is not what was asked.
 * TMDB data never goes out from here: groups already matched there are sent
 * as their pattern and 「Matched」 only.
 */
import { bytes } from "./format";
import { i18n, num, t } from "./i18n";
import type { Identity, IdentityType, MediaGroup, NumberRole } from "./media";

/** ModelRequest is services.AIRequest (json names as the Go struct). */
export interface ModelRequest {
  system: string;
  messages: { role: "user" | "assistant"; content: string }[];
  /** Tool / schema name, [a-zA-Z0-9_-]{1,64}. */
  outputName: string;
  /** JSON Schema (as text) of the one object the model must return. */
  outputSchema: string;
  /** Anthropic only. */
  maxTokens: number;
}

export interface TokenRange {
  low: number;
  high: number;
}

/** Disclosure is what the Authorization Card says a request sends. */
export interface Disclosure {
  title: string;
  /** "1,240 filenames, paths, sizes, and modification times. Folder names in paths will also be sent." */
  sends: string;
  /** File names in the request. */
  files: number;
  estimate: TokenRange;
}

/** Prepared is one request ready for consent and sending. */
export interface Prepared {
  request: ModelRequest;
  /** Output tokens the answer is expected to take, for the estimate. */
  expectedOutput: number;
  disclosure: Disclosure;
}

export class ModelOutputError extends Error {
  constructor(reason: string) {
    super(reason);
    this.name = "ModelOutputError";
  }
}

/* ---------------- token estimate ---------------- */

const WIDE = /[\u1100-\u11ff\u3040-\u30ff\u3100-\u312f\u3130-\u318f\u31f0-\u31ff\u3400-\u4dbf\u4e00-\u9fff\uac00-\ud7af\uf900-\ufaff\u{20000}-\u{3134f}]/u;

/**
 * estimateTokens is a local range for a request and its expected answer —
 * no provider is asked before consent. CJK, kana and hangul take 0.6–1.3
 * tokens a character across tokenizers, everything else 0.22–0.34; the
 * schema may be sent twice (system text and response_format).
 */
export function estimateTokens(req: ModelRequest, expectedOutput: number): TokenRange {
  let wide = 0;
  let other = 0;
  const count = (text: string) => {
    for (const ch of text) {
      if (WIDE.test(ch)) wide++;
      else other++;
    }
  };
  count(req.system);
  for (const m of req.messages) count(m.content);
  const prompt = { wide, other };
  wide = 0;
  other = 0;
  count(req.outputSchema + req.outputName);
  const schema = { wide, other };
  const messages = req.messages.length + 1;
  const low =
    prompt.wide * 0.6 + prompt.other * 0.22 + schema.wide * 0.6 + schema.other * 0.22 + messages * 4 + 10 + expectedOutput * 0.5;
  const high =
    prompt.wide * 1.3 + prompt.other * 0.34 + (schema.wide * 1.3 + schema.other * 0.34) * 2 + messages * 8 + 40 + expectedOutput * 1.5;
  return { low: Math.floor(low), high: Math.ceil(high) };
}

function addRanges(ranges: readonly TokenRange[]): TokenRange {
  return ranges.reduce((s, r) => ({ low: s.low + r.low, high: s.high + r.high }), { low: 0, high: 0 });
}

/** Two significant figures, rounded the way that widens the range. */
function rough(n: number, up: boolean): number {
  if (n < 100) return up ? Math.ceil(n) : Math.floor(n);
  const unit = 10 ** (String(Math.floor(n)).length - 2);
  return (up ? Math.ceil(n / unit) : Math.floor(n / unit)) * unit;
}

function wan(n: number, up: boolean): string {
  const tenths = up ? Math.ceil(n / 1000) : Math.floor(n / 1000);
  return (tenths / 10).toFixed(1).replace(/\.0$/, "");
}

/**
 * formatTokens says a token count the way the card does: a range
 * "about 800–1,200" / "about 12–18 thousand", or one number "8,000" / "50 thousand" (the
 * task limit).
 */
export function formatTokens(n: number | TokenRange): string {
  if (typeof n === "number") {
    return n < 10000 ? num(Math.round(n)) : t("{n} 万", { n: String(Math.round(n / 1000) / 10) });
  }
  if (n.high < 10000) {
    const low = num(rough(n.low, false));
    const high = num(rough(n.high, true));
    return low === high ? t("约 {n}", { n: low }) : t("约 {low}–{high}", { low, high });
  }
  const low = wan(n.low, false);
  const high = wan(n.high, true);
  return low === high ? t("约 {n} 万", { n: low }) : t("约 {low}–{high} 万", { low, high });
}

/** The explicit reply language keeps English instructions independent of the UI locale. */
export function replyLanguage(): string {
  const names = { "zh-Hans": "Simplified Chinese", "zh-Hant": "Traditional Chinese", en: "English", ja: "Japanese", ko: "Korean" };
  return names[i18n.locale];
}

/* ---------------- strict reading ---------------- */
// Exported for agent.ts, whose tool arguments are model output too.

export function objectOf(output: unknown, where: string): Record<string, unknown> {
  let value = output;
  if (typeof value === "string") {
    try {
      value = JSON.parse(value);
    } catch {
      throw new ModelOutputError(t("模型的回答不是 JSON"));
    }
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new ModelOutputError(t("{where}应为对象", { where }));
  return value as Record<string, unknown>;
}

export function text(o: Record<string, unknown>, key: string, max: number): string {
  const v = o[key];
  if (v === undefined || v === null) return "";
  if (typeof v !== "string") throw new ModelOutputError(t("{key} 应为文字", { key }));
  return Array.from(v.replace(/[\x00-\x1F]+/g, " ").trim()).slice(0, max).join("");
}

export function integer(o: Record<string, unknown>, key: string, min: number, max: number): number | null {
  const v = o[key];
  if (v === undefined || v === null) return null;
  if (typeof v !== "number" || !Number.isFinite(v)) throw new ModelOutputError(t("{key} 应为数字", { key }));
  return Math.min(max, Math.max(min, Math.round(v)));
}

export function flag(o: Record<string, unknown>, key: string): boolean {
  const v = o[key];
  if (v === undefined || v === null) return false;
  if (typeof v !== "boolean") throw new ModelOutputError(t("{key} 应为 true 或 false", { key }));
  return v;
}

export function choice<T extends string>(o: Record<string, unknown>, key: string, allowed: readonly T[], fallback: T): T {
  const v = o[key];
  if (v === undefined || v === null) return fallback;
  if (typeof v !== "string") throw new ModelOutputError(t("{key} 应为文字", { key }));
  const found = allowed.find((a) => a === v);
  if (found === undefined) throw new ModelOutputError(t("{key} 的取值「{value}」不在允许范围内", { key, value: v.slice(0, 40) }));
  return found;
}

export function list(o: Record<string, unknown>, key: string): unknown[] {
  const v = o[key];
  if (v === undefined || v === null) return [];
  if (!Array.isArray(v)) throw new ModelOutputError(t("{key} 应为数组", { key }));
  return v;
}

export function textList(o: Record<string, unknown>, key: string, maxItems: number, maxLength: number): string[] {
  const out: string[] = [];
  for (const v of list(o, key)) {
    if (typeof v !== "string") throw new ModelOutputError(t("{key} 里应为文字", { key }));
    const item = Array.from(v.trim()).slice(0, maxLength).join("");
    if (item && !out.includes(item)) out.push(item);
  }
  return out.slice(0, maxItems);
}

export function day(o: Record<string, unknown>, key: string): string {
  const v = text(o, key, 10);
  if (!v) return "";
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
  const d = m ? new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]))) : null;
  if (!m || !d || d.getUTCMonth() !== Number(m[2]) - 1 || d.getUTCDate() !== Number(m[3])) {
    throw new ModelOutputError(t("{key} 应为 YYYY-MM-DD 日期", { key }));
  }
  return v;
}

export function schemaText(schema: object): string {
  return JSON.stringify(schema);
}

/** oneLine keeps a name from breaking the request's line format. */
export function oneLine(s: string): string {
  return s.replace(/[\r\n\t|]+/g, " ").trim();
}

/* ---------------- Media Organization · Identification ---------------- */

const TYPES = ["series", "movie", "special", "subtitle", "junk", "unknown"] as const;
const YEAR_SOURCES = ["filename", "guess", "none"] as const;
const ROLES = ["episode", "season", "other"] as const;

const IDENTIFY_SCHEMA = {
  type: "object",
  properties: {
    groups: {
      type: "array",
      items: {
        type: "object",
        properties: {
          id: { type: "string", description: "Candidate group ID, e.g. g1" },
          type: { type: "string", enum: [...TYPES] },
          title: { type: "string", description: "Series or movie title" },
          year: { type: "integer" },
          yearSource: { type: "string", enum: [...YEAR_SOURCES] },
          season: { type: "integer" },
          numbers: { type: "array", items: { type: "string", enum: [...ROLES] }, description: "Corresponds to {n1} {n2} … in order" },
          episode: { type: "integer", description: "Episode number when the group has no variables" },
          language: { type: "string", description: "Subtitle language tag, e.g. chs, cht, eng" },
          folder: { type: "string", description: "Top-level folder the user requested for separate storage; otherwise empty" },
          reason: { type: "string", description: "Reason for junk or unknown" },
        },
        required: ["id", "type"],
      },
    },
  },
  required: ["groups"],
};

// Instructions are English; identifySystem explicitly selects the reply language.
const IDENTIFY_SYSTEM = [
  "You are a cloud drive movie and TV organization assistant. The code has already divided the user's files into candidate groups based on filename characteristics: each group provides a filename pattern ({n1} {n2} … are numbers that vary within the group), statistics, and a few samples. Identify each group individually. Output only JSON, and do not add any other fields.",
  "- type: series for TV series; movie for movies; special for specials (SP, OVA, OAD, theatrical editions, side stories); subtitle for subtitles; junk for leftover files (.torrent, .url, advertising images, promotional text, temporary files from download tools, etc.); unknown for unidentifiable files.",
  "- title: The series or movie title, preserving the language and wording used in the filename and removing tags such as subtitle group, resolution, and encoding; use the same title for the same work across different groups.",
  "- year: Fill in the year if it appears in the filename or path, with yearSource set to filename; if no year appears, fill it in only if you are very certain of the premiere or release year, with yearSource set to guess; otherwise omit it and set yearSource to none. Do not fabricate years.",
  "- season: The season number; use 1 if not explicitly specified.",
  "- numbers: Specify in order whether each variable is episode (episode number), season (season number), or other (resolution, year, ID, etc.). If the group has no variables, use an empty array and provide the episode number in episode.",
  "- subtitle: title, season, and numbers must match the corresponding video; set language to a language tag such as chs, cht, eng, or jpn.",
  "- For junk and unknown, write a one-sentence explanation in reason.",
  "- folder: Fill in a folder name only if the user explicitly requests placing a category in a separate top-level folder (e.g. “keep anime separate”); otherwise leave it empty.",
].join("\n");

/** Reasons follow the UI language; titles retain the filenames' original language. */
function identifySystem(): string {
  return `${IDENTIFY_SYSTEM}\n- Write reason in ${replyLanguage()}; title must preserve the language used in the filename.`;
}

const KIND_LABEL: Record<string, string> = { video: "Video", subtitle: "Subtitles", audio: "Audio", other: "Other" };

function folderLabel(scopeName: string, dir: readonly string[]): string {
  return oneLine([scopeName, ...dir].join("/"));
}

/** groupText is how one group goes out; the line prefixes are what the disclosure counts. */
function groupText(g: MediaGroup, scopeName: string): string {
  const vars = g.variables.map((v) => `${v.name} ${v.min}–${v.max} (${v.distinct} values)`).join(", ");
  const exts = Object.entries(g.exts)
    .map(([e, n]) => `${e || "None"}×${n}`)
    .join(" ");
  const shown = g.dirs.slice(0, 3).map((d) => folderLabel(scopeName, d));
  const more = g.dirs.length > shown.length ? `(another ${g.dirs.length - shown.length} folders)` : "";
  const size = g.size.min === g.size.max ? bytes(g.size.max, "0 B", 1) : `${bytes(g.size.min, "0 B", 1)}–${bytes(g.size.max, "0 B", 1)}`;
  const lines = [
    `${g.id} · ${KIND_LABEL[g.kind]} · ${g.files.length} files${vars ? ` · Variables ${vars}` : ""}`,
    `  Pattern: ${oneLine(g.pattern)}`,
    `  Location: ${shown.join("; ")}${more}`,
    `  Extension ${exts} · Size ${size}`,
    ...g.samples.map(
      (f) => `  Sample: ${oneLine(f.name)} | ${folderLabel(scopeName, f.dir)} | ${bytes(f.size, "0 B", 1)} | ${(f.modified || "").slice(0, 10)}`,
    ),
  ];
  return lines.join("\n");
}

function identifyOutput(groups: readonly MediaGroup[]): number {
  return 20 + groups.reduce((sum, g) => sum + 45 + g.variables.length * 4, 0);
}

function countIn(requests: readonly ModelRequest[], re: RegExp): number {
  return requests.reduce((sum, r) => sum + r.messages.reduce((s, m) => s + (m.content.match(re) ?? []).length, 0), 0);
}

export interface IdentifyBatch {
  request: ModelRequest;
  expectedOutput: number;
  /** The group ids this request asks about. */
  groups: string[];
}

export interface IdentifyTask {
  batches: IdentifyBatch[];
  disclosure: Disclosure;
}

/** Groups per request: past this a model starts dropping groups from its answer. */
export const BATCH_GROUPS = 60;

/**
 * identify asks what the groups are, in batches: a batch closes at
 * BATCH_GROUPS groups or when its estimated input passes `batchBudget`
 * tokens. One disclosure covers the task; its counts are read back from the
 * built requests, so the card and the wire cannot disagree. Callers send
 * only the groups parse.ts left at low confidence: the rest are known
 * without a model, and a TMDB-matched group must not go out with samples.
 */
export function identify(
  groups: readonly MediaGroup[],
  instructions: string,
  scopeName: string,
  batchBudget = 12000,
): IdentifyTask {
  const head = `Scope:${oneLine(scopeName)}\nAdditional user requirements:${instructions.trim() || "None"}\nCandidate groups:\n`;
  const system = identifySystem();
  const make = (list: readonly MediaGroup[]): IdentifyBatch => {
    const expectedOutput = identifyOutput(list);
    return {
      request: {
        system,
        messages: [{ role: "user", content: head + list.map((g) => groupText(g, scopeName)).join("\n") }],
        outputName: "identify",
        outputSchema: schemaText(IDENTIFY_SCHEMA),
        maxTokens: Math.min(32000, Math.max(2048, expectedOutput * 2)),
      },
      expectedOutput,
      groups: list.map((g) => g.id),
    };
  };
  const batches: IdentifyBatch[] = [];
  let current: MediaGroup[] = [];
  for (const g of groups) {
    const next = [...current, g];
    if (current.length && (next.length > BATCH_GROUPS || estimateTokens(make(next).request, 0).high > batchBudget)) {
      batches.push(make(current));
      current = [g];
    } else current = next;
  }
  if (current.length) batches.push(make(current));

  const requests = batches.map((b) => b.request);
  const n = countIn(requests, /^g\d+ · /gm);
  const m = countIn(requests, /^ {2}Sample: /gm);
  return {
    batches,
    disclosure: {
      title: t("影视识别"),
      sends: t("{groups} 个候选组的统计信息，以及 {files} 个样本文件名、所在路径、大小与修改时间。路径里的文件夹名也会发送。", { groups: n, files: m }),
      files: m,
      estimate: addRanges(batches.map((b) => estimateTokens(b.request, b.expectedOutput))),
    },
  };
}

/**
 * parseIdentities reads an identify (or follow-up) answer for `groups`: ids
 * not asked about and repeats are dropped, a group the model left out is
 * simply absent, every field is checked. `numbers` is fitted to the group's
 * variables (missing ones are "other").
 */
export function parseIdentities(output: unknown, groups: readonly Pick<MediaGroup, "id" | "variables">[]): Identity[] {
  const o = objectOf(output, t("回答"));
  if (!Array.isArray(o.groups)) throw new ModelOutputError(t("{key} 应为数组", { key: "groups" }));
  const asked = new Map(groups.map((g) => [g.id, g]));
  const out: Identity[] = [];
  const seen = new Set<string>();
  for (const item of o.groups) {
    const row = objectOf(item, t("groups 里的每一项"));
    const id = text(row, "id", 16);
    const group = asked.get(id);
    const type: IdentityType = choice(row, "type", TYPES, "unknown");
    const roles: NumberRole[] = list(row, "numbers").map((v) => {
      const found = ROLES.find((r) => r === v);
      if (found === undefined) throw new ModelOutputError(t("{key} 的取值「{value}」不在允许范围内", { key: "numbers", value: String(v).slice(0, 20) }));
      return found;
    });
    let year = integer(row, "year", 0, 9999);
    if (year !== null && (year < 1880 || year > 2100)) year = null;
    let yearSource = choice(row, "yearSource", YEAR_SOURCES, year === null ? "none" : "guess");
    if (year === null) yearSource = "none";
    const language = text(row, "language", 16);
    const identity: Identity = {
      id,
      type,
      title: text(row, "title", 200),
      year,
      yearSource,
      source: "model",
      season: integer(row, "season", 0, 999),
      numbers: [],
      episode: integer(row, "episode", 0, 99999),
      language: /^[\p{L}\p{N}&+_-]+$/u.test(language) ? language.toLowerCase() : "",
      folder: text(row, "folder", 100),
      reason: text(row, "reason", 200),
    };
    if (!group || seen.has(id)) continue;
    seen.add(id);
    identity.numbers = group.variables.map((_, i) => roles[i] ?? "other");
    out.push(identity);
  }
  return out;
}

/* ---------------- Media Organization · Add One Sentence ---------------- */

/**
 * followUp re-asks with the user's extra sentence: patterns and the last
 * answers only, no paths or samples. A group whose identity came from TMDB
 * goes as its pattern and 「Matched」 — its title and year are TMDB data and
 * stay on this machine.
 */
export function followUp(
  previous: readonly Identity[],
  groups: readonly Pick<MediaGroup, "id" | "pattern">[],
  instruction: string,
): Prepared {
  const byId = new Map(previous.map((i) => [i.id, i]));
  const lines = groups.map((g) => {
    const i = byId.get(g.id);
    let last = "Unrecognized";
    if (i?.source === "tmdb") last = "Matched";
    else if (i) {
      last = JSON.stringify({
        type: i.type,
        title: i.title,
        year: i.year,
        yearSource: i.yearSource,
        season: i.season,
        numbers: i.numbers,
        episode: i.episode,
        language: i.language,
        folder: i.folder,
      });
    }
    return `${g.id} · Mode:${oneLine(g.pattern)} · Previous round:${last}`;
  });
  const request: ModelRequest = {
    system: [
      identifySystem(),
      "This is a revision of the previous round's recognition results: return only the groups that need to change because of this user message, providing the complete new result for each group.",
      "For groups marked as “Matched” in the previous round, the title and year have already been determined: only folder may be modified, and only id and folder should be returned.",
    ].join("\n"),
    messages: [{ role: "user", content: `Additional user input:${instruction.trim()}\nCandidate groups and previous round's results:\n${lines.join("\n")}` }],
    outputName: "identify",
    outputSchema: schemaText(IDENTIFY_SCHEMA),
    maxTokens: 8192,
  };
  const expectedOutput = 20 + Math.min(groups.length, 40) * 50;
  const n = (request.messages[0].content.match(/^g\d+ · /gm) ?? []).length;
  return {
    request,
    expectedOutput,
    disclosure: {
      title: t("按补充要求重新识别"),
      sends: t("{groups} 个候选组的文件名模式和上一轮识别结果，以及你补充的这句话。不发送路径和样本。", { groups: n }),
      files: 0,
      estimate: estimateTokens(request, expectedOutput),
    },
  };
}

/**
 * mergeIdentities is the previous answer with the follow-up's groups
 * replaced. A TMDB-matched group takes only the new folder:
 * the model never saw its identity, so nothing else it says about it counts.
 */
export function mergeIdentities(previous: readonly Identity[], updates: readonly Identity[]): Identity[] {
  const next = new Map(updates.map((u) => [u.id, u]));
  const out = previous.map((p) => {
    const u = next.get(p.id);
    if (!u) return p;
    return p.source === "tmdb" ? { ...p, folder: u.folder } : u;
  });
  const known = new Set(previous.map((p) => p.id));
  for (const u of updates) if (!known.has(u.id)) out.push(u);
  return out;
}
