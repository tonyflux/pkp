/*
 * AI Assistant · Conversation Agent: one conversation
 * turn as a pure loop over injected dependencies — no store, no bindings.
 * The model sees the user's words, the earlier turns' texts and what the
 * local read-only tools found, under short handles (f1, c1 …): real ids
 * never leave the machine. It cannot write: the only way to change the
 * drive is a plan tool, whose card the user checks and confirms through the
 * existing Organize / transfer / share calls. Every tool call is parsed
 * strictly; anything unknown goes back to the model as an error and is
 * never executed. One consent covers the whole turn.
 */
import {
  choice,
  day,
  estimateTokens,
  flag,
  integer,
  list,
  ModelOutputError,
  objectOf,
  oneLine,
  replyLanguage,
  schemaText,
  text,
  textList,
  type ModelRequest,
  type TokenRange,
} from "./aiprompt";
import { presentSearchGroups } from "./assistantresults";
import { bytes } from "./format";
import { N_, t } from "./i18n";
import type { FileClass } from "./media";
import { nameError, PLAN_LIMIT } from "./organizer";
import { emptyQuery, localDay, type AIAggregate, type AIQuery, type Played } from "./searchquery";

/* ---------------- wire shapes (services.AIChat*, json names as Go) ---------------- */

export interface AITool {
  name: string;
  description: string;
  /** JSON Schema as text. */
  schema: string;
}

export interface AIToolCall {
  id: string;
  name: string;
  /** JSON text. */
  arguments: string;
}

export interface AIChatMessage {
  role: "user" | "assistant" | "tool";
  content: string;
  toolCalls: AIToolCall[];
  toolCallId: string;
}

export interface AIChatRequest {
  system: string;
  messages: AIChatMessage[];
  tools: AITool[];
  stream: boolean;
}

export interface AIChatReply {
  text: string;
  toolCalls: AIToolCall[] | null;
  /** "end" | "tool" | "length" */
  stop: string;
  input: number;
  outputTokens: number;
}

/** services.AIReply: the structured mode json-emulation runs on (AIComplete). */
export interface AIJsonReply {
  output: string;
  input: number;
  outputTokens: number;
}

/**
 * The exact message Go fails a tools request with when the model has none
 * (G-AI errAINoTools). Go translates it, so both the source and the
 * translation count.
 */
export const NO_TOOLS_MESSAGE = "这个模型不支持工具调用"; // i18n-ignore: Go's text, compared

export function isNoTools(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  return message.includes(NO_TOOLS_MESSAGE) || message.includes(t("这个模型不支持工具调用"));
}

/**
 * How Go opens the message of a request the service's content check turned
 * down (services.aiRefusedWords), in the source or the UI language.
 */
export const CONTENT_REFUSED = "模型服务的内容审核拒绝了这次请求"; // i18n-ignore: Go's text, compared

export function isContentRefused(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  return message.startsWith(CONTENT_REFUSED) || message.startsWith(t("模型服务的内容审核拒绝了这次请求"));
}

/* ---------------- handles ---------------- */

export type HandleKind = "file" | "folder" | "task";

export interface HandleRecord {
  /** "f12" for a file or folder, "c3" for a cloud-add task. */
  handle: string;
  id: string;
  kind: HandleKind;
  /** Folders: their path from the root ("Movies/Documentaries"), so search_files can take a handle for `under`. */
  path: string;
}

/** HandleSnapshot is how a conversation saves its handles (plain JSON). */
export interface HandleSnapshot {
  records: HandleRecord[];
}

const HANDLE = /^([fc])([1-9]\d{0,6})$/;

/**
 * Handles maps real ids to the short names the model sees, per
 * conversation: the same file keeps its handle across turns, so 「Move the second
 * one away」 in a later turn still points at what the model was shown.
 */
export class Handles {
  private readonly byHandle = new Map<string, HandleRecord>();
  private readonly byId = new Map<string, HandleRecord>();
  private files = 0;
  private tasks = 0;

  constructor(snapshot?: HandleSnapshot) {
    for (const r of snapshot?.records ?? []) {
      const m = HANDLE.exec(r.handle);
      if (!m || !r.id || this.byHandle.has(r.handle) || (m[1] === "c") !== (r.kind === "task")) continue;
      this.add({ handle: r.handle, id: r.id, kind: r.kind, path: r.path || "" });
      if (m[1] === "f") this.files = Math.max(this.files, Number(m[2]));
      else this.tasks = Math.max(this.tasks, Number(m[2]));
    }
  }

  private add(r: HandleRecord) {
    this.byHandle.set(r.handle, r);
    this.byId.set(`${r.kind === "task" ? "c" : "f"}:${r.id}`, r);
  }

  /** of is the handle for an id, made on first sight; a folder's path is kept up to date. */
  of(id: string, kind: HandleKind, path = ""): string {
    const known = this.byId.get(`${kind === "task" ? "c" : "f"}:${id}`);
    if (known) {
      if (path) known.path = path;
      return known.handle;
    }
    const handle = kind === "task" ? `c${++this.tasks}` : `f${++this.files}`;
    this.add({ handle, id, kind, path });
    return handle;
  }

  /** peek is the handle an id already has, without making one. */
  peek(id: string, kind: HandleKind): string | undefined {
    return this.byId.get(`${kind === "task" ? "c" : "f"}:${id}`)?.handle;
  }

  resolve(handle: string): HandleRecord | undefined {
    return this.byHandle.get(handle.trim().toLowerCase());
  }

  get size(): number {
    return this.byHandle.size;
  }

  snapshot(): HandleSnapshot {
    return { records: [...this.byHandle.values()].map((r) => ({ ...r })) };
  }
}

/* ---------------- what the store persists ---------------- */

export type AgentMode = "tools" | "json";

export type TraceState = "running" | "done" | "failed";

/** Trace is one tool step as the turn shows it: 「Queried local index」, never the raw JSON. */
export interface Trace {
  tool: string;
  label: string;
  state: TraceState;
  /** The tool's own words on success, the reason on failure. */
  detail: string;
}

export interface CardRow {
  /** The real id: cards stay on this computer. */
  id: string;
  name: string;
  folder: boolean;
  /** The folder it is in, "Movies/Documentaries" ("" = the root). */
  path: string;
  size: number;
  modified: string;
  /** 「Watched」「Didn't finish 42%」 or "". */
  played: string;
}

export interface CardAggregateRow {
  /** Folder id for by=folder, else the category or extension. */
  key: string;
  label: string;
  size: number;
  count: number;
}

export type CardState = "draft" | "running" | "done" | "failed" | "cancelled" | "undone";

interface CardBase {
  id: string;
  title: string;
  state: CardState;
  /** A receipt or a reason, as the card says it. */
  note: string;
}

/** A search, a folder's children, recent plays: rows the user can open or locate. */
export interface FilesCard extends CardBase {
  kind: "files";
  total: number;
  rows: CardRow[];
  aggregate: CardAggregateRow[];
  query: AIQuery | null;
}

/** 「What's here」 for one folder. */
export interface StatsCard extends CardBase {
  kind: "stats";
  folderId: string;
  path: string;
  stats: FolderStatsLike | null;
}

/**
 * A plan made by organize_media or propose_changes: the Change list lives in
 * the store under planId; the card carries what the conversation shows of
 * it and what became of it.
 */
export interface PlanCard extends CardBase {
  kind: "plan";
  tool: "organize_media" | "propose_changes";
  planId: string;
  scopeId: string;
  scopePath: string;
  counts: { changes: number; kept: number; trash: number; conflicts: number };
  sample: { from: string; to: string } | null;
  /** Where the names come from: 「Data from TMDB」「Generated by local rules」. */
  source: "" | "rules" | "tmdb" | "model";
  /** The organize record once it ran: Undo goes through it. */
  recordId: string;
}

export interface OfflineCard extends CardBase {
  kind: "offline";
  urls: string[];
  /** Earlier cloud-add tasks to add again, by their real ids. */
  taskIds: string[];
  folderId: string;
  path: string;
}

export interface DownloadCard extends CardBase {
  kind: "download";
  rows: CardRow[];
}

export interface ShareCard extends CardBase {
  kind: "share";
  rows: CardRow[];
}

export type Card = FilesCard | StatsCard | PlanCard | OfflineCard | DownloadCard | ShareCard;

/** Explicit presentation of this turn's searches; source ids and selected file ids stay local. */
export interface SearchResultGroup {
  title: string;
  sourceIds: string[];
  /** Absent means all available rows in the selected sources. */
  fileIds?: string[];
}

/** Why a turn stopped. */
export type TurnStop = "end" | "length" | "limit" | "budget" | "cancel" | "denied" | "error";

export interface AssistantScope {
  folder?: { id: string; name: string }[];
  entries?: { id: string; name: string; folder: boolean }[];
}

