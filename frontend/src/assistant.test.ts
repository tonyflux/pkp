// AI Assistant: nothing reaches a model
// before the account agreed, and one agreement covers a whole turn; Always Allow
// holds for that account and the metadata level only; L0 plans (file page,
// arrivals) never ask a model; TMDB answers never appear in a model request;
// an over-budget turn is never sent. The services binding records every call
// and answers like Go would; the model is a script.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { watch } from "vue";
import type {
  Account,
  AIReply,
  AIRequest,
  AISettings,
  Arrival,
  DriveStatus,
  Entry,
  OrganizeRecord,
  OrganizeRequest,
  TitleQuery,
} from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const calls: { name: string; args: unknown[] }[] = [];
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const service = () =>
    new Proxy(
      {},
      {
        get:
          (_target, name: string) =>
          (...args: unknown[]) => {
            calls.push({ name, args: JSON.parse(JSON.stringify(args ?? null)) });
            const answer = answers.get(name);
            try {
              return Promise.resolve(answer ? answer(...args) : null);
            } catch (err) {
              return Promise.reject(err);
            }
          },
      },
    );
  return { calls, answers, service };
});

vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service(),
  PlayerService: backend.service(),
  AppearanceService: backend.service(),
  MenuBarService: backend.service(),
  // Anonymous Usage Statistics is not what these tests watch: answered, not recorded.
  TelemetryService: new Proxy({}, { get: () => () => Promise.resolve(null) }),
}));

import {
  accounts,
  aiSettings,
  answerConsent,
  assistant,
  askAssistant,
  setAssistantScope,
  organizeAssistantResults,
  cleanupAssistantResults,
  planCleanupEntries,
  planBackTo,
  openConversation,
  openPlanCard,
  openAssistantSearch,
  localSearch,
  ui,
  assistantHome,
  drive,
  identifyRemaining,
  reviewPlan,
  renamePlanItem,
  connectModel,
  cancelAssistantTask,
  inbox,
  loadAccounts,
  loadInbox,
  openArrival,
  openArrivals,
  dismissArrivals,
  openPlanConfirm,
  runPlan,
  organizeEntries,
  pendingArrivals,
  planSummary,
  planReviewed,
  planUndecided,
  keepPlanItems,
  keepAllPlanItems,
  pickPlanItems,
  organizeFolder,
  ensureArrivalSummary,
  retryTurn,
  retryTurnId,
  submitComposer,
  type AskScope,
} from "./store";
import { emptyQuery } from "./searchquery";
import type { CardRow } from "./agent";
import { conversationBody, type ConversationView } from "./assistantcore";

/* ---------------- the drive ---------------- */

const consent: Record<string, { metadata: boolean; content: boolean }> = {};

function statusOf(id: string): DriveStatus {
  return {
    accountId: id,
    configured: true,
    account: `${id}@example.com`,
    clientId: "",
    deviceId: id,
    userId: `user-${id}`,
    apiBaseUrl: "",
    tokenExpiry: "",
    configDir: "",
    needsPassword: false,
  };
}

function accountOf(id: string): Account {
  return {
    id,
    login: `${id}@example.com`,
    label: id.toUpperCase(),
    nickname: "",
    avatar: "",
    current: false,
    needsPassword: false,
    usedAt: "",
    aiConsent: consent[id].metadata,
    aiContentConsent: consent[id].content,
  };
}

function entry(id: string, name: string, parentId: string, folder = false): Entry {
  return {
    id,
    name,
    parentId,
    kind: folder ? "drive#folder" : "drive#file",
    mimeType: folder ? "" : "video/x-matroska",
    size: folder ? 0 : 1_000_000_000,
    modifiedTime: "2026-10-01T00:00:00Z",
    thumbnail: "",
    folder,
    playable: !folder,
    starred: false,
    trashed: false,
    phase: "",
    category: folder ? "" : "video",
    duration: 0,
    preview: "",
    path: "",
    deleteTime: "",
  };
}

// Unclassified holds a release the rules name (and TMDB matches), and clips the rules cannot name.
const inbox0 = entry("dl", "待分类", "", true);
const series = [1, 2, 3].map((n) => entry(`s${n}`, `[SubGroup] Shanhai Jixing - 0${n} [1080p].mkv`, "dl"));
const clips = [1, 2, 3].map((n) => entry(`c${n}`, `VID_000${n}.mp4`, "dl"));
const below = [...series, ...clips];

// What TMDB answers: never to be seen in any model request.
const TMDB_TITLE = "TMDB秘密标题";
const TMDB_ORIGINAL = "TMDB Secret Original";

const settings: AISettings = {
  provider: "openai",
  baseUrl: "https://api.deepseek.com/v1",
  model: "deepseek-chat",
  tokenLimit: 50000,
  keyHint: "••••8E4C",
  keychain: true,
  configured: true,
  naming: "plex",
  seriesTemplate: "",
  movieTemplate: "",
  tools: "yes",
  stream: "yes",
};

/** The model's replies to AIChat, in order; past the script it just answers. */
let chatScript: Array<() => unknown> = [];
let arrivals: Arrival[] = [];
let organized = 0;
let reviews: Record<string, { key: string; fileId: string }[]> = {};

function reply(text: string, toolCalls: { name: string; arguments: unknown }[] = []) {
  return {
    text,
    toolCalls: toolCalls.map((c, i) => ({ id: `call_${i}`, name: c.name, arguments: JSON.stringify(c.arguments) })),
    stop: toolCalls.length ? "tool" : "end",
    input: 300,
    outputTokens: 40,
  };
}

/** identifyAnswer names every group the request asks about as a series, like a model would. */
function identifyAnswer(request: AIRequest) {
  const ids = [...(request.messages ?? []).map((m) => m.content).join("\n").matchAll(/^(g\d+) · /gm)].map((m) => m[1]);
  return {
    output: JSON.stringify({ groups: ids.map((id) => ({ id, type: "series", title: "Phone Clips", season: 1, numbers: ["episode"] })) }),
    input: 400,
    outputTokens: 60,
  };
}

/**
 * answer registers a method's answer with the binding's own argument
 * types; the proxy hands every call's arguments through untyped, as Wails
 * would deliver what the store passed.
 */
function answer<A extends unknown[]>(name: string, fn: (...args: A) => unknown) {
  backend.answers.set(name, fn as (...args: unknown[]) => unknown);
}

backend.answers.set("Status", () => drive.status);
backend.answers.set("Accounts", () => Object.keys(consent).map(accountOf));
backend.answers.set("SetAIConsent", (id, level, always) => {
  consent[String(id)][level === "content" ? "content" : "metadata"] = !!always;
});
backend.answers.set("AISettings", () => aiSettings.value);
backend.answers.set("AIChat", () => (chatScript.shift() ?? (() => reply("好的。")))());
answer("AIComplete", (_id: string, request: AIRequest) => identifyAnswer(request));
backend.answers.set("FolderTree", (_account, folder) => (folder === "dl" ? below : [inbox0, ...below]));
backend.answers.set("PlanTree", (_account, scope) => (scope === "dl" ? below : [inbox0, ...below]));
backend.answers.set("FolderTrail", (id) => (id === "dl" ? [{ id: "dl", name: "待分类" }] : []));
backend.answers.set("FindFolders", () => [{ id: "dl", path: "全部文件 / 待分类", trail: [{ id: "dl", name: "待分类" }] }]);
backend.answers.set("FolderStats", () => ({
  files: 6,
  folders: 0,
  size: 6_000_000_000,
  categories: [{ category: "video", count: 6, size: 6_000_000_000 }],
  dupGroups: 0,
  dupCopies: 0,
  dupWaste: 0,
  unfinished: 0,
  largest: [],
}));
backend.answers.set("SearchIndex", () => ({ hits: [], total: 0, aggregate: [], under: "", underMissing: false }));
answer("MatchTitles", (queries: TitleQuery[]) =>
  queries.map((q) => ({ key: q.key, found: true, kind: q.kind, id: 7, title: TMDB_TITLE, originalTitle: TMDB_ORIGINAL, year: 2023 })),
);
backend.answers.set("MediaResolutions", () => ({}));
backend.answers.set("Conversations", () => []);
backend.answers.set("OrganizeHistory", () => []);
backend.answers.set("Arrivals", () => arrivals);
answer("SuggestionReviews", (account: string) => reviews[account] ?? []);
answer("SetSuggestionReviews", (account: string, records: { key: string; fileId: string }[], keep: boolean) => {
  const keys = new Set(records.map((r) => r.key));
  reviews[account] = [...(reviews[account] ?? []).filter((r) => !keys.has(r.key)), ...(keep ? records : [])];
});
answer("Organize", (_account: string, req: OrganizeRequest): OrganizeRecord => {
  organized += 1;
  const record = {
    id: `r${organized}`,
    items: (req.changes ?? []).map((c) => ({ fileId: c.fileId, done: true, error: "" })),
    created: [],
    finished: true,
    stopped: false,
    undo: null,
  };
  // Only the fields the store reads; the rest of a record does not matter here.
  return record as unknown as OrganizeRecord;
});

