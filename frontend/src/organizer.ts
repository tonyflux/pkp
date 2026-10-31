/*
 * AI Assistant · Media Organization: the plan as pure functions. A plan is
 * a list of Change over a folder and everything below it (the Scope): move
 * and/or rename, Move to Recycle Bin, or Move to Recycle Bin Only If Empty After Execution for a source folder.
 * Whatever proposed it — media.ts from the model's identification — the user
 * decides every item, and problems() says what may not be sent. The Go side
 * checks the same invariants again against fresh listings before it acts
 * (services/organize.go), so these checks are for the preview, not for safety.
 */
import type { Entry, OrganizeChange } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "./i18n";

/** The plan never holds more than this many items; the drive side refuses a bigger request. */
export const PLAN_LIMIT = 5000;

/* ---------------- the Scope ---------------- */

/**
 * Tree is the Scope as the index sees it: every live entry below scopeId
 * (FolderTree), looked up by id and by folder. Paths are folder names below
 * the scope, which is also how a plan addresses its destinations.
 */
export interface Tree {
  scopeId: string;
  byId: ReadonlyMap<string, Entry>;
  /** Entries directly in a folder, keyed by the folder's id (the scope's own id for its top level). */
  children: ReadonlyMap<string, readonly Entry[]>;
  childrenOf(id: string): readonly Entry[];
  /** Names from below the scope down to and including id; [] for the scope itself or an id not in it. */
  pathOf(id: string): string[];
  /** Folder ids above id, nearest first, stopping below the scope. */
  ancestorsOf(id: string): string[];
}

export function treeOf(entries: readonly Entry[], scopeId: string): Tree {
  const byId = new Map<string, Entry>();
  const children = new Map<string, Entry[]>();
  for (const e of entries) {
    if (e.id === scopeId || byId.has(e.id)) continue;
    byId.set(e.id, e);
    const list = children.get(e.parentId);
    if (list) list.push(e);
    else children.set(e.parentId, [e]);
  }
  const paths = new Map<string, string[]>();
  const ancestorsOf = (id: string): string[] => {
    const out: string[] = [];
    let at = byId.get(id)?.parentId;
    // The depth guard only stops a cycle a broken index could hold.
    while (at !== undefined && at !== scopeId && out.length < 256) {
      const parent = byId.get(at);
      if (!parent) break;
      out.push(at);
      at = parent.parentId;
    }
    return out;
  };
  const pathOf = (id: string): string[] => {
    const cached = paths.get(id);
    if (cached) return cached.slice();
    const e = byId.get(id);
    if (!e) return [];
    const path = [...ancestorsOf(id).reverse().map((a) => byId.get(a)!.name), e.name];
    paths.set(id, path);
    return path.slice();
  };
  return {
    scopeId,
    byId,
    children,
    childrenOf: (id) => children.get(id) ?? [],
    pathOf,
    ancestorsOf,
  };
}

/* ---------------- the plan ---------------- */

export type Role = "video" | "subtitle" | "audio" | "other" | "folder";

export interface Change {
  item: Entry;
  /** The folder it is in now, as names below the scope. */
  from: string[];
  op: "move" | "trash" | "trashIfEmpty";
  /** Where it ends up, as names below the scope; trash items keep `from`. */
  toPath: string[];
  /** The name it ends up with; the user may edit it. */
  name: string;
  /** The name the plan generated: name !== proposed means the user edited it. */
  proposed: string;
  selected: boolean;
  /** Why the item cannot be organized at all; its row takes no edits. */
  locked: string;
  /** Why it is proposed: a Residual reason, Move to Recycle Bin Only If Empty After Execution, … */
  note: string;
  /** media.ts series key ("" for none): what the Organized tree folds by. */
  series: string;
  role: Role;
}

/** Names compare as the drive might treat them: regardless of case and Unicode form. */
export function nameKey(name: string): string {
  return name.normalize("NFC").toLowerCase();
}