/** Turn is one exchange: the user's words and everything the assistant did with them. JSON as is. */
export interface Turn {
  id: string;
  /** ISO time the turn started. */
  at: string;
  user: string;
  /** Local range snapshot; real ids are never serialized into model history. */
  scope?: AssistantScope;
  /** The assistant's text, every step's words joined. */
  text: string;
  traces: Trace[];
  cards: Card[];
  /** Absent: no explicit selection; []: no relevant search results. */
  resultGroups?: SearchResultGroup[];
  usage: { input: number; output: number; calls: number };
  stop: TurnStop;
  /** The failure, as the page shows it. */
  error: string;
  /** Consent refused: nothing was sent. */
  denied: boolean;
  /** Why the turn ended early when it did: 「Canceled」「This conversation's usage limit has been reached」…; "" otherwise. */
  notice: string;
  mode: AgentMode;
  /** Set when this turn fell back to single-step mode: the store says so once. */
  modeNote: string;
}

/**
 * retryable: the turn ended on an error and left nothing the user acted on
 * (every plan-like card still a draft), so Retry may ask again in its place.
 */
export function retryable(t: Turn): boolean {
  return t.stop === "error" && t.cards.every((c) => c.state === "draft" || c.kind === "files" || c.kind === "stats");
}

/* ---------------- tools ---------------- */

export const READ_TOOLS = [
  "search_files",
  "show_results",
  "find_folders",
  "folder_summary",
  "file_info",
  "recent_plays",
  "transfer_status",
  "storage_quota",
  "cleanup_results",
] as const;

export const PLAN_TOOLS = ["organize_media", "propose_changes", "propose_offline_add", "propose_download", "propose_share"] as const;

export type ToolName = (typeof READ_TOOLS)[number] | (typeof PLAN_TOOLS)[number];

/** A folder the model named: a handle (id set) or a path from the root (id "", resolved by the tool). */
export interface FolderRef {
  id: string;
  /** "" with id "" is the root. */
  path: string;
}

/** A file or folder the model named by handle. */
export interface Target {
  id: string;
  folder: boolean;
}

export interface ProposedItem extends Target {
  /** Folder names from the root; null = stays where it is. */
  toPath: string[] | null;
  /** The new name; "" = keeps its name. */
  name: string;
  /** Move to Trash (never a permanent delete). */
  trash: boolean;
}

/** ToolArgsMap is each tool's arguments after strict parsing, handles already resolved to real ids. */
export interface ToolArgsMap {
  search_files: { query: AIQuery };
  show_results: { groups: { title: string; searches: number[]; fileIds?: string[] }[] };
  find_folders: { path: string };
  folder_summary: { folder: FolderRef };
  file_info: { target: Target };
  recent_plays: Record<string, never>;
  transfer_status: Record<string, never>;
  storage_quota: Record<string, never>;
  cleanup_results: Record<string, never>;
  organize_media: { folder: FolderRef | null; targets: Target[]; instructions: string };
  propose_changes: { title: string; items: ProposedItem[] };
  propose_offline_add: { urls: string[]; taskIds: string[]; folder: FolderRef | null };
  propose_download: { targets: Target[] };
  propose_share: { targets: Target[] };
}

export type ParsedCall = { [K in ToolName]: { tool: K; args: ToolArgsMap[K] } }[ToolName];

/** What a tool did: the text for the model (built with the *ForModel helpers), the trace line, maybe a card. */
export interface ToolOutcome {
  forModel: string;
  trace: string;
  card?: Card;
}

export interface ToolContext {
  handles: Handles;
  turnId: string;
  signal?: AbortSignal;
}

export type ToolImpl<K extends ToolName> = (args: ToolArgsMap[K], ctx: ToolContext) => Promise<ToolOutcome>;

export type ToolImpls = { [K in Exclude<ToolName, "show_results">]?: ToolImpl<K> };

// Tool descriptions and schemas are English instructions for the model.
// running/done/failed are translated user-facing trace labels (N_ keys).
interface ToolDef {
  name: ToolName;
  description: string;
  schema: object;
  plan: boolean;
  running: string;
  done: string;
  /** The trace label when the page has no implementation for the tool. */
  failed: string;
}

const HANDLE_FIELD = { type: "string", description: "ID in the tool results, e.g. f12" };
const HANDLES_FIELD = { type: "array", items: HANDLE_FIELD, description: "IDs in the tool results, e.g. [\"f3\", \"f4\"]" };
const FOLDER_FIELD = {
  type: "string",
  description: "Folder: a folder ID in the tool results (e.g. f7), or a path from the root (e.g. Videos/Documentaries)",
};
const DATE_FIELD = { type: "string", description: "YYYY-MM-DD, an absolute date calculated relative to today, inclusive; omit if there is no time restriction" };

const CATEGORIES = ["", "video", "image", "document", "other"] as const;
const STARRED = ["", "yes", "no"] as const;
const PLAYED: readonly Played[] = ["", "any", "unfinished", "finished"];
const KINDS = ["", "file", "folder"] as const;
const SORTS = ["", "modified", "created", "size", "name", "played"] as const;
const AGGREGATE_BY = ["folder", "category", "ext"] as const;
const MEASURES = ["size", "count"] as const;

const NO_ARGS = { type: "object", properties: {} };

