import { describe, expect, it, vi, type Mock } from "vitest";
import {
  cardRows,
  compactHistory,
  fileInfoForModel,
  folderSummaryForModel,
  foldersForModel,
  Handles,
  indexBlocked,
  MAX_CALLS,
  mediaSummaryForModel,
  modelPath,
  NO_TOOLS_MESSAGE,
  parseToolCall,
  planForModel,
  retryable,
  rowsForModel,
  runTurn,
  searchForModel,
  transfersForModel,
  type AgentDeps,
  type AIChatReply,
  type AIChatRequest,
  type AIJsonReply,
  type FilesCard,
  type ModelRow,
  type PlanCard,
  type ToolImpls,
  type Turn,
  type TurnOptions,
} from "./agent";
import { ModelOutputError, type ModelRequest } from "./aiprompt";
import { conversationBody, readConversation } from "./assistantcore";
import { applyLanguage, i18n, LOCALES, t } from "./i18n";
import { emptyQuery } from "./searchquery";

const NOW = new Date(2026, 9, 16, 10, 0); // a Friday
const GiB = 1024 ** 3;

const hit: ModelRow = {
  id: "real-file-1",
  name: "山海纪行 01.mkv",
  folder: false,
  path: "全部文件 / 影视 / 山海纪行",
  size: GiB,
  modifiedTime: "2026-10-01T08:00:00+08:00",
  position: 600,
  duration: 1500,
};

function reply(text: string, calls: [string, unknown][] = [], input = 100): AIChatReply {
  return {
    text,
    toolCalls: calls.map(([name, args], i) => ({ id: `call-${i}`, name, arguments: typeof args === "string" ? args : JSON.stringify(args) })),
    stop: calls.length ? "tool" : "end",
    input,
    outputTokens: 20,
  };
}

/** fakeChat plays steps in order (the last repeats); text streams through onDelta one character at a time. */
function fakeChat(steps: (AIChatReply | Error | ((req: AIChatRequest) => AIChatReply | Error))[]) {
  const requests: AIChatRequest[] = [];
  const fn = vi.fn(async (req: AIChatRequest, onDelta: (t: string) => void) => {
    requests.push(JSON.parse(JSON.stringify(req)));
    const raw = steps[Math.min(requests.length - 1, steps.length - 1)];
    const step = typeof raw === "function" ? raw(req) : raw;
    if (step instanceof Error) throw step;
    for (const ch of step.text) onDelta(ch);
    return step;
  });
  return { fn, requests };
}

function fakeJson(outputs: (object | string | Error)[]) {
  const requests: ModelRequest[] = [];
  const fn = vi.fn(async (req: ModelRequest): Promise<AIJsonReply> => {
    requests.push(JSON.parse(JSON.stringify(req)));
    const step = outputs[Math.min(requests.length - 1, outputs.length - 1)];
    if (step instanceof Error) throw step;
    return { output: typeof step === "string" ? step : JSON.stringify(step), input: 80, outputTokens: 15 };
  });
  return { fn, requests };
}

function filesCard(rows: readonly ModelRow[]): FilesCard {
  return { kind: "files", id: "", title: "搜索结果", state: "done", note: "", total: rows.length, rows: cardRows(rows), aggregate: [], query: null };
}

function makeTools(): ToolImpls & Required<Pick<ToolImpls, "search_files" | "file_info" | "recent_plays" | "propose_changes">> {
  return {
    search_files: vi.fn(async (args, ctx) => ({
      forModel: searchForModel({ hits: [hit], total: 1, aggregate: null, under: "" }, args.query, ctx.handles),
      trace: "找到 1 项",
      card: filesCard([hit]),
    })),
    file_info: vi.fn(async (args, ctx) => ({ forModel: fileInfoForModel(hit, ctx.handles), trace: hit.name })),
    recent_plays: vi.fn(async () => ({ forModel: "没有播放记录。", trace: "" })),
    propose_changes: vi.fn(async (args) => ({
      forModel: planForModel({ changes: args.items.length, kept: 0, trash: 0, conflicts: 0 }),
      trace: `${args.items.length} 项`,
    })),
  };
}

function makeDeps(over: Partial<AgentDeps> = {}): AgentDeps & { consent: Mock } {
  return {
    chat: vi.fn(async () => {
      throw new Error("chat should not be called");
    }),
    json: vi.fn(async () => {
      throw new Error("json should not be called");
    }),
    tools: makeTools(),
    consent: vi.fn(async () => true),
    budget: 1_000_000,
    now: () => NOW,
    ...over,
  } as AgentDeps & { consent: Mock };
}

const opts = (over: Partial<TurnOptions> = {}): TurnOptions => ({ mode: "tools", alwaysAllow: false, stream: true, ...over });
const fresh = () => ({ turns: [] as Turn[], handles: new Handles() });

function searchTools(results: ModelRow[][]): ToolImpls {
  let next = 0;
  return {
    search_files: async ({ query }, { handles }) => {
      const rows = results[next++];
      const result = { hits: rows, total: rows.length, aggregate: null, under: query.under };
      return {
        forModel: searchForModel(result, query, handles),
        trace: `${rows.length} 项`,
        card: { ...filesCard(rows), query },
      };
    },
  };
}

