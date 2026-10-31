// How the player names renditions:
// Auto first, then what the video actually offers — the original as Original with
// its class, the transcodes by tier. The Go side lists the options and says
// which one plays; everything here is wording.
import type { Quality, QualityState } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { t, N_ } from "./i18n";

export const AUTO = "auto";

/** tierName is the class as people say it: 4K, 2K, 1080p … "" when unknown. */
export function tierName(tier: number): string {
  if (tier >= 2160) return "4K";
  if (tier >= 1440) return "2K";
  return tier > 0 ? `${tier}p` : "";
}

/** shortLabel names a rendition in a tight spot: "Original 4K", "1080p", "Original". */
export function shortLabel(q: Quality): string {
  const name = tierName(q.tier);
  if (!q.origin) return name;
  return name ? t("原画 {name}", { name }) : t("原画");
}

// The words per tier; 480p is offered by the production API on every
// transcoded video and takes the lowest tier, Smooth.
const words: Record<number, string> = { 2160: N_("超清"), 1440: N_("超清"), 1080: N_("高清"), 720: N_("标清"), 480: N_("流畅") };

/** qualityLabel is the menu name: "Original 4K", "Ultra HD 2K", "HD 1080p", "SD 720p", "Smooth 480p". */
export function qualityLabel(q: Quality): string {
  if (q.origin) return shortLabel(q);
  const word = words[q.tier];
  const name = tierName(q.tier);
  return word ? `${t(word)} ${name}` : name;
}

export interface QualityItem {
  /** What SetPlaybackQuality is given for this row. */
  choice: string;
  label: string;
  /** The hint on the right: Recommended, Current, 2160p, Data saver or nothing. */
  detail: string;
  selected: boolean;
}

/**
 * qualityItems are the menu rows in order: Auto, the original,
 * then the transcodes from the highest tier down — exactly the options the
 * player reported, never a tier the video does not have. The selected row is
 * Auto while auto decides, otherwise the rendition playing; it reads Current.
 */
export function qualityItems(q: QualityState): QualityItem[] {
  const options = q.options ?? [];
  const selected = q.choice === AUTO ? AUTO : q.current;
  const transcodes = options.filter((o) => !o.origin);
  const lightest = transcodes.length && options.length > 1 ? transcodes[transcodes.length - 1].key : "";
  const rows: QualityItem[] = [{ choice: AUTO, label: t("自动"), detail: t("推荐"), selected: selected === AUTO }];
  for (const o of options) {
    let detail = "";
    if (o.tier >= 1440) detail = `${o.tier}p`;
    else if (o.key === lightest) detail = t("省流量");
    rows.push({ choice: o.key, label: qualityLabel(o), detail, selected: selected === o.key });
  }
  return rows.map((r) => (r.selected ? { ...r, detail: t("当前") } : r));
}

/** canChoose: one rendition leaves nothing to switch between, so no entry. */
export function canChoose(q: QualityState | undefined): boolean {
  return (q?.options?.length ?? 0) > 1;
}

/** playingLabel names what plays now, for the menu header and the notices. */
export function playingLabel(q: QualityState): string {
  const o = q.options?.find((x) => x.key === q.current);
  return o ? shortLabel(o) : "";
}

/** entryValue is the dock button's value: Switching… while loading, else the setting. */
export function entryValue(q: QualityState): string {
  if (q.switching) return t("切换中…");
  if (q.choice === AUTO) return t("自动");
  return playingLabel(q);
}