export const TOOL_DEFS: readonly ToolDef[] = [
  {
    name: "search_files",
    description: "Find files or folders in the local index by criteria, or aggregate sizes by folder/type/extension. Returns at most 40 items and the total count.",
    plan: false,
    running: N_("正在查询本地索引"),
    done: N_("已查询本地索引"),
    failed: N_("无法查询本地索引"),
    schema: {
      type: "object",
      properties: {
        keywords: { type: "array", items: { type: "string" }, description: "Name keywords; any match is sufficient. May include synonyms or translated names, at most 10" },
        category: { type: "string", enum: [...CATEGORIES], description: "video videos, image images, document documents, other other types" },
        ext: { type: "array", items: { type: "string" }, description: "File extension, lowercase without the dot" },
        minSize: { type: "integer", description: "Minimum size in bytes (inclusive)" },
        maxSize: { type: "integer", description: "Maximum size in bytes (inclusive)" },
        createdAfter: DATE_FIELD,
        createdBefore: DATE_FIELD,
        modifiedAfter: DATE_FIELD,
        modifiedBefore: DATE_FIELD,
        under: FOLDER_FIELD,
        starred: { type: "string", enum: [...STARRED], description: "yes starred only, no exclude starred" },
        played: { type: "string", enum: [...PLAYED], description: "any previously played, unfinished not finished watching, finished finished watching" },
        kind: { type: "string", enum: [...KINDS], description: "Files only or folders only" },
        sort: { type: "string", enum: [...SORTS], description: "Sort key, defaults to modified" },
        asc: { type: "boolean", description: "Ascending order; defaults to descending" },
        limit: { type: "integer", description: "Item count 1–40" },
        aggregate: {
          type: "object",
          description: "Specify when requesting statistics (e.g. “Which folder takes up the most space?”); returns grouped totals rather than files",
          properties: {
            by: { type: "string", enum: [...AGGREGATE_BY] },
            measure: { type: "string", enum: [...MEASURES] },
            limit: { type: "integer", description: "1–50, defaults to 10" },
          },
          required: ["by"],
        },
      },
    },
  },
  {
    name: "show_results",
    description: "Select the final results relevant to the user's question from searches in this turn. Intermediate searches are collapsed in the process and are not automatically displayed as final results. Group by search ID; use handles to select only relevant files. If there are no relevant results, use groups=[]. Statistical searches must each be in a separate group and must not include handles. Further searches will clear this selection.",
    plan: false,
    running: N_("正在选择搜索结果"),
    done: N_("已选择搜索结果"),
    failed: N_("无法选择搜索结果"),
    schema: {
      type: "object",
      properties: {
        groups: {
          type: "array",
          maxItems: 8,
          items: {
            type: "object",
            properties: {
              title: { type: "string", minLength: 1, maxLength: 80 },
              searches: { type: "array", minItems: 1, maxItems: 64, items: { type: "integer", minimum: 1 } },
              handles: { ...HANDLES_FIELD, minItems: 1, maxItems: 320 },
            },
            required: ["title", "searches"],
          },
        },
      },
      required: ["groups"],
    },
  },
  {
    name: "find_folders",
    description: "Find folders by path or folder name, returning matching folder IDs and full paths.",
    plan: false,
    running: N_("正在查找文件夹"),
    done: N_("已查找文件夹"),
    failed: N_("无法查找文件夹"),
    schema: {
      type: "object",
      properties: { path: { type: "string", description: "Path from the root (e.g. Videos/Documentaries) or folder name" } },
      required: ["path"],
    },
  },
  {
    name: "folder_summary",
    description:
      "Get statistics for a folder (including subfolders): file count, size, type distribution, duplicate files, unfinished videos, the largest items, and at most 40 directly contained items. If folder is omitted, get statistics for the entire cloud drive.",
    plan: false,
    running: N_("正在统计文件夹"),
    done: N_("已统计文件夹"),
    failed: N_("无法统计文件夹"),
    schema: { type: "object", properties: { folder: FOLDER_FIELD } },
  },
  {
    name: "file_info",
    description: "Read details of a file or folder: size, timestamps, type, resolution, playback progress, etc.",
    plan: false,
    running: N_("正在读取文件详情"),
    done: N_("已读取文件详情"),
    failed: N_("无法读取文件详情"),
    schema: { type: "object", properties: { handle: HANDLE_FIELD }, required: ["handle"] },
  },
  {
    name: "recent_plays",
    description: "Recently played videos and their playback progress.",
    plan: false,
    running: N_("正在读取播放记录"),
    done: N_("已读取播放记录"),
    failed: N_("无法读取播放记录"),
    schema: NO_ARGS,
  },
  {
    name: "transfer_status",
    description: "Status of cloud-add tasks and local transfers; cloud-add task IDs start with c.",
    plan: false,
    running: N_("正在读取传输状态"),
    done: N_("已读取传输状态"),
    failed: N_("无法读取传输状态"),
    schema: NO_ARGS,
  },
  {
    name: "storage_quota",
    description: "Cloud drive storage usage, quota, and usage by type.",
    plan: false,
    running: N_("正在读取空间与配额"),
    done: N_("已读取空间与配额"),
    failed: N_("无法读取空间与配额"),
    schema: NO_ARGS,
  },
  {
    name: "cleanup_results",
    description: "Results of the last cleanup scan: duplicate file groups and empty folders.",
    plan: false,
    running: N_("正在读取清理扫描结果"),
    done: N_("已读取清理扫描结果"),
    failed: N_("无法读取清理扫描结果"),
    schema: NO_ARGS,
  },
  {
    name: "organize_media",
    description:
      "Organize movies and TV shows: categorize by series and movie, and rename using the user's naming template, with identification based on local rules and a reference database. Provide folders or IDs (at least one) to generate a plan card. Execute only after user confirmation.",
    plan: true,
    running: N_("正在生成整理计划"),
    done: N_("已生成整理计划"),
    failed: N_("无法生成整理计划"),
    schema: {
      type: "object",
      properties: {
        folder: FOLDER_FIELD,
        handles: HANDLES_FIELD,
        instructions: { type: "string", description: "Additional organization requirements from the user (e.g. “Keep anime separate”); omit if none" },
      },
    },
  },
  {
    name: "propose_changes",
    description:
      "General plan for moving, renaming, or moving items to the recycle bin (not movie and TV organization). Provide an ID and destination for each item; generate a plan card and execute only after the user reviews each item and confirms. Does not permanently delete anything.",
    plan: true,
    running: N_("正在生成计划草案"),
    done: N_("已生成计划草案"),
    failed: N_("无法生成计划草案"),
    schema: {
      type: "object",
      properties: {
        title: { type: "string", description: "Plan title, one sentence, no more than 30 characters" },
        items: {
          type: "array",
          items: {
            type: "object",
            properties: {
              handle: HANDLE_FIELD,
              toPath: { type: "array", items: { type: "string" }, description: "Names of each level of the destination folder from the root, e.g. [\"Videos\", \"Watched\"]; omit if not moving" },
              name: { type: "string", description: "New name (including extension); omit if not renaming" },
              trash: { type: "boolean", description: "Move to the recycle bin" },
            },
            required: ["handle"],
          },
        },
      },
      required: ["title", "items"],
    },
  },
  {
    name: "propose_offline_add",
    description: "Cloud-add draft: save user-provided links or cloud-add tasks to be added again (IDs starting with c) to a folder. Add only after user confirmation.",
    plan: true,
    running: N_("正在准备云添加草案"),
    done: N_("已准备云添加草案"),
    failed: N_("无法准备云添加草案"),
    schema: {
      type: "object",
      properties: {
        urls: { type: "array", items: { type: "string" }, description: "Links provided in the user's message (magnet, http, ed2k, etc.)" },
        tasks: { type: "array", items: { type: "string" }, description: "Cloud-add task IDs to add again, e.g. c2" },
        folder: FOLDER_FIELD,
      },
    },
  },
  {
    name: "propose_download",
    description: "Local download draft: IDs of files or folders to download. Start only after user confirmation.",
    plan: true,
    running: N_("正在准备下载草案"),
    done: N_("已准备下载草案"),
    failed: N_("无法准备下载草案"),
    schema: { type: "object", properties: { handles: HANDLES_FIELD }, required: ["handles"] },
  },
  {
    name: "propose_share",
    description: "Sharing draft: IDs of files or folders to share. Create the share only after user confirmation.",
    plan: true,
    running: N_("正在准备分享草案"),
    done: N_("已准备分享草案"),
    failed: N_("无法准备分享草案"),
    schema: { type: "object", properties: { handles: HANDLES_FIELD }, required: ["handles"] },
  },
];

const DEF_BY_NAME: ReadonlyMap<string, ToolDef> = new Map(TOOL_DEFS.map((d) => [d.name, d]));

/** chatTools is the tools list of a tools-mode request. */
export function chatTools(): AITool[] {
  return TOOL_DEFS.map((d) => ({ name: d.name, description: d.description, schema: schemaText(d.schema) }));
}

/* ---------------- strict argument parsing ---------------- */

function unknownHandles(bad: readonly string[]): ModelOutputError {
  return new ModelOutputError(t("未知的编号：{handles}（只能使用工具结果里出现过的编号）", { handles: bad.slice(0, 10).join("、") }));
}

function resolveHandle(raw: unknown, handles: Handles, kinds: readonly HandleKind[], key: string): HandleRecord | string {
  if (typeof raw !== "string") throw new ModelOutputError(t("{key} 里应为编号文字", { key }));
  const r = handles.resolve(raw);
  if (!r || !kinds.includes(r.kind)) return oneLine(raw).slice(0, 20) || t("（空）");
  return r;
}

/** handleList reads a list of handles: unknown ones fail the whole call, naming them; repeats collapse. */
function handleList(o: Record<string, unknown>, key: string, handles: Handles, kinds: readonly HandleKind[], max: number): HandleRecord[] {
  const out: HandleRecord[] = [];
  const bad: string[] = [];
  for (const v of list(o, key)) {
    const r = resolveHandle(v, handles, kinds, key);
    if (typeof r === "string") bad.push(r);
    else if (!out.includes(r)) out.push(r);
  }
  if (bad.length) throw unknownHandles(bad);
  if (out.length > max) throw new ModelOutputError(t("{key} 一次最多 {n} 项", { key, n: max }));
  return out;
}

const ROOT_NAME = "全部文件"; // i18n-ignore: the model's name for the root

/**
 * modelPath is a folder path as the model reads and writes it: "All Files /
 * Movies / Documentaries" (the search list's breadcrumb, in the UI language) and
 * "/Movies/Documentaries/" both become "Movies/Documentaries"; the root is "".
 */
export function modelPath(path: string): string {
  const root = t("全部文件");
  return path
    .split(/\s*[\\/]\s*/)
    .map((s) => s.trim())
    .filter((s, i) => s && !(i === 0 && (s === ROOT_NAME || s === root)))
    .join("/");
}

/** folderArg reads a folder by handle (must be a folder) or path; absent → null. */
function folderArg(o: Record<string, unknown>, key: string, handles: Handles): FolderRef | null {
  const v = text(o, key, 300);
  if (!v) return null;
  if (HANDLE.test(v.toLowerCase())) {
    const r = handles.resolve(v);
    if (!r) throw unknownHandles([v]);
    if (r.kind !== "folder") throw new ModelOutputError(t("{handle} 不是文件夹", { handle: v }));
    return { id: r.id, path: r.path };
  }
  return { id: "", path: modelPath(v) };
}

function target(r: HandleRecord): Target {
  return { id: r.id, folder: r.kind === "folder" };
}

function parseQuery(q: Record<string, unknown>, handles: Handles): AIQuery {
  const agg = q.aggregate;
  let aggregate: AIAggregate | null = null;
  if (agg !== undefined && agg !== null) {
    const a = objectOf(agg, "aggregate ");
    aggregate = {
      by: choice(a, "by", AGGREGATE_BY, "folder"),
      measure: choice(a, "measure", MEASURES, "size"),
      limit: integer(a, "limit", 0, 50) ?? 0,
    };
  }
  const under = folderArg(q, "under", handles);
  if (under && under.id && !under.path) throw new ModelOutputError(t("{key} 请用文件夹路径", { key: "under" }));
  return {
    ...emptyQuery(),
    keywords: textList(q, "keywords", 10, 64),
    category: choice(q, "category", CATEGORIES, ""),
    ext: textList(q, "ext", 20, 16)
      .map((e) => e.replace(/^\.+/, "").toLowerCase())
      .filter((e) => /^[a-z0-9]{1,10}$/.test(e)),
    minSize: integer(q, "minSize", 0, Number.MAX_SAFE_INTEGER) ?? 0,
    maxSize: integer(q, "maxSize", 0, Number.MAX_SAFE_INTEGER) ?? 0,
    createdAfter: day(q, "createdAfter"),
    createdBefore: day(q, "createdBefore"),
    modifiedAfter: day(q, "modifiedAfter"),
    modifiedBefore: day(q, "modifiedBefore"),
    under: under?.path ?? "",
    starred: choice(q, "starred", STARRED, ""),
    played: choice(q, "played", PLAYED, ""),
    kind: choice(q, "kind", KINDS, ""),
    sort: choice(q, "sort", SORTS, ""),
    asc: flag(q, "asc"),
    limit: integer(q, "limit", 0, ROW_LIMIT) ?? 0,
    aggregate,
  };
}