async function settle() {
  for (let i = 0; i < 400; i++) await Promise.resolve();
}

const callsOf = (name: string) => backend.calls.filter((c) => c.name === name);
const modelCalls = () => callsOf("AIChat").length + callsOf("AIComplete").length;
const modelBodies = () => JSON.stringify([...callsOf("AIChat"), ...callsOf("AIComplete")].map((c) => c.args));

async function say(text: string) {
  assistant.prompt = text;
  void submitComposer();
  await settle();
}

/** consentSheets counts every authorization sheet that comes on screen. */
let consentSheets = 0;
watch(
  () => assistant.consent,
  (sheet) => {
    if (sheet) consentSheets += 1;
  },
  { flush: "sync" },
);

const defaultAnswers = new Map(backend.answers);
afterEach(() => {
  backend.answers.clear();
  for (const [name, fn] of defaultAnswers) backend.answers.set(name, fn);
});

beforeEach(async () => {
  consent.a = { metadata: false, content: false };
  consent.b = { metadata: false, content: false };
  chatScript = [];
  arrivals = [];
  reviews = {};
  inbox.account = "";
  inbox.arrivals = [];
  inbox.summaries = {};
  assistant.scope = {};
  drive.trail = [];
  drive.entries = [];
  drive.picked = [];
  drive.indexProgress = null;
  localSearch.range = null;
  localSearch.result = null;
  localSearch.picked = [];
  localSearch.text = "";
  localSearch.account = "";
  localSearch.error = "";
  localSearch.loading = false;
  ui.query = "";
  ui.route = "assistant";
  ui.busy = false;
  drive.status = statusOf("a");
  await loadAccounts();
  aiSettings.value = { ...settings };
  assistantHome();
  await settle();
  backend.calls.length = 0;
  consentSheets = 0;
});

