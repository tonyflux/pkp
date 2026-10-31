// A Markdown reader for the file viewer: the
// CommonMark blocks people write in notes — headings, paragraphs, quotes,
// lists and task lists, fenced and indented code, rules — plus GFM tables,
// strikethrough and bare links. It builds a tree the viewer draws with Vue's
// h(), so every character of the file reaches the page as text, never as
// HTML: raw HTML in a file reads as the text it is. Nothing in a document
// loads anything: images stay labelled placeholders, and only http(s) and
// mailto links may leave the app (in the system browser, on a click).

export type Inline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "strong" | "em" | "del"; c: Inline[] }
  | { t: "link"; href: string; title: string; c: Inline[] }
  | { t: "image"; alt: string; src: string }
  | { t: "br" };

export interface ListItem {
  /** A task list item's box; null on an ordinary item. */
  checked: boolean | null;
  c: Block[];
}

export type Align = "left" | "center" | "right" | "";

export type Block =
  | { t: "heading"; level: number; c: Inline[]; text: string; anchor: string }
  | { t: "p"; c: Inline[] }
  | { t: "quote"; c: Block[] }
  | { t: "list"; ordered: boolean; start: number; tight: boolean; items: ListItem[] }
  | { t: "code"; lang: string; v: string }
  | { t: "hr" }
  | { t: "table"; align: Align[]; head: Inline[][]; rows: Inline[][][] };

export interface Heading {
  level: number;
  text: string;
  anchor: string;
}

export interface MarkdownDoc {
  blocks: Block[];
  /** Every heading in document order, for the outline. */
  headings: Heading[];
}

interface Ref {
  href: string;
  title: string;
}

interface Ctx {
  refs: Map<string, Ref>;
  anchors: Map<string, number>;
  headings: Heading[];
  // Inline content is parsed once every block is in: a reference link may
  // come before the definition it names.
  pending: (() => void)[];
}