const URL_SCHEME = /^(magnet:\?|https?:\/\/|ftp:\/\/|ed2k:\/\/|thunder:\/\/)/i;

function parseItems(o: Record<string, unknown>, handles: Handles): ProposedItem[] {
  const rows = list(o, "items");
  if (!rows.length) throw new ModelOutputError(t("{key} 不能为空", { key: "items" }));
  if (rows.length > PLAN_LIMIT) throw new ModelOutputError(t("{key} 一次最多 {n} 项", { key: "items", n: PLAN_LIMIT }));
  const out: ProposedItem[] = [];
  const seen = new Set<string>();
  const bad: string[] = [];
  rows.forEach((row, i) => {
    const item = objectOf(row, t("items 里的每一项"));
    const r = resolveHandle(item.handle, handles, ["file", "folder"], "handle");
    if (typeof r === "string") {
      bad.push(r);
      return;
    }
    if (seen.has(r.id)) throw new ModelOutputError(t("{handle} 出现了不止一次", { handle: r.handle }));
    seen.add(r.id);
    const trash = flag(item, "trash");
    let toPath: string[] | null = null;
    const rawPath = item.toPath;
    if (!trash && rawPath !== undefined && rawPath !== null) {
      const layers = typeof rawPath === "string" ? modelPath(rawPath).split("/").filter((s) => s) : textList(item, "toPath", 32, 255);
      for (const layer of layers) {
        const why = nameError(layer);
        if (why) throw new ModelOutputError(t("第 {n} 项的目标文件夹「{name}」不可用：{error}", { n: i + 1, name: layer.slice(0, 40), error: why }));
      }
      toPath = layers;
    }
    const name = trash ? "" : text(item, "name", 255);
    if (name) {
      const why = nameError(name);
      if (why) throw new ModelOutputError(t("第 {n} 项的新名称不可用：{error}", { n: i + 1, error: why }));
    }
    if (!trash && toPath === null && !name) throw new ModelOutputError(t("第 {n} 项没有目标位置、新名称或回收站", { n: i + 1 }));
    out.push({ ...target(r), toPath, name, trash });
  });
  if (bad.length) throw unknownHandles(bad);
  return out;
}

/**
 * parseToolCall reads one call strictly: an unknown tool, a bad field or a
 * handle that was never handed out throws ModelOutputError — the reason
 * goes back to the model and nothing runs. Handles come back as real ids.
 */
export function parseToolCall(name: string, args: unknown, handles: Handles): ParsedCall {
  const def = DEF_BY_NAME.get(name);
  if (!def) throw new ModelOutputError(t("没有这个工具：{name}", { name: oneLine(name).slice(0, 40) }));
  const o = args === "" || args === undefined || args === null ? {} : objectOf(args, t("参数"));
  switch (def.name) {
    case "search_files":
      return { tool: def.name, args: { query: parseQuery(o, handles) } };
    case "show_results": {
      if (!Array.isArray(o.groups)) throw new ModelOutputError(t("{key} 应为数组", { key: "groups" }));
      if (o.groups.length > 8) throw new ModelOutputError(t("{key} 一次最多 {n} 项", { key: "groups", n: 8 }));
      const groups = o.groups.map((raw) => {
        const group = objectOf(raw, "groups ");
        if (typeof group.title !== "string") throw new ModelOutputError(t("{key} 应为文字", { key: "title" }));
        const title = group.title.trim();
        if (!title) throw new ModelOutputError(t("{key} 不能为空", { key: "title" }));
        if (title.length > 80) throw new ModelOutputError(t("{key} 最多 {n} 个字符", { key: "title", n: 80 }));
        if (!Array.isArray(group.searches)) throw new ModelOutputError(t("{key} 应为数组", { key: "searches" }));
        if (!group.searches.length) throw new ModelOutputError(t("{key} 不能为空", { key: "searches" }));
        if (group.searches.length > 64) throw new ModelOutputError(t("{key} 一次最多 {n} 项", { key: "searches", n: 64 }));
        const searches: number[] = [];
        for (const ordinal of group.searches) {
          if (typeof ordinal !== "number" || !Number.isSafeInteger(ordinal) || ordinal < 1) {
            throw new ModelOutputError(t("搜索结果编号无效或没有可展示的内容"));
          }
          if (!searches.includes(ordinal)) searches.push(ordinal);
        }
        if (group.handles === undefined) return { title, searches };
        if (!Array.isArray(group.handles)) throw new ModelOutputError(t("{key} 应为数组", { key: "handles" }));
        if (!group.handles.length) throw new ModelOutputError(t("{key} 不能为空", { key: "handles" }));
        if (group.handles.length > 320) throw new ModelOutputError(t("{key} 一次最多 {n} 项", { key: "handles", n: 320 }));
        const fileIds = handleList(group, "handles", handles, ["file", "folder"], 320).map((r) => r.id);
        return { title, searches, fileIds };
      });
      return { tool: def.name, args: { groups } };
    }
    case "find_folders": {
      const path = modelPath(text(o, "path", 300));
      if (!path) throw new ModelOutputError(t("{key} 不能为空", { key: "path" }));
      return { tool: def.name, args: { path } };
    }
    case "folder_summary":
      return { tool: def.name, args: { folder: folderArg(o, "folder", handles) ?? { id: "", path: "" } } };
    case "file_info": {
      const r = resolveHandle(o.handle ?? "", handles, ["file", "folder"], "handle");
      if (typeof r === "string") throw unknownHandles([r]);
      return { tool: def.name, args: { target: target(r) } };
    }
    case "recent_plays":
    case "transfer_status":
    case "storage_quota":
    case "cleanup_results":
      return { tool: def.name, args: {} };
    case "organize_media": {
      const folder = folderArg(o, "folder", handles);
      const targets = handleList(o, "handles", handles, ["file", "folder"], PLAN_LIMIT).map(target);
      if (!folder && !targets.length) throw new ModelOutputError(t("需要 {a} 或 {b}", { a: "folder", b: "handles" }));
      return { tool: def.name, args: { folder, targets, instructions: text(o, "instructions", 500) } };
    }
    case "propose_changes":
      return { tool: def.name, args: { title: text(o, "title", 60) || t("整理计划"), items: parseItems(o, handles) } };
    case "propose_offline_add": {
      const urls = textList(o, "urls", 20, 4096);
      const badUrl = urls.find((u) => !URL_SCHEME.test(u));
      if (badUrl) throw new ModelOutputError(t("不支持的链接：{url}", { url: oneLine(badUrl).slice(0, 40) }));
      const taskIds = handleList(o, "tasks", handles, ["task"], 50).map((r) => r.id);
      if (!urls.length && !taskIds.length) throw new ModelOutputError(t("需要 {a} 或 {b}", { a: "urls", b: "tasks" }));
      return { tool: def.name, args: { urls, taskIds, folder: folderArg(o, "folder", handles) } };
    }
    case "propose_download":
    case "propose_share": {
      const targets = handleList(o, "handles", handles, ["file", "folder"], 200).map(target);
      if (!targets.length) throw new ModelOutputError(t("{key} 不能为空", { key: "handles" }));
      return { tool: def.name, args: { targets } };
    }
  }
}

/* ---------------- what tools tell the model ---------------- */

/** Rows a tool result lists at most; the rest is only counted. */
export const ROW_LIMIT = 40;

/** A file or folder as a tool reports it; search hits, entries and plays all fit. */
export interface ModelRow {
  id: string;
  name: string;
  folder: boolean;
  /** The folder it is in: "All Files / Movies" or "Movies". */
  path: string;
  size: number;
  modifiedTime: string;
  /** Play position and length in seconds, when there is a play record. */
  position?: number;
  duration?: number;
}

/** Model-facing play state is English; shown cards use the UI language. */
export function playState(position: number | undefined, duration: number | undefined, shown = false): string {
  const p = position ?? 0;
  const d = duration ?? 0;
  if (d <= 0 || p <= 0) return "";
  if (p >= 0.98 * d || d - p <= 10) return shown ? t("看过") : "Watched";
  if (p < 5) return shown ? t("打开过") : "Opened";
  const percent = Math.floor((p / d) * 100);
  return shown ? t("没看完 {n}%", { n: percent }) : `Unfinished ${percent}%`;
}

function size(n: number): string {
  return bytes(n, "0 B", 1);
}

function folderPathOf(r: ModelRow): string {
  return [modelPath(r.path), r.name].filter((s) => s).join("/");
}

// Tool result labels are English, independent of the UI language.
const ROW_HEADER = "ID | Name | Containing folder | Size | Modified time | Playback";

