import { describe, expect, it } from "vitest";
import { membershipLine } from "./membership";
import type { Membership, MembershipItem } from "../bindings/github.com/tonyflux/pkp/internal/services";

const item = (p: Partial<MembershipItem>): MembershipItem => ({ kind: "regional", name: "Regional Premium", active: false, expire: "", daysLeft: 0, ...p });
const membership = (p: Partial<Membership>): Membership => ({ active: false, expire: "", items: [], notice: "", ...p });

describe("membership line", () => {
  it("names what runs and until when, leaving out what ended", () => {
    const m = membership({
      active: true,
      expire: "2029-02-14T12:00:00+08:00",
      items: [
        item({ kind: "global", active: false, expire: "2026-01-07T12:00:00+08:00", daysLeft: -281 }),
        item({ kind: "regional", active: true, expire: "2029-02-14T12:00:00+08:00", daysLeft: 853 }),
      ],
    });
    expect(membershipLine(m)).toEqual({ title: "区域会员", detail: "2029/02/14 到期 · 剩 853 天", tone: "active" });
  });

  it("says the last day rather than 0 days remaining", () => {
    const m = membership({ active: true, expire: "2026-10-15T12:00:00+08:00", items: [item({ active: true, expire: "2026-10-15T12:00:00+08:00", daysLeft: 0 })] });
    expect(membershipLine(m).detail).toBe("2026/10/15 到期 · 今天到期");
  });

  it("an ended membership is dated by its latest end; none ever is Not activated", () => {
    const ended = membership({
      items: [item({ kind: "global", expire: "2021-11-27T12:00:00+08:00" }), item({ kind: "regional", expire: "2026-01-07T12:00:00+08:00" })],
    });
    expect(membershipLine(ended)).toEqual({ title: "会员已过期", detail: "2026/01/07 到期", tone: "ended" });
    expect(membershipLine(membership({}))).toEqual({ title: "未开通会员", detail: "", tone: "none" });
  });
});
