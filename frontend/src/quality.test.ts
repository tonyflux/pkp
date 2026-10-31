import { describe, expect, it } from "vitest";
import { entryValue, qualityItems } from "./quality";
import type { Quality, QualityState } from "../bindings/github.com/tonyflux/pkp/internal/services";

const origin = (tier: number): Quality => ({ key: "origin", origin: true, tier, width: 0, height: 0, bitRate: 0 });
const transcode = (tier: number): Quality => ({ key: String(tier), origin: false, tier, width: 0, height: 0, bitRate: 0 });
const state = (p: Partial<QualityState>): QualityState =>
  ({ options: [], current: "", choice: "auto", switching: "", failed: "", error: "", lowered: false, ...p }) as QualityState;

const rows = (q: QualityState) => qualityItems(q).map((r) => [r.label, r.detail, r.selected]);

describe("quality menu", () => {
  it("lists Auto and only the renditions the video has, with tier names in menu order", () => {
    // The production shape: a 4K original and three transcodes. No 2K row
    // appears, because the video has none.
    const q = state({ options: [origin(2160), transcode(1080), transcode(720), transcode(480)], current: "1080", choice: "1080" });
    expect(rows(q)).toEqual([
      ["自动", "推荐", false],
      ["原画 4K", "2160p", false],
      ["高清 1080p", "当前", true],
      ["标清 720p", "", false],
      ["流畅 480p", "省流量", false],
    ]);
  });

  it("marks Auto as the selection while auto decides, whatever it resolved to", () => {
    const q = state({ options: [origin(1080), transcode(1080), transcode(720)], current: "720", choice: "auto", lowered: true });
    expect(rows(q).filter(([, , selected]) => selected)).toEqual([["自动", "当前", true]]);
    expect(rows(q)[1]).toEqual(["原画 1080p", "", false]);
  });

  it("an original without picture metadata is just Original quality", () => {
    expect(qualityItems(state({ options: [origin(0), transcode(480)], current: "origin", choice: "origin" }))[1]).toMatchObject({ label: "原画", detail: "当前" });
  });

  it("the dock button says what is set, and Switching… while a rendition loads", () => {
    const options = [origin(2160), transcode(1080)];
    expect(entryValue(state({ options, current: "origin", choice: "auto" }))).toBe("自动");
    expect(entryValue(state({ options, current: "origin", choice: "origin" }))).toBe("原画 4K");
    expect(entryValue(state({ options, current: "1080", choice: "1080" }))).toBe("1080p");
    expect(entryValue(state({ options, current: "origin", choice: "1080", switching: "1080" }))).toBe("切换中…");
  });
});