describe("AI assistant · Scope and execution set", () => {
  it("keeps selection ids and name snapshots across composer turns", async () => {
    const selected = [entry("s1", "最初名称.mkv", "dl"), entry("s2", "第二项.mkv", "dl")];
    drive.entries = selected;
    drive.picked = selected.map((e) => e.id);
    setAssistantScope("selection");
    const scope = assistant.scope;
    chatScript = [
      () => reply("", [{ name: "search_files", arguments: {} }]), () => reply("第一轮。"),
      () => reply("", [{ name: "search_files", arguments: {} }]), () => reply("第二轮。"),
    ];
    void askAssistant("查找这些文件", scope);
    await settle();
    answerConsent(true, true);
    await settle();
    selected[0].name = "已改名.mkv";
    drive.entries = [clips[0]];
    drive.picked = [clips[0].id];
    drive.trail = [{ id: "elsewhere", name: "另一文件夹" }];
    await say("继续查找");
    expect(callsOf("SearchIndex").map((c) => c.args[1])).toEqual([
      expect.objectContaining({ ids: ["s1", "s2"] }), expect.objectContaining({ ids: ["s1", "s2"] }),
    ]);
    expect(assistant.conversation!.turns.map((turn) => turn.scope)).toEqual([scope, scope]);
    expect(assistant.scope.entries?.map((e) => e.name)).toEqual(["最初名称.mkv", "第二项.mkv"]);
    expect(modelBodies()).not.toContain("已改名.mkv");
    expect(modelBodies()).not.toContain("另一文件夹");
  });

  it("queries the exact folder id rather than resolving its ambiguous name", async () => {
    const folder = [{ id: "exact-folder", name: "待分类" }];
    chatScript = [() => reply("", [{ name: "search_files", arguments: {} }]), () => reply("完成。")];
    void askAssistant("查找视频", { folder });
    await settle();
    folder[0].id = "wrong-folder";
    folder[0].name = "已改名";
    answerConsent(true, false);
    await settle();
    expect(callsOf("SearchIndex")).toHaveLength(1);
    expect(callsOf("SearchIndex")[0].args[1]).toMatchObject({ folderId: "exact-folder", under: "待分类" });
    expect(callsOf("FindFolders")).toHaveLength(0);
    expect(assistant.conversation!.turns[0].scope?.folder).toEqual([{ id: "exact-folder", name: "待分类" }]);
  });

  it("rejects a known handle outside the selection without an operation card or execution", async () => {
    answer("SearchIndex", () => ({ hits: [clips[0]], total: 1, aggregate: [], under: "", underMissing: false }));
    chatScript = [() => reply("", [{ name: "search_files", arguments: {} }]), () => reply("找到。")];
    await say("先找文件");
    answerConsent(true, true);
    await settle();
    assistant.scope = { entries: [series[0]] };
    chatScript = [
      () => reply("", [{ name: "propose_changes", arguments: { title: "删除范围外文件", items: [{ handle: "f1", trash: true }] } }]),
      () => reply("已拒绝。"),
    ];
    await say("删除之前的结果");
    expect(assistant.conversation!.turns.at(-1)!.cards.filter((card) => card.kind === "plan")).toHaveLength(0);
    expect(modelBodies()).toContain("对象不在本轮范围内");
    expect(callsOf("Organize")).toHaveLength(0);
    expect(assistant.plan).toBeNull();
  });

  it.each(["organize_media", "propose_offline_add"] as const)("disambiguates %s and accepts the chosen handle", async (tool) => {
    const candidates = [
      { id: "first", path: "全部文件 / 甲 / 待分类", trail: [{ id: "first", name: "待分类" }] },
      { id: "second", path: "全部文件 / 乙 / 待分类", trail: [{ id: "second", name: "待分类" }] },
    ];
    answer("FindFolders", () => candidates);
    answer("FolderTrail", (id: string) => candidates.find((c) => c.id === id)?.trail ?? []);
    answer("FolderTree", (_account: string, folder: string) => folder === "second" ? series.map((file) => ({ ...file, parentId: "second" })) : []);
    chatScript = [
      () => reply("", [{ name: tool, arguments: { folder: "待分类", urls: ["magnet:?xt=urn:btih:0123456789012345678901234567890123456789"] } }]),
      () => {
        expect(callsOf("FolderTree")).toHaveLength(0);
        expect(assistant.live?.cards).toHaveLength(0);
        const request = callsOf("AIChat").at(-1)!.args[1] as AIRequest;
        const result = request.messages!.filter((m) => m.role === "tool").at(-1)!.content;
        expect(result).toContain("甲/待分类");
        expect(result).toContain("乙/待分类");
        const handle = /\b(f\d+)\s*\|\s*乙\/待分类/.exec(result)?.[1];
        expect(handle).toBeDefined();
        return reply("", [{ name: tool, arguments: { folder: handle, urls: ["magnet:?xt=urn:btih:0123456789012345678901234567890123456789"] } }]);
      },
      () => reply("已选择乙目录。"),
    ];
    await say("处理待分类");
    answerConsent(true, false);
    await settle();
    expect(callsOf("FindFolders")).toHaveLength(1);
    const cards = assistant.conversation!.turns.at(-1)!.cards;
    expect(cards).toHaveLength(1);
    if (tool === "organize_media") {
      expect(callsOf("FolderTree").map((c) => c.args)).toEqual([["a", "second"]]);
    } else expect(cards[0]).toMatchObject({ kind: "offline", folderId: "second", state: "draft" });
    expect(callsOf("Organize")).toHaveLength(0);
    expect(callsOf("OfflineAdd")).toHaveLength(0);
  });

  it("does not expose an outside namesake when resolving a folder in the current subtree", async () => {
    const root = { id: "root-scope", name: "当前范围" };
    const inside = { id: "inside", path: "全部文件 / 当前范围 / 待分类", trail: [root, { id: "inside", name: "待分类" }] };
    const outside = { id: "outside", path: "全部文件 / 私密目录 / 待分类", trail: [{ id: "private", name: "私密目录" }, { id: "outside", name: "待分类" }] };
    answer("FindFolders", () => [outside, inside]);
    answer("FolderTrail", (id: string) => id === inside.id ? inside.trail : outside.trail);
    chatScript = [
      () => reply("", [{ name: "find_folders", arguments: { path: "待分类" } }, { name: "propose_offline_add", arguments: { folder: "待分类", urls: ["magnet:?xt=urn:btih:0123456789012345678901234567890123456789"] } }]),
      () => reply("已生成范围内卡片。"),
    ];
    void askAssistant("添加到待分类", { folder: [root] });
    await settle();
    answerConsent(true, false);
    await settle();
    expect(modelBodies()).not.toContain("私密目录");
    expect(modelBodies()).toContain("当前范围/待分类");
    expect(assistant.conversation!.turns.at(-1)!.cards).toEqual([
      expect.objectContaining({ kind: "offline", folderId: "inside", state: "draft" }),
    ]);
    expect(callsOf("Organize")).toHaveLength(0);
    expect(callsOf("OfflineAdd")).toHaveLength(0);
  });

  it("builds an L0 draft only for deduplicated displayed ids without model or execution", async () => {
    answer("PlanTree", (_account: string, _folder: string, ids: string[]) => below.filter((e) => ids.includes(e.id)));
    const rows: CardRow[] = [series[0], series[1], series[0]].map((e) => ({
      id: e.id, name: e.name, folder: e.folder, path: "待分类", size: e.size, modified: e.modifiedTime, played: "",
    }));
    await organizeAssistantResults(rows);
    await settle();
    expect(assistant.plan).not.toBeNull();
    const plannedIds = assistant.plan!.groups.flatMap((group) => group.files.map((file) => file.id));
    expect([...new Set(plannedIds)].sort()).toEqual(["s1", "s2"]);
    expect(assistant.plan!.changes.every((change) => ["s1", "s2"].includes(change.item.id))).toBe(true);
    expect(modelCalls()).toBe(0);
    expect(callsOf("Organize")).toHaveLength(0);
  });

  it.each([
    { label: "files", entries: [series[0], clips[0], series[0]], ids: ["s1", "c1"] },
    { label: "a folder without expanding its children", entries: [inbox0, inbox0], ids: ["dl"] },
  ])("previews exact deduplicated displayed $label as unchecked trash without model or writes", async ({ entries, ids }) => {
    const rows: CardRow[] = entries.map((e) => ({
      id: e.id, name: e.name, folder: e.folder, path: "待分类", size: e.size, modified: e.modifiedTime, played: "",
    }));
    await cleanupAssistantResults(rows);
    await settle();
    const plan = assistant.plan!;
    expect(plan).toMatchObject({
      kind: "changes",
      ids,
    });
    expect(callsOf("PlanTree").map((call) => call.args)).toEqual([["a", "", ids, []]]);
    expect(plan.changes.map((change) => change.item.id)).toEqual(ids);
    expect(plan.changes.every((change) => change.op === "trash" && !change.selected
      && change.name === change.item.name)).toBe(true);
    expect(planSummary.value).toMatchObject({ selected: 0, moves: 0, renames: 0, trash: { count: 0 } });
    expect(modelCalls()).toBe(0);
    expect(backend.calls.every((call) => ["PlanTree", "SuggestionReviews"].includes(call.name))).toBe(true);
  });

  it("executes only selected cleanup candidates after final confirmation without moves or renames", async () => {
    const rows: CardRow[] = [series[0], series[1], clips[0]].map((e) => ({
      id: e.id, name: e.name, folder: e.folder, path: "待分类", size: e.size, modified: e.modifiedTime, played: "",
    }));
    await cleanupAssistantResults(rows);
    await runPlan(false);
    expect(callsOf("Organize")).toHaveLength(0);
    pickPlanItems(["s1"], true);
    await runPlan(false);
    expect(callsOf("Organize")).toHaveLength(0);
    expect(planSummary.value).toMatchObject({ selected: 1, moves: 0, renames: 0, trash: { count: 1 } });
    openPlanConfirm();
    await runPlan(false);
    await settle();
    expect(callsOf("Organize")).toHaveLength(1);
    const [account, request] = callsOf("Organize")[0].args as [string, OrganizeRequest];
    expect(account).toBe("a");
    expect(request.changes).toEqual([{
      fileId: "s1", folder: false, name: series[0].name, parentId: "dl",
      op: "trash", newName: "", toPath: ["待分类"], toParentId: "",
    }]);
    expect(callsOf("SetSuggestionReviews")).toHaveLength(0);
    expect(modelCalls()).toBe(0);
  });

  it("switches a reopened legacy media plan to exact unchecked cleanup candidates, excluding empty-folder helpers", async () => {
    const release = entry("release", "下载目录", "dl", true);
    const movies = series.map((file) => ({ ...file, parentId: release.id }));
    const candidates = [...movies, clips[0]];
    answer("FolderTree", () => [release, ...candidates]);
    answer("PlanTree", () => [inbox0, release, ...candidates, clips[1]]);
    const saved: ConversationView = {
      id: "legacy", title: "清理广告", createdAt: "", updatedAt: "",
      usage: { used: 0, limit: 50000 }, handles: { records: [] },
      turns: [{
        id: "old-turn", at: "", user: "清理广告", text: "", traces: [],
        cards: [{
          kind: "plan", id: "old-card", title: "旧整理计划", state: "draft", note: "",
          tool: "organize_media", planId: "old-plan", scopeId: "dl", scopePath: "待分类",
          counts: { changes: 0, kept: 4, trash: 0, conflicts: 0 }, sample: null, source: "rules", recordId: "",
        }],
        usage: { input: 0, output: 0, calls: 0 }, stop: "end", error: "", denied: false, notice: "", mode: "tools", modeNote: "",
      }],
      plans: { "old-plan": { kind: "media", scope: [{ id: "dl", name: "待分类" }], ids: [], instructions: "", identities: [] } },
    };
    answer("Conversation", () => conversationBody(saved));
    await openConversation(saved.id);
    await openPlanCard("old-card");
    expect(assistant.plan!.base.kept.map((c) => c.item.id)).toContain("c1");
    expect(assistant.plan!.changes.some((c) => c.op === "trashIfEmpty" && c.item.id === "release")).toBe(true);
    const expected = ["c1", "s1", "s2", "s3"];
    expect(planCleanupEntries.value.map((e) => e.id).sort()).toEqual(expected);
    backend.calls.length = 0;
    await cleanupAssistantResults(planCleanupEntries.value);
    expect(assistant.plan!.changes.map((c) => c.item.id).sort()).toEqual(expected);
    expect(assistant.plan!.changes.every((c) => c.op === "trash" && !c.selected && !c.locked)).toBe(true);
    expect(planSummary.value).toMatchObject({ selected: 0, trash: { count: 0 }, moves: 0 });
    expect(modelCalls()).toBe(0);
    expect(callsOf("FolderTree")).toHaveLength(0);
    expect(callsOf("Organize")).toHaveLength(0);
    expect(assistant.planBack).toBe("chat");
    planBackTo();
    expect(assistant.screen).toBe("chat");
    expect(assistant.conversation!.turns[0].cards[0]).toMatchObject({ id: "old-card", state: "draft" });
    expect(callsOf("SaveConversation")).toHaveLength(0);
  });

  it("opens the original query including excluded stars, dates, sorting and local ids at limit 200", async () => {
    drive.indexProgress = { ready: true } as NonNullable<typeof drive.indexProgress>;
    const query = {
      ...emptyQuery(), keywords: ["山海", "-预告"], category: "video", ext: ["mkv"],
      starred: "no", createdAfter: "2026-01-02", createdBefore: "2026-09-30",
      modifiedAfter: "2026-03-04", modifiedBefore: "2026-10-01",
      under: "影视/纪录片", sort: "name", asc: true, ids: ["s1", "s2"], folderId: "exact-folder", limit: 40,
    };
    openAssistantSearch(query);
    await settle();
    expect(callsOf("SearchIndex").length).toBeGreaterThan(0);
    for (const call of callsOf("SearchIndex")) expect(call.args).toEqual(["a", { ...query, limit: 200 }]);
    expect(localSearch.range).toMatchObject({ account: "a", ids: ["s1", "s2"], folderId: "exact-folder" });
    query.ids.push("outside");
    expect(localSearch.range!.ids).toEqual(["s1", "s2"]);
    expect(modelCalls()).toBe(0);
    expect(callsOf("Organize")).toHaveLength(0);
  });
});