describe("runTurn · explicit search presentation", () => {
  const other = { ...hit, id: "real-file-2", name: "工作笔记.txt" };

  it("selects a refined search instead of broad or zero-hit sources, retaining every candidate", async () => {
    const chat = fakeChat([
      reply("查找中。", [
        ["search_files", {}],
        ["search_files", { keywords: ["不存在"] }],
        ["search_files", { keywords: ["山海"] }],
      ]),
      reply("", [["show_results", { groups: [{ title: "山海纪行", searches: [3], handles: ["f1"] }] }]]),
      reply("找到了山海纪行。"),
    ]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools: searchTools([[hit, other], [], [hit]]) }), fresh(), "找山海", opts({ id: "refined" }));
    expect(turn.resultGroups).toEqual([{ title: "山海纪行", sourceIds: ["refined-3"], fileIds: [hit.id] }]);
    expect(turn.cards.map((card) => card.kind === "files" ? card.rows.map((row) => row.id) : [])).toEqual([[hit.id, other.id], [], [hit.id]]);
    expect(JSON.stringify(chat.requests)).not.toContain(hit.id);
    expect(JSON.stringify(chat.requests)).not.toContain("refined-");
  });

  it("keeps separate intent groups, allows shared sources and deduplicates subset handles", async () => {
    const chat = fakeChat([
      reply("", [["search_files", {}], ["search_files", { keywords: ["山海"] }]]),
      reply("", [["show_results", { groups: [
        { title: "纪录片", searches: [1, 2, 1], handles: ["f1", "f1"] },
        { title: "笔记", searches: [1], handles: ["f2"] },
        { title: "所有候选", searches: [1] },
      ] }]]),
      reply("按内容分好了。"),
    ]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools: searchTools([[hit, other], [hit]]) }), fresh(), "找纪录片和笔记", opts({ id: "groups" }));
    expect(turn.resultGroups).toEqual([
      { title: "纪录片", sourceIds: ["groups-1", "groups-2"], fileIds: [hit.id] },
      { title: "笔记", sourceIds: ["groups-1"], fileIds: [other.id] },
      { title: "所有候选", sourceIds: ["groups-1"] },
    ]);
  });

  it("rejects unknown, zero-hit and out-of-source selections atomically", async () => {
    const valid = { title: "纪录片", searches: [3], handles: ["f1"] };
    const chat = fakeChat([
      reply("", [["search_files", {}], ["search_files", { keywords: ["无"] }], ["search_files", { keywords: ["山海"] }]]),
      reply("", [["show_results", { groups: [valid] }]]),
      reply("", [
        ["show_results", { groups: [valid, { title: "未知", searches: [99] }] }],
        ["show_results", { groups: [{ title: "零命中", searches: [2] }] }],
        ["show_results", { groups: [{ title: "其它来源", searches: [3], handles: ["f2"] }] }],
        ["show_results", { groups: [{ title: "未知文件", searches: [3], handles: ["f999"] }] }],
      ]),
      reply("保留相关结果。"),
    ]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools: searchTools([[hit, other], [], [hit]]) }), fresh(), "找", opts({ id: "atomic" }));
    expect(turn.resultGroups).toEqual([{ title: "纪录片", sourceIds: ["atomic-3"], fileIds: [hit.id] }]);
    expect(turn.traces.filter((trace) => trace.tool === "show_results").map((trace) => trace.state)).toEqual(["done", "failed", "failed", "failed", "failed"]);
    const rejected = chat.requests[3].messages.filter((m) => m.role === "tool").slice(-4);
    expect(JSON.stringify(rejected)).not.toContain(hit.id);
  });

  it("does not allow old-turn search sources or failed searches", async () => {
    const state = fresh();
    const old = await runTurn(makeDeps({
      chat: fakeChat([reply("", [["search_files", {}]]), reply("旧结果。")]).fn,
      tools: searchTools([[hit]]),
    }), state, "之前", opts({ id: "old" }));
    state.turns.push(old);
    const tools: ToolImpls = { search_files: async () => { throw new Error("索引不可用"); } };
    const turn = await runTurn(makeDeps({
      chat: fakeChat([
        reply("", [["search_files", {}]]),
        reply("", [["show_results", { groups: [{ title: "旧结果", searches: [1] }] }]]),
        reply("本次未找到。"),
      ]).fn,
      tools,
    }), state, "再找", opts());
    expect(turn.resultGroups).toBeUndefined();
    expect(turn.cards).toEqual([]);
    expect(turn.traces.at(-1)?.state).toBe("failed");
  });

  it("distinguishes explicit no-results from omitted selection without extra model requests", async () => {
    const selected = await runTurn(makeDeps({
      chat: fakeChat([
        reply("", [["search_files", {}]]),
        reply("", [["show_results", { groups: [] }]]),
        reply("没有相关文件。"),
      ]).fn,
      tools: searchTools([[other]]),
    }), fresh(), "找纪录片", opts());
    expect(selected.resultGroups).toEqual([]);
    expect(selected.cards).toHaveLength(1);
    const chat = fakeChat([reply("", [["search_files", {}]]), reply("这次没有筛选结果。")]);
    const omitted = await runTurn(makeDeps({ chat: chat.fn, tools: searchTools([[hit]]) }), fresh(), "找", opts());
    expect(omitted.resultGroups).toBeUndefined();
    expect(chat.requests).toHaveLength(2);
  });

  it("invalidates an earlier selection when a later successful search changes candidates", async () => {
    const snapshots: Turn[] = [];
    const chat = fakeChat([
      reply("", [["search_files", {}]]),
      reply("", [["show_results", { groups: [{ title: "候选", searches: [1], handles: ["f1"] }] }]]),
      reply("", [["search_files", { keywords: ["不存在"] }]]),
      reply("没有更精确的结果。"),
    ]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools: searchTools([[hit], []]) }), fresh(), "找", opts({ onUpdate: (snapshot) => snapshots.push(snapshot) }));
    const prior = snapshots.find((snapshot) => snapshot.resultGroups !== undefined)!;
    expect(prior.resultGroups?.[0].fileIds).toEqual([hit.id]);
    expect(turn.resultGroups).toBeUndefined();
    expect(turn.cards).toHaveLength(2);
  });

  it("copies nested selection arrays for live updates and preserves them through conversation persistence", async () => {
    const state = fresh();
    const snapshots: Turn[] = [];
    const turn = await runTurn(makeDeps({
      chat: fakeChat([
        reply("", [["search_files", {}]]),
        reply("", [["show_results", { groups: [{ title: "纪录片", searches: [1], handles: ["f1"] }] }]]),
        reply("找到。"),
      ]).fn,
      tools: searchTools([[hit]]),
    }), state, "找", opts({ id: "saved", onUpdate: (snapshot) => snapshots.push(snapshot) }));
    const selected = snapshots.filter((snapshot) => snapshot.resultGroups !== undefined);
    selected[0].resultGroups![0].sourceIds.push("tampered");
    selected[0].resultGroups![0].fileIds!.push("tampered");
    expect(selected.at(-1)!.resultGroups).toEqual(turn.resultGroups);
    expect(turn.resultGroups).toEqual([{ title: "纪录片", sourceIds: ["saved-1"], fileIds: [hit.id] }]);
    const restored = readConversation(conversationBody({
      id: "conversation", title: "", createdAt: "", updatedAt: "", usage: { used: 0, limit: 1000 },
      turns: [turn], handles: state.handles.snapshot(), plans: {},
    }), "conversation");
    expect(restored?.turns[0].resultGroups).toEqual(turn.resultGroups);
    const legacy = { ...turn };
    delete legacy.resultGroups;
    expect(readConversation(JSON.stringify({ version: 1, turns: [legacy] }), "legacy")?.turns[0].resultGroups).toBeUndefined();
  });

  it("runs selection in JSON emulation with the same source ordinals and local-only ids", async () => {
    const json = fakeJson([
      { say: "先查。", call: { tool: "search_files", args: { keywords: ["无"] } } },
      { say: "", call: { tool: "search_files", args: { keywords: ["山海"], limit: 5 } } },
      { say: "", call: { tool: "show_results", args: { groups: [{ title: "山海纪行", searches: [2], handles: ["f1"] }] } } },
      { say: "找到山海纪行。" },
    ]);
    const turn = await runTurn(makeDeps({ json: json.fn, tools: searchTools([[], [hit]]) }), fresh(), "找山海", opts({ mode: "json", id: "json" }));
    expect(turn.resultGroups).toEqual([{ title: "山海纪行", sourceIds: ["json-2"], fileIds: [hit.id] }]);
    expect(JSON.stringify(json.requests)).not.toContain(hit.id);
    expect(JSON.stringify(json.requests)).not.toContain("json-2");
  });

  it("keeps aggregate selection separate and leaves true source totals untouched", async () => {
    const aggregate = { key: "secret-dir", label: "影视", size: GiB, count: 120 };
    const tools: ToolImpls = {
      search_files: async ({ query }, { handles }) => ({
        forModel: searchForModel({ hits: [], total: 120, aggregate: [aggregate], under: "" }, query, handles),
        trace: "120 项",
        card: { kind: "files", id: "", title: "统计", state: "done", note: "", total: 120, rows: [], aggregate: [aggregate], query },
      }),
    };
    const chat = fakeChat([
      reply("", [["search_files", { aggregate: { by: "folder" } }], ["search_files", { aggregate: { by: "folder" } }]]),
      reply("", [
        ["show_results", { groups: [{ title: "混合", searches: [1, 2] }] }],
        ["show_results", { groups: [{ title: "筛选", searches: [1], handles: ["f1"] }] }],
        ["show_results", { groups: [{ title: "文件夹统计", searches: [2] }] }],
      ]),
      reply("统计如上。"),
    ]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools }), fresh(), "统计", opts({ id: "aggregate" }));
    expect(turn.resultGroups).toEqual([{ title: "文件夹统计", sourceIds: ["aggregate-2"] }]);
    expect(turn.cards.map((card) => card.kind === "files" ? card.total : 0)).toEqual([120, 120]);
    expect(turn.traces.filter((trace) => trace.tool === "show_results").map((trace) => trace.state)).toEqual(["failed", "failed", "done"]);
    expect(JSON.stringify(chat.requests)).not.toContain(aggregate.key);
  });

  it.each([
    {},
    { groups: null },
    { groups: Array.from({ length: 9 }, () => ({ title: "x", searches: [1] })) },
    { groups: [{ title: "", searches: [1] }] },
    { groups: [{ title: "x".repeat(81), searches: [1] }] },
    { groups: [{ title: "x", searches: [] }] },
    { groups: [{ title: "x", searches: [0] }] },
    { groups: [{ title: "x", searches: [1.5] }] },
    { groups: [{ title: "x", searches: Array(65).fill(1) }] },
    { groups: [{ title: "x", searches: [1], handles: [] }] },
    { groups: [{ title: "x", searches: [1], handles: Array(321).fill("f1") }] },
  ])("rejects malformed or oversized presentation args: %j", (args) => {
    const handles = new Handles();
    handles.of(hit.id, "file");
    expect(() => parseToolCall("show_results", args, handles)).toThrow(ModelOutputError);
  });
});