function rowLine(r: ModelRow, handles: Handles): string {
  const h = handles.of(r.id, r.folder ? "folder" : "file", r.folder ? folderPathOf(r) : "");
  return [
    h,
    `${oneLine(r.name)}${r.folder ? "/" : ""}`,
    oneLine(modelPath(r.path)) || "Root directory",
    r.folder ? "-" : size(r.size),
    (r.modifiedTime || "").slice(0, 10) || "-",
    playState(r.position, r.duration) || "-",
  ].join(" | ");
}

/** rowsForModel lists up to ROW_LIMIT rows under handles, with the total when there are more. */
export function rowsForModel(rows: readonly ModelRow[], handles: Handles, total = rows.length): string {
  if (!rows.length) return "Nothing found.";
  const shown = rows.slice(0, ROW_LIMIT);
  const head = total > shown.length ? `Total ${total} items, showing the first ${shown.length} items:` : `Total ${total} items:`;
  return [head, ROW_HEADER, ...shown.map((r) => rowLine(r, handles))].join("\n");
}

/** cardRows is the same rows as a card keeps them (real ids, local only). */
export function cardRows(rows: readonly ModelRow[]): CardRow[] {
  return rows.slice(0, ROW_LIMIT).map((r) => ({
    id: r.id,
    name: r.name,
    folder: r.folder,
    path: modelPath(r.path),
    size: r.size,
    modified: r.modifiedTime,
    played: playState(r.position, r.duration, true),
  }));
}

export interface AggregateLike {
  key: string;
  label: string;
  size: number;
  count: number;
}

export interface SearchLike {
  hits: readonly ModelRow[] | null;
  total: number;
  aggregate: readonly AggregateLike[] | null;
  /** The folder path `under` resolved to, "" when not asked. */
  under: string;
  underMissing?: boolean;
}

/** searchForModel is a search_files result: rows or aggregate groups, the total, where it looked. */
export function searchForModel(result: SearchLike, query: Pick<AIQuery, "aggregate" | "under">, handles: Handles): string {
  const lines: string[] = [];
  if (query.under) {
    lines.push(result.underMissing ? `Could not find folder 「${oneLine(query.under)}」。` : `Scope:${oneLine(modelPath(result.under)) || "Root directory"}`);
    if (result.underMissing) return lines.join("\n");
  }
  if (query.aggregate) {
    const rows = (result.aggregate ?? []).slice(0, ROW_LIMIT);
    const folders = query.aggregate.by === "folder";
    lines.push(`Total: ${result.total} files, grouped by${folders ? "folder" : query.aggregate.by === "ext" ? "extension" : "type"}—totals:`);
    lines.push(`${folders ? "ID | Folder" : "Group"} | Size | File count`);
    for (const g of rows) {
      const label = folders ? modelPath(g.label) : g.label;
      const head = folders ? `${handles.of(g.key, "folder", label)} | ${oneLine(label) || "Root directory"}` : oneLine(g.label) || "(none)";
      lines.push(`${head} | ${size(g.size)} | ${g.count}`);
    }
    return lines.join("\n");
  }
  lines.push(rowsForModel(result.hits ?? [], handles, result.total));
  return lines.join("\n");
}

/** foldersForModel is a find_folders result. */
export function foldersForModel(matches: readonly { id: string; path: string }[], handles: Handles): string {
  if (!matches.length) return "No matching folder found.";
  const shown = matches.slice(0, ROW_LIMIT);
  return [
    `Found ${matches.length} folders:`,
    ...shown.map((m) => {
      const path = modelPath(m.path);
      return `${handles.of(m.id, "folder", path)} | ${oneLine(path) || "Root directory"}`;
    }),
  ].join("\n");
}

/** FolderStatsLike is services.FolderStats. */
export interface FolderStatsLike {
  files: number;
  folders: number;
  size: number;
  categories: readonly { category: string; count: number; size: number }[] | null;
  dupGroups: number;
  dupCopies: number;
  dupWaste: number;
  unfinished: number;
  largest: readonly ModelRow[] | null;
}

const CATEGORY_LABEL: Record<string, string> = { video: "Video", image: "Image", document: "Document", audio: "Audio", other: "Other" };

/** folderSummaryForModel is a folder_summary result: the subtree's figures and ≤40 direct children. */
export function folderSummaryForModel(
  path: string,
  stats: FolderStatsLike,
  children: readonly ModelRow[],
  handles: Handles,
): string {
  const where = modelPath(path);
  const lines = [
    `Folder:${oneLine(where) || "Entire cloud drive"}`,
    `Including subfolders, a total of ${stats.files} files;${stats.folders} folders, totaling ${size(stats.size)}`,
  ];
  const cats = (stats.categories ?? []).filter((c) => c.count > 0);
  if (cats.length) {
    lines.push(`Type:${cats.map((c) => `${CATEGORY_LABEL[c.category] ?? oneLine(c.category)} ${c.count} items ${size(c.size)}`).join("；")}`);
  }
  if (stats.dupGroups) lines.push(`Duplicate files:${stats.dupGroups} groups, with ${stats.dupCopies} extra copies, reclaimable space: ${size(stats.dupWaste)}`);
  if (stats.unfinished) lines.push(`Unfinished videos:${stats.unfinished} videos`);
  const largest = (stats.largest ?? []).slice(0, 5);
  if (largest.length) lines.push("Largest items:", ROW_HEADER, ...largest.map((r) => rowLine(r, handles)));
  lines.push(`Directly contains:${rowsForModel(children, handles)}`);
  return lines.join("\n");
}

export interface FileInfoLike extends ModelRow {
  createdTime?: string;
  category?: string;
  width?: number;
  height?: number;
  /** MediaResolutions: "2160p". */
  resolution?: string;
  /** Items directly inside a folder. */
  children?: number;
}

/** fileInfoForModel is a file_info result; only these fields ever go out (no hash, link or thumbnail). */
export function fileInfoForModel(f: FileInfoLike, handles: Handles): string {
  const lines = [ROW_HEADER, rowLine(f, handles)];
  if (f.createdTime) lines.push(`Saved at:${f.createdTime.slice(0, 10)}`);
  if (f.category) lines.push(`Type:${CATEGORY_LABEL[f.category] ?? oneLine(f.category)}`);
  if (f.resolution) lines.push(`Resolution:${oneLine(f.resolution)}`);
  else if (f.width && f.height) lines.push(`Picture:${f.width}×${f.height}`);
  if (f.duration && !f.folder) lines.push(`Duration:${Math.round(f.duration / 60)} minutes`);
  if (f.folder && f.children !== undefined) lines.push(`Directly contains ${f.children} items`);
  return lines.join("\n");
}

export interface PlayLike {
  fileId: string;
  name: string;
  position: number;
  duration: number;
  playedAt: string;
  available: boolean;
}

/** playsForModel is a recent_plays result. */
export function playsForModel(plays: readonly PlayLike[], handles: Handles): string {
  if (!plays.length) return "No playback history.";
  const shown = plays.slice(0, ROW_LIMIT);
  return [
    plays.length > shown.length ? `Total: ${plays.length} records, showing the latest ${shown.length} records:` : `Total: ${plays.length} entries:`,
    "ID | Name | Playback time | Progress",
    ...shown.map((p) =>
      [
        p.available ? handles.of(p.fileId, "file") : "-",
        oneLine(p.name) + (p.available ? "" : "(No longer in cloud storage)"),
        (p.playedAt || "").slice(0, 10) || "-",
        playState(p.position, p.duration) || "-",
      ].join(" | "),
    ),
  ].join("\n");
}

export interface TaskLike {
  id: string;
  name: string;
  phase: string;
  progress: number;
  fileSize: number;
  message: string;
  errorDetail: string;
  createdTime: string;
  updatedTime: string;
  savedTo: string;
}

export interface LocalTransferLike {
  name: string;
  /** 「Download」「Upload」 */
  kind: string;
  /** 「In progress」「Paused」「Failed」… as the transfer center says it. */
  state: string;
  progress: number;
}

const PHASE_LABEL: Record<string, string> = {
  PHASE_TYPE_PENDING: "Waiting",
  PHASE_TYPE_RUNNING: "In progress",
  PHASE_TYPE_COMPLETE: "Completed",
  PHASE_TYPE_ERROR: "Failed",
};

/**
 * transfersForModel is a transfer_status result: cloud-add tasks under c
 * handles — never their links or hashes — and the local transfers.
 */
export function transfersForModel(tasks: readonly TaskLike[], local: readonly LocalTransferLike[], handles: Handles): string {
  const lines: string[] = [];
  const shown = tasks.slice(0, ROW_LIMIT);
  lines.push(tasks.length ? `Total cloud add tasks: ${tasks.length} tasks${tasks.length > shown.length ? `, listing the latest ${shown.length} tasks` : ""}：` : "No cloud add tasks.");
  if (shown.length) {
    lines.push("ID | Name | Status | Progress | Size | Save to | Updated at | Description");
    for (const t of shown) {
      lines.push(
        [
          handles.of(t.id, "task"),
          oneLine(t.name),
          PHASE_LABEL[t.phase] ?? oneLine(t.phase),
          `${Math.round(t.progress)}%`,
          t.fileSize ? size(t.fileSize) : "-",
          oneLine(modelPath(t.savedTo)) || "-",
          (t.updatedTime || t.createdTime || "").slice(0, 16).replace("T", " ") || "-",
          oneLine([t.message, t.errorDetail].filter((s) => s).join(" ")).slice(0, 80) || "-",
        ].join(" | "),
      );
    }
  }
  const mine = local.slice(0, ROW_LIMIT);
  lines.push(local.length ? `Total local transfers: ${local.length} transfers:` : "No local transfers.");
  for (const l of mine) lines.push(`${oneLine(l.kind)} | ${oneLine(l.name)} | ${oneLine(l.state)} | ${Math.round(l.progress)}%`);
  return lines.join("\n");
}