describe("AI assistant · Per-turn authorization", () => {
  it("sends nothing before Send, and nothing at all after Do not send", async () => {
    await say("待分类里都有什么？");
    expect(assistant.consent?.title).toBe("这一轮，要把哪些信息发给模型？");
    expect(assistant.consent?.to).toBe("发送给 DeepSeek · deepseek-chat");
    expect(modelCalls()).toBe(0);

    answerConsent(false, false);
    await settle();
    expect(modelCalls()).toBe(0);
    const turn = assistant.conversation?.turns.at(-1);
    expect(turn?.denied).toBe(true);
    expect(turn?.notice).toBe("未发送：保留本地结果。");
  });

  it("asks once per turn, however many tools the model calls", async () => {
    chatScript = [
      () =>
        reply("我先看看。", [
          { name: "find_folders", arguments: { path: "待分类" } },
          { name: "folder_summary", arguments: { folder: "待分类" } },
        ]),
      () => reply("再查一下。", [{ name: "search_files", arguments: { keywords: ["Shanhai"] } }]),
      () => reply("待分类里有 6 个视频。"),
    ];
    await say("待分类里都有什么？");
    answerConsent(true, false);
    await settle();
    expect(consentSheets).toBe(1);
    expect(callsOf("AIChat").length).toBe(3);
    const turn = assistant.conversation!.turns.at(-1)!;
    expect(turn.traces.map((t) => t.state)).toEqual(["done", "done", "done"]);
    expect(turn.text).toContain("待分类里有 6 个视频。");
    expect(callsOf("SaveConversation").length).toBe(1);
  });

  it("Always allow holds for that account and the metadata level only", async () => {
    await say("待分类里都有什么？");
    answerConsent(true, true);
    await settle();
    expect(callsOf("SetAIConsent")[0].args).toEqual(["a", "metadata", true]);
    const a = accounts.list.find((x) => x.id === "a")!;
    expect(a.aiConsent).toBe(true);
    expect(a.aiContentConsent).toBe(false);
    expect(callsOf("AIChat").length).toBe(1);

    // The same account again: straight to the model, no sheet.
    await say("还有别的吗？");
    expect(consentSheets).toBe(1);
    expect(callsOf("AIChat").length).toBe(2);

    // Another account still asks.
    drive.status = statusOf("b");
    await say("待分类里都有什么？");
    expect(consentSheets).toBe(2);
    expect(callsOf("AIChat").length).toBe(2);
    answerConsent(false, false);
    await settle();
  });

  it("never sends a turn past the conversation's token limit", async () => {
    aiSettings.value = { ...settings, tokenLimit: 10 };
    await say("待分类里都有什么？");
    expect(consentSheets).toBe(0);
    expect(modelCalls()).toBe(0);
    expect(assistant.conversation?.turns.at(-1)?.stop).toBe("budget");
  });

  it("No restrictions lets the same conversation send again from its next turn", async () => {
    aiSettings.value = { ...settings, tokenLimit: 10 };
    await say("待分类里都有什么？");
    expect(assistant.conversation?.turns.at(-1)?.stop).toBe("budget");

    aiSettings.value = { ...settings, tokenLimit: 0 };
    chatScript = [() => reply("待分类里有 6 个视频。")];
    await say("待分类里都有什么？");
    answerConsent(true, false);
    await settle();
    expect(callsOf("AIChat").length).toBe(1);
    expect(assistant.conversation?.turns.at(-1)?.stop).toBe("end");
  });
});

