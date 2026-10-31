import { describe, expect, it } from "vitest";
import { ago, bytes, clock, day, kindLabel, maskLogin, stamp } from "./format";

describe("bytes", () => {
  it("picks the unit and keeps two decimals under 10", () => {
    expect(bytes(0)).toBe("—");
    expect(bytes(0, "0 B")).toBe("0 B");
    expect(bytes(1023)).toBe("1023 B");
    expect(bytes(1536)).toBe("1.50 KB");
    expect(bytes(5 * 1024 ** 4, "", 1)).toBe("5.0 TB");
    expect(bytes(123 * 1024 ** 2)).toBe("123 MB");
  });
});

describe("clock", () => {
  it("formats m:ss and h:mm:ss", () => {
    expect(clock(0)).toBe("0:00");
    expect(clock(NaN, "?")).toBe("?");
    expect(clock(65)).toBe("1:05");
    expect(clock(3661)).toBe("1:01:01");
  });
});

describe("day / ago", () => {
  it("renders dates and falls back on garbage", () => {
    expect(day("2026-10-08T00:00:00+08:00")).toMatch(/^2026\/10\/0[78]$/);
    expect(day("nope")).toBe("—");
    expect(day(null, "未提供")).toBe("未提供");
    expect(ago(new Date(Date.now() - 90_000).toISOString())).toBe("1 分钟前");
    expect(ago(new Date(Date.now() - 30 * 86_400_000).toISOString())).toMatch(/^\d{4}\/\d{2}\/\d{2}$/);
  });
});

describe("stamp", () => {
  // Built in local time, so the calendar-day boundaries hold in any zone.
  const now = new Date(2026, 9, 12, 0, 30);
  const at = (...p: [number, number, number, number, number]) => new Date(...p).toISOString();

  it("splits on calendar days, not 24-hour windows", () => {
    expect(stamp(at(2026, 9, 12, 0, 5), "—", now)).toBe("今天 00:05");
    expect(stamp(at(2026, 9, 11, 23, 59), "—", now)).toBe("昨天 23:59");
    expect(stamp(at(2026, 9, 11, 0, 0), "—", now)).toBe("昨天 00:00");
    expect(stamp(at(2026, 9, 10, 23, 59), "—", now)).toBe("10月10日 23:59");
  });

  it("drops the time across years and falls back on garbage", () => {
    expect(stamp(at(2025, 11, 31, 18, 5), "—", now)).toBe("2025/12/31");
    expect(stamp("", "未完成", now)).toBe("未完成");
    expect(stamp("nope", "—", now)).toBe("—");
  });
});

describe("kindLabel", () => {
  it("names folders, extension + category, and bare files", () => {
    expect(kindLabel({ name: "x", folder: true, category: "other" })).toBe("文件夹");
    expect(kindLabel({ name: "a.MKV", folder: false, category: "video" })).toBe("MKV 视频");
    expect(kindLabel({ name: "noext", folder: false, category: "other" })).toBe("文件");
  });
});

describe("maskLogin", () => {
  it("keeps what tells accounts apart and hides the rest", () => {
    expect(maskLogin("main@example.com")).toBe("ma••@example.com");
    // A one- or two-letter name keeps only its first letter: two would be all of it.
    expect(maskLogin("ab@example.com")).toBe("a••@example.com");
    expect(maskLogin("陈小明@example.com")).toBe("陈小••@example.com");
    expect(maskLogin(" 138 1234 5678 ")).toBe("138••••5678");
    expect(maskLogin("+8613812345678")).toBe("+86••••5678");
    expect(maskLogin("someone")).toBe("so••ne");
    expect(maskLogin("")).toBe("");
  });
});
