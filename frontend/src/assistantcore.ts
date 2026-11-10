/*
 * AI Assistant · the store's pure parts: what a saved
 * conversation holds and how it is read back, what a plan card says of its
 * plan, a plan made of the model's proposed changes, and the words of an
 * arrival's pending organization suggestion. Nothing here calls Go or touches reactive state, so
 * it is tested on its own.
 */
import type { Crumb } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { AssistantScope, Card, HandleSnapshot, PlanCard, ProposedItem, Turn } from "./agent";
import { OPERATION_ACTIONS, type OperationAction } from "./agent";
import { classify, type Identity } from "./media";
import { active, type Change, type Tree } from "./organizer";
import { N_, t } from "./i18n";

/** Where a plan's names come from, for its basis line and the record. */
export type PlanOrigin = "rules" | "tmdb" | "model" | "agent" | "rename";

/**
 * PlanProposal is what a conversation keeps of a plan card so the draft can
 * be built again after a restart: the inputs, never the Change list (the
 * drive may have moved on). Media plans keep only the model's identities —
 * TMDB answers are asked again, so none is ever written into the body.
 */
export type PlanProposal =
  | { kind: "media"; scope: Crumb[]; ids: string[]; instructions: string; identities: Identity[] }
  | { kind: "changes"; title: string; items: ProposedItem[] };

export interface ConversationView {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  /** Tokens used across turns, and the Settings · AI limit when the conversation started. */
  usage: { used: number; limit: number };
  turns: Turn[];
  scope?: AssistantScope;
  handles: HandleSnapshot;
  /** Per plan card id. */
  plans: Record<string, PlanProposal>;
}

const BODY_VERSION = 1;