describe("AI assistant · Retry", () => {
  it("offers Retry on a failed turn and asks the same words again in its place", async () => {
    chatScript = [
      () => {
        throw new Error("模型服务返回错误（HTTP 503）：overloaded");
      },
    ];
    await say("待分类里都有什么？");
    answerConsent(true, false);
    await settle();
    const failed = assistant.conversation!.turns.at(-1)!;
    expect(failed.stop).toBe("error");
    expect(failed.error).toBe("模型服务返回错误（HTTP 503）：overloaded");
    expect(retryTurnId.value).toBe(failed.id);

    void retryTurn(failed.id);
    await settle();
    expect(retryTurnId.value).toBe("");
    answerConsent(true, false);
    await settle();
    expect(assistant.conversation!.turns.map((t) => [t.user, t.stop])).toEqual([["待分类里都有什么？", "end"]]);
    expect(callsOf("AIChat").length).toBe(2);
    expect(retryTurnId.value).toBe("");
  });

  it.each(["folder", "selection"] as const)("waits for the index before manually retrying the original %s scope", async (kind) => {
    const scope: AskScope = kind === "folder"
      ? { folder: [{ id: "exact-folder", name: "待分类" }] }
      : { entries: clips.slice(0, 2).map(({ id, name, folder }) => ({ id, name, folder })) };
    const expectedQuery = kind === "folder" ? { folderId: "exact-folder", under: "待分类" } : { ids: ["c1", "c2"] };
    const question = "查找原范围里的视频";
    const waitingText = "本地索引尚未完成，请等建立完毕后再查找。";
    drive.indexProgress = { account: "a", ready: false } as NonNullable<typeof drive.indexProgress>;
    answer("SearchIndex", () => {
      throw new Error("本地索引还在建立，完成后才能查找");
    });
    chatScript = [
      () => reply("", [{ name: "search_files", arguments: {} }]),
      () => reply(waitingText),
      () => reply("", [{ name: "search_files", arguments: {} }]),
      () => reply("现在找到了原范围的视频。"),
    ];
    void askAssistant(question, scope);
    await settle();
    answerConsent(true, false);
    await settle();
    const conv = assistant.conversation!;
    const blocked = conv.turns.at(-1)!;
    expect(blocked.stop).toBe("end");
    expect(blocked.error).toBe("");
    expect(blocked.text).toBe(waitingText);
    expect(blocked.traces).toEqual([
      expect.objectContaining({ tool: "search_files", state: "failed", detail: "本地索引还在建立，完成后才能查找" }),
    ]);
    expect(retryTurnId.value).toBe(blocked.id);
    expect(modelCalls()).toBe(2);
    expect(consentSheets).toBe(1);

    drive.trail = [{ id: "changed-folder", name: "现在的文件夹" }];
    setAssistantScope("folder");
    const changedScope = assistant.scope;
    await retryTurn(blocked.id);
    await retryTurn(blocked.id);
    expect(conv.turns).toEqual([blocked]);
    expect(assistant.scope).toEqual(changedScope);
    expect(retryTurnId.value).toBe(blocked.id);
    expect(modelCalls()).toBe(2);
    expect(callsOf("SearchIndex")).toHaveLength(1);
    expect(callsOf("SaveConversation")).toHaveLength(1);
    expect(consentSheets).toBe(1);

    answer("SearchIndex", () => ({ hits: [clips[0]], total: 1, aggregate: [], under: "", underMissing: false }));
    drive.indexProgress = { ...drive.indexProgress!, ready: true };
    await settle();
    expect(conv.turns).toEqual([blocked]);
    expect(retryTurnId.value).toBe(blocked.id);
    expect(modelCalls()).toBe(2);
    expect(callsOf("SearchIndex")).toHaveLength(1);
    expect(consentSheets).toBe(1);

    drive.status = statusOf("b");
    expect(retryTurnId.value).toBe("");
    await retryTurn(blocked.id);
    drive.status = statusOf("a");
    assistant.running = true;
    expect(retryTurnId.value).toBe("");
    await retryTurn(blocked.id);
    assistant.running = false;
    await retryTurn("not-the-last-turn");
    expect(conv.turns).toEqual([blocked]);
    expect(modelCalls()).toBe(2);

    void retryTurn(blocked.id);
    await settle();
    expect(consentSheets).toBe(2);
    expect(modelCalls()).toBe(2);
    expect(retryTurnId.value).toBe("");
    answerConsent(true, false);
    await settle();
    expect(modelCalls()).toBe(4);
    const retriedRequest = callsOf("AIChat")[2].args[1] as AIRequest;
    expect(retriedRequest.messages!.filter((message) => message.role === "user").map((message) => message.content)).toEqual([question]);
    expect(callsOf("SearchIndex").map((call) => call.args)).toEqual([
      ["a", expect.objectContaining(expectedQuery)],
      ["a", expect.objectContaining(expectedQuery)],
    ]);
    expect(callsOf("FindFolders")).toHaveLength(0);
    expect(conv.turns).toHaveLength(1);
    expect(conv.turns[0]).toMatchObject({ user: question, scope, stop: "end", text: "现在找到了原范围的视频。" });
    expect(conv.turns[0].cards).toEqual([
      expect.objectContaining({ kind: "files", total: 1, rows: [expect.objectContaining({ id: "c1" })] }),
    ]);
    expect(assistant.scope).toEqual(scope);
    expect(retryTurnId.value).toBe("");
  });
});

describe("AI assistant · L0 plans", () => {
  it("organizes a selection and an arrival without any model request", async () => {
    await organizeEntries([...series, clips[0]]);
    await settle();
    expect(assistant.screen).toBe("plan");
    expect(assistant.plan?.kind).toBe("media");
    expect(assistant.plan?.changes.some((c) => c.op === "move" && c.selected)).toBe(true);

    arrivals = [
      {
        id: "task-1",
        account: "a",
        name: "Shanhai Jixing",
        fileId: "dl",
        parentId: "",
        savedTo: "",
        source: "cloud",
        completedAt: "2026-10-16T06:00:00Z",
        state: "new",
        recordId: "",
        hasSuggestions: false,
      },
    ];
    await loadInbox("a");
    await ensureArrivalSummary(inbox.arrivals[0]);
    expect(inbox.summaries["task-1"].state).toBe("ready");
    expect(inbox.summaries["task-1"].organizable).toBeGreaterThan(0);
    await openArrival(inbox.arrivals[0]);
    await settle();
    expect(assistant.plan?.arrivals.map((a) => a.id)).toEqual(["task-1"]);
    expect(modelCalls()).toBe(0);
    expect(callsOf("Organize").length).toBe(0);
  });

  it("never puts a TMDB answer into a model request: identify, follow-ups, turns", async () => {
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    await settle();
    const plan = assistant.plan!;
    expect(plan.identities.some((i) => i.source === "tmdb" && i.title === TMDB_TITLE)).toBe(true);
    expect(plan.lowGroups).toBeGreaterThan(0);

    void identifyRemaining();
    await settle();
    expect(consentSheets).toBe(1);
    expect(modelCalls()).toBe(0);
    answerConsent(true, false);
    await settle();
    expect(callsOf("AIComplete").length).toBeGreaterThan(0);
    expect(assistant.plan!.lowGroups).toBe(0);

    await say("剧集放进「剧集」文件夹");
    expect(consentSheets).toBe(2);
    answerConsent(true, false);
    await settle();

    // A turn whose tool builds the same plan: the tool result goes back to the model.
    assistantHome();
    chatScript = [() => reply("我来整理。", [{ name: "organize_media", arguments: { folder: "待分类" } }]), () => reply("计划已生成，请检查。")];
    await say("整理待分类");
    answerConsent(true, false);
    await settle();
    expect(callsOf("AIChat").length).toBe(2);
    expect(assistant.conversation!.turns.at(-1)!.cards.some((c) => c.kind === "plan")).toBe(true);

    expect(callsOf("MatchTitles").length).toBeGreaterThan(0);
    const bodies = modelBodies();
    expect(bodies).not.toContain(TMDB_TITLE);
    expect(bodies).not.toContain(TMDB_ORIGINAL);
  });
});