/** nameError refuses a new name a folder on any of the user's computers could not hold. */
export function nameError(name: string): string {
  if (!name.trim()) return t("请输入文件名");
  if (/[\\/:*?"<>|\x00-\x1F]/.test(name) || /[.\s]$/.test(name) || name === "." || name === "..") {
    return t("名称不能含路径分隔符、保留字符或末尾空格/句点");
  }
  return "";
}

function samePath(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((name, i) => name === b[i]);
}

/** hasChange: the item would end up other than it is now. */
export function hasChange(c: Change): boolean {
  return c.op !== "move" || c.name !== c.item.name || !samePath(c.toPath, c.from);
}

/** What the plan would do: ticked, not locked and different from now. */
export function active(changes: readonly Change[]): Change[] {
  return changes.filter((c) => c.selected && !c.locked && hasChange(c));
}

/** edited: the user changed the name the plan proposed. */
export function edited(c: Change): boolean {
  return c.name !== c.proposed;
}

/** rename is the change with the user's name; `proposed` stays, so the row knows it was edited. */
export function rename(c: Change, name: string): Change {
  return { ...c, name: name.trim() };
}

/* ---------------- checking ---------------- */

/** A folder the plan makes on the way: where it goes and every change that passes through it. */
interface Made {
  name: string;
  parent: string;
  path: string[];
  via: string[];
}

interface Layout {
  /** change id → the folder it lands in: "e:<id>" existing, "n:…" one to be made. */
  target: Map<string, string>;
  made: Map<string, Made>;
  errors: Map<string, string>;
}

/**
 * layout walks each move's toPath layer by layer from the scope: a layer
 * already there as one folder is reused; a name held by a file, by several
 * entries, or by a folder the plan itself moves or trashes stops every item
 * through it; anything else is made (once per path, the first spelling).
 */
function layout(moves: readonly Change[], tree: Tree, changed: ReadonlySet<string>): Layout {
  const target = new Map<string, string>();
  const made = new Map<string, Made>();
  const errors = new Map<string, string>();
  const childrenByName = new Map<string, Map<string, Entry[]>>();
  for (const c of moves) {
    let loc = `e:${tree.scopeId}`;
    let path: string[] = [];
    let error = "";
    for (const layer of c.toPath) {
      const bad = nameError(layer);
      if (bad) {
        error = t("文件夹名「{name}」不可用：{error}", { name: layer, error: bad });
        break;
      }
      path = [...path, layer];
      if (loc.startsWith("e:")) {
        const parent = loc.slice(2);
        let names = childrenByName.get(parent);
        if (!names) {
          names = new Map<string, Entry[]>();
          for (const entry of tree.childrenOf(parent)) {
            const key = nameKey(entry.name);
            const holders = names.get(key);
            if (holders) holders.push(entry);
            else names.set(key, [entry]);
          }
          childrenByName.set(parent, names);
        }
        const holders = names.get(nameKey(layer)) ?? [];
        if (holders.length > 1) {
          error = t("这里有多个「{name}」", { name: layer });
          break;
        }
        if (holders.length === 1) {
          const h = holders[0];
          if (!h.folder) error = t("「{name}」已被同名文件占用", { name: layer });
          else if (changed.has(h.id)) {
            error = t("「{name}」本身也在这次计划里改动", { name: layer });
            if (!errors.has(h.id)) errors.set(h.id, t("有项要移进这个文件夹，它不能同时改动"));
          } else loc = `e:${h.id}`;
          if (error) break;
          continue;
        }
      }
      const next = `n:${loc}\u0000${nameKey(layer)}`;
      const m = made.get(next) ?? { name: layer, parent: loc, path, via: [] };
      m.via.push(c.item.id);
      made.set(next, m);
      loc = next;
    }
    if (error) errors.set(c.item.id, error);
    else target.set(c.item.id, loc);
  }
  // A folder made only for changes that failed further down still counts:
  // the others through it are fine. One made for nobody cannot happen.
  return { target, made, errors };
}

/** The folders rule 1 and 2 treat as changed: moved, renamed or trashed (trashIfEmpty runs last, only when empty). */
function changedFolders(doing: readonly Change[]): Set<string> {
  return new Set(doing.filter((c) => c.item.folder && c.op !== "trashIfEmpty").map((c) => c.item.id));
}

/**
 * problems says, for each change the plan would make, why it must not:
 * a folder and something inside it both changed; a destination layer
 * held by a file, by several entries, or itself changed; a new name no folder
 * can hold; or a name it would share where it lands — with an item staying
 * (unticked or untouched), one already there, a folder to be made, or
 * another change. Trashed items hold their names until the end. Both sides
 * of a clash are marked. Unticked changes are never checked.
 */
export function problems(changes: readonly Change[], tree: Tree): Map<string, string> {
  const doing = active(changes);
  const out = new Map<string, string>();
  const fail = (id: string, reason: string) => {
    if (!out.has(id)) out.set(id, reason);
  };
  const changed = changedFolders(doing);

  // 1. ancestor and descendant never both.
  for (const c of doing) {
    for (const up of tree.ancestorsOf(c.item.id)) {
      if (!changed.has(up)) continue;
      fail(c.item.id, t("它所在的文件夹也在这次计划里改动，两者不能同时改"));
      fail(up, t("文件夹里的项也在这次计划里改动，两者不能同时改"));
    }
  }

  const moves = doing.filter((c) => c.op === "move");
  for (const c of moves) {
    if (c.name !== c.item.name) {
      const bad = nameError(c.name);
      if (bad) fail(c.item.id, bad);
    }
  }

  // 2. the destination path, layer by layer.
  const { target, made, errors } = layout(moves, tree, changed);
  for (const [id, reason] of errors) fail(id, reason);

  // 3. names where everything ends up.
  const held = new Map<string, string[]>();
  const hold = (loc: string, name: string, who: string) => {
    const slot = `${loc}\u0001${nameKey(name)}`;
    const list = held.get(slot);
    if (list) list.push(who);
    else held.set(slot, [who]);
  };
  const moving = new Set(moves.filter((c) => !out.has(c.item.id)).map((c) => c.item.id));
  for (const e of tree.byId.values()) if (!moving.has(e.id)) hold(`e:${e.parentId}`, e.name, "");
  for (const c of moves) {
    const loc = target.get(c.item.id);
    if (loc && moving.has(c.item.id)) hold(loc, c.name, `c:${c.item.id}`);
  }
  for (const [loc, m] of made) hold(m.parent, m.name, `m:${loc}`);
  for (const holders of held.values()) {
    if (holders.length < 2) continue;
    for (const who of holders) {
      if (who.startsWith("c:")) fail(who.slice(2), t("目标位置重名，请修改名称或取消此项"));
      else if (who.startsWith("m:")) {
        const m = made.get(who.slice(2))!;
        for (const id of m.via) fail(id, t("要新建的文件夹「{name}」和那里的其他项重名", { name: m.name }));
      }
    }
  }
  return out;
}

/* ---------------- confirming and carrying out ---------------- */

export interface PlanSummary {
  selected: number;
  moves: number;
  renames: number;
  /** Folders the run makes, as names below the scope, parents before children. */
  newFolders: string[][];
  trash: { count: number; size: number };
  trashIfEmpty: number;
  conflicts: number;
  /** More than PLAN_LIMIT items: the plan cannot be sent, please narrow the scope. */
  overLimit: boolean;
}

function comparePaths(a: readonly string[], b: readonly string[]): number {
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const d = a[i].localeCompare(b[i], undefined, { numeric: true });
    if (d) return d;
  }
  return a.length - b.length;
}