describe("runTurn · consent", () => {
  it("asks once per turn, however many calls the turn makes", async () => {
    const chat = fakeChat([
      reply("我查一下。", [["search_files", { keywords: ["山海"] }]]),
      (req) => {
        const result = req.messages[req.messages.length - 1];
        const handle = /f\d+/.exec(result.content)![0];
        return reply("", [["file_info", { handle }]]);
      },
      reply("找到了：山海纪行 01.mkv，看到第 10 分钟。"),
    ]);
    const deps = makeDeps({ chat: chat.fn });
    const turn = await runTurn(deps, fresh(), "上次没看完的山海纪行在哪", opts());

    expect(deps.consent).toHaveBeenCalledTimes(1);
    const disclosure = deps.consent.mock.calls[0][0];
    expect(disclosure.level).toBe("metadata");
    expect(disclosure.estimate.low).toBeGreaterThan(0);
    expect(disclosure.notSent).toContain("TMDB");
    expect(chat.fn).toHaveBeenCalledTimes(3);
    expect(turn.stop).toBe("end");
    expect(turn.text).toBe("我查一下。\n\n找到了：山海纪行 01.mkv，看到第 10 分钟。");
    expect(turn.traces.map((t) => [t.label, t.state, t.detail])).toEqual([
      ["已查询本地索引", "done", "找到 1 项"],
      ["已读取文件详情", "done", "山海纪行 01.mkv"],
    ]);
    expect(turn.cards).toHaveLength(1);
    expect(turn.cards[0].id).not.toBe("");
    expect(turn.usage).toEqual({ input: 300, output: 60, calls: 3 });
    expect(deps.tools.file_info).toHaveBeenCalledWith({ target: { id: "real-file-1", folder: false } }, expect.anything());

    // Real ids never go out; the tool result names the file by handle.
    const wire = JSON.stringify(chat.requests);
    expect(wire).not.toContain("real-file-1");
    const toolMessage = chat.requests[1].messages[chat.requests[1].messages.length - 1];
    expect(toolMessage).toMatchObject({ role: "tool", toolCallId: "call-0" });
    expect(chat.requests[0].tools.map((t) => t.name)).toContain("propose_changes");
    expect(chat.requests[0].stream).toBe(true);
  });

  it("does not ask when the account's metadata is Always allow", async () => {
    const chat = fakeChat([reply("好的")]);
    const deps = makeDeps({ chat: chat.fn });
    const turn = await runTurn(deps, fresh(), "你好", opts({ alwaysAllow: true }));
    expect(deps.consent).not.toHaveBeenCalled();
    expect(turn.text).toBe("好的");
  });

  it("sends nothing when consent is refused", async () => {
    const chat = fakeChat([reply("不该发出")]);
    const json = fakeJson([{ say: "不该发出" }]);
    const deps = makeDeps({ chat: chat.fn, json: json.fn, consent: vi.fn(async () => false) });
    const turn = await runTurn(deps, fresh(), "整理纪录片", opts());
    expect(chat.fn).not.toHaveBeenCalled();
    expect(json.fn).not.toHaveBeenCalled();
    expect(turn).toMatchObject({ denied: true, stop: "denied", text: "", traces: [], cards: [] });
    expect(turn.notice).toContain("未向模型发送任何内容");
  });
});