export interface QuotaLike {
  limit: number;
  usage: number;
  usageInTrash: number;
  isUnlimited: boolean;
  breakdown: { video: number; image: number; document: number; other: number } | null;
}

/** quotaForModel is a storage_quota result. */
export function quotaForModel(q: QuotaLike): string {
  const lines = [
    q.isUnlimited ? `Used ${size(q.usage)}(unlimited storage)` : `Used ${size(q.usage)} / total ${size(q.limit)}, remaining ${size(Math.max(0, q.limit - q.usage))}`,
    `Recycle bin usage: ${size(q.usageInTrash)}`,
  ];
  const b = q.breakdown;
  if (b) lines.push(`Videos ${size(b.video)}; Images ${size(b.image)}; Documents ${size(b.document)}; Other ${size(b.other)}`);
  return lines.join("\n");
}

export interface DupLike {
  groups: readonly {
    name: string;
    size: number;
    count: number;
    waste: number;
    copies: readonly { id: string; name: string; path: string; modifiedTime: string }[] | null;
  }[] | null;
  total: number;
  copies: number;
  waste: number;
}

export interface EmptyLike {
  folders: readonly { id: string; name: string; path: string }[] | null;
  total: number;
}

/** cleanupForModel is a cleanup_results result; null = that scan has not run. */
export function cleanupForModel(dups: DupLike | null, empties: EmptyLike | null, handles: Handles): string {
  const lines: string[] = [];
  if (!dups) lines.push("No duplicate file scan results yet.");
  else {
    lines.push(`Duplicate files: ${dups.total} groups, with ${dups.copies} extra copies, reclaimable space: ${size(dups.waste)}`);
    for (const g of (dups.groups ?? []).slice(0, ROW_LIMIT)) {
      const copies = (g.copies ?? [])
        .slice(0, 5)
        .map((c) => {
          const where = modelPath(c.path);
          return `${handles.of(c.id, "file")}（${oneLine(where) || "Root directory"}）`;
        })
        .join("、");
      lines.push(`${oneLine(g.name)} | ${size(g.size)} × ${g.count} copies | Reclaimable space: ${size(g.waste)} | ${copies}`);
    }
  }
  if (!empties) lines.push("No empty folder scan results yet.");
  else {
    lines.push(`Empty folders: ${empties.total} folders:`);
    for (const f of (empties.folders ?? []).slice(0, ROW_LIMIT)) {
      // EmptyFolder.path ends with the folder itself.
      const path = modelPath(f.path);
      lines.push(`${handles.of(f.id, "folder", path)} | ${oneLine(path)}`);
    }
  }
  return lines.join("\n");
}

const CLASS_LABEL: Record<FileClass, string> = { video: "Video", subtitle: "Subtitles", audio: "Audio", other: "Other" };

/**
 * mediaSummaryForModel says what organize_media found, group by group: the
 * file-name pattern, how many files and whether the group was already
 * matched on TMDB. It takes no titles at all, so a
 * TMDB answer cannot reach the model through it (TMDB isolation).
 */
export function mediaSummaryForModel(
  groups: readonly { id: string; kind: FileClass; pattern: string; files: readonly unknown[] }[],
  matched: ReadonlySet<string>,
): string {
  if (!groups.length) return "No movie or TV files to organize.";
  const shown = groups.slice(0, ROW_LIMIT);
  return [
    groups.length > shown.length ? `Total: ${groups.length} groups, listing the first ${shown.length} groups:` : `Total: ${groups.length} groups:`,
    ...shown.map(
      (g) => `${g.id} · ${CLASS_LABEL[g.kind]} · ${g.files.length} files · Mode:${oneLine(g.pattern)} · ${matched.has(g.id) ? "Matched" : "Unmatched"}`,
    ),
  ].join("\n");
}

/** planForModel is what a plan tool reports: the card exists, nothing ran. */
export function planForModel(counts: PlanCard["counts"], detail = ""): string {
  const conflicts = counts.conflicts ? `，${counts.conflicts} items have conflicts requiring user action` : "";
  const line = `Plan card generated:${counts.changes} changes (including ${counts.trash} items moved to the recycle bin),${counts.kept} items left unchanged${conflicts}. Not yet executed; waiting for the user to review and confirm in the card.`;
  return detail ? `${line}\n${detail}` : line;
}

/* ---------------- the prompt ---------------- */

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/**
 * systemPrompt states the safety rules the model works under; the real
 * guarantees are in code (strict parsing, plan cards, Go's whitelist), the
 * prompt only guides the model. Instructions are English and replies follow
 * the explicitly requested UI language.
 */
export function systemPrompt(today: Date, context = ""): string {
  const lang = replyLanguage();
  return [
    "You are the AI assistant in the PKP cloud drive client, helping users find files, view statistics, organize movies, TV shows, and files, and arrange transfers and sharing in their own cloud drives.",
    `Today is ${localDay(today)} (${WEEKDAYS[today.getDay()]}).`,
    context ? `User's current location: ${oneLine(context)}` : "",
    "Rules:",
    "1. Read-only tools (search_files, show_results, find_folders, folder_summary, file_info, recent_plays, transfer_status, storage_quota, cleanup_results) run directly on the local machine and may be called at any time without asking for the user's consent.",
    "2. You cannot modify the cloud drive directly. Moving, renaming, creating folders, moving to the recycle bin, cloud adding, downloading, and sharing must only be handled by generating plan cards using organize_media or tools whose names start with propose_. Execution may occur only after the user reviews and confirms. After generating a card, ask the user to review and confirm in the card; do not say the operation is already complete.",
    "3. Refer to files and folders (IDs starting with f) and cloud-add tasks (IDs starting with c) only using IDs that have appeared in tool results; do not invent IDs, paths, or filenames. When unsure, check with read-only tools first.",
    "4. Tool results list at most 40 items and provide the total count; if more are needed, narrow the criteria and search again.",
    "5. Use organize_media for organizing movies and TV shows (categorizing and renaming by series or movie), with names determined by local rules and the metadata database; use propose_changes for other moves, renames, and moves to the recycle bin. Generate only one plan for the same request.",
    "6. You do not have access to file contents, subtitles, or metadata database (TMDB) information. Groups marked 'Matched' have already been identified locally; do not guess their titles.",
    "7. Answer concisely. Do not show users IDs, JSON, or your reasoning process; use filenames when mentioning files.",
    "8. All intermediate searches are kept only in the collapsed search process; neither the last query nor the broadest query is automatically treated as the answer. After completing searches, if you are not generating an action plan, you must call show_results before your final response, using search IDs returned by tools in the current turn to select genuinely relevant result groups. Name and group different intents separately; one group may combine multiple related searches, and handles may select only the relevant files within them. Do not select irrelevant searches or searches with zero hits; if there are no relevant results, use groups=[]. Titles must summarize the content in the user's language, not just say 'Search results'. Statistical searches must be in separate groups without handles. Answer normally in the final text; do not repeat the JSON.",
    `Always use ${lang} to reply to the user.`,
  ]
    .filter((l) => l)
    .join("\n");
}

const JSON_STEP_SCHEMA = {
  type: "object",
  properties: {
    say: { type: "string", description: "User-facing text: an answer or a one-sentence progress update" },
    call: {
      type: "object",
      description: "Provide when a tool call is needed, at most one per turn; omit when you can answer directly",
      properties: {
        tool: { type: "string", enum: TOOL_DEFS.map((d) => d.name) },
        args: { type: "object", description: "Tool arguments, in the format required by that tool" },
      },
      required: ["tool"],
    },
  },
  required: ["say"],
};

function jsonSystem(base: string): string {
  return [
    base,
    "",
    "The current model does not support tool calling. Switch to single-step mode: output only one JSON object at a time: {\"say\": \"…\", \"call\": {\"tool\": \"…\", \"args\": {…}}}.",
    "When a tool is needed, provide call (one per turn); the tool result will be given to you in the next message. When you can answer directly, omit call and put the answer in say.",
    "Available tools (name: description; argument format):",
    ...TOOL_DEFS.map((d) => `- ${d.name}：${d.description}; arguments ${schemaText(d.schema)}`),
  ].join("\n");
}

/* ---------------- history ---------------- */

/** Earlier turns kept in a request, newest last, and the characters they may take. */
export const HISTORY_TURNS = 12;
export const HISTORY_CHARS = 8000;