describe("AI assistant · Explicit whole-plan review", () => {
  beforeEach(() => {
    answer("MatchTitles", () => []);
    answer("FolderTree", () => series);
  });

  function propose(groups: unknown[]) {
    answer("AIComplete", () => ({ output: JSON.stringify({ groups }), input: 400, outputTokens: 60 }));
  }

  async function approveReview() {
    const pending = reviewPlan();
    await settle();
    answerConsent(true, false);
    await pending;
  }

  it("reviews fully rule-matched groups only after consent and changes the preview without drive writes", async () => {
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    const before = assistant.plan!;
    expect(before.low).toEqual([]);
    expect(before.identities.every((i) => i.source === "rules")).toBe(true);
    propose(before.groups.map((g) => ({ id: g.id, type: "series", title: "Corrected Series", season: 2, numbers: ["episode"] })));

    const denied = reviewPlan();
    await settle();
    expect(assistant.consent).not.toBeNull();
    expect(modelCalls()).toBe(0);
    answerConsent(false, false);
    await denied;
    expect(assistant.plan).toBe(before);
    expect(modelCalls()).toBe(0);

    const pending = reviewPlan();
    await settle();
    expect(modelCalls()).toBe(0);
    answerConsent(true, false);
    await pending;
    expect(callsOf("AIComplete")).toHaveLength(1);
    for (const group of before.groups) expect(modelBodies()).toContain(group.pattern);
    expect(assistant.plan!.identities.every((i) => i.title === "Corrected Series" && i.season === 2)).toBe(true);
    expect(assistant.plan!.changes.some((c) => c.name.includes("Corrected Series - S02"))).toBe(true);
    expect(assistant.plan!.tokens).toBe(460);
    expect(callsOf("Organize")).toHaveLength(0);
    expect(callsOf("RemoveFiles")).toHaveLength(0);
  });

  it("keeps unresolved-only identification separate from already named rules", async () => {
    answer("FolderTree", () => below);
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    const before = assistant.plan!;
    const pending = identifyRemaining();
    await settle();
    expect(modelCalls()).toBe(0);
    answerConsent(true, false);
    await pending;
    const sent = callsOf("AIComplete").map((c) => (c.args[1] as AIRequest).messages!.map((m) => m.content).join("\n")).join("\n");
    const ids = [...sent.matchAll(/^(g\d+) · /gm)].map((m) => m[1]);
    expect(ids.sort()).toEqual([...before.low].sort());
    for (const group of before.groups.filter((g) => !before.low.includes(g.id))) expect(sent).not.toContain(group.pattern);
    expect(assistant.plan!.identities.find((i) => i.source === "rules")).toEqual(before.identities.find((i) => i.source === "rules"));
  });

  it("preserves manual names and move picks but clears an explicitly selected move when AI proposes trash", async () => {
    const movies = ["Alpha", "Bravo", "Charlie"].map((name, i) => entry(`movie${i}`, `${name}.2021.1080p.mkv`, "dl"));
    answer("FolderTree", () => movies);
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    renamePlanItem("movie0", "My chosen name.mkv");
    pickPlanItems(["movie0"], false);
    pickPlanItems(["movie1", "movie2"], true);
    const plan = assistant.plan!;
    propose(plan.groups.map((g) => ({
      id: g.id, type: g.files.some((f) => f.id === "movie2") ? "junk" : "movie",
      title: "Revised title", year: 2021, yearSource: "filename", folder: "Reviewed", numbers: ["other"],
    })));
    await approveReview();
    expect(assistant.plan!.changes.find((c) => c.item.id === "movie0")).toMatchObject({ op: "move", name: "My chosen name.mkv", selected: false });
    expect(assistant.plan!.changes.find((c) => c.item.id === "movie1")).toMatchObject({ op: "move", selected: true });
    expect(assistant.plan!.changes.find((c) => c.item.id === "movie2")).toMatchObject({ op: "trash", selected: false });
    expect(assistant.plan!.picks.movie2).toBe(false);
    expect(callsOf("Organize")).toHaveLength(0);
  });

  it.each([false, true])("retains reviewed proposals after saved conversation reopen without exposing or overwriting TMDB data (matched: %s)", async (matched) => {
    if (matched) backend.answers.set("MatchTitles", defaultAnswers.get("MatchTitles")!);
    chatScript = [() => reply("", [{ name: "organize_media", arguments: { folder: "待分类" } }]), () => reply("请检查。")];
    await say("整理待分类");
    answerConsent(true, false);
    await settle();
    const conv = assistant.conversation!;
    const card = conv.turns.at(-1)!.cards.find((c) => c.kind === "plan")!;
    await openPlanCard(card.id);
    const before = assistant.plan!;
    propose(before.groups.map((g) => ({
      id: g.id, type: "movie", title: "AI replacement", year: 2037, yearSource: "guess", folder: "Reviewed",
    })));
    backend.calls.length = 0;
    await approveReview();
    const reviewed = assistant.plan!;
    expect(reviewed.identities.every((i) => i.folder === "Reviewed")).toBe(true);
    if (matched) {
      expect(reviewed.identities).toEqual(before.identities.map((i) => ({ ...i, folder: "Reviewed" })));
      expect(modelBodies()).not.toContain(TMDB_TITLE);
      expect(modelBodies()).not.toContain(TMDB_ORIGINAL);
      expect(modelBodies()).not.toContain("2023");
    } else expect(reviewed.identities.every((i) => i.title === "AI replacement")).toBe(true);
    const saved = callsOf("SaveConversation").at(-1)!;
    expect(saved).toBeDefined();
    const body = saved.args[2] as string;
    expect(reviewed.modelIdentities.every((i) => i.source !== "tmdb" && i.title !== TMDB_TITLE)).toBe(true);
    answer("Conversation", () => body);
    assistantHome();
    await openConversation(conv.id);
    await openPlanCard(card.id);
    expect(assistant.plan!.identities).toEqual(reviewed.identities);
    expect(assistant.plan!.changes).toEqual(reviewed.changes);
    expect(callsOf("AIComplete")).toHaveLength(1);
    expect(callsOf("Organize")).toHaveLength(0);
  });

  it("resumes whole-plan review after connecting a missing model and still waits for consent", async () => {
    aiSettings.value = { ...settings, configured: false };
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    const before = assistant.plan!;
    propose(before.groups.map((g) => ({ id: g.id, type: "series", title: "Connected review", season: 1, numbers: ["episode"] })));
    await reviewPlan();
    expect(assistant.connect).not.toBeNull();
    expect(modelCalls()).toBe(0);
    answer("SetAIConnection", () => settings);
    answer("TestAIConnection", () => ({ ok: true, tools: "yes", stream: "yes" }));
    const connecting = connectModel("deepseek", "");
    await settle();
    expect(assistant.connect).toBeNull();
    expect(assistant.consent).not.toBeNull();
    expect(modelCalls()).toBe(0);
    answerConsent(true, false);
    expect(await connecting).toBe(true);
    expect(assistant.plan!.identities.every((i) => i.title === "Connected review")).toBe(true);
    expect(callsOf("AIComplete")).toHaveLength(1);
  });

  it("keeps the preview unchanged for empty replies, budget limits and content refusals", async () => {
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    const before = assistant.plan!;
    aiSettings.value = { ...settings, tokenLimit: 1 };
    await reviewPlan();
    expect(assistant.consent).toBeNull();
    expect(modelCalls()).toBe(0);
    expect(assistant.plan).toBe(before);
    aiSettings.value = { ...settings };
    answer("AIComplete", () => { throw new Error("input new_sensitive (1026)"); });
    await approveReview();
    expect(assistant.plan).toBe(before);
    expect(assistant.busy).toBe("");
    expect(assistant.noticeTone).toBe("error");
    propose([]);
    await approveReview();
    expect(assistant.plan!.changes).toEqual(before.changes);
    expect(assistant.plan!.tokens).toBe(460);
    expect(callsOf("Organize")).toHaveLength(0);
  });

  it.each(["cancel", "account"] as const)("ignores a late whole-plan reply after %s", async (action) => {
    await organizeFolder([{ id: "dl", name: "待分类" }]);
    const before = assistant.plan!;
    let resolve!: (reply: AIReply) => void;
    const promise = new Promise<AIReply>((done) => { resolve = done; });
    answer("AIComplete", () => promise);
    const pending = reviewPlan();
    await settle();
    answerConsent(true, false);
    await settle();
    const response = identifyAnswer(callsOf("AIComplete")[0].args[1] as AIRequest);
    if (action === "cancel") cancelAssistantTask();
    else {
      // External account changes enter through refresh, which drops the old task.
      answer("Status", () => statusOf("b"));
      await loadAccounts();
      expect(drive.status?.accountId).toBe("b");
    }
    resolve(response);
    await pending;
    if (action === "cancel") expect(assistant.plan).toBe(before);
    else expect(assistant.plan).toBeNull();
    expect(callsOf("AICancel")).toHaveLength(1);
    expect(callsOf("Organize")).toHaveLength(0);
  });
});