describe("runTurn · validation", () => {
  it("never executes a call with an unknown handle or tool; the reason goes back to the model", async () => {
    const state = fresh();
    state.handles.of("real-file-1", "file");
    const chat = fakeChat([
      reply("", [
        ["file_info", { handle: "f9" }],
        ["propose_changes", { title: "清理", items: [{ handle: "f1", trash: true }, { handle: "f42", trash: true }] }],
        ["delete_everything", {}],
        ["file_info", "{not json"],
      ]),
      reply("那几个编号不存在。"),
    ]);
    const deps = makeDeps({ chat: chat.fn });
    const turn = await runTurn(deps, state, "删掉它们", opts());
    expect(deps.tools.file_info).not.toHaveBeenCalled();
    expect(deps.tools.propose_changes).not.toHaveBeenCalled();
    const results = chat.requests[1].messages.filter((m) => m.role === "tool");
    expect(results.map((m) => m.toolCallId)).toEqual(["call-0", "call-1", "call-2", "call-3"]);
    expect(results[0].content).toContain("未知的编号：f9");
    expect(results[1].content).toContain("未知的编号：f42");
    expect(results[2].content).toContain("没有这个工具");
    expect(results[3].content).toContain("不是 JSON");
    expect(turn.traces.every((t) => t.state === "failed" && t.label === "已拒绝 AI 的无效调用")).toBe(true);
    expect(turn.cards).toEqual([]);
    expect(turn.text).toBe("那几个编号不存在。");
  });

  it("answers a tool that throws with its error and goes on", async () => {
    const chat = fakeChat([reply("", [["recent_plays", {}]]), reply("暂时读不到播放记录。")]);
    const tools = makeTools();
    tools.recent_plays = vi.fn(async () => {
      throw new Error("网络不可用");
    });
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools }), fresh(), "最近看了什么", opts());
    expect(turn.traces[0]).toMatchObject({ state: "failed", detail: "网络不可用" });
    expect(chat.requests[1].messages.at(-1)!.content).toContain("网络不可用");
    expect(turn.stop).toBe("end");
  });
});

describe("indexBlocked and retryable", () => {
  async function blockedTurn() {
    const tools = makeTools();
    tools.search_files = vi.fn(async () => {
      throw new Error(t("本地索引还在建立，完成后才能查找"));
    });
    const chat = fakeChat([reply("", [["search_files", {}]]), reply("请等索引完成后再试。")]);
    return runTurn(makeDeps({ chat: chat.fn, tools }), fresh(), "找山海纪行", opts());
  }

  it("retries saved normally ended index failures in every trace language after the UI language changes", async () => {
    const { pref, locale } = i18n;
    try {
      for (const { value } of LOCALES) {
        applyLanguage(value, value);
        const saved = JSON.parse(JSON.stringify(await blockedTurn())) as Turn;
        const next = value === "en" ? "ja" : "en";
        applyLanguage(next, next);
        expect(saved.stop).toBe("end");
        expect(indexBlocked(saved)).toBe(true);
        expect(retryable(saved)).toBe(true);
      }
    } finally {
      applyLanguage(pref, locale);
    }
  });

  it("requires the exact canonical error in a failed tool trace, not model prose or an unrelated failure", async () => {
    const turn = await blockedTurn();
    const trace = turn.traces[0];
    turn.text = trace.detail;
    turn.traces = [{ ...trace, detail: "search permission denied" }];
    expect(indexBlocked(turn)).toBe(false);
    expect(retryable(turn)).toBe(false);
    expect(indexBlocked({ traces: [{ ...trace, detail: `Error: ${trace.detail}` }] })).toBe(false);
    expect(indexBlocked({ traces: [{ ...trace, state: "done" }] })).toBe(false);
    expect(indexBlocked({ traces: [{ ...trace, state: "running" }] })).toBe(false);
  });

  it("does not turn cancellation, denial, budget or other early stops into retryable index waits", async () => {
    const turn = await blockedTurn();
    for (const stop of ["cancel", "denied", "budget", "length", "limit"] as const) {
      expect(retryable({ ...turn, stop })).toBe(false);
    }
  });

  it("keeps acted-on operation cards nonretryable for index waits and ordinary errors", async () => {
    const turn = await blockedTurn();
    const plan: PlanCard = {
      kind: "plan", id: "plan", title: "Plan", state: "draft", note: "", tool: "propose_changes",
      planId: "local-plan", scopeId: "", scopePath: "", counts: { changes: 1, kept: 0, trash: 0, conflicts: 0 },
      sample: null, source: "model", recordId: "",
    };
    turn.cards = [filesCard([hit]), plan];
    expect(retryable(turn)).toBe(true);
    plan.state = "done";
    expect(retryable(turn)).toBe(false);
    expect(retryable({ ...turn, stop: "error" })).toBe(false);
  });
});

