// How an account's PikPak membership reads in one line: which one runs and
// until when, or that it ended, or that there never was one. The server
// keeps an item per membership kind, running or ended.
import type { Membership, MembershipItem } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { day } from "./format";
import { N_, t } from "./i18n";

const KIND_NAME: Record<string, string> = {
  global: N_("全球会员"),
  regional: N_("区域会员"),
};

export interface MembershipLine {
  /** "Regional member", "Membership expired", "No membership". */
  title: string;
  /** "Expires 2029/02/14 · 853 days remaining"; "" when there is nothing to date. */
  detail: string;
  tone: "active" | "ended" | "none";
}

export function membershipLine(m: Membership): MembershipLine {
  const items = m.items ?? [];
  if (m.active) {
    const running = items.filter((i) => i.active);
    const title = running.map((i) => (KIND_NAME[i.kind] ? t(KIND_NAME[i.kind]) : i.name || t("PikPak 会员"))).join(" · ") || t("PikPak 会员");
    const parts = m.expire ? [t("{date} 到期", { date: day(m.expire) })] : [];
    if (running.length) {
      const left = Math.max(...running.map((i) => i.daysLeft));
      parts.push(left <= 0 ? t("今天到期") : t("剩 {n} 天", { n: left }));
    }
    return { title, detail: parts.join(" · "), tone: "active" };
  }
  // The most recent end is the one worth naming.
  const last = items.reduce<MembershipItem | null>((a, b) => (!a || Date.parse(b.expire) > Date.parse(a.expire) ? b : a), null);
  if (last?.expire) return { title: t("会员已过期"), detail: t("{date} 到期", { date: day(last.expire) }), tone: "ended" };
  return { title: t("未开通会员"), detail: "", tone: "none" };
}