const FENCE = /^( {0,3})(`{3,}|~{3,})[ \t]*([^\s`]*)/;
const ATX = /^ {0,3}(#{1,6})(?:[ \t]+(.*)|$)/;
const HR = /^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$/;
const QUOTE = /^ {0,3}>/;
const LIST = /^( {0,3})([-+*]|\d{1,9}[.)])([ \t]+|$)/;
const SETEXT = /^ {0,3}(=+|-+)[ \t]*$/;
const REF_DEF = /^ {0,3}\[([^\]]+)\]:[ \t]*<?([^\s>]+)>?(?:[ \t]+(?:"([^"]*)"|'([^']*)'|\(([^)]*)\)))?[ \t]*$/;
const TABLE_DELIM = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;
const ASCII_PUNCT = /[!-/:-@[-`{-~]/;
const WORD = /[\p{L}\p{N}]/u;

export function parseMarkdown(source: string): MarkdownDoc {
  const lines = source
    .replace(/\r\n?/g, "\n")
    .split("\n")
    .map((line) => line.replace(/^\t+/, (tabs) => "    ".repeat(tabs.length)));
  const ctx: Ctx = { refs: new Map(), anchors: new Map(), headings: [], pending: [] };
  const blocks = parseBlocks(lines, ctx);
  for (const finish of ctx.pending) finish();
  return { blocks, headings: ctx.headings };
}

const blank = (line: string) => line.trim() === "";
const indentOf = (line: string) => line.length - line.trimStart().length;

function listKind(m: RegExpExecArray): string {
  const marker = m[2];
  return /\d/.test(marker) ? `o${marker.slice(-1)}` : `u${marker}`;
}

/** startsBlock says whether a line ends the paragraph above it. */
function startsBlock(line: string): boolean {
  if (blank(line) || ATX.test(line) || FENCE.test(line) || HR.test(line) || QUOTE.test(line)) return true;
  const m = LIST.exec(line);
  // An ordered item interrupts a paragraph only when it starts at 1, and no
  // empty item does: "2019. Year" in a sentence stays the sentence.
  return !!m && !!m[3] && (!/\d/.test(m[2]) || parseInt(m[2], 10) === 1);
}

function parseBlocks(lines: string[], ctx: Ctx): Block[] {
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (blank(line)) {
      i++;
      continue;
    }

    const fence = FENCE.exec(line);
    if (fence && !(fence[2][0] === "`" && line.slice(fence[0].length).includes("`"))) {
      const [, pad, marks, lang] = fence;
      const body: string[] = [];
      i++;
      while (i < lines.length) {
        const close = /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(lines[i]);
        if (close && close[1][0] === marks[0] && close[1].length >= marks.length) {
          i++;
          break;
        }
        body.push(lines[i].replace(new RegExp(`^ {0,${pad.length}}`), ""));
        i++;
      }
      out.push({ t: "code", lang, v: body.join("\n") });
      continue;
    }

    if (indentOf(line) >= 4) {
      const body: string[] = [];
      while (i < lines.length && (blank(lines[i]) || indentOf(lines[i]) >= 4)) body.push(lines[i++].slice(4));
      while (body.length && blank(body[body.length - 1])) body.pop();
      out.push({ t: "code", lang: "", v: body.join("\n") });
      continue;
    }

    const atx = ATX.exec(line);
    if (atx) {
      const raw = (atx[2] ?? "").replace(/(?:^|[ \t]+)#+[ \t]*$/, "").trim();
      out.push(heading(atx[1].length, raw, ctx));
      i++;
      continue;
    }

    if (HR.test(line)) {
      out.push({ t: "hr" });
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const body: string[] = [];
      while (i < lines.length) {
        const l = lines[i];
        if (QUOTE.test(l)) body.push(l.replace(/^ {0,3}> ?/, ""));
        // A lazy line carries on the quote's paragraph.
        else if (!blank(l) && body.length && !blank(body[body.length - 1]) && !startsBlock(l)) body.push(l);
        else break;
        i++;
      }
      out.push({ t: "quote", c: parseBlocks(body, ctx) });
      continue;
    }

    if (LIST.test(line)) {
      i = parseList(lines, i, ctx, out);
      continue;
    }

    // A table: a header row with pipes, then the delimiter row.
    if (line.includes("|") && i + 1 < lines.length && TABLE_DELIM.test(lines[i + 1])) {
      const head = splitRow(line);
      const delims = splitRow(lines[i + 1]);
      if (head.length === delims.length) {
        const align = delims.map((d): Align => {
          const left = d.startsWith(":");
          const right = d.endsWith(":");
          return left && right ? "center" : right ? "right" : left ? "left" : "";
        });
        const block: Block = { t: "table", align, head: [], rows: [] };
        const rows: string[][] = [];
        i += 2;
        while (i < lines.length && !blank(lines[i]) && lines[i].includes("|") && !startsBlock(lines[i])) {
          const cells = splitRow(lines[i++]);
          rows.push(head.map((_, k) => cells[k] ?? ""));
        }
        ctx.pending.push(() => {
          block.head = head.map((cell) => parseInline(cell, ctx));
          block.rows = rows.map((row) => row.map((cell) => parseInline(cell, ctx)));
        });
        out.push(block);
        continue;
      }
    }

    const def = REF_DEF.exec(line);
    if (def) {
      const key = refKey(def[1]);
      if (!ctx.refs.has(key)) ctx.refs.set(key, { href: def[2], title: def[3] ?? def[4] ?? def[5] ?? "" });
      i++;
      continue;
    }

    // A paragraph, unless an underline makes it a heading.
    const text: string[] = [line.trim()];
    i++;
    let level = 0;
    while (i < lines.length) {
      const l = lines[i];
      const underline = SETEXT.exec(l);
      if (underline) {
        level = underline[1][0] === "=" ? 1 : 2;
        i++;
        break;
      }
      if (startsBlock(l)) break;
      text.push(l.trim() === "" ? "" : l.replace(/^ +/, ""));
      i++;
    }
    const raw = text.join("\n");
    if (level) {
      out.push(heading(level, raw, ctx));
    } else {
      const block: Block = { t: "p", c: [] };
      ctx.pending.push(() => (block.c = parseInline(raw, ctx)));
      out.push(block);
    }
  }
  return out;
}

function heading(level: number, raw: string, ctx: Ctx): Block {
  const block: Block = { t: "heading", level, c: [], text: "", anchor: "" };
  ctx.pending.push(() => {
    block.c = parseInline(raw, ctx);
    block.text = plainText(block.c).trim();
    block.anchor = anchorFor(block.text, ctx);
    ctx.headings.push({ level, text: block.text, anchor: block.anchor });
  });
  return block;
}

/** parseList reads one list from lines[i] on and returns the index after it. */
function parseList(lines: string[], start: number, ctx: Ctx, out: Block[]): number {
  const first = LIST.exec(lines[start])!;
  const kind = listKind(first);
  const ordered = kind[0] === "o";
  const list: Block = { t: "list", ordered, start: ordered ? parseInt(first[2], 10) : 1, tight: true, items: [] };
  let i = start;
  while (i < lines.length) {
    const m = LIST.exec(lines[i]);
    if (!m || listKind(m) !== kind) break;
    const spaces = m[3].length;
    const markerEnd = m[1].length + m[2].length;
    // Content starts after the marker and its spaces; more than four spaces
    // means one, the rest belonging to the content (an indented code block).
    const indent = spaces === 0 || spaces > 4 ? markerEnd + 1 : markerEnd + spaces;
    const body: string[] = [lines[i].slice(Math.min(indent, lines[i].length))];
    i++;
    while (i < lines.length) {
      const l = lines[i];
      if (blank(l)) {
        body.push("");
        i++;
        continue;
      }
      if (indentOf(l) >= indent) {
        body.push(l.slice(indent));
        i++;
        continue;
      }
      // Less indented: the item ends, unless a lazy line carries on its
      // paragraph.
      if (blank(body[body.length - 1]) || LIST.test(l) || startsBlock(l)) break;
      body.push(l.trim());
      i++;
    }
    let trailing = 0;
    while (body.length > 1 && blank(body[body.length - 1])) {
      body.pop();
      trailing++;
    }
    const next = i < lines.length ? LIST.exec(lines[i]) : null;
    if (trailing && next && listKind(next) === kind) list.tight = false;
    if (body.some((l, k) => k > 0 && blank(l) && k < body.length - 1 && !blank(body[k + 1]) && indentOf(body[k + 1]) < 4)) {
      list.tight = false;
    }
    let checked: boolean | null = null;
    const task = /^\[([ xX])\](?:[ \t]+|$)/.exec(body[0]);
    if (task) {
      checked = task[1] !== " ";
      body[0] = body[0].slice(task[0].length);
    }
    list.items.push({ checked, c: parseBlocks(body, ctx) });
    if (!next || listKind(next) !== kind) break;
  }
  out.push(list);
  return i;
}

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cell = "";
  let code = 0;
  for (let k = 0; k < s.length; k++) {
    const ch = s[k];
    if (ch === "\\" && s[k + 1] === "|") {
      cell += "|";
      k++;
    } else if (ch === "`") {
      code = code ? 0 : 1;
      cell += ch;
    } else if (ch === "|" && !code) {
      cells.push(cell.trim());
      cell = "";
    } else {
      cell += ch;
    }
  }
  cells.push(cell.trim());
  return cells;
}