// English labels describe earlier cards to the model.
const CARD_STATE_LABEL: Record<CardState, string> = {
  draft: "Awaiting confirmation",
  running: "Executing",
  done: "Executed",
  failed: "Failed",
  cancelled: "Canceled",
  undone: "Undone",
};

function cardLine(c: Card, handles: Handles, visibleSearch = false): string {
  const head = `[Card:${oneLine(c.title)}，${CARD_STATE_LABEL[c.state]}］`;
  if (c.kind !== "files" && c.kind !== "download" && c.kind !== "share") return head;
  const named = c.rows
    .slice(0, visibleSearch ? undefined : 20)
    .map((r, index) => {
      const h = handles.peek(r.id, r.folder ? "folder" : "file");
      if (visibleSearch) return `${index + 1}. ${h ? `${h} ` : ""}${oneLine(r.name)}`;
      return h ? `${h} ${oneLine(r.name)}` : "";
    })
    .filter((s) => s);
  return named.length ? `${head} ${named.join("；")}` : head;
}

/**
 * compactHistory is the earlier turns as plain user/assistant pairs: the
 * texts, plus each card's title, state and the handles it showed, so 「Move the sec
 * ond one away too」 still resolves. Tool transcripts are not replayed. Denied
 * turns sent nothing and are left out; the oldest go first past the caps.
 */
export function compactHistory(turns: readonly Turn[], handles: Handles): { role: "user" | "assistant"; content: string }[] {
  const pairs: { role: "user" | "assistant"; content: string }[][] = [];
  let chars = 0;
  for (let i = turns.length - 1; i >= 0 && pairs.length < HISTORY_TURNS; i--) {
    const t = turns[i];
    if (t.denied || !t.user.trim()) continue;
    const scope = [
      ...(t.scope?.folder ?? []).map((folder) => `Folder:${oneLine(folder.name)}`),
      ...(t.scope?.entries ?? []).map((entry, index) => {
        const h = handles.peek(entry.id, entry.folder ? "folder" : "file");
        return `${index + 1}. ${h ? `${h} ` : ""}${oneLine(entry.name)}`;
      }),
    ];
    const hasPlan = t.cards.some((card) => card.kind === "plan");
    const groups = hasPlan ? [] : presentSearchGroups(t.cards, t.resultGroups, false);
    const cards = t.cards.filter((card) => card.kind !== "files" || (!hasPlan && card.query == null));
    const answer = [
      t.text.trim() || (t.error ? `(Error:${oneLine(t.error)}）` : "(No text response)"),
      ...groups.map(({ card }) => cardLine(card, handles, true)),
      ...cards.map((card) => cardLine(card, handles)),
    ].join("\n");
    const pair = [
      { role: "user" as const, content: [t.user.trim(), ...(scope.length ? [`[Scope:${scope.join("；")}］`] : [])].join("\n") },
      { role: "assistant" as const, content: answer },
    ];
    const length = pair[0].content.length + pair[1].content.length;
    if (pairs.length && chars + length > HISTORY_CHARS) break;
    chars += length;
    pairs.unshift(pair);
  }
  return pairs.flat();
}

/* ---------------- the turn ---------------- */

/** Model calls one turn may make. */
export const MAX_CALLS = 8;
/** Tool calls one reply may make; past this they are answered with an error. */
export const MAX_TOOLS_PER_REPLY = 8;
/** A tool result is cut here, whatever the tool built. */
export const RESULT_CHARS = 12000;
/** Output tokens a step is expected to take, for the estimate. */
const STEP_OUTPUT = 400;

/** TurnDisclosure is what the authorization card of a turn says. */
export interface TurnDisclosure {
  title: string;
  sends: string;
  notSent: string;
  /** The first request of the turn. */
  estimate: TokenRange;
  level: "metadata";
}

export interface AgentDeps {
  /** Tools mode: services.AIChat; deltas stream through onDelta. */
  chat(request: AIChatRequest, onDelta: (text: string) => void, signal?: AbortSignal): Promise<AIChatReply>;
  /** Json emulation: services.AIComplete with the step schema. */
  json(request: ModelRequest, signal?: AbortSignal): Promise<AIJsonReply>;
  tools: ToolImpls;
  /** Asked once, before the turn's first request, unless TurnOptions.alwaysAllow. */
  consent(disclosure: TurnDisclosure): Promise<boolean>;
  /** Tokens this conversation may still spend. */
  budget: number;
  now?(): Date;
  /** The page's words for an error (store errorText); err.message otherwise. */
  describe?(err: unknown): string;
}

export interface ConversationState {
  /** Earlier turns, oldest first. */
  turns: readonly Turn[];
  handles: Handles;
}

export interface TurnOptions {
  /** "json" when the model is known to have no tools (AISettings.tools "no"). */
  mode: AgentMode;
  /** The account's L1 is always allowed: no consent card. */
  alwaysAllow: boolean;
  stream: boolean;
  signal?: AbortSignal;
  /** Where the user asked from (「Folder: Videos/Documentaries」), one line in the system prompt. */
  context?: string;
  id?: string;
  /** Every change of the turn as it happens (a fresh copy each time). */
  onUpdate?(turn: Turn): void;
}

type PlainMessage = { role: "user" | "assistant"; content: string };

function chatEstimate(req: AIChatRequest): TokenRange {
  return estimateTokens(
    {
      system: req.system,
      messages: req.messages.map((m) => ({
        role: m.role === "assistant" ? "assistant" : "user",
        content: m.content + m.toolCalls.map((c) => c.name + c.arguments).join(""),
      })),
      outputName: "",
      outputSchema: req.tools.length ? JSON.stringify(req.tools) : "",
      maxTokens: 0,
    },
    STEP_OUTPUT,
  );
}

/** Consecutive messages of one role become one: the structured mode alternates user and assistant. */
function alternate(messages: readonly PlainMessage[]): PlainMessage[] {
  const out: PlainMessage[] = [];
  for (const m of messages) {
    const last = out[out.length - 1];
    if (last && last.role === m.role) last.content = `${last.content}\n\n${m.content}`;
    else out.push({ ...m });
  }
  return out;
}

/** toPlain turns a tools-mode transcript into the json mode's, when a model turns out to have no tools mid-turn. */
function toPlain(messages: readonly AIChatMessage[]): PlainMessage[] {
  return alternate(
    messages.map((m) => {
      if (m.role === "tool") return { role: "user", content: `Tool result:\n${m.content}` };
      const calls = m.toolCalls.map((c) => `Call tool ${c.name} ${c.arguments}`);
      return { role: m.role, content: [m.content, ...calls].filter((s) => s).join("\n") || "(None)" };
    }),
  );
}

function cut(s: string): string {
  return s.length > RESULT_CHARS ? `${s.slice(0, RESULT_CHARS)}\n…(Result too long; truncated)` : s;
}

/**
 * runTurn drives one turn: consent once, then up to MAX_CALLS model calls,
 * each followed by the read-only tools it asked for (and plan tools, which
 * only make cards). It stops when the model answers without a tool, on
 * cancel, on the budget, or at the call limit — whatever happened so far
 * stays in the turn. It never throws for the model's or a tool's failure.
 */