describe("runTurn · limits", () => {
  it(`stops after ${MAX_CALLS} model calls`, async () => {
    const chat = fakeChat([reply("", [["recent_plays", {}]])]);
    const deps = makeDeps({ chat: chat.fn });
    const turn = await runTurn(deps, fresh(), "一直查", opts());
    expect(chat.fn).toHaveBeenCalledTimes(MAX_CALLS);
    expect(MAX_CALLS).toBe(8);
    expect(turn.stop).toBe("limit");
    expect(turn.notice).toContain("8 次");
    expect(deps.consent).toHaveBeenCalledTimes(1);
  });

  it("stops before a call the budget cannot hold, keeping what was done", async () => {
    const chat = fakeChat([
      reply("先查。", [["search_files", {}]], 49_000),
      reply("不该发出"),
    ]);
    const deps = makeDeps({ chat: chat.fn, budget: 50_000 });
    const turn = await runTurn(deps, fresh(), "找视频", opts());
    expect(chat.fn).toHaveBeenCalledTimes(1);
    expect(turn.stop).toBe("budget");
    expect(turn.notice).toContain("用量上限");
    expect(turn.text).toBe("先查。");
    expect(turn.traces).toHaveLength(1);
    expect(turn.cards).toHaveLength(1);
  });

  it("asks nothing and sends nothing when the first request does not fit", async () => {
    const chat = fakeChat([reply("不该发出")]);
    const deps = makeDeps({ chat: chat.fn, budget: 10 });
    const turn = await runTurn(deps, fresh(), "找视频", opts());
    expect(deps.consent).not.toHaveBeenCalled();
    expect(chat.fn).not.toHaveBeenCalled();
    expect(turn.stop).toBe("budget");
  });

  it("stops on cancel, keeping the steps already done", async () => {
    const controller = new AbortController();
    const tools = makeTools();
    const search = tools.search_files;
    tools.search_files = vi.fn(async (args, ctx) => {
      const out = await search(args, ctx);
      controller.abort();
      return out;
    });
    const chat = fakeChat([reply("", [["search_files", {}], ["recent_plays", {}]]), reply("不该发出")]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, tools }), fresh(), "找", opts({ signal: controller.signal }));
    expect(chat.fn).toHaveBeenCalledTimes(1);
    expect(tools.recent_plays).not.toHaveBeenCalled();
    expect(turn.stop).toBe("cancel");
    expect(turn.cards).toHaveLength(1);
  });

  it("reports a cancel that interrupts the request as a cancel, not an error", async () => {
    const controller = new AbortController();
    const chat = vi.fn(async () => {
      controller.abort();
      throw new Error("已取消");
    });
    const turn = await runTurn(makeDeps({ chat }), fresh(), "找", opts({ signal: controller.signal }));
    expect(turn).toMatchObject({ stop: "cancel", error: "" });
  });

  it("ends with the model's error", async () => {
    const chat = fakeChat([new Error("401 unauthorized")]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, describe: (e) => `模型服务拒绝：${(e as Error).message}` }), fresh(), "找", opts());
    expect(turn).toMatchObject({ stop: "error", error: "模型服务拒绝：401 unauthorized" });
    expect(retryable(turn)).toBe(true);
  });
});

