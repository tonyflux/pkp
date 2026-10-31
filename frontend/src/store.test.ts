import { describe, expect, it } from "vitest";
import { holePath, matchesCategory, pickShuffle, proxyDraftError, shareStatus, sortEntries } from "./store";
import type { Entry, ShareSummary } from "../bindings/github.com/tonyflux/pkp/internal/services";

const entry = (p: Partial<Entry>): Entry => ({ id: "", name: "", size: 0, modifiedTime: "", category: "other", ...p } as Entry);

describe("sortEntries", () => {
  const rows = [
    entry({ id: "b", name: "贝", size: 2, modifiedTime: "2026-01-02T00:00:00Z" }),
    entry({ id: "a", name: "啊", size: 3, modifiedTime: "2026-01-03T00:00:00Z" }),
    entry({ id: "c", name: "次", size: 1, modifiedTime: "bad-date" }),
  ];
  it("sorts by pinyin name, size and date in both directions without mutating", () => {
    expect(sortEntries(rows, "name", true).map((r) => r.id)).toEqual(["a", "b", "c"]);
    expect(sortEntries(rows, "size", false).map((r) => r.id)).toEqual(["a", "b", "c"]);
    expect(sortEntries(rows, "size", true).map((r) => r.id)).toEqual(["c", "b", "a"]);
    // An unparsable date compares as equal, never as NaN.
    expect(sortEntries(rows, "modified", false).slice(0, 2).map((r) => r.id)).toEqual(["a", "b"]);
    expect(rows.map((r) => r.id)).toEqual(["b", "a", "c"]);
  });
});

describe("matchesCategory", () => {
  it("treats every file outside the named chips as other", () => {
    expect(matchesCategory(entry({ category: "archive" }), "other")).toBe(true);
    expect(matchesCategory(entry({ category: "audio" }), "other")).toBe(false);
    expect(matchesCategory(entry({ category: "audio" }), "audio")).toBe(true);
    expect(matchesCategory(entry({ category: "video" }), "other")).toBe(false);
    expect(matchesCategory(entry({ category: "video" }), "video")).toBe(true);
    expect(matchesCategory(entry({ category: "video" }), "all")).toBe(true);
  });
});

describe("pickShuffle", () => {
  it("never returns the current index and covers every other one", () => {
    for (let i = 0; i < 5; i++) {
      const seen = new Set<number>();
      for (let r = 0; r < 1; r += 0.05) seen.add(pickShuffle(5, i, () => Math.min(r, 0.999)));
      expect(seen.has(i)).toBe(false);
      expect(seen.size).toBe(4);
    }
  });
});

describe("holePath", () => {
  it("cuts an even-odd rounded rect out of the full box", () => {
    const p = holePath({ x: 0, y: 0, width: 400, height: 300 }, { x: 10, y: 20, width: 100, height: 50 }, 12);
    expect(p.startsWith('path(evenodd, "M0 0H400V300H0Z')).toBe(true);
    expect(p).toContain("M22 20H98A12 12 0 0 1 110 32");
    // The radius is clamped to half the shorter side.
    expect(holePath({ x: 0, y: 0, width: 10, height: 10 }, { x: 0, y: 0, width: 4, height: 8 }, 12)).toContain("A2 2 0 0 1");
  });
});

describe("shareStatus", () => {
  const share = (p: Partial<ShareSummary>): ShareSummary => ({ status: "active", daysLeft: -1, statusText: "", ...p } as ShareSummary);
  it("maps server status and days left to one vocabulary", () => {
    expect(shareStatus(share({}))).toEqual({ tone: "ok", text: "永久有效" });
    expect(shareStatus(share({ daysLeft: 0 }))).toEqual({ tone: "warn", text: "今天到期" });
    expect(shareStatus(share({ daysLeft: 7 }))).toEqual({ tone: "warn", text: "剩余 7 天" });
    expect(shareStatus(share({ status: "expired" }))).toEqual({ tone: "off", text: "已过期" });
    expect(shareStatus(share({ status: "blocked", statusText: "违规" }))).toEqual({ tone: "off", text: "违规" });
  });
});

describe("proxyDraftError", () => {
  it("only checks Custom proxy, and refuses an empty host or a port outside 1–65535", () => {
    expect(proxyDraftError("direct", "", 0)).toBe("");
    expect(proxyDraftError("system", "", 0)).toBe("");
    expect(proxyDraftError("custom", "127.0.0.1", 7890)).toBe("");
    expect(proxyDraftError("custom", "  ", 7890)).toBe("请填写代理服务器地址");
    expect(proxyDraftError("custom", "127.0.0.1", 0)).toBe("端口需要是 1 到 65535 之间的整数");
    expect(proxyDraftError("custom", "127.0.0.1", 65536)).toBe("端口需要是 1 到 65535 之间的整数");
    // An empty Port field reads as NaN, which is not a port either.
    expect(proxyDraftError("custom", "127.0.0.1", Number.NaN)).toBe("端口需要是 1 到 65535 之间的整数");
    expect(proxyDraftError("custom", "127.0.0.1", 65535)).toBe("");
  });
});