describe("AI assistant · Content moderation refusal", () => {
  // Clips the rules cannot name, one group each; the content check turns down one name.
  const mix = entry("mix", "杂项", "", true);
  const names = ["Alpha clip.mp4", "Bravo clip.mp4", "Forbidden clip.mp4", "Delta clip.mp4"];
  const REFUSED = "模型服务的内容审核拒绝了这次请求：input new_sensitive (1026)";
  let files: Entry[] = [];
  let refuses: (request: AIRequest) => boolean = () => false;

  beforeEach(() => {
    files = names.map((name, i) => entry(`m${i}`, name, "mix"));
    refuses = () => false;
    backend.answers.set("FolderTree", (_account, folder) => (folder === "mix" ? files : [mix, ...files]));
    backend.answers.set("PlanTree", (_account, scope) => (scope === "mix" ? files : [mix, ...files]));
    backend.answers.set("FolderTrail", (id) => (id === "mix" ? [{ id: "mix", name: "杂项" }] : []));
    answer("AIComplete", (_id: string, request: AIRequest) => {
      if (refuses(request)) throw new Error(REFUSED);
      return identifyAnswer(request);
    });
    return () => {
      backend.answers.set("FolderTree", (_account, folder) => (folder === "dl" ? below : [inbox0, ...below]));
      backend.answers.set("PlanTree", (_account, scope) => (scope === "dl" ? below : [inbox0, ...below]));
      backend.answers.set("FolderTrail", (id) => (id === "dl" ? [{ id: "dl", name: "待分类" }] : []));
      answer("AIComplete", (_id: string, request: AIRequest) => identifyAnswer(request));
    };
  });

  /** identifyMix asks AI about Miscellaneous's low groups and returns how many there were. */
  async function identifyMix(): Promise<number> {
    await organizeFolder([{ id: "mix", name: "杂项" }]);
    await settle();
    const low = assistant.plan!.lowGroups;
    void identifyRemaining();
    await settle();
    answerConsent(true, false);
    await settle();
    return low;
  }
  const sent = () => callsOf("AIComplete").map((c) => (c.args[1] as AIRequest).messages!.map((m) => m.content).join("\n"));

  it("asks again in halves and identifies every group but the refused one", async () => {
    refuses = (request) => JSON.stringify(request).includes("Forbidden");
    expect(await identifyMix()).toBe(4);
    // 4 refused → 2 + 2 (the half with it refused again) → 1 + 1.
    expect(sent().map((s) => names.filter((n) => s.includes(n)).length).sort()).toEqual([1, 1, 2, 2, 4]);
    const plan = assistant.plan!;
    expect(plan.lowGroups).toBe(1);
    expect(plan.modelIdentities.length).toBe(3);
    expect(assistant.noticeTone).toBe("warning");
    expect(assistant.notice).toBe(`${REFUSED}。有 1 组没有识别，保持原样，其余照常识别；可能是这些组的文件名或路径里有它认为敏感的词，换个模型服务或改名后可以再试。`);
  });

  it("keeps every group as it was when the check refuses them all, in a bounded number of requests", async () => {
    refuses = () => true;
    expect(await identifyMix()).toBe(4);
    expect(callsOf("AIComplete").length).toBe(7);
    expect(assistant.plan!.lowGroups).toBe(4);
    expect(assistant.noticeTone).toBe("warning");
    expect(assistant.notice).toBe(`${REFUSED}。有 4 组没有识别，保持原样；可能是这些组的文件名或路径里有它认为敏感的词，换个模型服务或改名后可以再试。`);
  });

  it("stops splitting after REFUSAL_SPLITS when the refusal is about what every batch shares", async () => {
    files = Array.from({ length: 40 }, (_, i) => entry(`m${i}`, `${String.fromCharCode(65 + (i % 26))}${String.fromCharCode(97 + Math.floor(i / 26))} clip.mp4`, "mix"));
    refuses = () => true;
    const low = await identifyMix();
    expect(low).toBeGreaterThan(16);
    // 15 splits of two requests each, after the first: every other batch is refused whole.
    expect(callsOf("AIComplete").length).toBe(31);
    expect(assistant.plan!.lowGroups).toBe(low);
    expect(assistant.notice).toContain(`有 ${low} 组没有识别`);
  });

  it("still stops at any other error, the plan unchanged", async () => {
    answer("AIComplete", () => {
      throw new Error("模型服务返回错误（HTTP 503）：overloaded");
    });
    await identifyMix();
    expect(callsOf("AIComplete").length).toBe(1);
    expect(assistant.plan!.lowGroups).toBe(4);
    expect(assistant.noticeTone).toBe("error");
    expect(assistant.notice).toBe("模型服务返回错误（HTTP 503）：overloaded，计划保持不变");
  });
});


describe("AI assistant · Actionable arrival reminders", () => {
  it.each([false, true])("does not remind for unidentified files (model configured: %s), without resolving the arrival", async (configured) => {
    aiSettings.value = { ...settings, configured };
    arrivals = [{ id: "unknown-arrival", fileId: "c1", account: "a", name: "VID_0001.mp4", parentId: "dl", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false }];
    await loadInbox("a");
    await ensureArrivalSummary(inbox.arrivals[0]);
    expect(inbox.summaries["unknown-arrival"]).toMatchObject({ state: "ready", organizable: 0 });
    expect(pendingArrivals.value).toEqual([]);
    expect(inbox.arrivals[0]).toMatchObject({ state: "new", recordId: "" });
    expect(callsOf("SetArrival")).toEqual([]);
    expect(callsOf("Organize")).toEqual([]);
    expect(modelCalls()).toBe(0);
  });

  it("reminds for local suggestions without a model and rechecks silent arrivals after an index pass", async () => {
    aiSettings.value = { ...settings, configured: false };
    arrivals = [{ id: "rechecked", fileId: "c1", account: "a", name: "VID_0001.mp4", parentId: "dl", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false }];
    await loadInbox("a");
    expect(pendingArrivals.value).toEqual([]);
    const updated = below.map((e) => e.id === "c1" ? { ...e, name: "[SubGroup] Shanhai Jixing - 04 [1080p].mkv" } : e);
    answer("PlanTree", (_account: string, scope: string) => scope === "dl" ? updated : [inbox0, ...updated]);
    drive.indexProgress = { ...drive.indexProgress!, account: "a", ready: true, syncedAt: "2026-11-01T09:00:00Z" };
    await settle();
    expect(pendingArrivals.value.map((a) => a.id)).toEqual(["rechecked"]);
    expect(inbox.arrivals[0].state).toBe("new");
    expect(inbox.summaries.rechecked.organizable).toBe(1);
    expect(modelCalls()).toBe(0);
    expect(callsOf("Organize")).toEqual([]);
  });

  it("treats an empty arrival as having no suggestions, not a failed analysis to retry", async () => {
    answer("PlanTree", () => [inbox0]);
    arrivals = [{ id: "empty-arrival", fileId: "dl", account: "a", name: inbox0.name, parentId: "", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false }];
    await loadInbox("a");
    expect(inbox.summaries["empty-arrival"]).toMatchObject({ state: "ready", files: 0, organizable: 0 });
    expect(pendingArrivals.value).toEqual([]);
    expect(inbox.arrivals[0].state).toBe("new");
    expect(modelCalls()).toBe(0);
  });

  it("checks another account without using the visible account's folders or replacing its summaries", async () => {
    arrivals = [{ id: "other-arrival", fileId: "s1", account: "b", name: "Shanhai Jixing", parentId: "dl", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false }];
    answer("PlanTree", (account: string, scope: string) => account === "b" ? (scope === "dl" ? below : [inbox0, ...below]) : [clips[0]]);
    const visible = inbox.summaries;
    await loadInbox("b");
    expect(inbox.summaries).toBe(visible);
    expect(pendingArrivals.value).toEqual([]);
    expect(arrivals[0]).toMatchObject({ state: "new", hasSuggestions: true });
    expect(modelCalls()).toBe(0);
  });
});