/** summarize is the Confirmation Layer: what the selected plan does, with `errors` from problems(). */
export function summarize(changes: readonly Change[], tree: Tree, errors: ReadonlyMap<string, string>): PlanSummary {
  const doing = active(changes);
  const moves = doing.filter((c) => c.op === "move");
  const fine = moves.filter((c) => !errors.has(c.item.id));
  const { made } = layout(fine, tree, changedFolders(doing));
  const trash = doing.filter((c) => c.op === "trash");
  return {
    selected: doing.length,
    moves: moves.filter((c) => !samePath(c.toPath, c.from)).length,
    renames: moves.filter((c) => c.name !== c.item.name).length,
    newFolders: [...made.values()].map((m) => m.path).sort(comparePaths),
    trash: { count: trash.length, size: trash.reduce((sum, c) => sum + (c.item.folder ? 0 : c.item.size || 0), 0) },
    trashIfEmpty: doing.filter((c) => c.op === "trashIfEmpty").length,
    conflicts: doing.filter((c) => errors.has(c.item.id)).length,
    overLimit: doing.length > PLAN_LIMIT,
  };
}

/**
 * toChanges is the Organize request for what the plan would do. toPath is
 * always set (trash items: where they are); After-Execution-Empty folders come last,
 * deepest first, so a parent is looked at after its emptied children.
 */
export function toChanges(changes: readonly Change[]): OrganizeChange[] {
  const doing = active(changes);
  const ifEmpty = doing.filter((c) => c.op === "trashIfEmpty").sort((a, b) => b.from.length - a.from.length);
  return [...doing.filter((c) => c.op !== "trashIfEmpty"), ...ifEmpty].map((c) => ({
    fileId: c.item.id,
    folder: c.item.folder,
    name: c.item.name,
    parentId: c.item.parentId,
    op: c.op === "move" ? "" : c.op,
    newName: c.op === "move" && c.name !== c.item.name ? c.name : "",
    toPath: c.op === "move" ? c.toPath.slice() : c.from.slice(),
    toParentId: "",
  }));
}