export async function runTurn(deps: AgentDeps, state: ConversationState, userText: string, options: TurnOptions): Promise<Turn> {
  const now = deps.now?.() ?? new Date();
  const turn: Turn = {
    id: options.id ?? `t${now.getTime().toString(36)}`,
    at: now.toISOString(),
    user: userText.trim(),
    text: "",
    traces: [],
    cards: [],
    usage: { input: 0, output: 0, calls: 0 },
    stop: "end",
    error: "",
    denied: false,
    notice: "",
    mode: options.mode,
    modeNote: "",
  };
  const segments: string[] = [];
  let live = "";
  const publish = () => {
    turn.text = [...segments, live].map((s) => s.trim()).filter((s) => s).join("\n\n");
    options.onUpdate?.({
      ...turn,
      traces: turn.traces.map((t) => ({ ...t })),
      cards: turn.cards.slice(),
      ...(turn.resultGroups === undefined ? {} : {
        resultGroups: turn.resultGroups.map((g) => ({
          ...g,
          sourceIds: g.sourceIds.slice(),
          ...(g.fileIds === undefined ? {} : { fileIds: g.fileIds.slice() }),
        })),
      }),
    });
  };
  const finish = (stop: TurnStop, notice = "", error = ""): Turn => {
    turn.stop = stop;
    turn.notice = notice;
    turn.error = error;
    live = "";
    publish();
    return turn;
  };
  const describe = (err: unknown) => deps.describe?.(err) ?? (err instanceof Error ? err.message : String(err));
  const cancelled = () => !!options.signal?.aborted;
  const ctx: ToolContext = { handles: state.handles, turnId: turn.id, signal: options.signal };

  const system = systemPrompt(now, options.context);
  const history = compactHistory(state.turns, state.handles);
  const chatMessages: AIChatMessage[] = [
    ...history.map((m) => ({ ...m, toolCalls: [], toolCallId: "" })),
    { role: "user", content: turn.user, toolCalls: [], toolCallId: "" },
  ];
  let plain: PlainMessage[] = [];
  const tools = chatTools();
  const chatRequest = (): AIChatRequest => ({ system, messages: chatMessages.slice(), tools, stream: options.stream });
  const jsonRequest = (): ModelRequest => ({
    system: jsonSystem(system),
    messages: alternate(plain),
    outputName: "agent_step",
    outputSchema: schemaText(JSON_STEP_SCHEMA),
    maxTokens: 4096,
  });
  const estimate = (): TokenRange => (turn.mode === "tools" ? chatEstimate(chatRequest()) : estimateTokens(jsonRequest(), STEP_OUTPUT));
  const switchToJson = () => {
    turn.mode = "json";
    turn.modeNote = t("这个模型不支持工具调用，已改用单步模式：每步只能查一项，回答会慢一些。");
    plain = toPlain(chatMessages);
  };
  if (turn.mode === "json") plain = toPlain(chatMessages);

  const used = () => turn.usage.input + turn.usage.output;
  const overBudget = () => used() + estimate().low > deps.budget;
  const BUDGET_NOTICE = t("已达到本对话的用量上限，停在这里；已有的结果都保留。");

  // One consent for the whole turn, before anything is sent.
  if (overBudget()) return finish("budget", BUDGET_NOTICE);
  if (!options.alwaysAllow) {
    const go = await deps.consent({
      title: t("这一轮要发给模型的内容"),
      sends: t(
        "你的这句话、本对话之前的问答文字，以及 AI 调用本地工具后查到的文件名、所在路径、大小与时间（每次最多 40 项）。路径中的文件夹名也会发送。",
      ),
      notSent: t("文件内容、字幕、账号凭据、下载链接和 TMDB 返回的资料。"),
      estimate: estimate(),
      level: "metadata",
    });
    if (!go) {
      turn.denied = true;
      return finish("denied", t("已取消，未向模型发送任何内容。"));
    }
  }

  /** execute runs one validated call; a rejection or a failure becomes the text the model reads. */
  const execute = async (name: string, args: unknown): Promise<string> => {
    let call: ParsedCall;
    try {
      call = parseToolCall(name, args, state.handles);
      if (call.tool === "show_results") {
        const sources = turn.cards.filter((card): card is FilesCard => card.kind === "files" && card.query !== null);
        const groups: SearchResultGroup[] = call.args.groups.map((group) => {
          const selected = group.searches.map((ordinal) => {
            const source = sources[ordinal - 1];
            if (!source || source.state !== "done" || (!source.rows.length && !source.aggregate.length)) {
              throw new ModelOutputError(t("搜索结果编号无效或没有可展示的内容"));
            }
            return source;
          });
          if (selected.some((source) => source.query?.aggregate || source.aggregate.length)) {
            if (selected.length !== 1 || group.fileIds !== undefined) {
              throw new ModelOutputError(t("统计结果必须单独展示，且不能筛选文件编号"));
            }
          }
          if (group.fileIds !== undefined) {
            const available = new Set(selected.flatMap((source) => source.rows.map((row) => row.id)));
            if (group.fileIds.some((id) => !available.has(id))) throw new ModelOutputError(t("结果编号不属于所选搜索"));
          }
          return {
            title: group.title,
            sourceIds: selected.map((source) => source.id),
            ...(group.fileIds === undefined ? {} : { fileIds: group.fileIds.slice() }),
          };
        });
        turn.resultGroups = groups;
        turn.traces.push({ tool: call.tool, label: t("已选择搜索结果"), state: "done", detail: "" });
        publish();
        return `Selected ${groups.length} final result groups; all search records are still retained.`;
      }
    } catch (err) {
      const reason = describe(err);
      turn.traces.push({ tool: oneLine(name).slice(0, 40), label: t("已拒绝 AI 的无效调用"), state: "failed", detail: reason });
      publish();
      return `Call rejected, not executed:${reason}`;
    }
    const def = DEF_BY_NAME.get(call.tool)!;
    // The map is indexed by the call's own tool, which TypeScript cannot correlate with its args.
    const impl = deps.tools[call.tool] as unknown as ((args: unknown, ctx: ToolContext) => Promise<ToolOutcome>) | undefined;
    if (!impl) {
      turn.traces.push({ tool: def.name, label: t(def.failed), state: "failed", detail: t("这个功能暂不可用") });
      publish();
      return "This tool is temporarily unavailable.";
    }
    const trace: Trace = { tool: def.name, label: t(def.running), state: "running", detail: "" };
    turn.traces.push(trace);
    publish();
    try {
      const outcome = await impl(call.args, ctx);
      trace.state = "done";
      trace.label = t(def.done);
      trace.detail = outcome.trace;
      if (outcome.card) {
        if (!outcome.card.id) outcome.card.id = `${turn.id}-${turn.cards.length + 1}`;
        turn.cards.push(outcome.card);
      }
      let result = outcome.forModel;
      if (call.tool === "search_files" && outcome.card?.kind === "files" && outcome.card.query !== null) {
        delete turn.resultGroups;
        const ordinal = turn.cards.filter((card) => card.kind === "files" && card.query !== null).length;
        const query = call.args.query;
        const cap = query.aggregate ? Math.min(ROW_LIMIT, query.aggregate.limit || 10) : query.limit || ROW_LIMIT;
        const handles = outcome.card.rows.map((row) => state.handles.of(row.id, row.folder ? "folder" : "file"));
        result = [
          `Search ID:${ordinal}(valid only for this round); preview limit:${cap}; actual matches:${outcome.card.total}; loaded:${query.aggregate ? outcome.card.aggregate.length : outcome.card.rows.length}`,
          `Criteria:${JSON.stringify(query)}; selectable file IDs:${handles.join(", ") || "None (statistics or zero results)"}`,
          outcome.forModel,
        ].join("\n");
      }
      publish();
      return cut(result);
    } catch (err) {
      const reason = describe(err);
      trace.state = "failed";
      trace.detail = reason;
      publish();
      return `Tool execution failed:${reason}`;
    }
  };

  while (turn.usage.calls < MAX_CALLS) {
    if (cancelled()) return finish("cancel", t("已取消。"));
    if (overBudget()) return finish("budget", BUDGET_NOTICE);
    turn.usage.calls++;

    if (turn.mode === "tools") {
      let reply: AIChatReply;
      try {
        reply = await deps.chat(
          chatRequest(),
          (delta) => {
            live += delta;
            publish();
          },
          options.signal,
        );
      } catch (err) {
        live = "";
        if (cancelled()) return finish("cancel", t("已取消。"));
        if (isNoTools(err)) {
          switchToJson();
          publish();
          continue;
        }
        return finish("error", "", describe(err));
      }
      turn.usage.input += reply.input;
      turn.usage.output += reply.outputTokens;
      live = "";
      segments.push(reply.text);
      publish();
      const calls = reply.toolCalls ?? [];
      chatMessages.push({ role: "assistant", content: reply.text, toolCalls: calls, toolCallId: "" });
      if (!calls.length) return finish(reply.stop === "length" ? "length" : "end", reply.stop === "length" ? t("回答超出长度，已截断。") : "");
      for (const [i, c] of calls.entries()) {
        // Every call gets an answer, or the next request is malformed for the provider.
        const result =
          i >= MAX_TOOLS_PER_REPLY
            ? `At most ${MAX_TOOLS_PER_REPLY} tools may be called at a time; this one was not executed.`
            : cancelled()
              ? "Canceled, not executed."
              : await execute(c.name, c.arguments);
        chatMessages.push({ role: "tool", content: result, toolCalls: [], toolCallId: c.id });
      }
      continue;
    }

    let reply: AIJsonReply;
    try {
      reply = await deps.json(jsonRequest(), options.signal);
    } catch (err) {
      if (cancelled()) return finish("cancel", t("已取消。"));
      return finish("error", "", describe(err));
    }
    turn.usage.input += reply.input;
    turn.usage.output += reply.outputTokens;
    let say: string;
    let call: { tool: string; args: unknown } | null = null;
    try {
      const o = objectOf(reply.output, t("回答"));
      if (o.say !== undefined && o.say !== null && typeof o.say !== "string") throw new ModelOutputError(t("{key} 应为文字", { key: "say" }));
      say = typeof o.say === "string" ? o.say.trim().slice(0, 8000) : "";
      if (o.call !== undefined && o.call !== null) {
        const c = objectOf(o.call, "call ");
        const tool = text(c, "tool", 64);
        if (!tool) throw new ModelOutputError(t("call 缺少 tool"));
        call = { tool, args: c.args ?? {} };
      }
    } catch (err) {
      return finish("error", "", t("{error}，没有执行任何操作", { error: describe(err) }));
    }
    segments.push(say);
    publish();
    plain.push({ role: "assistant", content: JSON.stringify(call ? { say, call } : { say }) });
    if (!call) return finish("end");
    if (cancelled()) return finish("cancel", t("已取消。"));
    const result = await execute(call.tool, call.args);
    plain.push({ role: "user", content: `Tool ${call.tool} results:\n${result}` });
  }
  return finish("limit", t("这一轮已调用模型 {n} 次，先停在这里；可以继续追问。", { n: MAX_CALLS }));
}
