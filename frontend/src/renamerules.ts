/*
 * Rename by Rules: the file page's selection renamed by a
 * few deterministic rules — no model, no guessing what a work is. The result
 * is an ordinary plan (organizer.ts Change), so it is previewed, edited,
 * checked and confirmed like any other, and undone from the records.
 */
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { classify, splitName } from "./media";
import { nameError, type Change } from "./organizer";

export type RenameRule =
  | { kind: "stripTags" }
  | { kind: "padNumbers"; width: number }
  | { kind: "replace"; find: string; with: string };

/* ---------------- stripTags ---------------- */

const CLOSE: Record<string, string> = { "[": "]", "【": "】", "(": ")", "（": "）" };

type Part = { tag: false; text: string } | { tag: true; text: string; inner: string };

function parts(stem: string): Part[] {
  const out: Part[] = [];
  let i = 0;
  let plain = "";
  const chars = Array.from(stem);
  while (i < chars.length) {
    const close = CLOSE[chars[i]];
    const end = close ? chars.indexOf(close, i + 1) : -1;
    if (end < 0) {
      plain += chars[i++];
      continue;
    }
    if (plain) out.push({ tag: false, text: plain });
    plain = "";
    out.push({ tag: true, text: chars.slice(i, end + 1).join(""), inner: chars.slice(i + 1, end).join("").trim() });
    i = end + 1;
  }
  if (plain) out.push({ tag: false, text: plain });
  return out;
}

/**
 * A bracket that says which episode or which year — [01], (2023), [S01E02],
 * 【Episode 3】 — is part of what the file is, not a release tag: it stays.
 */
const KEEP = /^(\d{1,4}(\.5)?(v\d)?|s\d{1,2}e\d{1,4}|ep?\s?\d{1,4}|第[\d一二三四五六七八九十百零〇]+[集话話期季部]|sp\d{0,2}|ova\d{0,2})$/i;

/** Text worth keeping: anything besides separators. */
const MEANINGFUL = /[\p{L}\p{N}]/u;

/** Separators a removed tag leaves at either end. */
const EDGE = /^[\s._\-–—~·]+|[\s._\-–—~·]+$/g;

/**
 * stripTags removes the release tags at both ends of a name — Subtitle Group,
 * resolution, codec, language in [] 【】 () — and keeps the title, the
 * episode and the year. A name made only of brackets
 * ("[Subtitle Group][Series Title][01][1080p]") reads like guessit does: the first bracket
 * is the group, the next the title, numbers stay.
 */
function stripTags(stem: string): string {
  const ps = parts(stem);
  const removable = (p: Part) => p.tag && !KEEP.test(p.inner);
  if (ps.some((p) => !p.tag && MEANINGFUL.test(p.text))) {
    // Only the ends go, so what stays is the original text, spacing included.
    let start = 0;
    let end = ps.length;
    while (start < end && (removable(ps[start]) || !MEANINGFUL.test(ps[start].text))) start++;
    while (end > start && (removable(ps[end - 1]) || !MEANINGFUL.test(ps[end - 1].text))) end--;
    return ps.slice(start, end).map((p) => p.text).join("").replace(EDGE, "") || stem;
  }
  const tags = ps.filter(removable);
  const title = tags.length > 1 ? tags[1] : tags[0];
  const kept = ps.filter((p) => p === title || !removable(p)).map((p) => (p === title && p.tag ? p.inner : p.text));
  return kept.join(" ").replace(/\s{2,}/g, " ").replace(EDGE, "") || stem;
}

/* ---------------- padNumbers ---------------- */

/** Letters right before a number that make it an episode or season: S01, E03, EP03, Episode 3 is spaced. */
const NUMBER_PREFIX = /^(s|e|ep|episode|vol|part|pt)$/i;

/**
 * padNumbers widens the number runs that count episodes or seasons to
 * `width` digits. Left alone: years, resolutions (1080p, 4K, 1920x1080),
 * codecs and audio (x264, H.265, 10bit, 5.1, AAC2.0), versions (v2) and
 * anything glued to other letters (HDR10).
 */
function padNumbers(stem: string, width: number): string {
  const w = Math.min(6, Math.max(2, Math.round(width)));
  return stem.replace(/\d+/g, (run, at: number) => {
    if (run.length >= w || /^(19|20)\d\d$/.test(run)) return run;
    const before = stem.slice(0, at);
    const after = stem.slice(at + run.length);
    // Decimals (5.1, 2.0, 1.5 as a half episode) and codecs (H.264, x265);
    // Show.2023.3 and EP7.2160p are a number beside a year or a resolution.
    if (/(^|\D)\d{1,2}[.,]$/.test(before) || /^[.,]\d{1,2}(?!\d)/.test(after) || /(^|[^A-Za-z0-9])[hx]\.?$/i.test(before)) return run;
    // 1x05 is season x episode; 1920x1080 is a resolution.
    const cross = /(\d+)x$/i.exec(before);
    if (cross) return cross[1].length <= 2 ? run.padStart(w, "0") : run;
    if (/^x\d/i.test(after)) return /^x\d{1,3}(?!\d)/i.test(after) ? run.padStart(w, "0") : run;
    const letters = /[A-Za-z]+$/.exec(before)?.[0];
    if (letters && !NUMBER_PREFIX.test(letters)) return run;
    // Glued to a letter after: 1080p, 4K, 10bit, 2nd — except S01E02 and 01v2.
    if (/^[A-Za-z]/.test(after) && !/^e\d/i.test(after) && !/^v\d/i.test(after)) return run;
    return run.padStart(w, "0");
  });
}

/* ---------------- public ---------------- */

/**
 * applyRules is the name after the rules, in order. The extension never
 * changes (a folder has none); a rule that would leave nothing keeps what
 * was there before it.
 */
export function applyRules(name: string, rules: readonly RenameRule[], folder = false): string {
  const { stem, ext } = folder ? { stem: name, ext: "" } : splitName(name);
  let out = stem;
  for (const r of rules) {
    let next = out;
    if (r.kind === "stripTags") next = stripTags(out);
    else if (r.kind === "padNumbers") next = padNumbers(out, r.width);
    else if (r.find) next = out.split(r.find).join(r.with);
    if (next.trim()) out = next.trim();
  }
  return out + ext;
}

/**
 * renameChanges is the Rename by Rules plan: a rename in place for every entry
 * whose name the rules change. `from` says where an entry is, as names below
 * the plan's scope. A name no folder could hold stays in the plan unticked,
 * the reason in its note, so the user sees it and can fix it.
 */
export function renameChanges(entries: readonly Entry[], rules: readonly RenameRule[], from: (e: Entry) => string[]): Change[] {
  const out: Change[] = [];
  for (const e of entries) {
    const name = applyRules(e.name, rules, e.folder);
    if (name === e.name) continue;
    const bad = nameError(name);
    const dir = from(e);
    out.push({
      item: e,
      from: dir,
      op: "move",
      toPath: dir.slice(),
      name,
      proposed: name,
      selected: !bad,
      locked: "",
      note: bad,
      series: "",
      role: e.folder ? "folder" : classify(e),
    });
  }
  return out;
}
