export type TimedFormat = "srt" | "ass" | "ssa" | "vtt" | "lrc";
export type TimedEncoding = "auto" | "utf-8" | "gb18030" | "big5" | "utf-16le" | "utf-16be";
export type TimedCue = { id: number; start: number; end: number | null; text: string };
export const TIMED_MAX_BYTES = 16 * 1024 * 1024;
export const TIMED_MAX_CUES = 100000;

export function decodeTimed(bytes: Uint8Array, encoding: TimedEncoding): string {
  if (encoding !== "auto") return new TextDecoder(encoding).decode(bytes);
  if (bytes[0] === 0xff && bytes[1] === 0xfe) return new TextDecoder("utf-16le").decode(bytes);
  if (bytes[0] === 0xfe && bytes[1] === 0xff) return new TextDecoder("utf-16be").decode(bytes);
  try { return new TextDecoder("utf-8", { fatal: true }).decode(bytes); }
  catch { return new TextDecoder("gb18030").decode(bytes); }
}

/** Milliseconds, accepting SRT/ASS hours and VTT/LRC minutes. */
export function cueTime(value: string): number | null {
  const match = /^(?:(\d+):)?(\d+):(\d{2})(?:[.,](\d{1,3}))?$/.exec(value.trim());
  if (!match || Number(match[3]) >= 60 || (match[1] !== undefined && Number(match[2]) >= 60)) return null;
  const result = ((Number(match[1] ?? 0) * 60 + Number(match[2])) * 60 + Number(match[3])) * 1000 + Number((match[4] ?? "").padEnd(3, "0"));
  return Number.isSafeInteger(result) ? result : null;
}

function subtitleText(value: string): string {
  const entities: Record<string, string> = { amp: "&", lt: "<", gt: ">", nbsp: "\u00a0", lrm: "\u200e", rlm: "\u200f", quot: '"', apos: "'" };
  return value.replace(/<\/?(?:b|i|u|s|ruby|rt|c(?:\.[\w-]+)*|v|lang|font)(?:\s[^>]*)?>|<\d{2}:\d{2}(?::\d{2})?\.\d{3}>/gi, "")
    .replace(/&(#x[\da-f]+|#\d+|amp|lt|gt|nbsp|lrm|rlm|quot|apos);/gi, (whole, name: string) => {
      if (name[0] !== "#") return entities[name.toLowerCase()] ?? whole;
      const code = name[1].toLowerCase() === "x" ? parseInt(name.slice(2), 16) : Number(name.slice(1));
      return code > 0 && code <= 0x10ffff && !(code >= 0xd800 && code <= 0xdfff) ? String.fromCodePoint(code) : whole;
    });
}

function assText(value: string): string {
  let drawing = false;
  let output = "";
  for (const chunk of value.split(/(\{[^}]*\})/g)) {
    if (chunk.startsWith("{")) {
      for (const flag of chunk.matchAll(/\\p(\d+)/g)) drawing = Number(flag[1]) !== 0;
    } else if (!drawing) output += chunk;
  }
  return output.replace(/\\[Nn]/g, "\n").replace(/\\h/g, "\u00a0");
}

export function parseTimed(source: string, format: TimedFormat): TimedCue[] {
  const lines = source.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n").split("\n");
  const cues: TimedCue[] = [];
  const add = (start: number | null, end: number | null, text: string) => {
    if (start === null || (end !== null && end < start)) return;
    if (cues.length >= TIMED_MAX_CUES) throw new RangeError("TIMED_CUE_LIMIT");
    cues.push({ id: cues.length, start, end, text });
  };
  if (format === "ass" || format === "ssa") {
    let events = false;
    let fields = format === "ssa" ? ["marked", "start", "end", "style", "name", "marginl", "marginr", "marginv", "effect", "text"] : ["layer", "start", "end", "style", "name", "marginl", "marginr", "marginv", "effect", "text"];
    for (const line of lines) {
      if (/^\s*\[/.test(line)) { events = /^\s*\[events\]\s*$/i.test(line); continue; }
      if (!events) continue;
      if (/^\s*format\s*:/i.test(line)) { fields = line.slice(line.indexOf(":") + 1).split(",").map(field => field.trim().toLowerCase()); continue; }
      if (!/^\s*dialogue\s*:/i.test(line)) continue;
      const raw = line.slice(line.indexOf(":") + 1).trimStart();
      const textIndex = fields.indexOf("text");
      if (textIndex < 0 || fields.indexOf("start") < 0 || fields.indexOf("end") < 0) continue;
      const values = raw.split(",");
      if (values.length < fields.length) continue;
      values.splice(textIndex, values.length - fields.length + 1, values.slice(textIndex, values.length - (fields.length - textIndex - 1)).join(","));
      const start = cueTime(values[fields.indexOf("start")]);
      const end = cueTime(values[fields.indexOf("end")]);
      if (end !== null) add(start, end, assText(values[textIndex]));
    }
  } else if (format === "lrc") {
    let offset = 0;
    for (const line of lines) {
      const match = /\[offset\s*:\s*([+-]?\d+)\]/i.exec(line);
      if (match && Number.isSafeInteger(Number(match[1]))) offset = Number(match[1]);
    }
    for (const line of lines) {
      const stamps = [...line.matchAll(/\[(\d+:\d{2}(?:[.:]\d{1,3})?)\]/g)];
      if (!stamps.length) continue;
      const lastStamp = stamps[stamps.length - 1];
      const text = line.slice((lastStamp.index ?? 0) + lastStamp[0].length).replace(/<\d+:\d{2}(?:\.\d{1,3})?>/g, "");
      for (const stamp of stamps) {
        const start = cueTime(stamp[1].replace(/:(\d{1,3})$/, (whole, fraction: string) => stamp[1].split(":").length === 3 ? `.${fraction}` : whole));
        if (start !== null) add(Math.max(0, start + offset), null, text);
      }
    }
  } else {
    const blocks = lines.join("\n").split(/\n(?:[\t ]*\n)+/);
    for (const block of blocks) {
      const parts = block.split("\n");
      if (format === "vtt" && /^(?:WEBVTT(?:\s|$)|NOTE(?:\s|$)|STYLE\s*$|REGION\s*$)/.test(parts[0])) continue;
      const timingIndex = parts.findIndex(line => line.includes("-->"));
      if (timingIndex < 0 || timingIndex > 1) continue;
      const timing = /^\s*(\S+)\s+-->\s+(\S+)(?:\s+.*)?$/.exec(parts[timingIndex]);
      if (timing) {
        const end = cueTime(timing[2]);
        if (end !== null) add(cueTime(timing[1]), end, subtitleText(parts.slice(timingIndex + 1).join("\n")));
      }
    }
  }
  cues.sort((a, b) => a.start - b.start || a.id - b.id);
  if (format === "lrc") {
    let next: number | null = null;
    for (let i = cues.length - 1; i >= 0; i--) {
      if (i < cues.length - 1 && cues[i + 1].start > cues[i].start) next = cues[i + 1].start;
      cues[i].end = next;
    }
  }
  return cues;
}

export function formatCueTime(ms: number): string {
  const value = Math.max(0, Math.floor(ms));
  return `${String(Math.floor(value / 3600000)).padStart(2, "0")}:${String(Math.floor(value / 60000) % 60).padStart(2, "0")}:${String(Math.floor(value / 1000) % 60).padStart(2, "0")}.${String(value % 1000).padStart(3, "0")}`;
}