describe("runTurn · streaming and history", () => {
  it("publishes the text as it streams", async () => {
    const chat = fakeChat([reply("山海纪行")]);
    const seen: string[] = [];
    await runTurn(makeDeps({ chat: chat.fn }), fresh(), "你好", opts({ onUpdate: (t) => seen.push(t.text) }));
    expect(seen).toContain("山");
    expect(seen).toContain("山海");
    expect(seen.at(-1)).toBe("山海纪行");
  });

  it("sends earlier turns as texts with their cards' handles, leaving refused turns out", async () => {
    const state = fresh();
    const first = await runTurn(
      makeDeps({ chat: fakeChat([reply("", [["search_files", { keywords: ["山海"] }]]), reply("找到 1 个。")]).fn }),
      state,
      "找山海纪行",
      opts(),
    );
    const refused = await runTurn(makeDeps({ consent: vi.fn(async () => false) }), state, "不发送的这句", opts());
    const chat = fakeChat([reply("好的。")]);
    await runTurn(makeDeps({ chat: chat.fn }), { turns: [first, refused], handles: state.handles }, "把它移到已看", opts());
    const messages = chat.requests[0].messages;
    expect(messages.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
    expect(messages[0].content).toBe("找山海纪行");
    expect(messages[1].content).toContain("找到 1 个。");
    expect(messages[1].content).toContain("f1 山海纪行 01.mkv");
    expect(JSON.stringify(messages)).not.toContain("不发送的这句");
    expect(messages[2].content).toBe("把它移到已看");
  });

  it("sends only final groups in UI order, with all forty visible rows numbered", async () => {
    const state = fresh();
    const prior = await runTurn(makeDeps({ chat: fakeChat([reply("已筛选。")]).fn }), state, "找文件", opts());
    const rows = Array.from({ length: 40 }, (_, i) => ({ ...hit, id: `visible-${i + 1}`, name: `Visible ${i + 1}` }));
    const hidden = { ...hit, id: "hidden", name: "Hidden candidate" };
    rowsForModel(rows, state.handles);
    rowsForModel([hidden], state.handles);
    prior.cards = [
      { ...filesCard([hidden, ...rows.slice(0, 20)]), id: "first", query: emptyQuery() },
      { ...filesCard(rows.slice(20)), id: "second", query: emptyQuery() },
    ];
    prior.resultGroups = [
      { title: "Final group", sourceIds: ["second", "first"], fileIds: rows.map((row) => row.id).reverse() },
      { title: "Another group", sourceIds: ["first"], fileIds: [rows[1].id] },
    ];
    const chat = fakeChat([reply("好的。")]);
    await runTurn(makeDeps({ chat: chat.fn }), { ...state, turns: [prior] }, "第二个和第四十个", opts());
    const history = chat.requests[0].messages[1].content;
    expect(history).not.toContain(hidden.name);
    expect(history).toContain(`2. ${state.handles.peek(rows[1].id, "file")} Visible 2`);
    expect(history).toContain(`40. ${state.handles.peek(rows[39].id, "file")} Visible 40`);
    expect(history.indexOf("Visible 1")).toBeLessThan(history.indexOf("Visible 21"));
    expect(history.indexOf("Final group")).toBeLessThan(history.indexOf("Another group"));
    expect(history).not.toContain("visible-40");
  });

  it("does not treat explicit empty selections or unselected multiple searches as answers", async () => {
    const state = fresh();
    const prior = await runTurn(makeDeps({ chat: fakeChat([reply("没有选中的结果。")]).fn }), state, "找文件", opts());
    rowsForModel([hit], state.handles);
    const source: FilesCard = { ...filesCard([hit]), id: "source", query: emptyQuery() };
    for (const turn of [
      { ...prior, cards: [source], resultGroups: [] },
      { ...prior, cards: [source, { ...source, id: "other" }], resultGroups: undefined },
    ]) {
      const chat = fakeChat([reply("好的。")]);
      await runTurn(makeDeps({ chat: chat.fn }), { ...state, turns: [turn] }, "第二个", opts());
      expect(chat.requests[0].messages[1].content).not.toContain(hit.name);
    }
    expect(compactHistory([{ ...prior, cards: [source] }], state.handles)[1].content).toContain(hit.name);
  });

  it("keeps plan context without presenting collapsed searches or file cards as visible", async () => {
    const state = fresh();
    const prior = await runTurn(makeDeps({ chat: fakeChat([reply("请确认计划。")]).fn }), state, "整理", opts());
    rowsForModel([hit], state.handles);
    prior.cards = [
      { ...filesCard([hit]), id: "source", query: emptyQuery() },
      filesCard([hit]),
      {
        kind: "plan", id: "plan", title: "Visible plan", state: "draft", note: "", tool: "propose_changes",
        planId: "local-plan", scopeId: "", scopePath: "", counts: { changes: 1, kept: 0, trash: 0, conflicts: 0 },
        sample: null, source: "model", recordId: "",
      },
    ];
    prior.resultGroups = [{ title: "Collapsed search", sourceIds: ["source"] }];
    const chat = fakeChat([reply("好的。")]);
    await runTurn(makeDeps({ chat: chat.fn }), { ...state, turns: [prior] }, "确认哪个", opts());
    const history = chat.requests[0].messages[1].content;
    expect(history).toContain("Visible plan");
    expect(history).not.toContain("Collapsed search");
    expect(history).not.toContain(hit.name);
  });

  it("describes local scope with names and handles, never raw ids", async () => {
    const state = fresh();
    const prior = await runTurn(makeDeps({ chat: fakeChat([reply("好的。")]).fn }), state, "整理这些", opts());
    rowsForModel([hit], state.handles);
    prior.scope = {
      folder: [{ id: "private-folder-id", name: "全部文件 / 影视" }],
      entries: [{ id: hit.id, name: hit.name, folder: false }],
    };
    const history = compactHistory([prior], state.handles);
    expect(history[0].content).toContain("全部文件 / 影视");
    expect(history[0].content).toContain(`1. ${state.handles.peek(hit.id, "file")} ${hit.name}`);
    expect(JSON.stringify(history)).not.toContain("private-folder-id");
    expect(JSON.stringify(history)).not.toContain(hit.id);
  });

  it("caps the history, newest turns kept", () => {
    const turn = (i: number): Turn => ({
      id: `t${i}`,
      at: "",
      user: `问题 ${i}`,
      text: "答".repeat(1000),
      traces: [],
      cards: [],
      usage: { input: 0, output: 0, calls: 1 },
      stop: "end",
      error: "",
      denied: false,
      notice: "",
      mode: "tools",
      modeNote: "",
    });
    const history = compactHistory(Array.from({ length: 20 }, (_, i) => turn(i)), new Handles());
    expect(history.length).toBeLessThanOrEqual(16);
    expect(history.at(-2)!.content).toBe("问题 19");
    expect(history[0].role).toBe("user");
  });
});

describe("runTurn · json emulation", () => {
  it("runs one tool per step through the structured mode", async () => {
    const json = fakeJson([
      { say: "我查一下。", call: { tool: "search_files", args: { keywords: ["山海"], played: "unfinished" } } },
      { say: "找到 1 个：山海纪行 01.mkv。" },
    ]);
    const deps = makeDeps({ json: json.fn });
    const turn = await runTurn(deps, fresh(), "没看完的山海纪行", opts({ mode: "json" }));
    expect(deps.chat).not.toHaveBeenCalled();
    expect(deps.consent).toHaveBeenCalledTimes(1);
    expect(json.fn).toHaveBeenCalledTimes(2);
    expect(deps.tools.search_files).toHaveBeenCalledWith(
      { query: expect.objectContaining({ keywords: ["山海"], played: "unfinished" }) },
      expect.anything(),
    );
    const first = json.requests[0];
    expect(first.outputName).toBe("agent_step");
    expect(JSON.parse(first.outputSchema).required).toEqual(["say"]);
    expect(first.system).toContain("propose_offline_add");
    const second = json.requests[1].messages;
    expect(second.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
    expect(second[2].content).toContain("f1 | 山海纪行 01.mkv");
    expect(JSON.stringify(json.requests)).not.toContain("real-file-1");
    expect(turn).toMatchObject({ stop: "end", mode: "json", text: "我查一下。\n\n找到 1 个：山海纪行 01.mkv。" });
    expect(turn.usage.calls).toBe(2);
  });

  it("feeds an unknown tool back and fails on output that is not the format", async () => {
    const json = fakeJson([{ say: "", call: { tool: "rm_rf", args: {} } }, "这不是 JSON"]);
    const deps = makeDeps({ json: json.fn });
    const turn = await runTurn(deps, fresh(), "删", opts({ mode: "json" }));
    expect(json.requests[1].messages.at(-1)!.content).toContain("没有这个工具");
    expect(turn.stop).toBe("error");
    expect(turn.error).toContain("不是 JSON");
    expect(turn.error).toContain("没有执行任何操作");
  });

  it("switches to the structured mode when the model has no tools, without a second consent", async () => {
    const chat = fakeChat([new Error(NO_TOOLS_MESSAGE)]);
    const json = fakeJson([{ say: "你好，我能帮你找文件和整理。" }]);
    const deps = makeDeps({ chat: chat.fn, json: json.fn });
    const turn = await runTurn(deps, fresh(), "你好", opts());
    expect(chat.fn).toHaveBeenCalledTimes(1);
    expect(json.fn).toHaveBeenCalledTimes(1);
    expect(deps.consent).toHaveBeenCalledTimes(1);
    expect(turn).toMatchObject({ stop: "end", mode: "json", error: "", text: "你好，我能帮你找文件和整理。" });
    expect(turn.modeNote).toContain("不支持工具调用");
  });

  it("carries the tool results over when the switch happens mid-turn", async () => {
    const chat = fakeChat([reply("先查。", [["search_files", {}]]), new Error(`调用失败：${NO_TOOLS_MESSAGE}`)]);
    const json = fakeJson([{ say: "找到 1 个。" }]);
    const turn = await runTurn(makeDeps({ chat: chat.fn, json: json.fn }), fresh(), "找视频", opts());
    const messages = json.requests[0].messages;
    expect(messages.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
    expect(messages[1].content).toContain("search_files");
    expect(messages[2].content).toContain("f1 | 山海纪行 01.mkv");
    expect(turn).toMatchObject({ stop: "end", mode: "json", text: "先查。\n\n找到 1 个。" });
    expect(turn.usage.calls).toBe(3);
  });
});

describe("Handles", () => {
  it("keeps one handle per id, files and tasks apart, and survives a save", () => {
    const h = new Handles();
    expect(h.of("a", "file")).toBe("f1");
    expect(h.of("b", "folder", "影视")).toBe("f2");
    expect(h.of("a", "file")).toBe("f1");
    expect(h.of("a", "task")).toBe("c1");
    expect(h.resolve("F2")).toMatchObject({ id: "b", kind: "folder", path: "影视" });
    expect(h.peek("zzz", "file")).toBeUndefined();
    const restored = new Handles(JSON.parse(JSON.stringify(h.snapshot())));
    expect(restored.resolve("f1")?.id).toBe("a");
    expect(restored.of("new", "file")).toBe("f3");
    expect(restored.of("t2", "task")).toBe("c2");
  });

  it("drops saved records that do not fit", () => {
    const h = new Handles({
      records: [
        { handle: "f1", id: "a", kind: "file", path: "" },
        { handle: "x1", id: "b", kind: "file", path: "" },
        { handle: "c1", id: "c", kind: "file", path: "" },
        { handle: "f1", id: "d", kind: "file", path: "" },
      ],
    });
    expect(h.size).toBe(1);
  });
});

describe("parseToolCall", () => {
  const handles = new Handles();
  const file = handles.of("file-1", "file");
  const folder = handles.of("folder-1", "folder", "影视/纪录片");
  const task = handles.of("task-1", "task");

  it("reads search_files strictly", () => {
    const call = parseToolCall("search_files", JSON.stringify({ under: folder, played: "finished", limit: 999, ext: [".MKV"] }), handles);
    expect(call).toMatchObject({ tool: "search_files", args: { query: { under: "影视/纪录片", played: "finished", limit: 40, ext: ["mkv"] } } });
    expect(() => parseToolCall("search_files", { category: "music" }, handles)).toThrow(ModelOutputError);
    expect(() => parseToolCall("search_files", { createdAfter: "2026-13-01" }, handles)).toThrow(ModelOutputError);
    expect(parseToolCall("search_files", { under: "全部文件 / 影视" }, handles)).toMatchObject({ args: { query: { under: "影视" } } });
  });

  it("reads folders by handle or path", () => {
    expect(parseToolCall("folder_summary", {}, handles)).toEqual({ tool: "folder_summary", args: { folder: { id: "", path: "" } } });
    expect(parseToolCall("folder_summary", { folder }, handles)).toEqual({
      tool: "folder_summary",
      args: { folder: { id: "folder-1", path: "影视/纪录片" } },
    });
    expect(() => parseToolCall("folder_summary", { folder: file }, handles)).toThrow("不是文件夹");
    expect(() => parseToolCall("find_folders", { path: " / " }, handles)).toThrow(ModelOutputError);
  });

  it("reads organize_media", () => {
    expect(() => parseToolCall("organize_media", {}, handles)).toThrow("需要 folder 或 handles");
    expect(parseToolCall("organize_media", { handles: [file, folder, file], instructions: "动漫单独放" }, handles)).toEqual({
      tool: "organize_media",
      args: {
        folder: null,
        targets: [
          { id: "file-1", folder: false },
          { id: "folder-1", folder: true },
        ],
        instructions: "动漫单独放",
      },
    });
  });

  it("reads propose_changes item by item", () => {
    const call = parseToolCall(
      "propose_changes",
      {
        title: "挪到已看",
        items: [
          { handle: file, toPath: "影视/已看", name: "a.mkv" },
          { handle: folder, trash: true, name: "ignored" },
        ],
      },
      handles,
    );
    expect(call).toEqual({
      tool: "propose_changes",
      args: {
        title: "挪到已看",
        items: [
          { id: "file-1", folder: false, toPath: ["影视", "已看"], name: "a.mkv", trash: false },
          { id: "folder-1", folder: true, toPath: null, name: "", trash: true },
        ],
      },
    });
    expect(() => parseToolCall("propose_changes", { title: "x", items: [{ handle: file, toPath: ["a/b"] }] }, handles)).toThrow("不可用");
    expect(() => parseToolCall("propose_changes", { title: "x", items: [{ handle: file, name: "a:b" }] }, handles)).toThrow("不可用");
    expect(() => parseToolCall("propose_changes", { title: "x", items: [{ handle: file }] }, handles)).toThrow("没有目标位置");
    expect(() => parseToolCall("propose_changes", { title: "x", items: [{ handle: file, trash: true }, { handle: file, trash: true }] }, handles)).toThrow(
      "不止一次",
    );
    expect(() => parseToolCall("propose_changes", { title: "x", items: [] }, handles)).toThrow("不能为空");
  });

  it("reads offline adds, downloads and shares", () => {
    expect(parseToolCall("propose_offline_add", { urls: ["magnet:?xt=urn:btih:abc"], tasks: [task], folder }, handles)).toEqual({
      tool: "propose_offline_add",
      args: { urls: ["magnet:?xt=urn:btih:abc"], taskIds: ["task-1"], folder: { id: "folder-1", path: "影视/纪录片" } },
    });
    expect(() => parseToolCall("propose_offline_add", { urls: ["javascript:alert(1)"] }, handles)).toThrow("不支持的链接");
    expect(() => parseToolCall("propose_offline_add", { tasks: [file] }, handles)).toThrow("未知的编号");
    expect(() => parseToolCall("propose_offline_add", {}, handles)).toThrow("需要 urls 或 tasks");
    expect(parseToolCall("propose_share", { handles: [folder] }, handles)).toEqual({
      tool: "propose_share",
      args: { targets: [{ id: "folder-1", folder: true }] },
    });
    expect(() => parseToolCall("propose_download", { handles: [] }, handles)).toThrow("不能为空");
    expect(() => parseToolCall("file_info", { handle: task }, handles)).toThrow("未知的编号");
  });
});

describe("what tools tell the model", () => {
  it("lists at most 40 rows with the total, under handles", () => {
    const handles = new Handles();
    const rows: ModelRow[] = Array.from({ length: 45 }, (_, i) => ({ ...hit, id: `secret-${i}`, name: `第${i + 1}集.mkv` }));
    const out = rowsForModel(rows, handles, 120);
    expect(out.split("\n")).toHaveLength(42);
    expect(out).not.toContain("secret-");
    expect(out).toContain("f40 | 第40集.mkv");
    expect(out).not.toContain("第41集");
  });

  it("names aggregate folders by handle and reports a missing folder", () => {
    const handles = new Handles();
    const agg = searchForModel(
      { hits: null, total: 30, aggregate: [{ key: "dir-9", label: "全部文件 / 影视", size: 5 * GiB, count: 12 }], under: "" },
      { aggregate: { by: "folder", measure: "size", limit: 10 }, under: "" },
      handles,
    );
    expect(agg).toContain("f1 | 影视 | 5.0 GB | 12");
    expect(handles.resolve("f1")).toMatchObject({ id: "dir-9", kind: "folder", path: "影视" });
    const missing = searchForModel({ hits: [], total: 0, aggregate: null, under: "", underMissing: true }, { aggregate: null, under: "不存在" }, handles);
    expect(missing).toContain("不存在");
  });

  it("summarises a folder with its children and largest items", () => {
    const handles = new Handles();
    const out = folderSummaryForModel(
      "全部文件 / 影视",
      {
        files: 12,
        folders: 2,
        size: 20 * GiB,
        categories: [{ category: "video", count: 10, size: 19 * GiB }],
        dupGroups: 1,
        dupCopies: 1,
        dupWaste: GiB,
        unfinished: 3,
        largest: [hit],
      },
      [{ ...hit, id: "child", name: "纪录片", folder: true, path: "全部文件 / 影视" }],
      handles,
    );
    expect(out).toContain("纪录片/");
    expect(handles.resolve("f2")).toMatchObject({ kind: "folder", path: "影视/纪录片" });
  });

  it("never passes on what a tool row carries beyond the listed fields", () => {
    const handles = new Handles();
    const detail = { ...hit, hash: "ABCDEF0123456789", thumbnail: "https://thumb/secret", createdTime: "2026-09-30T00:00:00Z", width: 1920, height: 1080 };
    const info = fileInfoForModel(detail, handles);
    expect(info).not.toContain("ABCDEF");
    expect(info).not.toContain("thumb");
    expect(info).toContain("1920×1080");
    const task = {
      id: "task-real",
      name: "纪录片合集",
      phase: "PHASE_TYPE_ERROR",
      progress: 30,
      fileSize: 0,
      message: "Failed",
      errorDetail: "task_file_deleted",
      createdTime: "2026-10-10T08:00:00Z",
      updatedTime: "2026-10-10T09:00:00Z",
      savedTo: "全部文件 / 影视",
      hash: "0123456789ABCDEF0123456789ABCDEF01234567",
      url: "magnet:?xt=urn:btih:0123456789abcdef",
      fileId: "file-real",
    };
    const transfers = transfersForModel([task], [], handles);
    expect(transfers).toContain("c1 | 纪录片合集");
    expect(transfers).not.toContain("0123456789");
    expect(transfers).not.toContain("magnet");
    expect(transfers).not.toContain("task-real");
    expect(foldersForModel([{ id: "x", path: "全部文件 / 影视 / 纪录片" }], handles)).toContain("影视/纪录片");
  });

  it("sends matched media groups as pattern and Matched, never a title", () => {
    const groups = [
      {
        id: "g1",
        kind: "video" as const,
        pattern: "[Group] 山海纪行 - {n1} [1080p].mkv",
        files: [{}, {}, {}],
        identity: { title: "Mountains and Seas", year: 2023, overview: "TMDB overview" },
      },
      { id: "g2", kind: "subtitle" as const, pattern: "random_{n1}.ass", files: [{}] },
    ];
    const out = mediaSummaryForModel(groups, new Set(["g1"]));
    expect(out).not.toContain("Mountains");
    expect(out).not.toContain("2023");
    expect(out).not.toContain("overview");
  });

  it("strips the canonical root without changing folder names", () => {
    expect(modelPath("全部文件 / Media / Documentaries")).toBe("Media/Documentaries");
    expect(modelPath("全部文件")).toBe("");
  });

});