function refKey(label: string): string {
  return label.trim().replace(/\s+/g, " ").toLowerCase();
}

function anchorFor(text: string, ctx: Ctx): string {
  const base =
    text
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s_-]/gu, "")
      .trim()
      .replace(/\s+/g, "-") || "section";
  const seen = ctx.anchors.get(base) ?? 0;
  ctx.anchors.set(base, seen + 1);
  return seen ? `${base}-${seen}` : base;
}

export function plainText(nodes: Inline[]): string {
  return nodes
    .map((n) => {
      switch (n.t) {
        case "text":
        case "code":
          return n.v;
        case "image":
          return n.alt;
        case "br":
          return " ";
        default:
          return plainText(n.c);
      }
    })
    .join("");
}

/* ---------- inline ---------- */

export function parseInline(src: string, ctx: Pick<Ctx, "refs">): Inline[] {
  const out: Inline[] = [];
  let buf = "";
  const flush = () => {
    if (buf) out.push({ t: "text", v: buf });
    buf = "";
  };
  let i = 0;
  while (i < src.length) {
    const ch = src[i];

    if (ch === "\\") {
      const next = src[i + 1];
      if (next === "\n") {
        flush();
        out.push({ t: "br" });
        i += 2;
        continue;
      }
      if (next && ASCII_PUNCT.test(next)) {
        buf += next;
        i += 2;
        continue;
      }
      buf += ch;
      i++;
      continue;
    }

    if (ch === "\n") {
      const hard = / {2,}$/.test(buf);
      buf = buf.replace(/ +$/, "");
      if (hard) {
        flush();
        out.push({ t: "br" });
      } else {
        buf += "\n";
      }
      i++;
      while (src[i] === " ") i++;
      continue;
    }

    if (ch === "`") {
      const run = /^`+/.exec(src.slice(i))![0];
      const close = codeClose(src, i + run.length, run.length);
      if (close >= 0) {
        flush();
        let code = src.slice(i + run.length, close).replace(/\n/g, " ");
        if (code.length > 2 && code.startsWith(" ") && code.endsWith(" ") && code.trim()) code = code.slice(1, -1);
        out.push({ t: "code", v: code });
        i = close + run.length;
        continue;
      }
      buf += run;
      i += run.length;
      continue;
    }

    if (ch === "!" && src[i + 1] === "[") {
      const link = linkAt(src, i + 1, ctx);
      if (link) {
        flush();
        out.push({ t: "image", alt: plainText(parseInline(link.label, ctx)), src: link.href });
        i = link.end;
        continue;
      }
    }

    if (ch === "[") {
      const link = linkAt(src, i, ctx);
      if (link) {
        flush();
        out.push({ t: "link", href: link.href, title: link.title, c: parseInline(link.label, ctx) });
        i = link.end;
        continue;
      }
    }

    if (ch === "<") {
      const auto = /^<((?:https?|mailto):[^\s<>]+)>/i.exec(src.slice(i));
      const mail = auto ? null : /^<([\w.+-]+@[\w-]+(?:\.[\w-]+)+)>/.exec(src.slice(i));
      if (auto || mail) {
        flush();
        const text = (auto ?? mail)![1];
        out.push({ t: "link", href: auto ? text : `mailto:${text}`, title: "", c: [{ t: "text", v: text }] });
        i += (auto ?? mail)![0].length;
        continue;
      }
    }

    if ((ch === "h" || ch === "H") && !(i > 0 && WORD.test(src[i - 1]))) {
      const bare = /^https?:\/\/[^\s<]+/i.exec(src.slice(i));
      if (bare) {
        const url = trimUrl(bare[0]);
        if (url.length > "https://".length - 1) {
          flush();
          out.push({ t: "link", href: url, title: "", c: [{ t: "text", v: url }] });
          i += url.length;
          continue;
        }
      }
    }

    if (ch === "*" || ch === "_" || ch === "~") {
      const em = emphasisAt(src, i, ctx);
      if (em) {
        flush();
        out.push(em.node);
        i = em.end;
        continue;
      }
      // A run that opens nothing is text, whole: its second mark must not
      // try again as a single one.
      const run = /^(\*+|_+|~+)/.exec(src.slice(i))![0];
      buf += run;
      i += run.length;
      continue;
    }

    buf += ch;
    i++;
  }
  flush();
  return out;
}

function codeClose(src: string, from: number, length: number): number {
  for (let k = from; k < src.length; ) {
    if (src[k] !== "`") {
      k++;
      continue;
    }
    let n = 0;
    while (src[k + n] === "`") n++;
    if (n === length) return k;
    k += n;
  }
  return -1;
}