describe("AI assistant · Batch library import", () => {
  const arrival = (id: string, fileId: string): Arrival => ({ id, fileId, account: "a", name: fileId, parentId: "dl", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false });

  it("keeps unselected arrivals and files outside the merged plan, and requires confirmation", async () => {
    arrivals = [arrival("one", "s1"), arrival("two", "s2"), arrival("other", "s3")];
    await loadInbox("a");
    await openArrivals(inbox.arrivals.slice(0, 2));
    const plan = assistant.plan!;
    expect(plan.arrivals.map((a) => a.id)).toEqual(["one", "two"]);
    expect(plan.groups.flatMap((g) => g.files.map((f) => f.id)).sort()).toEqual(["s1", "s2"]);
    expect(plan.changes.filter((c) => c.op === "move").map((c) => c.item.id).sort()).toEqual(["s1", "s2"]);
    await runPlan();
    expect(callsOf("Organize")).toEqual([]);
    expect(pendingArrivals.value.map((a) => a.id)).toEqual(["one", "two", "other"]);
    expect(modelCalls()).toBe(0);
  });

  it("only resolves arrivals whose files actually completed; stopped or unselected items stay pending", async () => {
    arrivals = [arrival("one", "s1"), arrival("two", "s2"), arrival("other", "s3")];
    await loadInbox("a");
    await openArrivals(inbox.arrivals.slice(0, 2));
    const original = backend.answers.get("Organize")!;
    answer("Organize", () => ({ id: "partial", items: [{ fileId: "s1", done: true, error: "" }, { fileId: "s2", done: true, error: "目标位置重名" }], created: [], finished: true, stopped: true, undo: null }));
    answer("SetArrival", (_account: string, id: string, state: string) => { const a = arrivals.find((a) => a.id === id); if (a) a.state = state; });
    try {
      openPlanConfirm();
      await runPlan();
      await settle();
      expect(pendingArrivals.value.map((a) => a.id)).toEqual(["two", "other"]);
    } finally {
      backend.answers.set("Organize", original);
      backend.answers.delete("SetArrival");
    }
  });

  it("ignores selected reminders, retaining failures for retry without organizing files", async () => {
    arrivals = [arrival("one", "s1"), arrival("two", "s2"), arrival("other", "s3")];
    await loadInbox("a");
    answer("SetArrival", (_account: string, id: string) => { if (id === "two") throw new Error("磁盘已满"); });
    try {
      expect(await dismissArrivals(inbox.arrivals.slice(0, 2))).toBe(false);
      expect(pendingArrivals.value.map((a) => a.id)).toEqual(["two", "other"]);
      expect(callsOf("Organize")).toEqual([]);
    } finally { backend.answers.delete("SetArrival"); }
  });

  it("keeps unidentified arrivals unprocessed but silent after completing the identified series", async () => {
    const unknown = Array.from({ length: 7 }, (_, i) => entry(`unknown-${i}`, `VID_${1000 + i}.mp4`, "dl"));
    const originalTree = backend.answers.get("PlanTree")!;
    backend.answers.set("PlanTree", (_account, scope) => scope === "dl" ? [...series, ...unknown] : [inbox0, ...series, ...unknown]);
    arrivals = [arrival("identified", "s1"), ...unknown.map((e) => arrival(`arrival-${e.id}`, e.id))];
    answer("SetArrival", (_account: string, id: string, state: string) => { const a = arrivals.find((a) => a.id === id); if (a) a.state = state; });
    try {
      await loadInbox("a");
      await openArrivals([...inbox.arrivals]);
      expect(planSummary.value?.selected).toBe(1);
      openPlanConfirm();
      await runPlan(false);
      await settle();
      const unprocessed = inbox.arrivals.filter((a) => a.state === "new");
      expect(unprocessed.map((a) => a.fileId)).toEqual(unknown.map((e) => e.id));
      expect(pendingArrivals.value).toEqual([]);
      await openArrivals(unprocessed);
      expect(planSummary.value?.selected).toBe(0);
      expect(assistant.plan?.base.kept.map((c) => c.item.id).sort()).toEqual(unknown.map((e) => e.id).sort());
      openPlanConfirm();
      expect(assistant.confirming).toBe(true);
      await runPlan();
      expect(callsOf("Organize")).toHaveLength(1);
      expect(inbox.arrivals.filter((a) => a.state === "new")).toHaveLength(7);
      expect(pendingArrivals.value).toEqual([]);
    } finally {
      backend.answers.set("PlanTree", originalTree);
      backend.answers.delete("SetArrival");
    }
  });
});

describe("AI assistant · Retention decisions", () => {
  const arrival: Arrival = { id: "folder-arrival", fileId: "dl", account: "a", name: "待分类", parentId: "", savedTo: "", source: "cloud", completedAt: "2026-10-17T11:00:00Z", state: "new", recordId: "", hasSuggestions: false };

  async function openFolderArrival() {
    arrivals = [{ ...arrival }];
    await loadInbox("a");
    await openArrival(inbox.arrivals[0]);
  }

  it("restores kept decisions after reopen and leaves an unidentified undone keep unprocessed but silent", async () => {
    await openFolderArrival();
    await keepAllPlanItems();
    expect(planSummary.value?.selected).toBe(0);
    expect(planReviewed.value.map((c) => c.item.id).sort()).toEqual(below.map((e) => e.id).sort());
    expect(pendingArrivals.value).toEqual([]);
    openPlanConfirm();
    await runPlan();
    expect(callsOf("Organize")).toEqual([]);
    await openArrival(inbox.arrivals[0]);
    expect(planReviewed.value.map((c) => c.item.id).sort()).toEqual(below.map((e) => e.id).sort());
    await keepPlanItems(["c1"], false);
    expect(planUndecided.value.map((c) => c.item.id)).toContain("c1");
    expect(inbox.arrivals[0].state).toBe("new");
    expect(pendingArrivals.value).toEqual([]);
  });

  it("does not treat unchecked as a decision and requires an explicit unresolved choice", async () => {
    await openFolderArrival();
    pickPlanItems(series.map((e) => e.id), false);
    openPlanConfirm();
    await runPlan();
    expect(assistant.confirming).toBe(true);
    expect(pendingArrivals.value.map((a) => a.id)).toEqual([arrival.id]);
    expect(reviews.a ?? []).toEqual([]);
    await runPlan(false);
    expect(pendingArrivals.value.map((a) => a.id)).toEqual([arrival.id]);
    expect(callsOf("Organize")).toEqual([]);
  });

  it("counts explicit keeps together with executed files, not failed files", async () => {
    await openFolderArrival();
    await keepPlanItems(clips.map((e) => e.id));
    openPlanConfirm();
    await runPlan(false);
    expect(pendingArrivals.value).toEqual([]);
    expect(inbox.arrivals[0].state).toBe("organized");
    expect(assistant.receiptKept).toBe(3);
  });

  it("leaves review and reminders pending when the decision cannot be saved", async () => {
    await openFolderArrival();
    const original = backend.answers.get("SetSuggestionReviews")!;
    answer("SetSuggestionReviews", () => { throw new Error("磁盘已满"); });
    try { await expect(keepAllPlanItems()).rejects.toThrow("磁盘已满"); }
    finally { backend.answers.set("SetSuggestionReviews", original); }
    expect(planReviewed.value).toEqual([]);
    expect(pendingArrivals.value.map((a) => a.id)).toEqual([arrival.id]);
    expect(planSummary.value?.selected).toBe(3);
  });

  it("a changed proposal or file input invalidates only that keep; wording does not", async () => {
    await openFolderArrival();
    await keepAllPlanItems();
    const original = backend.answers.get("PlanTree")!;
    answer("PlanTree", (_account: string, scope: string) => {
      const updated = below.map((e) => e.id === "c1" ? { ...e, name: "VID_CHANGED.mp4" } : e);
      return scope === "dl" ? updated : [inbox0, ...updated];
    });
    try {
      await loadInbox("a");
      await settle();
      expect(inbox.arrivals[0].state).toBe("new");
      expect(pendingArrivals.value).toEqual([]);
      await openArrival(inbox.arrivals[0]);
      expect(planReviewed.value.map((c) => c.item.id).sort()).toEqual(below.filter((e) => e.id !== "c1").map((e) => e.id).sort());
      expect(planUndecided.value.map((c) => c.item.id)).toEqual(["c1"]);
    } finally { backend.answers.set("PlanTree", original); }
  });
});