/** conversationBody is what SaveConversation stores: the view as plain JSON. */
export function conversationBody(c: ConversationView): string {
  return JSON.stringify({ version: BODY_VERSION, ...c });
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function storedScope(v: unknown): boolean {
  return isObject(v) &&
    (v.folder === undefined || (Array.isArray(v.folder) && v.folder.every((c) => isObject(c) && typeof c.id === "string" && typeof c.name === "string"))) &&
    (v.entries === undefined || (Array.isArray(v.entries) && v.entries.every((e) => isObject(e) && typeof e.id === "string" && typeof e.name === "string" && typeof e.folder === "boolean")));
}

function storedCard(v: unknown): boolean {
  if (!isObject(v)) return false;
  if (v.kind !== "operation" && v.kind !== "resources") return true;
  if (typeof v.state !== "string" || !["draft", "running", "done", "failed", "cancelled", "undone"].includes(v.state) ||
    typeof v.id !== "string" || typeof v.title !== "string" || typeof v.note !== "string" || !Array.isArray(v.rows) ||
    !v.rows.every((r) => isObject(r) && typeof r.id === "string" && typeof r.name === "string" && typeof r.folder === "boolean" &&
      typeof r.path === "string" && typeof r.modified === "string" && typeof r.size === "number" && Number.isFinite(r.size))) return false;
  if (v.kind === "resources") {
    return typeof v.resource === "string" && ["trash", "shares", "transfers"].includes(v.resource) && typeof v.nextPageToken === "string" &&
      v.rows.every((r) => isObject(r) && typeof r.kind === "string" && ["trash_file", "trash_folder", "share", "task", "upload", "download"].includes(r.kind) &&
        typeof r.parentId === "string" && typeof r.state === "string" && typeof r.progress === "number" && Number.isFinite(r.progress));
  }
  return OPERATION_ACTIONS.includes(v.action as OperationAction) &&
    typeof v.folderId === "string" && typeof v.path === "string" && typeof v.name === "string" && storedScope(v.scope) &&
    Array.isArray(v.targetIds) && v.targetIds.length <= 200 && v.targetIds.every((id) => typeof id === "string") &&
    isObject(v.progress) && Number.isSafeInteger(v.progress.done) && Number.isSafeInteger(v.progress.total) &&
    typeof v.stopped === "boolean" && Array.isArray(v.results) &&
    v.results.every((r) => isObject(r) && typeof r.id === "string" && typeof r.name === "string" && typeof r.error === "string");
}

/**
 * readConversation reads a saved body back; anything that is not a
 * conversation this client wrote is null, and the page says it could not be
 * opened rather than showing half of it.
 */
export function readConversation(body: string, id: string): ConversationView | null {
  let o: unknown;
  try {
    o = JSON.parse(body);
  } catch {
    return null;
  }
  if (!isObject(o) || o.version !== BODY_VERSION || !Array.isArray(o.turns)) return null;
  const usage = isObject(o.usage) ? o.usage : {};
  const handles = isObject(o.handles) && Array.isArray(o.handles.records) ? (o.handles as unknown as HandleSnapshot) : { records: [] };
  const turns = (o.turns as unknown[]).filter((turn): turn is Turn => isObject(turn) && typeof turn.id === "string" && Array.isArray(turn.cards) && Array.isArray(turn.traces));
  if (turns.some((turn) => turn.cards.some((card) => !storedCard(card)))) return null;
  for (const turn of turns) for (const card of turn.cards) {
    if (["operation", "offline", "download", "share"].includes(card.kind) && card.state === "running") {
      card.state = "failed";
      card.note = t("上次操作中断，未自动重试；请重新查询实际状态。");
    }
  }
  return {
    id,
    title: typeof o.title === "string" ? o.title : "",
    createdAt: typeof o.createdAt === "string" ? o.createdAt : "",
    updatedAt: typeof o.updatedAt === "string" ? o.updatedAt : "",
    usage: { used: Number(usage.used) || 0, limit: Number(usage.limit) || 0 },
    turns,
    scope: isObject(o.scope) ? (o.scope as AssistantScope) : undefined,
    handles,
    plans: isObject(o.plans) ? (o.plans as Record<string, PlanProposal>) : {},
  };
}

/** conversationTitle is the first words the user said, on one line. */
export function conversationTitle(text: string): string {
  const line = text.replace(/\s+/g, " ").trim();
  return line.length > 40 ? `${line.slice(0, 40)}…` : line || t("新对话");
}

/** Any unconfirmed write card lets the home page resume the conversation. */
export function hasPendingPlan(turns: readonly Turn[]): boolean {
  return turns.some((turn) => turn.cards.some((card) => card.state === "draft" && card.kind !== "files" && card.kind !== "stats" && card.kind !== "resources"));
}

/** findCard is a card by id across the turns, with the turn holding it. */
export function findCard(turns: readonly Turn[], cardId: string): { turn: Turn; card: Card } | null {
  for (const turn of turns) {
    const card = turn.cards.find((c) => c.id === cardId);
    if (card) return { turn, card };
  }
  return null;
}

/** planCardOf is the plan card showing planId, if the conversation has one. */
export function planCardOf(turns: readonly Turn[], planId: string): { turn: Turn; card: PlanCard } | null {
  for (const turn of turns) {
    const card = turn.cards.find((c): c is PlanCard => c.kind === "plan" && c.planId === planId);
    if (card) return { turn, card };
  }
  return null;
}

/* ---------------- what a plan says of itself ---------------- */

/**
 * originOf is where a media plan's names came from: the model when it named
 * any group, else a TMDB match, else the rules alone.
 */
export function originOf(identities: readonly Identity[]): PlanOrigin {
  const sources = new Set(identities.filter((i) => i.type !== "unknown").map((i) => i.source));
  if (sources.has("model")) return "model";
  if (sources.has("tmdb")) return "tmdb";
  return "rules";
}

const BASIS: Record<PlanOrigin, string> = {
  rules: N_("本地文件名规则生成的建议，没有使用模型。"),
  tmdb: N_("本地规则与 TMDB 精确匹配生成的建议；模型没有直接写入云盘。"),
  model: N_("本地规则与模型识别生成的建议；模型没有直接写入云盘。"),
  agent: N_("AI 在对话中提出的改动；模型没有直接写入云盘，执行前由你逐项确认。"),
  rename: N_("按你设定的改名规则生成，没有使用模型。"),
};

export function basisOf(origin: PlanOrigin): string {
  return t(BASIS[origin]);
}

/** cardSource is the plan card's 「Data from TMDB」/「Generated by local rules」 key. */
export function cardSource(origin: PlanOrigin): PlanCard["source"] {
  return origin === "agent" || origin === "rename" ? "" : origin;
}

/** planCounts is what a plan card and the model are told of a plan: ticked changes, trash among them, kept, conflicts. */
export function planCounts(changes: readonly Change[], kept: number, errors: ReadonlyMap<string, string>): PlanCard["counts"] {
  const doing = active(changes);
  return {
    changes: doing.length,
    kept: kept + changes.filter((c) => !c.locked && !c.selected).length,
    trash: doing.filter((c) => c.op !== "move").length,
    conflicts: doing.filter((c) => errors.has(c.item.id)).length,
  };
}

/** planSample is the one change a plan card shows: the first ticked move that renames or moves, a video when there is one. */
export function planSample(changes: readonly Change[]): PlanCard["sample"] {
  const moves = active(changes).filter((x) => x.op === "move");
  const c = moves.find((x) => x.role === "video") ?? moves[0];
  if (!c) return null;
  return { from: c.item.name, to: [...c.toPath, c.name].join(" / ") };
}

/**
 * planRoute is a plan card's one-line direction, 「Pending classification → Documentaries / Shanhai Journey / Season 01」:
 * the scope, then the folder path every ticked move shares (the scope alone when they share none).
 */
export function planRoute(scopeName: string, changes: readonly Change[]): string {
  const target = planTarget(changes);
  return target ? `${scopeName} → ${target}` : scopeName;
}

/**
 * proposedChanges turns the model's propose_changes items into a plan over
 * `tree` (root scope): every item it named that the index still has, a move
 * or rename where it asked, Move to Trash unticked until the user ticks it.
 * Items the tree no longer holds are left out and counted.
 */
export function proposedChanges(tree: Tree, items: readonly ProposedItem[]): { changes: Change[]; missing: number } {
  const changes: Change[] = [];
  let missing = 0;
  for (const it of items) {
    const entry = tree.byId.get(it.id);
    if (!entry) {
      missing++;
      continue;
    }
    const from = tree.pathOf(entry.id).slice(0, -1);
    const name = it.trash ? entry.name : it.name || entry.name;
    changes.push({
      item: entry,
      from,
      op: it.trash ? "trash" : "move",
      toPath: it.trash || !it.toPath ? from.slice() : it.toPath.slice(),
      name,
      proposed: name,
      selected: !it.trash,
      locked: "",
      note: it.trash ? t("AI 建议移到回收站，确认后再勾选") : "",
      series: "",
      role: entry.folder ? "folder" : classify(entry),
    });
  }
  return { changes, missing };
}

/** commonTrail is the deepest folder every trail runs through ([] = the root). */
export function commonTrail(trails: readonly (readonly Crumb[])[]): Crumb[] {
  const out: Crumb[] = [];
  if (!trails.length) return out;
  for (let i = 0; trails.every((t) => t[i] && t[i].id === trails[0][i].id); i++) out.push({ id: trails[0][i].id, name: trails[0][i].name });
  return out;
}

/* ---------------- Organize on import ---------------- */

/** arrivalBasis is the pending organization suggestion's pill: where the names came from, and that no model was asked. */
export function arrivalBasis(origin: PlanOrigin): string {
  if (origin === "tmdb") return t("名称来自 TMDB 精确匹配 · 未请求模型");
  if (origin === "model") return t("部分名称由模型识别");
  return t("按文件名规则命名 · 未请求模型");
}

/** arrivalLine is the pending organization suggestion's second line: what is kept besides the moves, and that nothing ran. */
export function arrivalLine(keep: number, versions: boolean): string {
  return `${versions ? t("同一集的不同版本都保留；") : ""}${keep ? t("另外 {n} 项不确定，保持原样。", { n: keep }) : ""}${t("没有自动执行。")}`;
}

/** planTarget is where a plan's ticked moves go, as the folder path they share below the scope ("" for none). */
export function planTarget(changes: readonly Change[]): string {
  const paths = active(changes)
    .filter((c) => c.op === "move")
    .map((c) => c.toPath);
  const common: string[] = [];
  for (let i = 0; paths.length && paths.every((p) => i < p.length && p[i] === paths[0][i]); i++) common.push(paths[0][i]);
  return common.join(" / ");
}