// trimUrl drops what a sentence put after a bare link: closing punctuation,
// an unmatched parenthesis, and the full-width marks of Chinese text.
function trimUrl(url: string): string {
  let u = url.replace(/[，。；：！？、）】」』》]+.*$/u, "");
  for (;;) {
    const last = u[u.length - 1];
    if (/[.,;:!?'"*_~]/.test(last)) u = u.slice(0, -1);
    else if (last === ")" && (u.match(/\(/g)?.length ?? 0) < (u.match(/\)/g)?.length ?? 0)) u = u.slice(0, -1);
    else return u;
  }
}

interface LinkMatch {
  label: string;
  href: string;
  title: string;
  end: number;
}

/** linkAt reads [label](href "title"), [label][ref], [label][] or [ref] at src[i] === "[". */
function linkAt(src: string, i: number, ctx: Pick<Ctx, "refs">): LinkMatch | null {
  let depth = 0;
  let close = -1;
  for (let k = i; k < src.length; k++) {
    const ch = src[k];
    if (ch === "\\") {
      k++;
    } else if (ch === "`") {
      const run = /^`+/.exec(src.slice(k))![0];
      const end = codeClose(src, k + run.length, run.length);
      k = end >= 0 ? end + run.length - 1 : k + run.length - 1;
    } else if (ch === "[") {
      depth++;
    } else if (ch === "]" && --depth === 0) {
      close = k;
      break;
    }
  }
  if (close < 0) return null;
  const label = src.slice(i + 1, close);

  if (src[close + 1] === "(") {
    const m = /^\(\s*(?:<([^<>\n]*)>|((?:[^\s()\\]|\\.|\((?:[^\s()\\]|\\.)*\))*))(?:\s+(?:"([^"]*)"|'([^']*)'|\(([^)]*)\)))?\s*\)/.exec(
      src.slice(close + 1),
    );
    if (!m) return null;
    const href = (m[1] ?? m[2] ?? "").replace(/\\(.)/g, "$1");
    return { label, href, title: m[3] ?? m[4] ?? m[5] ?? "", end: close + 1 + m[0].length };
  }

  if (src[close + 1] === "[") {
    const end = src.indexOf("]", close + 2);
    if (end < 0) return null;
    const name = src.slice(close + 2, end) || label;
    const ref = ctx.refs.get(refKey(name));
    return ref ? { label, href: ref.href, title: ref.title, end: end + 1 } : null;
  }

  const ref = ctx.refs.get(refKey(label));
  return ref ? { label, href: ref.href, title: ref.title, end: close + 1 } : null;
}

/** emphasisAt reads *em*, _em_, **strong**, __strong__ or ~~del~~ at src[i]. */
function emphasisAt(src: string, i: number, ctx: Pick<Ctx, "refs">): { node: Inline; end: number } | null {
  const ch = src[i];
  const double = src[i + 1] === ch;
  if (ch === "~" && !double) return null;
  const delim = double ? ch + ch : ch;
  const after = src[i + delim.length];
  if (!after || /\s/.test(after)) return null;
  // snake_case and 2*3*4 are not emphasis.
  if (ch === "_" && i > 0 && WORD.test(src[i - 1])) return null;
  for (let j = i + delim.length + 1; j < src.length; ) {
    const k = src.indexOf(delim, j);
    if (k < 0) return null;
    const before = src[k - 1];
    const follow = src[k + delim.length];
    const partOfLongerRun = delim.length === 1 && (follow === ch || before === ch);
    const escaped = before === "\\";
    const intraword = ch === "_" && !!follow && WORD.test(follow);
    if (!/\s/.test(before) && !partOfLongerRun && !escaped && !intraword) {
      const inner = src.slice(i + delim.length, k);
      const t = ch === "~" ? "del" : double ? "strong" : "em";
      return { node: { t, c: parseInline(inner, ctx) }, end: k + delim.length };
    }
    j = k + 1;
  }
  return null;
}

/** linkTarget says where a link may go: out to the system browser, to a heading of this document, or nowhere. */
export function linkTarget(href: string): "external" | "anchor" | null {
  if (/^(?:https?:\/\/|mailto:)/i.test(href)) return "external";
  if (href.startsWith("#") && href.length > 1) return "anchor";
  return null;
}
